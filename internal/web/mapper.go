package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"time"

	"github.com/robfig/cron/v3"
	"pricewatcher/internal/config"
	"pricewatcher/internal/db"
	"pricewatcher/internal/scraper"
)

// BuildBasePage constructs the shared metadata page wrap.
func BuildBasePage(title string, activePage string, srvDb *db.DB, cfg *config.Config, scheduler *cron.Cron, running bool) BasePage {
	products, _ := srvDb.GetProducts()
	
	// Compute counts
	total := len(products)
	deals := 0
	fails := 0

	for _, p := range products {
		history, err := srvDb.GetPriceHistory(p.ID, 1)
		if err == nil && len(history) > 0 {
			lastPrice := history[len(history)-1].Price
			if lastPrice > 0 && lastPrice <= p.TargetPrice {
				deals++
			}
			if history[len(history)-1].LayerUsed == 0 {
				fails++
			}
		}
	}

	status := "Ready"
	if running {
		status = "Running"
	}

	runs, _ := srvDb.GetRuns(1)
	var lastRun RunSummary
	if len(runs) > 0 {
		var finished time.Time
		if runs[0].FinishedAt != nil {
			finished = *runs[0].FinishedAt
		}
		lastRun = RunSummary{
			ID:         runs[0].ID,
			Status:     runs[0].Status,
			FinishedAt: finished,
		}
	}

	nextRun := "unscheduled"
	if entries := scheduler.Entries(); len(entries) > 0 {
		nextRun = entries[0].Next.Format("2006-01-02 15:04:05")
	}

	return BasePage{
		Title:      title,
		ActivePage: activePage,
		Status: StatusSummary{
			TotalProducts: total,
			DealsFound:    deals,
			FailedScrapes: fails,
			Status:        status,
		},
		LastRun: lastRun,
		NextRun: nextRun,
	}
}

// BuildDashboardPage constructs the context for dashboard page rendering.
func BuildDashboardPage(srvDb *db.DB, cfg *config.Config, scheduler *cron.Cron, running bool) (DashboardPage, error) {
	base := BuildBasePage("Dashboard", "dashboard", srvDb, cfg, scheduler, running)

	dbProducts, err := srvDb.GetProducts()
	if err != nil {
		return DashboardPage{}, err
	}

	cards := MapProductsToCards(srvDb, dbProducts)

	return DashboardPage{
		BasePage: base,
		Products: cards,
	}, nil
}

// BuildProductsPage constructs the context for products watchlist rendering.
func BuildProductsPage(srvDb *db.DB, cfg *config.Config, scheduler *cron.Cron, running bool) (ProductsPage, error) {
	base := BuildBasePage("Products Watchlist", "products", srvDb, cfg, scheduler, running)

	dbProducts, err := srvDb.GetProducts()
	if err != nil {
		return ProductsPage{}, err
	}

	cards := MapProductsToCards(srvDb, dbProducts)

	return ProductsPage{
		BasePage: base,
		Products: cards,
	}, nil
}

// BuildProductPage constructs the details context for single-product views.
func BuildProductPage(productURL string, srvDb *db.DB, cfg *config.Config, scheduler *cron.Cron, running bool) (ProductPage, error) {
	base := BuildBasePage("Product Details", "products", srvDb, cfg, scheduler, running)

	products, err := srvDb.GetProducts()
	if err != nil {
		return ProductPage{}, err
	}

	var product *db.Product
	for _, p := range products {
		if p.URL == productURL {
			product = &p
			break
		}
	}

	if product == nil {
		return ProductPage{}, nil
	}

	history, err := srvDb.GetPriceHistory(product.ID, 90)
	if err != nil {
		history = []db.PricePoint{}
	}
	
	logs, _ := srvDb.GetScrapeLogsForProduct(productURL)

	title := ""
	currentPrice := 0.0
	if len(history) > 0 {
		title = history[len(history)-1].Title
		currentPrice = history[len(history)-1].Price
	} else if len(logs) > 0 {
		title = logs[0].Title
	}

	shortURL := productURL
	if len(shortURL) > 40 {
		shortURL = shortURL[:40] + "..."
	}

	// Chart history serialization
	type chartPoint struct {
		ScrapedAt string  `json:"scraped_at"`
		Price     float64 `json:"price"`
		Target    float64 `json:"target"`
	}
	var chartPoints []chartPoint
	var uiHistory []PricePoint

	for _, p := range history {
		chartPoints = append(chartPoints, chartPoint{
			ScrapedAt: p.ScrapedAt.Format(time.RFC3339),
			Price:     p.Price,
			Target:    p.Target,
		})
		uiHistory = append(uiHistory, PricePoint{
			ScrapedAt:   p.ScrapedAt,
			Price:       p.Price,
			TargetPrice: p.Target,
		})
	}

	var uiLogs []ScrapeLogRecord
	for _, l := range logs {
		uiLogs = append(uiLogs, ScrapeLogRecord{
			ScrapedAt: l.ScrapedAt,
			LayerUsed: l.LayerUsed,
			Error:     l.Error,
		})
	}

	historyJSON, _ := json.Marshal(chartPoints)

	details := ProductDetails{
		ID:           product.ID,
		URL:          productURL,
		Base64URL:    base64.URLEncoding.EncodeToString([]byte(productURL)),
		ShortURL:     shortURL,
		Title:        title,
		CurrentPrice: currentPrice,
		TargetPrice:  product.TargetPrice,
		HistoryJSON:  template.JS(historyJSON),
		History:      uiHistory,
		Logs:         uiLogs,
	}

	return ProductPage{
		BasePage: base,
		Product:  details,
	}, nil
}

// BuildLogsPage maps structure arrays for logs tables.
func BuildLogsPage(logRingBuffer *LogRingBuffer, srvDb *db.DB, cfg *config.Config, scheduler *cron.Cron, running bool) LogsPage {
	base := BuildBasePage("Logs", "logs", srvDb, cfg, scheduler, running)
	entries := logRingBuffer.GetEntries()

	return LogsPage{
		BasePage: base,
		Logs:     entries,
	}
}

// BuildSettingsPage groups system configurations panel.
func BuildSettingsPage(cfg *config.Config, srvDb *db.DB, scheduler *cron.Cron, running bool) SettingsPage {
	base := BuildBasePage("Settings", "settings", srvDb, cfg, scheduler, running)

	uiConfig := UIConfig{
		FeedURL:         cfg.FeedURL,
		CronSchedule:    cfg.CronSchedule,
		BrowserEndpoint: cfg.CloakBrowserCDP,
		AlertCooldown:   cfg.AppriseURL,
		WorkerCount:     cfg.MaxHTTPConcurrent,
	}

	return SettingsPage{
		BasePage: base,
		Config:   uiConfig,
	}
}

// Helper: map db.Product items list to ProductCard array.
func MapProductsToCards(srvDb *db.DB, dbProducts []db.Product) []ProductCard {
	var cards []ProductCard
	for _, p := range dbProducts {
		history, err := srvDb.GetPriceHistory(p.ID, 7)
		if err != nil {
			continue
		}

		var currentPrice float64
		var layerUsed int
		var title string
		var prices []float64
		var scrapedAt time.Time

		for _, pt := range history {
			prices = append(prices, pt.Price)
		}

		if len(history) > 0 {
			last := history[len(history)-1]
			currentPrice = last.Price
			layerUsed = last.LayerUsed
			title = last.Title
			scrapedAt = last.ScrapedAt
		}

		shortURL := p.URL
		if len(shortURL) > 30 {
			shortURL = shortURL[:30] + "..."
		}

		timeSince := "never"
		if !scrapedAt.IsZero() {
			timeSince = formatTimeSince(scrapedAt)
		}

		cards = append(cards, ProductCard{
			ID:                  p.ID,
			URL:                 p.URL,
			Base64URL:           base64.URLEncoding.EncodeToString([]byte(p.URL)),
			ShortURL:            shortURL,
			Site:                scraper.DetectSite(p.URL),
			Title:               title,
			CurrentPrice:        currentPrice,
			TargetPrice:         p.TargetPrice,
			IsOnTarget:          currentPrice > 0 && currentPrice <= p.TargetPrice,
			Active:              p.Active,
			TimeSinceLastScrape: timeSince,
			LayerUsed:           layerUsed,
		})
	}
	return cards
}

func formatTimeSince(t time.Time) string {
	diff := time.Since(t)
	if diff < time.Minute {
		return "just now"
	}
	if diff < time.Hour {
		return fmt.Sprintf("%dm ago", int(diff.Minutes()))
	}
	if diff < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(diff.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(diff.Hours()/24))
}
