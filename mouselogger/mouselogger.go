package mouselogger

import (
	"log"
	"sort"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"mac-tracer/storage"
)

const (
	WH_MOUSE_LL = 14

	WM_LBUTTONUP  = 0x0202
	WM_RBUTTONUP  = 0x0205
	WM_MBUTTONUP  = 0x0208
	WM_MOUSEWHEEL = 0x020A
)

var (
	user32                = syscall.NewLazyDLL("user32.dll")
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	mlSetWindowsHookEx    = user32.NewProc("SetWindowsHookExW")
	mlCallNextHookEx      = user32.NewProc("CallNextHookEx")
	mlUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	mlGetMessage          = user32.NewProc("GetMessageW")
	mlPostThreadMessage   = user32.NewProc("PostThreadMessageW")
	mlGetCurrentThreadId  = kernel32.NewProc("GetCurrentThreadId")
	mlGetModuleHandle     = kernel32.NewProc("GetModuleHandleW")
)

type msLLHookStruct struct {
	X           int32
	Y           int32
	MouseData   uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type ClickCount struct {
	Button string
	Count  int64
}

type MouseLogger struct {
	store *storage.Storage

	mu     sync.Mutex
	counts map[string]int64

	currentMinute string
	minuteCounts  map[string]int64

	hookHandle uintptr
	threadID   uint32
	stopCh     chan struct{}
}

func New(store *storage.Storage) *MouseLogger {
	return &MouseLogger{
		store:        store,
		counts:       make(map[string]int64),
		minuteCounts: make(map[string]int64),
		stopCh:       make(chan struct{}),
	}
}

func (m *MouseLogger) Start() {
	saved, err := m.store.LoadMouseStats()
	if err != nil {
		log.Printf("load mouse stats: %v", err)
	} else {
		m.counts = saved
	}

	go m.hookLoop()
	go m.persistLoop()
}

func (m *MouseLogger) Stop() {
	close(m.stopCh)
	if m.threadID != 0 {
		mlPostThreadMessage.Call(uintptr(m.threadID), 0x0012, 0, 0)
	}
	m.unhook()
	m.persist()
}

func (m *MouseLogger) GetStats() []ClickCount {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]ClickCount, 0, len(m.counts))
	for button, count := range m.counts {
		result = append(result, ClickCount{Button: button, Count: count})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Button < result[j].Button
	})
	return result
}

func (m *MouseLogger) TotalCount() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var total int64
	for _, count := range m.counts {
		total += count
	}
	return total
}

var mouseloggerRef *MouseLogger

func lowLevelMouseProc(nCode int, wParam uintptr, lParam uintptr) uintptr {
	if nCode >= 0 && mouseloggerRef != nil {
		button := ""
		switch wParam {
		case WM_LBUTTONUP:
			button = "Left"
		case WM_RBUTTONUP:
			button = "Right"
		case WM_MBUTTONUP:
			button = "Middle"
		case WM_MOUSEWHEEL:
			kbd := (*msLLHookStruct)(unsafe.Pointer(lParam))
			delta := int16(kbd.MouseData >> 16)
			if delta > 0 {
				button = "ScrollUp"
			} else {
				button = "ScrollDown"
			}
		}
		if button != "" {
			mouseloggerRef.mu.Lock()
			mouseloggerRef.counts[button]++
			mouseloggerRef.touchMinuteLocked()
			mouseloggerRef.minuteCounts[button]++
			mouseloggerRef.mu.Unlock()
		}
	}
	ret, _, _ := mlCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return ret
}

func (m *MouseLogger) hookLoop() {
	mouseloggerRef = m

	hMod, _, _ := mlGetModuleHandle.Call(0)
	cb := syscall.NewCallback(lowLevelMouseProc)

	handle, _, err := mlSetWindowsHookEx.Call(WH_MOUSE_LL, cb, hMod, 0)
	if handle == 0 {
		log.Printf("SetWindowsHookEx mouse failed: %v", err)
		return
	}
	m.hookHandle = handle

	tid, _, _ := mlGetCurrentThreadId.Call()
	m.threadID = uint32(tid)

	var msg struct {
		HWnd    uintptr
		Message uint32
		WParam  uintptr
		LParam  uintptr
		Time    uint32
		Pt      [2]int32
	}

	for {
		ret, _, _ := mlGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if ret == 0 || int32(ret) == -1 {
			break
		}
	}

	m.unhook()
}

func (m *MouseLogger) unhook() {
	if m.hookHandle != 0 {
		mlUnhookWindowsHookEx.Call(m.hookHandle)
		m.hookHandle = 0
	}
}

func (m *MouseLogger) persistLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.persist()
		}
	}
}

func (m *MouseLogger) persist() {
	m.mu.Lock()
	snapshot := make(map[string]int64, len(m.counts))
	for button, count := range m.counts {
		snapshot[button] = count
	}
	m.mu.Unlock()

	if err := m.store.SaveMouseStats(snapshot); err != nil {
		log.Printf("save mouse stats: %v", err)
	}
	m.persistMinute()
}

func (m *MouseLogger) touchMinuteLocked() {
	now := time.Now().Format("2006-01-02T15:04")
	if now != m.currentMinute {
		if m.currentMinute != "" {
			m.persistMinuteLocked()
		}
		m.currentMinute = now
		m.minuteCounts = make(map[string]int64)
	}
}

func (m *MouseLogger) persistMinute() {
	m.mu.Lock()
	m.persistMinuteLocked()
	m.mu.Unlock()
}

func (m *MouseLogger) persistMinuteLocked() {
	if m.currentMinute == "" || len(m.minuteCounts) == 0 {
		return
	}
	snapshot := make(map[string]int64, len(m.minuteCounts))
	for button, count := range m.minuteCounts {
		snapshot[button] = count
	}
	minute := m.currentMinute
	m.mu.Unlock()
	if err := m.store.SaveMouseStatsMinute(minute, snapshot); err != nil {
		log.Printf("save mouse stats minute: %v", err)
	}
	m.mu.Lock()
}
