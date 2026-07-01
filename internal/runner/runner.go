package runner

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"pricewatcher/internal/config"
	"pricewatcher/internal/db"
	"pricewatcher/internal/feed"
	"pricewatcher/internal/notify"
	"pricewatcher/internal/scraper"
	"pricewatcher/internal/urlutil"
)

// maxRunTimeout is the maximum time a single scrape cycle is allowed to run.
const maxRunTimeout = 45 * time.Minute

// RunOnce executes a single run cycle: fetch feed, scrape products, alert, and log.
func RunOnce(ctx context.Context, database *db.DB, cfg *config.Config) error {
	slog.Info("Starting scrape run")
	startTime := time.Now()

	// 1. Check if CloakBrowser CDP is reachable
	skipL3 := false
	cdpClient := &http.Client{Timeout: 5 * time.Second}
	if err := urlutil.ValidateHTTP(cfg.CloakBrowserCDP); err != nil {
		slog.Warn("CloakBrowser CDP URL invalid, skipping Layer 3 for this run", "error", err)
		skipL3 = true
	} else if respCdp, errCdp := cdpClient.Get(cfg.CloakBrowserCDP); errCdp != nil {
		slog.Warn("CloakBrowser CDP unreachable, skipping Layer 3 for this run", "error", errCdp)
		skipL3 = true
	} else {
		respCdp.Body.Close()
	}

	// Create a sub-context with the skipL3 flag
	runCtx := context.WithValue(ctx, "skip_l3", skipL3)

	// Start run entry in DB
	runID, err := database.StartRun()
	if err != nil {
		slog.Error("Failed to start run in database", "error", err)
	}
	
	if runID > 0 {
		runCtx = context.WithValue(runCtx, "run_id", runID)
	}

	httpClient := &http.Client{Timeout: cfg.HTTPTimeout}

	// 2. Sync from GAS feed (if FeedURL is set)
	if cfg.FeedURL != "" {
		feedItems, err := feed.FetchFeed(runCtx, cfg.FeedURL, httpClient)
		if err != nil {
			slog.Error("Feed fetch failed, aborting run", "error", err)
			notify.SendNotification(runCtx, cfg.AppriseURL, "⚠️ PriceWatcher: feed fetch failed", fmt.Sprintf("Error: %v", err))
			if runID > 0 {
				if dbErr := database.FinishRun(runID, 0, 0, 0, "failed"); dbErr != nil {
					slog.Error("Failed to update run status in database", "error", dbErr)
				}
			}
			return err
		}

		slog.Info("Feed fetched successfully, syncing to DB", "count", len(feedItems))
		syncedCount := 0
		var feedURLs []string
		for _, item := range feedItems {
			feedURLs = append(feedURLs, item.URL)
			_, dbErr := database.UpsertProduct(item.URL, item.Price, "feed")
			if dbErr != nil {
				slog.Error("Failed to upsert feed product in database", "url", item.URL, "error", dbErr)
			} else {
				syncedCount++
			}
		}
		slog.Info("Synced feed products to DB", "synced", syncedCount)

		// Soft-delete feed products that are no longer in the sheet
		if removedCount, rmErr := database.MarkRemovedFeedProducts(feedURLs); rmErr != nil {
			slog.Error("Failed to mark removed feed products", "error", rmErr)
		} else if removedCount > 0 {
			slog.Info("Marked feed products as removed (not in latest feed)", "count", removedCount)
		}
	}

	// 3. Get products to scrape from DB
	dbProducts, err := database.GetAllProducts()
	if err != nil {
		slog.Error("Failed to load products from database", "error", err)
		if runID > 0 {
			_ = database.FinishRun(runID, 0, 0, 0, "failed")
		}
		return err
	}

	productCount := len(dbProducts)
	slog.Info("Scrape queue loaded from DB", "product_count", productCount)

	var scrapeProducts []scraper.Product
	for _, p := range dbProducts {
		scrapeProducts = append(scrapeProducts, scraper.Product{
			ID:     p.ID,
			URL:    p.URL,
			Target: p.TargetPrice,
		})
	}

	// 4. Scrape concurrently
	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.MaxHTTPConcurrent)
	results := make([]scraper.ScrapeResult, len(scrapeProducts))

	for i, prod := range scrapeProducts {
		wg.Add(1)
		go func(idx int, p scraper.Product) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			res := scraper.ScrapeProduct(runCtx, p, httpClient, cfg)
			results[idx] = res
		}(i, prod)
	}

	wg.Wait()

	// 5. Process results
	alertCount := 0
	errorCount := 0

	for i, res := range results {
		prod := scrapeProducts[i]
		durationMs := res.Duration.Milliseconds()

		if res.Error != "" {
			errorCount++
			// Scrape failed
			dbErr := database.InsertScrapeLog(runID, res.URL, "", -1, res.Target, 0, res.Error, durationMs)
			if dbErr != nil {
				slog.Error("Failed to insert scrape log in database", "url", res.URL, "error", dbErr)
			}

			// Cooldown check for failure alert
			if shouldAlert(res.URL, database, cfg.AlertCooldownHrs) {
				notify.SendNotification(runCtx, cfg.AppriseURL,
					"⚠️ PriceWatcher scrape failed",
					fmt.Sprintf("Could not get price for:\n%s", res.URL),
				)
				alertCount++
				dbErr = database.UpdateLastAlerted(res.URL, "scrape_failed")
				if dbErr != nil {
					slog.Error("Failed to update alert state in database", "url", res.URL, "error", dbErr)
				}
				slog.Info("Alert sent", "url", res.URL, "alert_type", "scrape_failed", "run_id", runID)
			}
		} else {
			// Scrape OK
			dbErr := database.InsertScrapeLog(runID, res.URL, res.Title, res.Price, res.Target, res.LayerUsed, "", durationMs)
			if dbErr != nil {
				slog.Error("Failed to insert scrape log in database", "url", res.URL, "error", dbErr)
			}

			dbErr = database.InsertPriceHistory(prod.ID, res.Title, res.Price, res.Target, res.LayerUsed)
			if dbErr != nil {
				slog.Error("Failed to insert price history in database", "url", res.URL, "error", dbErr)
			}

			// Cooldown check for price drop
			if res.Price <= res.Target {
				if shouldAlert(res.URL, database, cfg.AlertCooldownHrs) {
					titleLimit := safeSlice(res.Title, 40)
					notify.SendNotification(runCtx, cfg.AppriseURL,
						fmt.Sprintf("💰 Price Drop: %s", titleLimit),
						fmt.Sprintf("%s\nNow: ₹%.2f  Target: ₹%.2f\n%s", res.Title, res.Price, res.Target, res.URL),
					)
					alertCount++
					dbErr = database.UpdateLastAlerted(res.URL, "price_drop")
					if dbErr != nil {
						slog.Error("Failed to update alert state in database", "url", res.URL, "error", dbErr)
					}
					slog.Info("Alert sent", "url", res.URL, "price", res.Price, "target", res.Target, "alert_type", "price_drop", "run_id", runID)
				}
			}
		}
	}

	// Update run status
	status := "ok"
	if errorCount > 0 {
		if errorCount == productCount {
			status = "failed"
		} else {
			status = "partial"
		}
	}

	if runID > 0 {
		if dbErr := database.FinishRun(runID, productCount, alertCount, errorCount, status); dbErr != nil {
			slog.Error("Failed to finish run in database", "run_id", runID, "error", dbErr)
		}
	}

	slog.Info("Run finished",
		slog.Int64("run_id", runID),
		slog.Int64("duration_ms", time.Since(startTime).Milliseconds()),
		slog.Int("alerts", alertCount),
		slog.Int("errors", errorCount),
		slog.String("status", status),
	)

	return nil
}

func shouldAlert(url string, db *db.DB, cooldownHours int) bool {
	lastAlerted, _, err := db.GetLastAlerted(url)
	if err != nil {
		slog.Error("Failed to check alert cooldown from database", "url", url, "error", err)
		return true // default to alerting if DB query fails
	}
	if lastAlerted.IsZero() {
		return true
	}
	return time.Since(lastAlerted) > time.Duration(cooldownHours)*time.Hour
}

func safeSlice(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen]) + "..."
	}
	return s
}
