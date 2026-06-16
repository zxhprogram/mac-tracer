package keylogger

import (
	"fmt"
	"log"
	"sort"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"mac-tracer/storage"
)

const (
	WM_KEYUP    = 0x0101
	WM_SYSKEYUP = 0x0105

	WH_KEYBOARD_LL = 13
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procSetWindowsHookEx    = user32.NewProc("SetWindowsHookExW")
	procCallNextHookEx      = user32.NewProc("CallNextHookEx")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	procGetMessage          = user32.NewProc("GetMessageW")
	procPostThreadMessage   = user32.NewProc("PostThreadMessageW")
	procGetCurrentThreadId  = kernel32.NewProc("GetCurrentThreadId")
	procGetModuleHandle     = kernel32.NewProc("GetModuleHandleW")
)

type kbdLLHookStruct struct {
	VkCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type KeyCount struct {
	Key   string
	Count int64
}

type KeyLogger struct {
	store *storage.Storage

	mu     sync.Mutex
	counts map[string]int64

	currentMinute string
	minuteCounts  map[string]int64

	hookHandle uintptr
	threadID   uint32
	stopCh     chan struct{}
}

func New(store *storage.Storage) *KeyLogger {
	return &KeyLogger{
		store:        store,
		counts:       make(map[string]int64),
		minuteCounts: make(map[string]int64),
		stopCh:       make(chan struct{}),
	}
}

func (k *KeyLogger) Start() {
	saved, err := k.store.LoadKeyStats()
	if err != nil {
		log.Printf("load key stats: %v", err)
	} else {
		k.counts = saved
	}

	go k.hookLoop()
	go k.persistLoop()
}

func (k *KeyLogger) Stop() {
	close(k.stopCh)
	// Post WM_QUIT to the hook thread's message loop to unblock GetMessage
	if k.threadID != 0 {
		procPostThreadMessage.Call(uintptr(k.threadID), 0x0012 /* WM_QUIT */, 0, 0)
	}
	k.unhook()
	k.persist()
}

func (k *KeyLogger) GetStats() []KeyCount {
	k.mu.Lock()
	defer k.mu.Unlock()

	result := make([]KeyCount, 0, len(k.counts))
	for key, count := range k.counts {
		result = append(result, KeyCount{Key: key, Count: count})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Key < result[j].Key
	})
	return result
}

func (k *KeyLogger) TotalCount() int64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	var total int64
	for _, count := range k.counts {
		total += count
	}
	return total
}

var keyNameMap = map[uint32]string{
	0x08: "Backspace", 0x09: "Tab", 0x0D: "Enter",
	0x10: "Shift", 0x11: "Ctrl", 0x12: "Alt",
	0x13: "Pause", 0x14: "CapsLock", 0x1B: "Esc",
	0x20: "Space", 0x21: "PageUp", 0x22: "PageDown",
	0x23: "End", 0x24: "Home", 0x25: "Left",
	0x26: "Up", 0x27: "Right", 0x28: "Down",
	0x2C: "PrtSc", 0x2D: "Insert", 0x2E: "Delete",
	0x5B: "LWin", 0x5C: "RWin", 0x5D: "Menu",
	0x60: "Num0", 0x61: "Num1", 0x62: "Num2", 0x63: "Num3",
	0x64: "Num4", 0x65: "Num5", 0x66: "Num6", 0x67: "Num7",
	0x68: "Num8", 0x69: "Num9",
	0x6A: "Num*", 0x6B: "Num+", 0x6D: "Num-", 0x6F: "Num/",
	0x70: "F1", 0x71: "F2", 0x72: "F3", 0x73: "F4",
	0x74: "F5", 0x75: "F6", 0x76: "F7", 0x77: "F8",
	0x78: "F9", 0x79: "F10", 0x7A: "F11", 0x7B: "F12",
	0x90: "NumLock", 0x91: "ScrollLock",
	0xA0: "LShift", 0xA1: "RShift", 0xA2: "LCtrl", 0xA3: "RCtrl",
	0xA4: "LAlt", 0xA5: "RAlt",
	0xBA: ";", 0xBB: "=", 0xBC: ",", 0xBD: "-",
	0xBE: ".", 0xBF: "/", 0xC0: "`",
	0xDB: "[", 0xDC: "\\", 0xDD: "]", 0xDE: "'",
}

func vkToName(vkCode uint32) string {
	if name, ok := keyNameMap[vkCode]; ok {
		return name
	}
	// Printable ASCII: 0x30-0x39 (0-9), 0x41-0x5A (A-Z)
	if vkCode >= 0x30 && vkCode <= 0x39 {
		return string(rune(vkCode))
	}
	if vkCode >= 0x41 && vkCode <= 0x5A {
		return string(rune(vkCode))
	}
	return fmt.Sprintf("0x%02X", vkCode)
}

// lowLevelKeyboardProc is the callback for the keyboard hook
var keyloggerRef *KeyLogger

func lowLevelKeyboardProc(nCode int, wParam uintptr, lParam uintptr) uintptr {
	if nCode >= 0 && (wParam == WM_KEYUP || wParam == WM_SYSKEYUP) {
		kbd := (*kbdLLHookStruct)(unsafe.Pointer(lParam))
		keyName := vkToName(kbd.VkCode)
		if keyName != "" && keyloggerRef != nil {
			keyloggerRef.mu.Lock()
			keyloggerRef.counts[keyName]++
			keyloggerRef.touchMinuteLocked()
			keyloggerRef.minuteCounts[keyName]++
			keyloggerRef.mu.Unlock()
		}
	}
	ret, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return ret
}

func (k *KeyLogger) hookLoop() {
	keyloggerRef = k

	hMod, _, _ := procGetModuleHandle.Call(0)

	cb := syscall.NewCallback(lowLevelKeyboardProc)

	handle, _, err := procSetWindowsHookEx.Call(
		WH_KEYBOARD_LL,
		cb,
		hMod,
		0,
	)
	if handle == 0 {
		log.Printf("SetWindowsHookEx failed: %v", err)
		return
	}
	k.hookHandle = handle

	tid, _, _ := procGetCurrentThreadId.Call()
	k.threadID = uint32(tid)

	// Message pump — required for low-level hooks to work
	var msg struct {
		HWnd    uintptr
		Message uint32
		WParam  uintptr
		LParam  uintptr
		Time    uint32
		Pt      [2]int32
	}

	for {
		ret, _, _ := procGetMessage.Call(
			uintptr(unsafe.Pointer(&msg)),
			0, 0, 0,
		)
		if ret == 0 || int32(ret) == -1 {
			break
		}
	}

	k.unhook()
}

func (k *KeyLogger) unhook() {
	if k.hookHandle != 0 {
		procUnhookWindowsHookEx.Call(k.hookHandle)
		k.hookHandle = 0
	}
}

func (k *KeyLogger) persistLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-k.stopCh:
			return
		case <-ticker.C:
			k.persist()
		}
	}
}

func (k *KeyLogger) persist() {
	k.mu.Lock()
	snapshot := make(map[string]int64, len(k.counts))
	for key, count := range k.counts {
		snapshot[key] = count
	}
	k.mu.Unlock()

	if err := k.store.SaveKeyStats(snapshot); err != nil {
		log.Printf("save key stats: %v", err)
	}
	k.persistMinute()
}

func (k *KeyLogger) touchMinuteLocked() {
	now := time.Now().Format("2006-01-02T15:04")
	if now != k.currentMinute {
		if k.currentMinute != "" {
			k.persistMinuteLocked()
		}
		k.currentMinute = now
		k.minuteCounts = make(map[string]int64)
	}
}

func (k *KeyLogger) persistMinute() {
	k.mu.Lock()
	k.persistMinuteLocked()
	k.mu.Unlock()
}

func (k *KeyLogger) persistMinuteLocked() {
	if k.currentMinute == "" || len(k.minuteCounts) == 0 {
		return
	}
	snapshot := make(map[string]int64, len(k.minuteCounts))
	for key, count := range k.minuteCounts {
		snapshot[key] = count
	}
	minute := k.currentMinute
	k.mu.Unlock()
	if err := k.store.SaveKeyStatsMinute(minute, snapshot); err != nil {
		log.Printf("save key stats minute: %v", err)
	}
	k.mu.Lock()
}
