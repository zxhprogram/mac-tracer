package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"mac-tracer/apptracker"
	"mac-tracer/collector"
	"mac-tracer/keylogger"
	"mac-tracer/mouselogger"
	"mac-tracer/storage"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#5F5FD7")).
			Padding(0, 2)

	sectionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")).
			Bold(true)

	uploadStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#00D7FF"))
	dlStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#50FA7B"))

	barStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#5F5FD7"))
	barBgStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#3C3C3C"))

	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#5F5FD7")).
			Padding(1, 2)

	keyHintStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4"))
	activeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFB86C")).Bold(true)

	rankStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4"))
	keyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#F8F8F2"))
	countStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#50FA7B"))

	totalStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFB86C")).Bold(true)

	filterActiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFB86C")).Bold(true)
	filterStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4"))
)

type page int

const (
	pageNetwork page = iota
	pageKeys
	pageMouse
	pageApps
)

type TimeRange int

const (
	rangeAllTime TimeRange = iota
	rangeToday
	rangeYesterday
	range7d
	range30d
)

var timeRangeNames = []string{"All", "Today", "Yesterday", "7d", "30d"}

type tickMsg time.Time

type Model struct {
	collector   *collector.Collector
	keylogger   *keylogger.KeyLogger
	mouselogger *mouselogger.MouseLogger
	apptracker  *apptracker.AppTracker
	store       *storage.Storage
	startTime   time.Time
	width       int
	height      int
	curPage     page
	timeRange   TimeRange
}

func NewModel(c *collector.Collector, kl *keylogger.KeyLogger, ml *mouselogger.MouseLogger, at *apptracker.AppTracker, store *storage.Storage) Model {
	return Model{
		collector:   c,
		keylogger:   kl,
		mouselogger: ml,
		apptracker:  at,
		store:       store,
		startTime:   time.Now(),
		width:       50,
		height:      20,
		curPage:     pageNetwork,
		timeRange:   rangeAllTime,
	}
}

func (m Model) Init() tea.Cmd {
	return tickCmd()
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "n":
			m.curPage = pageNetwork
			return m, nil
		case "k":
			m.curPage = pageKeys
			return m, nil
		case "m":
			m.curPage = pageMouse
			return m, nil
		case "a":
			m.curPage = pageApps
			return m, nil
		case "left", "h":
			if m.timeRange > 0 {
				m.timeRange--
			} else {
				m.timeRange = TimeRange(len(timeRangeNames) - 1)
			}
			return m, nil
		case "right", "l":
			if int(m.timeRange) < len(timeRangeNames)-1 {
				m.timeRange++
			} else {
				m.timeRange = 0
			}
			return m, nil
		}
	case tickMsg:
		return m, tickCmd()
	}
	return m, nil
}

func (m Model) View() string {
	var content string
	switch m.curPage {
	case pageNetwork:
		content = m.viewNetwork()
	case pageKeys:
		content = m.viewKeys()
	case pageMouse:
		content = m.viewMouse()
	case pageApps:
		content = m.viewApps()
	}

	footer := m.renderFooter()
	return borderStyle.Render(content + "\n\n" + footer)
}

func (m Model) renderFooter() string {
	labels := []struct {
		page  page
		label string
	}{
		{pageNetwork, "[N] Network"},
		{pageKeys, "[K] Keys"},
		{pageMouse, "[M] Mouse"},
		{pageApps, "[A] Apps"},
	}

	parts := make([]string, len(labels))
	for i, l := range labels {
		s := l.label
		if m.curPage == l.page {
			s = activeStyle.Render(s)
		}
		parts[i] = s
	}
	parts = append(parts, "[Q] Quit")

	return keyHintStyle.Render(strings.Join(parts, "  "))
}

func (m Model) viewTimeFilter() string {
	parts := make([]string, len(timeRangeNames))
	for i, name := range timeRangeNames {
		if TimeRange(i) == m.timeRange {
			parts[i] = filterActiveStyle.Render(name)
		} else {
			parts[i] = filterStyle.Render(name)
		}
	}
	arrow := filterStyle.Render
	return arrow("◀") + " " + strings.Join(parts, " │ ") + " " + arrow("▶")
}

func (m Model) timeRangeMinutes() (start, end string) {
	now := time.Now()
	switch m.timeRange {
	case rangeToday:
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Format("2006-01-02T15:04")
		end = now.Format("2006-01-02T15:04")
	case rangeYesterday:
		y := now.AddDate(0, 0, -1)
		start = time.Date(y.Year(), y.Month(), y.Day(), 0, 0, 0, 0, y.Location()).Format("2006-01-02T15:04")
		end = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Format("2006-01-02T15:04")
	case range7d:
		start = now.AddDate(0, 0, -7).Format("2006-01-02T15:04")
		end = now.Format("2006-01-02T15:04")
	case range30d:
		start = now.AddDate(0, 0, -30).Format("2006-01-02T15:04")
		end = now.Format("2006-01-02T15:04")
	}
	return
}

func (m Model) viewNetwork() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(" Network Traffic Monitor "))
	b.WriteString("\n\n")
	b.WriteString(m.viewTimeFilter())
	b.WriteString("\n\n")

	if m.timeRange == rangeAllTime {
		s := m.collector.GetStats()
		duration := time.Since(m.startTime)

		b.WriteString(speedBar("▲ Upload  ", s.UploadSpeed, uploadStyle))
		b.WriteString("\n")
		b.WriteString(speedBar("▼ Download", s.DownloadSpeed, dlStyle))
		b.WriteString("\n\n")

		b.WriteString(sectionStyle.Render("──── Totals ────"))
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("%s  %s\n", uploadStyle.Render("▲"), formatBytes(s.TotalUpload)))
		b.WriteString(fmt.Sprintf("%s  %s\n", dlStyle.Render("▼"), formatBytes(s.TotalDownload)))
		b.WriteString("\n")

		b.WriteString(sectionStyle.Render("──── Session ────"))
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("%s  %s\n", uploadStyle.Render("▲"), formatBytes(s.SessionUpload)))
		b.WriteString(fmt.Sprintf("%s  %s\n", dlStyle.Render("▼"), formatBytes(s.SessionDownload)))
		b.WriteString(fmt.Sprintf("%s  %s\n", keyHintStyle.Render("⏱"), formatDuration(duration)))
		b.WriteString("\n")

		b.WriteString(sectionStyle.Render("──── Today ────"))
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("%s  %s    %s  %s",
			uploadStyle.Render("▲"), formatBytes(s.TodayUpload),
			dlStyle.Render("▼"), formatBytes(s.TodayDownload),
		))
	} else {
		start, end := m.timeRangeMinutes()
		up, down, err := m.store.QueryTrafficRange(start, end)
		if err != nil {
			b.WriteString(keyHintStyle.Render(fmt.Sprintf("  Query error: %v", err)))
		} else {
			b.WriteString(sectionStyle.Render("──── Period Totals ────"))
			b.WriteString("\n")
			b.WriteString(fmt.Sprintf("%s  %s\n", uploadStyle.Render("▲"), formatBytes(up)))
			b.WriteString(fmt.Sprintf("%s  %s\n", dlStyle.Render("▼"), formatBytes(down)))
			b.WriteString("\n")
			b.WriteString(keyHintStyle.Render(fmt.Sprintf("  %s ~ %s", start, end)))
		}
	}

	return b.String()
}

func (m Model) viewKeys() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(" Keypress Statistics "))
	b.WriteString("\n\n")
	b.WriteString(m.viewTimeFilter())
	b.WriteString("\n\n")

	var stats []keylogger.KeyCount
	var total int64

	if m.timeRange == rangeAllTime {
		stats = m.keylogger.GetStats()
		total = m.keylogger.TotalCount()
	} else {
		start, end := m.timeRangeMinutes()
		mm, err := m.store.QueryKeyStatsRange(start, end)
		if err != nil {
			b.WriteString(keyHintStyle.Render(fmt.Sprintf("  Query error: %v", err)))
			return b.String()
		}
		stats = make([]keylogger.KeyCount, 0, len(mm))
		for key, count := range mm {
			stats = append(stats, keylogger.KeyCount{Key: key, Count: count})
			total += count
		}
		sort.Slice(stats, func(i, j int) bool {
			if stats[i].Count != stats[j].Count {
				return stats[i].Count > stats[j].Count
			}
			return stats[i].Key < stats[j].Key
		})
	}

	b.WriteString(fmt.Sprintf("Total: %s", totalStyle.Render(fmt.Sprintf("%d", total))))
	b.WriteString("\n\n")
	b.WriteString(sectionStyle.Render("──── Key Counts ────"))
	b.WriteString("\n")

	if len(stats) == 0 {
		b.WriteString(keyHintStyle.Render("  No keys recorded yet..."))
		return b.String()
	}

	renderCountList(&b, stats, func(i int) (string, string, int64) {
		return stats[i].Key, fmt.Sprintf("%d", stats[i].Count), stats[i].Count
	})

	return b.String()
}

func (m Model) viewMouse() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(" Mouse Statistics "))
	b.WriteString("\n\n")
	b.WriteString(m.viewTimeFilter())
	b.WriteString("\n\n")

	var stats []mouselogger.ClickCount
	var total int64

	if m.timeRange == rangeAllTime {
		stats = m.mouselogger.GetStats()
		total = m.mouselogger.TotalCount()
	} else {
		start, end := m.timeRangeMinutes()
		mm, err := m.store.QueryMouseStatsRange(start, end)
		if err != nil {
			b.WriteString(keyHintStyle.Render(fmt.Sprintf("  Query error: %v", err)))
			return b.String()
		}
		stats = make([]mouselogger.ClickCount, 0, len(mm))
		for button, count := range mm {
			stats = append(stats, mouselogger.ClickCount{Button: button, Count: count})
			total += count
		}
		sort.Slice(stats, func(i, j int) bool {
			if stats[i].Count != stats[j].Count {
				return stats[i].Count > stats[j].Count
			}
			return stats[i].Button < stats[j].Button
		})
	}

	b.WriteString(fmt.Sprintf("Total: %s", totalStyle.Render(fmt.Sprintf("%d", total))))
	b.WriteString("\n\n")
	b.WriteString(sectionStyle.Render("──── Button Counts ────"))
	b.WriteString("\n")

	if len(stats) == 0 {
		b.WriteString(keyHintStyle.Render("  No clicks recorded yet..."))
		return b.String()
	}

	renderCountList(&b, stats, func(i int) (string, string, int64) {
		return stats[i].Button, fmt.Sprintf("%d", stats[i].Count), stats[i].Count
	})

	return b.String()
}

func (m Model) viewApps() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(" App Usage Time "))
	b.WriteString("\n\n")
	b.WriteString(m.viewTimeFilter())
	b.WriteString("\n\n")

	var stats []apptracker.AppUsage
	var totalSec int64

	if m.timeRange == rangeAllTime {
		stats = m.apptracker.GetStats()
		totalSec = m.apptracker.TotalTime()
	} else {
		start, end := m.timeRangeMinutes()
		mm, err := m.store.QueryAppUsageRange(start, end)
		if err != nil {
			b.WriteString(keyHintStyle.Render(fmt.Sprintf("  Query error: %v", err)))
			return b.String()
		}
		stats = make([]apptracker.AppUsage, 0, len(mm))
		for app, seconds := range mm {
			stats = append(stats, apptracker.AppUsage{App: app, Seconds: seconds})
			totalSec += seconds
		}
		sort.Slice(stats, func(i, j int) bool {
			if stats[i].Seconds != stats[j].Seconds {
				return stats[i].Seconds > stats[j].Seconds
			}
			return stats[i].App < stats[j].App
		})
	}

	b.WriteString(fmt.Sprintf("Total: %s", totalStyle.Render(formatDuration(time.Duration(totalSec)*time.Second))))
	b.WriteString("\n\n")
	b.WriteString(sectionStyle.Render("──── Application Time ────"))
	b.WriteString("\n")

	if len(stats) == 0 {
		b.WriteString(keyHintStyle.Render("  No apps tracked yet..."))
		return b.String()
	}

	renderCountList(&b, stats, func(i int) (string, string, int64) {
		dur := formatDuration(time.Duration(stats[i].Seconds) * time.Second)
		return stats[i].App, dur, stats[i].Seconds
	})

	return b.String()
}

type listEntry interface {
	comparable
}

func renderCountList[E any](b *strings.Builder, entries []E, getRow func(i int) (name string, value string, sortVal int64)) {
	maxRows := 20
	if len(entries) < maxRows {
		maxRows = len(entries)
	}

	var maxSort int64
	if len(entries) > 0 {
		_, _, maxSort = getRow(0)
	}

	for i := 0; i < maxRows; i++ {
		name, value, sortVal := getRow(i)
		rank := rankStyle.Render(fmt.Sprintf("%2d.", i+1))
		n := keyStyle.Render(padKey(name, 14))
		v := countStyle.Render(value)

		barLen := 0
		if maxSort > 0 {
			barLen = int(float64(20) * float64(sortVal) / float64(maxSort))
		}
		bar := barStyle.Render(strings.Repeat("█", barLen))

		b.WriteString(fmt.Sprintf("%s %s %s %s\n", rank, n, v, bar))
	}

	if len(entries) > maxRows {
		b.WriteString(keyHintStyle.Render(fmt.Sprintf("\n  ... and %d more", len(entries)-maxRows)))
	}
}

func padKey(s string, width int) string {
	if len(s) >= width {
		return s[:width]
	}
	return s + strings.Repeat(" ", width-len(s))
}

func speedBar(label string, bps int64, style lipgloss.Style) string {
	maxSpeed := int64(50 * 1024 * 1024)
	if bps > maxSpeed {
		maxSpeed = bps
	}
	barWidth := 20
	filled := int(float64(barWidth) * float64(bps) / float64(maxSpeed))
	if filled > barWidth {
		filled = barWidth
	}

	bar := barStyle.Render(strings.Repeat("█", filled)) +
		barBgStyle.Render(strings.Repeat("░", barWidth-filled))

	speedStr := formatSpeed(bps)
	return fmt.Sprintf("%s %s  %s", style.Render(label), bar, speedStr)
}

func formatSpeed(bps int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bps >= GB:
		return fmt.Sprintf("%.1f GB/s", float64(bps)/float64(GB))
	case bps >= MB:
		return fmt.Sprintf("%.1f MB/s", float64(bps)/float64(MB))
	case bps >= KB:
		return fmt.Sprintf("%.1f KB/s", float64(bps)/float64(KB))
	default:
		return fmt.Sprintf("%d B/s", bps)
	}
}

func formatBytes(b int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case b >= TB:
		return fmt.Sprintf("%.2f TB", float64(b)/float64(TB))
	case b >= GB:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.2f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.2f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
