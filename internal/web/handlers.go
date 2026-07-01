package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pricewatcher/internal/db"
	"pricewatcher/internal/feed"
	"pricewatcher/internal/notify"
	"pricewatcher/internal/runner"
	"pricewatcher/internal/scraper"
)

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	s.runningMu.Lock()
	running := s.running
	s.runningMu.Unlock()

	data, err := BuildDashboardPage(s.db, s.cfg, s.cron, running)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := s.templates.ExecuteTemplate(w, "dashboard.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleProducts(w http.ResponseWriter, r *http.Request) {
	s.runningMu.Lock()
	running := s.running
	s.runningMu.Unlock()

	data, err := BuildProductsPage(s.db, s.cfg, s.cron, running)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := s.templates.ExecuteTemplate(w, "products.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleProductDetail(w http.ResponseWriter, r *http.Request) {
	urlB64 := r.PathValue("url_b64")
	urlBytes, err := base64.URLEncoding.DecodeString(urlB64)
	if err != nil {
		http.Error(w, "invalid base64 url", http.StatusBadRequest)
		return
	}
	productURL := string(urlBytes)

	s.runningMu.Lock()
	running := s.running
	s.runningMu.Unlock()

	data, err := BuildProductPage(productURL, s.db, s.cfg, s.cron, running)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if data.Product.ID == 0 {
		http.Error(w, "product not found", http.StatusNotFound)
		return
	}

	if err := s.templates.ExecuteTemplate(w, "product.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	s.runningMu.Lock()
	running := s.running
	s.runningMu.Unlock()

	data := BuildLogsPage(s.logBuffer, s.db, s.cfg, s.cron, running)
	if err := s.templates.ExecuteTemplate(w, "logs.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	s.runningMu.Lock()
	running := s.running
	s.runningMu.Unlock()

	data := BuildSettingsPage(s.cfg, s.db, s.cron, running)
	if err := s.templates.ExecuteTemplate(w, "settings.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleRunDetailsPartial(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	runID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid run id", http.StatusBadRequest)
		return
	}

	logs, err := s.db.GetScrapeLogsForRun(runID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<tr id="run-details-%d" class="bg-slate-950/40">
		<td colspan="7" class="px-6 py-4 border-b border-slate-800">
			<div class="space-y-2 p-2">
				<div class="flex justify-between items-center">
					<h4 class="font-bold text-white text-xs">Scrape records for run #%d:</h4>
					<button class="text-xs text-slate-500 hover:text-slate-400 font-semibold" 
					        onclick="document.getElementById('run-details-%d').remove()">Close Details</button>
				</div>
				<div class="overflow-x-auto border border-slate-800 rounded-lg">
					<table class="min-w-full divide-y divide-slate-800 font-mono text-[10px] bg-slate-950/20">
						<thead class="bg-slate-900 text-slate-400">
							<tr>
								<th class="px-4 py-2 text-left">URL</th>
								<th class="px-4 py-2 text-left">Title</th>
								<th class="px-4 py-2 text-right">Price</th>
								<th class="px-4 py-2 text-right">Target</th>
								<th class="px-4 py-2 text-center">Layer</th>
								<th class="px-4 py-2 text-right">Time</th>
								<th class="px-4 py-2 text-left">Error</th>
							</tr>
						</thead>
						<tbody class="divide-y divide-slate-850 text-slate-300">`, runID, runID, runID)

	for _, entry := range logs {
		priceStr := fmt.Sprintf("₹%.2f", entry.Price)
		if entry.Price < 0 {
			priceStr = "N/A"
		}
		layerStr := fmt.Sprintf("L%d", entry.LayerUsed)
		if entry.LayerUsed == 0 {
			layerStr = "FAIL"
		}
		errorClass := ""
		if entry.Error != "" {
			errorClass = "text-rose-400"
		}

		fmt.Fprintf(w, `<tr class="hover:bg-slate-800/20">
			<td class="px-4 py-2 truncate max-w-xs" title="%s">%s</td>
			<td class="px-4 py-2 truncate max-w-xs" title="%s">%s</td>
			<td class="px-4 py-2 text-right text-white font-bold">%s</td>
			<td class="px-4 py-2 text-right">₹%.2f</td>
			<td class="px-4 py-2 text-center">%s</td>
			<td class="px-4 py-2 text-right">%dms</td>
			<td class="px-4 py-2 %s truncate max-w-xs" title="%s">%s</td>
		</tr>`, entry.URL, entry.URL, entry.Title, entry.Title, priceStr, entry.Target, layerStr, entry.DurationMs, errorClass, entry.Error, entry.Error)
	}

	if len(logs) == 0 {
		fmt.Fprint(w, `<tr><td colspan="7" class="px-4 py-4 text-center text-slate-500">No logs for this run.</td></tr>`)
	}

	fmt.Fprint(w, `</tbody></table></div></div></td></tr>`)
}

func (s *Server) handleAPIRun(w http.ResponseWriter, r *http.Request) {
	s.runningMu.Lock()
	if s.running {
		s.runningMu.Unlock()
		http.Error(w, "scraper is already running", http.StatusConflict)
		return
	}
	s.running = true
	s.runningMu.Unlock()

	urlB64 := r.URL.Query().Get("url")

	go func() {
		defer func() {
			s.runningMu.Lock()
			s.running = false
			s.runningMu.Unlock()
		}()

		ctx := context.Background()

		if urlB64 != "" {
			urlBytes, err := base64.URLEncoding.DecodeString(urlB64)
			if err != nil {
				slog.Error("API single run base64 decode failed", "error", err)
				return
			}
			targetURL := string(urlBytes)
			slog.Info("Starting manual single product scrape", "url", targetURL)

			products, err := s.db.GetProducts()
			if err != nil {
				slog.Error("API single run product lookup failed", "error", err)
				return
			}
			
			var prod *db.Product
			for _, p := range products {
				if p.URL == targetURL {
					prod = &p
					break
				}
			}

			if prod == nil {
				slog.Error("API single run product not found in db", "url", targetURL)
				return
			}

			targetPrice := prod.TargetPrice

			runID, err := s.db.StartRun()
			if err != nil {
				slog.Error("API single run start in DB failed", "error", err)
			}

			runCtx := context.WithValue(ctx, "run_id", runID)

			skipL3 := false
			cdpClient := &http.Client{Timeout: 5 * time.Second}
			respCdp, errCdp := cdpClient.Get(s.cfg.CloakBrowserCDP)
			if errCdp != nil {
				slog.Warn("CloakBrowser CDP unreachable, skipping Layer 3 for single run", "error", errCdp)
				skipL3 = true
			} else {
				respCdp.Body.Close()
			}
			runCtx = context.WithValue(runCtx, "skip_l3", skipL3)

			httpClient := &http.Client{Timeout: s.cfg.HTTPTimeout}
			res := scraper.ScrapeProduct(runCtx, scraper.Product{
				ID:     prod.ID,
				URL:    prod.URL,
				Target: targetPrice,
			}, httpClient, s.cfg)

			durationMs := res.Duration.Milliseconds()
			status := "ok"

			if res.Error != "" {
				status = "failed"
				_ = s.db.InsertScrapeLog(runID, res.URL, "", -1, targetPrice, 0, res.Error, durationMs)

				if shouldAlert(res.URL, s.db, s.cfg.AlertCooldownHrs) {
					notify.SendNotification(runCtx, s.cfg.AppriseURL,
						"⚠️ PriceWatcher scrape failed",
						fmt.Sprintf("Could not get price for:\n%s", res.URL),
					)
					_ = s.db.UpdateLastAlerted(res.URL, "scrape_failed")
				}
			} else {
				_ = s.db.InsertScrapeLog(runID, res.URL, res.Title, res.Price, targetPrice, res.LayerUsed, "", durationMs)
				_ = s.db.InsertPriceHistory(prod.ID, res.Title, res.Price, targetPrice, res.LayerUsed)

				if res.Price <= targetPrice {
					if shouldAlert(res.URL, s.db, s.cfg.AlertCooldownHrs) {
						titleLimit := safeSlice(res.Title, 40)
						notify.SendNotification(runCtx, s.cfg.AppriseURL,
							fmt.Sprintf("💰 Price Drop: %s", titleLimit),
							fmt.Sprintf("%s\nNow: ₹%.2f  Target: ₹%.2f\n%s", res.Title, res.Price, targetPrice, res.URL),
						)
						_ = s.db.UpdateLastAlerted(res.URL, "price_drop")
					}
				}
			}

			if runID > 0 {
				_ = s.db.FinishRun(runID, 1, 0, 0, status)
			}
			slog.Info("Single product scrape finished", "url", targetURL, "status", status)

		} else {
			_ = runner.RunOnce(ctx, s.db, s.cfg)
		}
	}()

	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("run initiated"))
}

func (s *Server) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	s.runningMu.Lock()
	running := s.running
	s.runningMu.Unlock()

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/html")
		if running {
			fmt.Fprint(w, `<div id="status-badge" hx-get="/api/status" hx-trigger="every 5s" hx-swap="outerHTML" class="inline-flex items-center space-x-2">
				<span class="relative flex h-2 w-2">
					<span class="animate-ping absolute inline-flex h-full w-full rounded-full bg-amber-400 opacity-75"></span>
					<span class="relative inline-flex rounded-full h-2 w-2 bg-amber-500"></span>
				</span>
				<span class="inline-flex items-center px-3 py-1 rounded-full text-xs font-semibold bg-amber-500/10 text-amber-400 border border-amber-500/20 font-sans">
					Scraping Running...
				</span>
			</div>`)
		} else {
			fmt.Fprint(w, `<div id="status-badge" hx-get="/api/status" hx-trigger="every 5s" hx-swap="outerHTML">
				<span class="inline-flex items-center px-3 py-1 rounded-full text-xs font-semibold bg-emerald-500/10 text-emerald-400 border border-emerald-500/20 font-sans">
					Ready
				</span>
			</div>`)
		}
		return
	}

	runs, _ := s.db.GetRuns(1)
	var lastRun map[string]interface{}
	if len(runs) > 0 {
		r := runs[0]
		lastRun = map[string]interface{}{
			"id":          r.ID,
			"started_at":  r.StartedAt,
			"finished_at": r.FinishedAt,
			"status":      r.Status,
		}
	}

	nextRun := ""
	if entries := s.cron.Entries(); len(entries) > 0 {
		nextRun = entries[0].Next.Format(time.RFC3339)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"running":    running,
		"last_run":   lastRun,
		"next_run":   nextRun,
	})
}

func (s *Server) handleAPIProducts(w http.ResponseWriter, r *http.Request) {
	products, err := s.db.GetProducts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(products)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (s *Server) handleAddProduct(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL         string  `json:"url"`
		TargetPrice float64 `json:"target_price"`
		Title       string  `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	body.URL = strings.TrimSpace(body.URL)
	body.Title = strings.TrimSpace(body.Title)
	if body.URL == "" || body.TargetPrice <= 0 {
		http.Error(w, "invalid url or target price", http.StatusBadRequest)
		return
	}

	id, err := s.db.UpsertProduct(body.URL, body.TargetPrice, "manual")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Set custom title if provided
	if body.Title != "" {
		if err := s.db.UpdateProduct(id, body.Title, body.TargetPrice, "active"); err != nil {
			slog.Error("Failed to set custom title on new product", "id", id, "error", err)
			http.Error(w, "product added but failed to save title", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":           id,
		"url":          body.URL,
		"target_price": body.TargetPrice,
		"title":        body.Title,
	})
}

func (s *Server) handleDeleteProduct(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := s.db.DeleteProduct(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUpdateProductTarget(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	var body struct {
		TargetPrice float64 `json:"target_price"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if body.TargetPrice <= 0 {
		http.Error(w, "invalid target price", http.StatusBadRequest)
		return
	}

	if err := s.db.UpdateProductTarget(id, body.TargetPrice); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":           id,
		"target_price": body.TargetPrice,
	})
}

func (s *Server) handleUpdateProduct(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	var body struct {
		Title       string  `json:"title"`
		TargetPrice float64 `json:"target_price"`
		Status      string  `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	body.Title = strings.TrimSpace(body.Title)
	if body.TargetPrice <= 0 {
		http.Error(w, "invalid target price", http.StatusBadRequest)
		return
	}

	validStatuses := map[string]bool{"active": true, "paused": true, "removed": true}
	if body.Status == "" {
		body.Status = "active"
	}
	if !validStatuses[body.Status] {
		http.Error(w, "invalid status (must be active, paused, or removed)", http.StatusBadRequest)
		return
	}

	if err := s.db.UpdateProduct(id, body.Title, body.TargetPrice, body.Status); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":           id,
		"title":        body.Title,
		"target_price": body.TargetPrice,
		"status":       body.Status,
	})
}

func (s *Server) handleToggleProductActive(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	newActive, err := s.db.ToggleProductActive(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":     id,
		"active": newActive,
	})
}

func (s *Server) handleFeedSync(w http.ResponseWriter, r *http.Request) {
	if s.cfg.FeedURL == "" {
		http.Error(w, "feed url not configured", http.StatusBadRequest)
		return
	}

	httpClient := &http.Client{Timeout: s.cfg.HTTPTimeout}
	feedItems, err := feed.FetchFeed(r.Context(), s.cfg.FeedURL, httpClient)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to fetch feed: %v", err), http.StatusInternalServerError)
		return
	}

	dbFeedItems := make([]db.FeedItem, len(feedItems))
	for i, item := range feedItems {
		dbFeedItems[i] = db.FeedItem{URL: item.URL, Price: item.Price}
	}

	syncedCount, removedCount, syncErr := s.db.SyncFeedProducts(dbFeedItems)
	if syncErr != nil {
		http.Error(w, fmt.Sprintf("sync error: %v", syncErr), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"synced":  syncedCount,
		"removed": removedCount,
	})
}

func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FeedURL          string `json:"feed_url"`
		CronSchedule     string `json:"cron_schedule"`
		BrowserEndpoint  string `json:"browser_endpoint"`
		AppriseURL       string `json:"apprise_url"`
		AlertCooldownHrs int    `json:"alert_cooldown_hrs"`
		WorkerCount      int    `json:"worker_count"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if body.WorkerCount < 1 {
		body.WorkerCount = 1
	}
	if body.AlertCooldownHrs < 0 {
		body.AlertCooldownHrs = 0
	}

	// Persist each setting to the DB
	settings := map[string]string{
		"feed_url":           body.FeedURL,
		"cron_schedule":      body.CronSchedule,
		"browser_endpoint":   body.BrowserEndpoint,
		"apprise_url":        body.AppriseURL,
		"alert_cooldown_hrs": strconv.Itoa(body.AlertCooldownHrs),
		"worker_count":       strconv.Itoa(body.WorkerCount),
	}
	for k, v := range settings {
		if err := s.db.SaveSetting(k, v); err != nil {
			slog.Error("Failed to save setting", "key", k, "error", err)
		}
	}

	// Apply live-updatable settings to in-memory config
	if body.FeedURL != "" {
		s.cfg.FeedURL = body.FeedURL
	}
	if body.AppriseURL != "" {
		s.cfg.AppriseURL = body.AppriseURL
	}
	if body.BrowserEndpoint != "" {
		s.cfg.CloakBrowserCDP = body.BrowserEndpoint
	}
	if body.AlertCooldownHrs >= 0 {
		s.cfg.AlertCooldownHrs = body.AlertCooldownHrs
	}
	if body.WorkerCount > 0 {
		s.cfg.MaxHTTPConcurrent = body.WorkerCount
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"message": "Settings saved. Schedule and port changes take effect after restart.",
	})
}

func shouldAlert(url string, srvDb *db.DB, cooldownHours int) bool {
	lastAlerted, _, err := srvDb.GetLastAlerted(url)
	if err != nil {
		return true
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
