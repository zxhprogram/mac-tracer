package storage

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Storage struct {
	db *sql.DB
	mu sync.Mutex
}

type TrafficTotals struct {
	TotalUpload   int64
	TotalDownload int64
}

type InterfaceCounter struct {
	Name      string
	BytesSent int64
	BytesRecv int64
}

type DailyStat struct {
	Date     string
	Upload   int64
	Download int64
}

func New(dbPath string) (*Storage, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	s := &Storage{db: db}
	if err := s.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return s, nil
}

func (s *Storage) Close() error {
	return s.db.Close()
}

func (s *Storage) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS traffic (
		id            INTEGER PRIMARY KEY CHECK (id = 1),
		total_upload  INTEGER NOT NULL DEFAULT 0,
		total_download INTEGER NOT NULL DEFAULT 0,
		updated_at    DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS interface_counters (
		name       TEXT PRIMARY KEY,
		bytes_sent INTEGER NOT NULL DEFAULT 0,
		bytes_recv INTEGER NOT NULL DEFAULT 0,
		updated_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS daily_stats (
		date     TEXT PRIMARY KEY,
		upload   INTEGER NOT NULL DEFAULT 0,
		download INTEGER NOT NULL DEFAULT 0
	);

	INSERT OR IGNORE INTO traffic (id, total_upload, total_download, updated_at)
		VALUES (1, 0, 0, '2000-01-01T00:00:00Z');

	CREATE TABLE IF NOT EXISTS key_stats (
		key   TEXT PRIMARY KEY,
		count INTEGER NOT NULL DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS mouse_stats (
		button TEXT PRIMARY KEY,
		count  INTEGER NOT NULL DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS app_usage_stats (
		app     TEXT PRIMARY KEY,
		seconds INTEGER NOT NULL DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS traffic_minute (
		minute   TEXT NOT NULL,
		upload   INTEGER NOT NULL DEFAULT 0,
		download INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (minute)
	);

	CREATE TABLE IF NOT EXISTS key_stats_minute (
		minute TEXT NOT NULL,
		key    TEXT NOT NULL,
		count  INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (minute, key)
	);

	CREATE TABLE IF NOT EXISTS mouse_stats_minute (
		minute  TEXT NOT NULL,
		button  TEXT NOT NULL,
		count   INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (minute, button)
	);

	CREATE TABLE IF NOT EXISTS app_usage_minute (
		minute  TEXT NOT NULL,
		app     TEXT NOT NULL,
		seconds INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (minute, app)
	);
	`
	_, err := s.db.Exec(schema)
	return err
}

func (s *Storage) LoadTotals() (TrafficTotals, error) {
	var t TrafficTotals
	err := s.db.QueryRow(
		"SELECT total_upload, total_download FROM traffic WHERE id = 1",
	).Scan(&t.TotalUpload, &t.TotalDownload)
	return t, err
}

func (s *Storage) SaveTotals(upload, download int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		"UPDATE traffic SET total_upload = ?, total_download = ?, updated_at = ? WHERE id = 1",
		upload, download, time.Now().UTC(),
	)
	return err
}

func (s *Storage) LoadInterfaceCounters() (map[string]InterfaceCounter, error) {
	rows, err := s.db.Query("SELECT name, bytes_sent, bytes_recv FROM interface_counters")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[string]InterfaceCounter)
	for rows.Next() {
		var c InterfaceCounter
		if err := rows.Scan(&c.Name, &c.BytesSent, &c.BytesRecv); err != nil {
			return nil, err
		}
		m[c.Name] = c
	}
	return m, rows.Err()
}

func (s *Storage) SaveInterfaceCounters(counters []InterfaceCounter) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO interface_counters (name, bytes_sent, bytes_recv, updated_at)
		VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, c := range counters {
		if _, err := stmt.Exec(c.Name, c.BytesSent, c.BytesRecv, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Storage) LoadDailyStats(date string) (DailyStat, error) {
	var d DailyStat
	d.Date = date
	err := s.db.QueryRow(
		"SELECT upload, download FROM daily_stats WHERE date = ?", date,
	).Scan(&d.Upload, &d.Download)
	if err == sql.ErrNoRows {
		return d, nil
	}
	return d, err
}

func (s *Storage) SaveDailyStats(date string, upload, download int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`
		INSERT INTO daily_stats (date, upload, download) VALUES (?, ?, ?)
		ON CONFLICT(date) DO UPDATE SET upload = ?, download = ?`,
		date, upload, download, upload, download,
	)
	return err
}

func (s *Storage) LoadKeyStats() (map[string]int64, error) {
	rows, err := s.db.Query("SELECT key, count FROM key_stats")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[string]int64)
	for rows.Next() {
		var key string
		var count int64
		if err := rows.Scan(&key, &count); err != nil {
			return nil, err
		}
		m[key] = count
	}
	return m, rows.Err()
}

func (s *Storage) SaveKeyStats(stats map[string]int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO key_stats (key, count) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET count = ?
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for key, count := range stats {
		if _, err := stmt.Exec(key, count, count); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Storage) LoadMouseStats() (map[string]int64, error) {
	rows, err := s.db.Query("SELECT button, count FROM mouse_stats")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[string]int64)
	for rows.Next() {
		var button string
		var count int64
		if err := rows.Scan(&button, &count); err != nil {
			return nil, err
		}
		m[button] = count
	}
	return m, rows.Err()
}

func (s *Storage) SaveMouseStats(stats map[string]int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO mouse_stats (button, count) VALUES (?, ?)
		ON CONFLICT(button) DO UPDATE SET count = ?
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for button, count := range stats {
		if _, err := stmt.Exec(button, count, count); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Storage) LoadAppUsageStats() (map[string]int64, error) {
	rows, err := s.db.Query("SELECT app, seconds FROM app_usage_stats")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[string]int64)
	for rows.Next() {
		var app string
		var seconds int64
		if err := rows.Scan(&app, &seconds); err != nil {
			return nil, err
		}
		m[app] = seconds
	}
	return m, rows.Err()
}

func (s *Storage) SaveAppUsageStats(stats map[string]int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO app_usage_stats (app, seconds) VALUES (?, ?)
		ON CONFLICT(app) DO UPDATE SET seconds = ?
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for app, seconds := range stats {
		if _, err := stmt.Exec(app, seconds, seconds); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// --- Minute-level persistence ---

func (s *Storage) SaveTrafficMinute(minute string, upload, download int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`
		INSERT INTO traffic_minute (minute, upload, download) VALUES (?, ?, ?)
		ON CONFLICT(minute) DO UPDATE SET upload = upload + ?, download = download + ?`,
		minute, upload, download, upload, download,
	)
	return err
}

func (s *Storage) SaveKeyStatsMinute(minute string, stats map[string]int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO key_stats_minute (minute, key, count) VALUES (?, ?, ?)
		ON CONFLICT(minute, key) DO UPDATE SET count = count + ?
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for key, count := range stats {
		if _, err := stmt.Exec(minute, key, count, count); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Storage) SaveMouseStatsMinute(minute string, stats map[string]int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO mouse_stats_minute (minute, button, count) VALUES (?, ?, ?)
		ON CONFLICT(minute, button) DO UPDATE SET count = count + ?
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for button, count := range stats {
		if _, err := stmt.Exec(minute, button, count, count); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Storage) SaveAppUsageMinute(minute string, stats map[string]int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO app_usage_minute (minute, app, seconds) VALUES (?, ?, ?)
		ON CONFLICT(minute, app) DO UPDATE SET seconds = seconds + ?
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for app, seconds := range stats {
		if _, err := stmt.Exec(minute, app, seconds, seconds); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// --- Minute-level queries ---

func (s *Storage) QueryTrafficRange(start, end string) (upload, download int64, err error) {
	err = s.db.QueryRow(
		"SELECT COALESCE(SUM(upload),0), COALESCE(SUM(download),0) FROM traffic_minute WHERE minute >= ? AND minute < ?",
		start, end,
	).Scan(&upload, &download)
	return
}

func (s *Storage) QueryKeyStatsRange(start, end string) (map[string]int64, error) {
	rows, err := s.db.Query(
		"SELECT key, SUM(count) FROM key_stats_minute WHERE minute >= ? AND minute < ? GROUP BY key",
		start, end,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[string]int64)
	for rows.Next() {
		var key string
		var count int64
		if err := rows.Scan(&key, &count); err != nil {
			return nil, err
		}
		m[key] = count
	}
	return m, rows.Err()
}

func (s *Storage) QueryMouseStatsRange(start, end string) (map[string]int64, error) {
	rows, err := s.db.Query(
		"SELECT button, SUM(count) FROM mouse_stats_minute WHERE minute >= ? AND minute < ? GROUP BY button",
		start, end,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[string]int64)
	for rows.Next() {
		var button string
		var count int64
		if err := rows.Scan(&button, &count); err != nil {
			return nil, err
		}
		m[button] = count
	}
	return m, rows.Err()
}

func (s *Storage) QueryAppUsageRange(start, end string) (map[string]int64, error) {
	rows, err := s.db.Query(
		"SELECT app, SUM(seconds) FROM app_usage_minute WHERE minute >= ? AND minute < ? GROUP BY app",
		start, end,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[string]int64)
	for rows.Next() {
		var app string
		var seconds int64
		if err := rows.Scan(&app, &seconds); err != nil {
			return nil, err
		}
		m[app] = seconds
	}
	return m, rows.Err()
}
