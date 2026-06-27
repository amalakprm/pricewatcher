package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	conn *sql.DB
	mu   sync.Mutex
}

type Product struct {
	ID          int64     `json:"id"`
	URL         string    `json:"url"`
	TargetPrice float64   `json:"target_price"`
	Source      string    `json:"source"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
}

type PricePoint struct {
	Price     float64   `json:"price"`
	Target    float64   `json:"target"`
	ScrapedAt time.Time `json:"scraped_at"`
	LayerUsed int       `json:"layer_used"`
	Title     string    `json:"title"`
}

type Alert struct {
	URL         string    `json:"url"`
	LastAlerted time.Time `json:"last_alerted"`
	AlertType   string    `json:"alert_type"`
}

type Run struct {
	ID           int64      `json:"id"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	ProductCount int        `json:"product_count"`
	AlertCount   int        `json:"alert_count"`
	ErrorCount   int        `json:"error_count"`
	Status       string     `json:"status"`
}

type ScrapeLogEntry struct {
	ID         int64     `json:"id"`
	RunID      int64     `json:"run_id"`
	URL        string    `json:"url"`
	Title      string    `json:"title"`
	Price      float64   `json:"price"`
	Target     float64   `json:"target"`
	LayerUsed  int       `json:"layer_used"`
	Error      string    `json:"error"`
	DurationMs int64     `json:"duration_ms"`
	ScrapedAt  time.Time `json:"scraped_at"`
}

func NewDB(dbPath string) (*DB, error) {
	// Ensure parent directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Set connection limits for sqlite
	conn.SetMaxOpenConns(1)

	d := &DB{conn: conn}
	if err := d.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	return d, nil
}

func (d *DB) Close() error {
	return d.conn.Close()
}

func (d *DB) migrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS products (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			url          TEXT NOT NULL UNIQUE,
			target_price REAL NOT NULL DEFAULT 0,
			source       TEXT NOT NULL DEFAULT 'manual',
			active       INTEGER NOT NULL DEFAULT 1,
			created_at   DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS price_history (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			product_id  INTEGER NOT NULL REFERENCES products(id),
			title       TEXT,
			price       REAL NOT NULL,
			target      REAL NOT NULL,
			layer_used  INTEGER,
			scraped_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS alerts (
			url           TEXT PRIMARY KEY,
			last_alerted  DATETIME NOT NULL,
			alert_type    TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS runs (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			started_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
			finished_at   DATETIME,
			product_count INTEGER,
			alert_count   INTEGER,
			error_count   INTEGER,
			status        TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS scrape_log (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id      INTEGER REFERENCES runs(id),
			url         TEXT NOT NULL,
			title       TEXT,
			price       REAL,
			target      REAL,
			layer_used  INTEGER,
			error       TEXT,
			duration_ms INTEGER,
			scraped_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
	}

	for _, q := range queries {
		if _, err := d.conn.Exec(q); err != nil {
			return err
		}
	}

	// Columns auto-addition migration on startup using ignore
	alterQueries := []string{
		`ALTER TABLE products ADD COLUMN target_price REAL NOT NULL DEFAULT 0;`,
		`ALTER TABLE products ADD COLUMN source TEXT NOT NULL DEFAULT 'manual';`,
		`ALTER TABLE products ADD COLUMN active INTEGER NOT NULL DEFAULT 1;`,
	}
	for _, q := range alterQueries {
		_, _ = d.conn.Exec(q) // Ignore errors if columns already exist
	}

	return nil
}

func (d *DB) MigrateLegacyAlerts(legacyAlertFile string) error {
	if legacyAlertFile == "" {
		return nil
	}
	if _, err := os.Stat(legacyAlertFile); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(legacyAlertFile)
	if err != nil {
		return fmt.Errorf("failed to read legacy alert file: %w", err)
	}

	// Legacy format: {url: iso_timestamp_string}
	var legacyMap map[string]string
	if err := json.Unmarshal(data, &legacyMap); err != nil {
		return fmt.Errorf("failed to parse legacy alert file: %w", err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	count := 0
	for url, timestampStr := range legacyMap {
		t, err := time.Parse(time.RFC3339, timestampStr)
		if err != nil {
			// Fallback: try parsing with custom layout or just use now
			t = time.Now()
		}

		_, err = tx.Exec(`
			INSERT INTO alerts (url, last_alerted, alert_type)
			VALUES (?, ?, ?)
			ON CONFLICT(url) DO UPDATE SET
				last_alerted = excluded.last_alerted,
				alert_type = excluded.alert_type
		`, url, t, "price_drop")
		if err != nil {
			return err
		}
		count++
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	return fmt.Errorf("MIGRATED_OK:%d", count) // returned to help caller log it
}

func (d *DB) GetLastAlerted(url string) (time.Time, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var lastAlerted time.Time
	var alertType string
	err := d.conn.QueryRow("SELECT last_alerted, alert_type FROM alerts WHERE url = ?", url).Scan(&lastAlerted, &alertType)
	if err == sql.ErrNoRows {
		return time.Time{}, "", nil
	}
	return lastAlerted, alertType, err
}

func (d *DB) UpdateLastAlerted(url string, alertType string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`
		INSERT INTO alerts (url, last_alerted, alert_type)
		VALUES (?, ?, ?)
		ON CONFLICT(url) DO UPDATE SET
			last_alerted = excluded.last_alerted,
			alert_type = excluded.alert_type
	`, url, time.Now(), alertType)
	return err
}

func (d *DB) UpsertProduct(url string, targetPrice float64, source string) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var id int64
	var existingSource string
	err := d.conn.QueryRow("SELECT id, source FROM products WHERE url = ?", url).Scan(&id, &existingSource)
	if err == sql.ErrNoRows {
		res, err := d.conn.Exec(`
			INSERT INTO products (url, target_price, source, active)
			VALUES (?, ?, ?, 1)
		`, url, targetPrice, source)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	} else if err != nil {
		return 0, err
	}

	if source == "feed" {
		if existingSource == "feed" {
			_, err = d.conn.Exec("UPDATE products SET target_price = ? WHERE id = ?", targetPrice, id)
			if err != nil {
				return 0, err
			}
		}
	} else {
		_, err = d.conn.Exec(`
			UPDATE products
			SET target_price = ?, source = ?, active = 1
			WHERE id = ?
		`, targetPrice, source, id)
		if err != nil {
			return 0, err
		}
	}

	return id, nil
}

func (d *DB) GetProducts() ([]Product, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query("SELECT id, url, target_price, source, active, created_at FROM products ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var products []Product
	for rows.Next() {
		var p Product
		var activeInt int
		if err := rows.Scan(&p.ID, &p.URL, &p.TargetPrice, &p.Source, &activeInt, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.Active = activeInt != 0
		products = append(products, p)
	}
	return products, nil
}

func (d *DB) GetAllProducts() ([]Product, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query("SELECT id, url, target_price, source, active, created_at FROM products WHERE active = 1 ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var products []Product
	for rows.Next() {
		var p Product
		var activeInt int
		if err := rows.Scan(&p.ID, &p.URL, &p.TargetPrice, &p.Source, &activeInt, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.Active = activeInt != 0
		products = append(products, p)
	}
	return products, nil
}

func (d *DB) DeleteProduct(id int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var url string
	_ = d.conn.QueryRow("SELECT url FROM products WHERE id = ?", id).Scan(&url)

	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if url != "" {
		_, _ = tx.Exec("DELETE FROM scrape_log WHERE url = ?", url)
		_, _ = tx.Exec("DELETE FROM alerts WHERE url = ?", url)
	}
	_, _ = tx.Exec("DELETE FROM price_history WHERE product_id = ?", id)
	_, err = tx.Exec("DELETE FROM products WHERE id = ?", id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) UpdateProductTarget(id int64, targetPrice float64) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`
		UPDATE products
		SET target_price = ?, source = 'manual'
		WHERE id = ?
	`, targetPrice, id)
	return err
}

func (d *DB) SetProductActive(id int64, active bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	activeInt := 0
	if active {
		activeInt = 1
	}
	_, err := d.conn.Exec("UPDATE products SET active = ? WHERE id = ?", activeInt, id)
	return err
}

func (d *DB) InsertPriceHistory(productID int64, title string, price float64, target float64, layerUsed int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`
		INSERT INTO price_history (product_id, title, price, target, layer_used)
		VALUES (?, ?, ?, ?, ?)
	`, productID, title, price, target, layerUsed)
	return err
}

func (d *DB) GetPriceHistory(productID int64, limit int) ([]PricePoint, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(`
		SELECT price, target, scraped_at, layer_used, COALESCE(title, '')
		FROM (
			SELECT price, target, scraped_at, layer_used, title
			FROM price_history
			WHERE product_id = ?
			ORDER BY scraped_at DESC
			LIMIT ?
		)
		ORDER BY scraped_at ASC
	`, productID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []PricePoint
	for rows.Next() {
		var pt PricePoint
		if err := rows.Scan(&pt.Price, &pt.Target, &pt.ScrapedAt, &pt.LayerUsed, &pt.Title); err != nil {
			return nil, err
		}
		history = append(history, pt)
	}
	return history, nil
}

func (d *DB) ToggleProductActive(id int64) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var activeInt int
	err := d.conn.QueryRow("SELECT active FROM products WHERE id = ?", id).Scan(&activeInt)
	if err != nil {
		return false, err
	}

	newActive := 1 - activeInt
	_, err = d.conn.Exec("UPDATE products SET active = ? WHERE id = ?", newActive, id)
	return newActive != 0, err
}

func (d *DB) StartRun() (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	res, err := d.conn.Exec("INSERT INTO runs (started_at, status) VALUES (?, ?)", time.Now(), "running")
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) FinishRun(runID int64, productCount, alertCount, errorCount int, status string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`
		UPDATE runs
		SET finished_at = ?, product_count = ?, alert_count = ?, error_count = ?, status = ?
		WHERE id = ?
	`, time.Now(), productCount, alertCount, errorCount, status, runID)
	return err
}

func (d *DB) InsertScrapeLog(runID int64, url string, title string, price float64, target float64, layerUsed int, errMsg string, durationMs int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var pVal, tVal interface{}
	pVal = price
	tVal = target
	if price < 0 {
		pVal = nil
	}
	if target < 0 {
		tVal = nil
	}

	_, err := d.conn.Exec(`
		INSERT INTO scrape_log (run_id, url, title, price, target, layer_used, error, duration_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, runID, url, title, pVal, tVal, layerUsed, errMsg, durationMs)
	return err
}

func (d *DB) GetRuns(limit int) ([]Run, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(`
		SELECT id, started_at, finished_at, product_count, alert_count, error_count, status
		FROM runs
		ORDER BY id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.StartedAt, &r.FinishedAt, &r.ProductCount, &r.AlertCount, &r.ErrorCount, &r.Status); err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, nil
}

func (d *DB) GetScrapeLogsForRun(runID int64) ([]ScrapeLogEntry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(`
		SELECT id, run_id, url, title, COALESCE(price, -1.0), COALESCE(target, -1.0), COALESCE(layer_used, 0), COALESCE(error, ''), duration_ms, scraped_at
		FROM scrape_log
		WHERE run_id = ?
		ORDER BY id ASC
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []ScrapeLogEntry
	for rows.Next() {
		var entry ScrapeLogEntry
		if err := rows.Scan(&entry.ID, &entry.RunID, &entry.URL, &entry.Title, &entry.Price, &entry.Target, &entry.LayerUsed, &entry.Error, &entry.DurationMs, &entry.ScrapedAt); err != nil {
			return nil, err
		}
		logs = append(logs, entry)
	}
	return logs, nil
}

func (d *DB) GetScrapeLogsForProduct(url string) ([]ScrapeLogEntry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(`
		SELECT id, run_id, url, title, COALESCE(price, -1.0), COALESCE(target, -1.0), COALESCE(layer_used, 0), COALESCE(error, ''), duration_ms, scraped_at
		FROM scrape_log
		WHERE url = ?
		ORDER BY scraped_at DESC
		LIMIT 100
	`, url)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []ScrapeLogEntry
	for rows.Next() {
		var entry ScrapeLogEntry
		if err := rows.Scan(&entry.ID, &entry.RunID, &entry.URL, &entry.Title, &entry.Price, &entry.Target, &entry.LayerUsed, &entry.Error, &entry.DurationMs, &entry.ScrapedAt); err != nil {
			return nil, err
		}
		logs = append(logs, entry)
	}
	return logs, nil
}
