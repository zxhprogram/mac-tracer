package collector

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/net"
	"mac-tracer/storage"
)

type Stats struct {
	UploadSpeed     int64 // bytes/sec
	DownloadSpeed   int64 // bytes/sec
	TotalUpload     int64 // accumulated total bytes
	TotalDownload   int64
	SessionUpload   int64
	SessionDownload int64
	TodayUpload     int64
	TodayDownload   int64
}

type Collector struct {
	store *storage.Storage

	mu           sync.Mutex
	stats        Stats
	lastCounters map[string]storage.InterfaceCounter // per-interface last reading

	currentMinute  string
	minuteUpload   int64
	minuteDownload int64

	stopCh chan struct{}
}

func New(store *storage.Storage) *Collector {
	return &Collector{
		store:        store,
		lastCounters: make(map[string]storage.InterfaceCounter),
		stopCh:       make(chan struct{}),
	}
}

func (c *Collector) Start() {
	// Load persisted totals
	totals, err := c.store.LoadTotals()
	if err != nil {
		log.Printf("load totals: %v", err)
	}
	c.stats.TotalUpload = totals.TotalUpload
	c.stats.TotalDownload = totals.TotalDownload

	// Load persisted interface counters (for delta computation across restarts)
	savedCounters, err := c.store.LoadInterfaceCounters()
	if err != nil {
		log.Printf("load interface counters: %v", err)
	}
	c.lastCounters = savedCounters

	// Load today's stats
	today := time.Now().Format("2006-01-02")
	daily, err := c.store.LoadDailyStats(today)
	if err != nil {
		log.Printf("load daily stats: %v", err)
	}
	c.stats.TodayUpload = daily.Upload
	c.stats.TodayDownload = daily.Download

	// First tick: read current counters without computing delta
	// This establishes baseline for next tick's delta
	c.firstTick()

	go c.loop()
}

func (c *Collector) Stop() {
	close(c.stopCh)
	c.persist()
}

func (c *Collector) GetStats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

func (c *Collector) firstTick() {
	counters, err := net.IOCounters(true)
	if err != nil {
		log.Printf("first tick read: %v", err)
		return
	}

	var catchupUpload, catchupDownload int64

	for _, ctr := range counters {
		if isLoopback(ctr.Name) {
			continue
		}

		curSent := int64(ctr.BytesSent)
		curRecv := int64(ctr.BytesRecv)

		last, exists := c.lastCounters[ctr.Name]
		if !exists {
			// New interface (first launch): count all current bytes as historical baseline
			catchupUpload += curSent
			catchupDownload += curRecv
		} else if curSent > last.BytesSent || curRecv > last.BytesRecv {
			// Traffic happened while app was closed: count the gap
			if curSent > last.BytesSent {
				catchupUpload += curSent - last.BytesSent
			}
			if curRecv > last.BytesRecv {
				catchupDownload += curRecv - last.BytesRecv
			}
		}
		// If current < saved (counter reset while app was closed), no catchup needed

		c.lastCounters[ctr.Name] = storage.InterfaceCounter{
			Name:      ctr.Name,
			BytesSent: curSent,
			BytesRecv: curRecv,
		}
	}

	if catchupUpload > 0 || catchupDownload > 0 {
		c.stats.TotalUpload += catchupUpload
		c.stats.TotalDownload += catchupDownload
		c.stats.TodayUpload += catchupUpload
		c.stats.TodayDownload += catchupDownload
	}
}

func (c *Collector) loop() {
	ticker := time.NewTicker(time.Second)
	persistTicker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	defer persistTicker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.sample()
		case <-persistTicker.C:
			c.persist()
		}
	}
}

func (c *Collector) sample() {
	counters, err := net.IOCounters(true)
	if err != nil {
		log.Printf("sample: %v", err)
		return
	}

	var uploadDelta, downloadDelta int64

	for _, ctr := range counters {
		if isLoopback(ctr.Name) {
			continue
		}

		curSent := int64(ctr.BytesSent)
		curRecv := int64(ctr.BytesRecv)

		last, exists := c.lastCounters[ctr.Name]

		var dSent, dRecv int64
		if !exists {
			// New interface appeared, treat current as baseline (no delta)
			dSent = 0
			dRecv = 0
		} else if curSent < last.BytesSent || curRecv < last.BytesRecv {
			// Counter reset (network disconnect/reconnect)
			// The current value is traffic since reconnection, count it all
			dSent = curSent
			dRecv = curRecv
		} else {
			dSent = curSent - last.BytesSent
			dRecv = curRecv - last.BytesRecv
		}

		uploadDelta += dSent
		downloadDelta += dRecv

		c.lastCounters[ctr.Name] = storage.InterfaceCounter{
			Name:      ctr.Name,
			BytesSent: curSent,
			BytesRecv: curRecv,
		}
	}

	// Remove interfaces that disappeared
	currentNames := make(map[string]bool)
	for _, ctr := range counters {
		if !isLoopback(ctr.Name) {
			currentNames[ctr.Name] = true
		}
	}
	for name := range c.lastCounters {
		if !currentNames[name] {
			delete(c.lastCounters, name)
		}
	}

	c.mu.Lock()
	c.stats.UploadSpeed = uploadDelta
	c.stats.DownloadSpeed = downloadDelta
	c.stats.TotalUpload += uploadDelta
	c.stats.TotalDownload += downloadDelta
	c.stats.SessionUpload += uploadDelta
	c.stats.SessionDownload += downloadDelta
	c.stats.TodayUpload += uploadDelta
	c.stats.TodayDownload += downloadDelta
	c.touchMinuteLocked()
	c.minuteUpload += uploadDelta
	c.minuteDownload += downloadDelta
	c.mu.Unlock()
}

func (c *Collector) persist() {
	c.mu.Lock()
	s := c.stats
	counters := make([]storage.InterfaceCounter, 0, len(c.lastCounters))
	for _, ctr := range c.lastCounters {
		counters = append(counters, ctr)
	}
	c.mu.Unlock()

	if err := c.store.SaveTotals(s.TotalUpload, s.TotalDownload); err != nil {
		log.Printf("save totals: %v", err)
	}
	if err := c.store.SaveInterfaceCounters(counters); err != nil {
		log.Printf("save counters: %v", err)
	}
	today := time.Now().Format("2006-01-02")
	if err := c.store.SaveDailyStats(today, s.TodayUpload, s.TodayDownload); err != nil {
		log.Printf("save daily: %v", err)
	}
	c.persistMinute()
}

func (c *Collector) touchMinuteLocked() {
	now := time.Now().Format("2006-01-02T15:04")
	if now != c.currentMinute {
		if c.currentMinute != "" {
			c.persistMinuteLocked()
		}
		c.currentMinute = now
		c.minuteUpload = 0
		c.minuteDownload = 0
	}
}

func (c *Collector) persistMinute() {
	c.mu.Lock()
	c.persistMinuteLocked()
	c.mu.Unlock()
}

func (c *Collector) persistMinuteLocked() {
	if c.currentMinute == "" || (c.minuteUpload == 0 && c.minuteDownload == 0) {
		return
	}
	minute := c.currentMinute
	upload := c.minuteUpload
	download := c.minuteDownload
	c.mu.Unlock()
	if err := c.store.SaveTrafficMinute(minute, upload, download); err != nil {
		log.Printf("save traffic minute: %v", err)
	}
	c.mu.Lock()
}

func isLoopback(name string) bool {
	return name == "lo" || fmt.Sprintf("%s", name) == "lo0"
}
