package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"mac-tracer/apptracker"
	"mac-tracer/collector"
	"mac-tracer/keylogger"
	"mac-tracer/mouselogger"
	"mac-tracer/storage"
	"mac-tracer/ui"
)

func main() {
	dbPath := defaultDBPath()

	store, err := storage.New(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage init: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	c := collector.New(store)
	c.Start()
	defer c.Stop()

	kl := keylogger.New(store)
	kl.Start()
	defer kl.Stop()

	ml := mouselogger.New(store)
	ml.Start()
	defer ml.Stop()

	at := apptracker.New(store)
	at.Start()
	defer at.Stop()

	p := tea.NewProgram(ui.NewModel(c, kl, ml, at, store), tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tui error: %v\n", err)
		os.Exit(1)
	}
}

func defaultDBPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "mac-tracer.db"
	}
	dir := filepath.Dir(exe)

	// Try to use executable directory, fall back to current directory
	dbDir := dir
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		dbDir = "."
	}

	return filepath.Join(dbDir, "mac-tracer.db")
}
