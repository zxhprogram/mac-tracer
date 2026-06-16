package apptracker

import (
	"log"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"mac-tracer/storage"
)

var (
	user32                 = syscall.NewLazyDLL("user32.dll")
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	atGetForegroundWindow  = user32.NewProc("GetForegroundWindow")
	atGetWindowThreadPID   = user32.NewProc("GetWindowThreadProcessId")
	atOpenProcess          = kernel32.NewProc("OpenProcess")
	atCloseHandle          = kernel32.NewProc("CloseHandle")
	atQueryFullProcessName = kernel32.NewProc("QueryFullProcessImageNameW")
)

const (
	PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
)

type AppUsage struct {
	App     string
	Seconds int64
}

type AppTracker struct {
	store *storage.Storage

	mu    sync.Mutex
	usage map[string]int64 // app name → seconds

	currentMinute string
	minuteUsage   map[string]int64

	stopCh chan struct{}
}

func New(store *storage.Storage) *AppTracker {
	return &AppTracker{
		store:       store,
		usage:       make(map[string]int64),
		minuteUsage: make(map[string]int64),
		stopCh:      make(chan struct{}),
	}
}

func (a *AppTracker) Start() {
	saved, err := a.store.LoadAppUsageStats()
	if err != nil {
		log.Printf("load app usage: %v", err)
	} else {
		a.usage = saved
	}

	go a.trackLoop()
	go a.persistLoop()
}

func (a *AppTracker) Stop() {
	close(a.stopCh)
	a.persist()
}

func (a *AppTracker) GetStats() []AppUsage {
	a.mu.Lock()
	defer a.mu.Unlock()

	result := make([]AppUsage, 0, len(a.usage))
	for app, seconds := range a.usage {
		result = append(result, AppUsage{App: app, Seconds: seconds})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Seconds != result[j].Seconds {
			return result[i].Seconds > result[j].Seconds
		}
		return result[i].App < result[j].App
	})
	return result
}

func (a *AppTracker) TotalTime() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	var total int64
	for _, seconds := range a.usage {
		total += seconds
	}
	return total
}

func (a *AppTracker) trackLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-a.stopCh:
			return
		case <-ticker.C:
			appName := getForegroundAppName()
			if appName == "" {
				continue
			}
			a.mu.Lock()
			a.usage[appName]++
			a.touchMinuteLocked()
			a.minuteUsage[appName]++
			a.mu.Unlock()
		}
	}
}

func (a *AppTracker) persistLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-a.stopCh:
			return
		case <-ticker.C:
			a.persist()
		}
	}
}

func (a *AppTracker) persist() {
	a.mu.Lock()
	snapshot := make(map[string]int64, len(a.usage))
	for app, seconds := range a.usage {
		snapshot[app] = seconds
	}
	a.mu.Unlock()

	if err := a.store.SaveAppUsageStats(snapshot); err != nil {
		log.Printf("save app usage: %v", err)
	}
	a.persistMinute()
}

func (a *AppTracker) touchMinuteLocked() {
	now := time.Now().Format("2006-01-02T15:04")
	if now != a.currentMinute {
		if a.currentMinute != "" {
			a.persistMinuteLocked()
		}
		a.currentMinute = now
		a.minuteUsage = make(map[string]int64)
	}
}

func (a *AppTracker) persistMinute() {
	a.mu.Lock()
	a.persistMinuteLocked()
	a.mu.Unlock()
}

func (a *AppTracker) persistMinuteLocked() {
	if a.currentMinute == "" || len(a.minuteUsage) == 0 {
		return
	}
	snapshot := make(map[string]int64, len(a.minuteUsage))
	for app, seconds := range a.minuteUsage {
		snapshot[app] = seconds
	}
	minute := a.currentMinute
	a.mu.Unlock()
	if err := a.store.SaveAppUsageMinute(minute, snapshot); err != nil {
		log.Printf("save app usage minute: %v", err)
	}
	a.mu.Lock()
}

func getForegroundAppName() string {
	hwnd, _, _ := atGetForegroundWindow.Call()
	if hwnd == 0 {
		return ""
	}

	var pid uint32
	atGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}

	handle, _, _ := atOpenProcess.Call(PROCESS_QUERY_LIMITED_INFORMATION, 0, uintptr(pid))
	if handle == 0 {
		return ""
	}
	defer atCloseHandle.Call(handle)

	// QueryFullProcessImageNameW
	var nameSize uint32 = 512
	var nameBuf [512]uint16

	ok, _, _ := atQueryFullProcessName.Call(
		handle,
		0, // ProcessNameWin32
		uintptr(unsafe.Pointer(&nameBuf[0])),
		uintptr(unsafe.Pointer(&nameSize)),
	)
	if ok == 0 {
		return ""
	}

	name := syscall.UTF16ToString(nameBuf[:nameSize])
	return filepath.Base(name)
}
