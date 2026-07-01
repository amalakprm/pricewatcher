package web

import (
	"html/template"
	"time"
)

// BasePage contains layout metadata that is shared across all pages.
type BasePage struct {
	Title      string
	ActivePage string        // "dashboard", "products", "logs", "settings"
	Status     StatusSummary // Polled scraper status
	LastRun    RunSummary    // Metadata for the last execution cycle
	NextRun    string        // Next trigger schedule label
}

// StatusSummary aggregates watchlist metrics and scraper active state.
type StatusSummary struct {
	TotalProducts int
	DealsFound    int
	FailedScrapes int
	Status        string // "Ready", "Running", "Idle"
}

// RunSummary represents a minimal run cycle info structure.
type RunSummary struct {
	ID         int64
	Status     string
	FinishedAt time.Time
}

// DashboardPage defines the context mapped to dashboard.html.
type DashboardPage struct {
	BasePage
	Products []ProductCard
}

// ProductsPage defines the context mapped to products.html.
type ProductsPage struct {
	BasePage
	Products []ProductCard
}

// ProductCard represents a single item on the list grids.
type ProductCard struct {
	ID                  int64
	URL                 string
	Base64URL           string
	ShortURL            string
	Site                string
	Title               string
	CurrentPrice        float64
	TargetPrice         float64
	IsOnTarget          bool
	Active              bool
	Status              string // "active", "paused", "removed"
	Source              string // "manual", "feed"
	CustomTitle         string
	Notes               string
	TimeSinceLastScrape string
	LayerUsed           int
}

// ProductPage defines the context mapped to product.html (single-product detail).
type ProductPage struct {
	BasePage
	Product ProductDetails
}

// ProductDetails represents extensive metrics for a single item.
type ProductDetails struct {
	ID           int64
	URL          string
	Base64URL    string
	ShortURL     string
	Title        string
	CurrentPrice float64
	TargetPrice  float64
	Status       string // "active", "paused", "removed"
	Source       string // "manual", "feed"
	CustomTitle  string
	Notes        string
	HistoryJSON  template.JS
	History      []PricePoint      // Price history points list
	Logs         []ScrapeLogRecord // Scrape logs history list
}

// PricePoint maps price changes.
type PricePoint struct {
	ScrapedAt   time.Time
	Price       float64
	TargetPrice float64
}

// ScrapeLogRecord maps single scrape log execution history.
type ScrapeLogRecord struct {
	ScrapedAt time.Time
	LayerUsed int
	Error     string
}

// LogsPage defines the context mapped to logs.html.
type LogsPage struct {
	BasePage
	Logs []LogEntry
}

// LogEntry represents a single structured log entry inside the ring buffer.
type LogEntry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"msg"`
}

// SettingsPage defines the context mapped to settings.html.
type SettingsPage struct {
	BasePage
	Config UIConfig
}

// UIConfig represents the settings fields exposed in the configuration form.
type UIConfig struct {
	FeedURL         string
	CronSchedule    string
	BrowserEndpoint string
	AlertCooldown   string // Apprise URL
	WorkerCount     int
}
