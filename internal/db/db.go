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

// Product represents a tracked product in the database.
// Status values: "active" (scraping enabled), "paused" (manually paused),
// "removed" (soft-deleted because it was removed from the Google Sheet feed).
type Product struct {
	ID             int64     `json:"id"`
	URL            string    `json:"url"`
	Title          string    `json:"title"`           // optional custom display title
	TargetPrice    float64   `json:"target_price"`
	Source         string    `json:"source"`          // "feed" or "manual"
	Status         string    `json:"status"`          // "active", "paused", "removed"
	RemovalPending bool      `json:"removal_pending"` // grace-period flag before full removal
	Active         bool      `json:"active"`          // derived: Status == "active"
	CreatedAt      time.Time `json:"created_at"`
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
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			url              TEXT NOT NULL UNIQUE,
			title            TEXT NOT NULL DEFAULT '',
			target_price     REAL NOT NULL DEFAULT 0,
			source           TEXT NOT NULL DEFAULT 'manual',
			status           TEXT NOT NULL DEFAULT 'active',
			removal_pending  INTEGER NOT NULL DEFAULT 0,
			active           INTEGER NOT NULL DEFAULT 1,
			created_at       DATETIME DEFAULT CURRENT_TIMESTAMP
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
		`CREATE TABLE IF NOT EXISTS settings (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
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
		`ALTER TABLE products ADD COLUMN title TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE products ADD COLUMN status TEXT NOT NULL DEFAULT 'active';`,
		`ALTER TABLE products ADD COLUMN removal_pending INTEGER NOT NULL DEFAULT 0;`,
	}
	for _, q := range alterQueries {
		_, _ = d.conn.Exec(q) // Ignore errors if columns already exist
	}

	// Data migration: sync status column from active for existing rows
	_, _ = d.conn.Exec(`UPDATE products SET status = 'paused' WHERE active = 0 AND status = 'active'`)

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
	var existingSource, existingStatus string
	err := d.conn.QueryRow("SELECT id, source, status FROM products WHERE url = ?", url).Scan(&id, &existingSource, &existingStatus)
	if err == sql.ErrNoRows {
		res, err := d.conn.Exec(`
			INSERT INTO products (url, target_price, source, status, active)
			VALUES (?, ?, ?, 'active', 1)
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
			// Update target price and clear any pending-removal grace flag.
			// The CASE keeps active/paused status intact; only re-activates 'removed' products
			// that reappear in the feed (they were removed by a prior sync but are back now).
			_, err = d.conn.Exec(`
				UPDATE products
				SET target_price = ?, removal_pending = 0, status = CASE WHEN status = 'removed' THEN 'active' ELSE status END, active = 1
				WHERE id = ?
			`, targetPrice, id)
			if err != nil {
				return 0, err
			}
		}
		// Do not overwrite manually-added products from a feed sync
	} else {
		_, err = d.conn.Exec(`
			UPDATE products
			SET target_price = ?, source = ?, status = 'active', active = 1
			WHERE id = ?
		`, targetPrice, source, id)
		if err != nil {
			return 0, err
		}
	}

	return id, nil
}

// SyncFeedProducts diffs the incoming feed against existing feed-sourced products.
// Products present in the feed are upserted; feed-sourced products missing from the
// feed for the first time get the grace flag set (removal_pending=1), and those
// already flagged get soft-deleted (status='removed'). Manually-added products are
// never touched by this function.
func (d *DB) SyncFeedProducts(items []FeedItem) (synced, removed int, err error) {
	// Build a set of incoming URLs
	incoming := make(map[string]bool, len(items))
	for _, item := range items {
		incoming[item.URL] = true
	}

	// Upsert each incoming item
	for _, item := range items {
		_, upsertErr := d.UpsertProduct(item.URL, item.Price, "feed")
		if upsertErr != nil {
			err = upsertErr
			return
		}
		synced++
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// Find all active/paused feed-sourced products NOT in the new feed
	rows, queryErr := d.conn.Query(`
		SELECT id, url, removal_pending
		FROM products
		WHERE source = 'feed' AND status != 'removed'
	`)
	if queryErr != nil {
		err = queryErr
		return
	}
	defer rows.Close()

	type pendingRow struct {
		id             int64
		url            string
		removalPending bool
	}
	var toCheck []pendingRow
	for rows.Next() {
		var pr pendingRow
		var rp int
		if scanErr := rows.Scan(&pr.id, &pr.url, &rp); scanErr != nil {
			err = scanErr
			return
		}
		pr.removalPending = rp != 0
		toCheck = append(toCheck, pr)
	}
	rows.Close()

	for _, pr := range toCheck {
		if incoming[pr.url] {
			continue // still present in feed — clear any grace flag
		}
		if pr.removalPending {
			// Second consecutive sync without this product → soft-delete
			_, execErr := d.conn.Exec(`
				UPDATE products SET status = 'removed', active = 0
				WHERE id = ?
			`, pr.id)
			if execErr != nil {
				err = execErr
				return
			}
			removed++
		} else {
			// First time missing — set grace flag
			_, execErr := d.conn.Exec(`
				UPDATE products SET removal_pending = 1
				WHERE id = ?
			`, pr.id)
			if execErr != nil {
				err = execErr
				return
			}
		}
	}

	return synced, removed, nil
}

// FeedItem is a local alias used by SyncFeedProducts to avoid an import cycle.
// The actual feed.FeedItem is expected to match this shape.
type FeedItem struct {
	URL   string
	Price float64
}

func (d *DB) GetProducts() ([]Product, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(`
		SELECT id, url, COALESCE(title,''), target_price, source, COALESCE(status,'active'), COALESCE(removal_pending,0), active, created_at
		FROM products ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var products []Product
	for rows.Next() {
		var p Product
		var activeInt, rp int
		if err := rows.Scan(&p.ID, &p.URL, &p.Title, &p.TargetPrice, &p.Source, &p.Status, &rp, &activeInt, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.RemovalPending = rp != 0
		p.Active = p.Status == "active"
		products = append(products, p)
	}
	return products, nil
}

func (d *DB) GetAllProducts() ([]Product, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(`
		SELECT id, url, COALESCE(title,''), target_price, source, COALESCE(status,'active'), COALESCE(removal_pending,0), active, created_at
		FROM products WHERE status = 'active' ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var products []Product
	for rows.Next() {
		var p Product
		var activeInt, rp int
		if err := rows.Scan(&p.ID, &p.URL, &p.Title, &p.TargetPrice, &p.Source, &p.Status, &rp, &activeInt, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.RemovalPending = rp != 0
		p.Active = p.Status == "active"
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

// UpdateProductTarget changes only the target price for a product.
// It intentionally does not modify the source field so that feed-managed
// products are not accidentally converted to manually-managed ones.
func (d *DB) UpdateProductTarget(id int64, targetPrice float64) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`
		UPDATE products
		SET target_price = ?
		WHERE id = ?
	`, targetPrice, id)
	return err
}

// UpdateProduct edits the user-visible fields of a product.
// Pass an empty title string to leave it unchanged.
func (d *DB) UpdateProduct(id int64, title string, targetPrice float64, status string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	active := 1
	if status == "removed" || status == "paused" {
		active = 0
	}
	_, err := d.conn.Exec(`
		UPDATE products
		SET title = ?, target_price = ?, status = ?, active = ?
		WHERE id = ?
	`, title, targetPrice, status, active, id)
	return err
}

func (d *DB) SetProductActive(id int64, active bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	status := "paused"
	activeInt := 0
	if active {
		status = "active"
		activeInt = 1
	}
	_, err := d.conn.Exec("UPDATE products SET active = ?, status = ? WHERE id = ?", activeInt, status, id)
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

	var currentStatus string
	err := d.conn.QueryRow("SELECT COALESCE(status,'active') FROM products WHERE id = ?", id).Scan(&currentStatus)
	if err != nil {
		return false, err
	}

	newActive := currentStatus != "active"
	newStatus := "paused"
	newActiveInt := 0
	if newActive {
		newStatus = "active"
		newActiveInt = 1
	}
	_, err = d.conn.Exec("UPDATE products SET active = ?, status = ? WHERE id = ?", newActiveInt, newStatus, id)
	return newActive, err
}

// GetSetting retrieves a persisted setting value from the DB. Returns "" if not found.
func (d *DB) GetSetting(key string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var value string
	err := d.conn.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// SaveSetting persists a key-value setting to the DB.
func (d *DB) SaveSetting(key, value string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
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
