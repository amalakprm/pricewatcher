package scraper

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"pricewatcher/internal/config"
)

var (
	l3Sem = make(chan struct{}, 1) // Layer 3 serialisation semaphore
)

type Product struct {
	ID     int64
	URL    string
	Target float64
}

type ScrapeResult struct {
	URL       string
	Title     string
	Price     float64
	Target    float64
	LayerUsed int
	Error     string
	Duration  time.Duration
}

// ScrapeProduct orchestrates scraping a single product, falling back through L1 -> L2 -> L3.
func ScrapeProduct(ctx context.Context, product Product, httpClient *http.Client, cfg *config.Config) ScrapeResult {
	start := time.Now()
	site := strings.ToUpper(DetectSite(product.URL))
	
	// Check run ID from context
	runID, _ := ctx.Value("run_id").(int64)

	res := ScrapeResult{
		URL:    product.URL,
		Target: product.Target,
	}

	logger := slog.With(
		slog.String("url", product.URL),
		slog.Int64("run_id", runID),
	)

	// --- Layer 1 ---
	htmlStr, title, price, err := ScrapeLayer1(ctx, product.URL, httpClient)
	if err == nil {
		res.Title = title
		res.Price = price
		res.LayerUsed = 1
		res.Duration = time.Since(start)

		logger.Info(fmt.Sprintf("%s | L1 OK | ₹%.2f | %q", site, price, title),
			slog.Int("layer", 1),
			slog.Float64("price", price),
			slog.Int64("duration_ms", res.Duration.Milliseconds()),
		)
		return res
	}

	// --- Layer 2 ---
	// If L1 fetched HTML but price parsing failed, try L2 on the fetched HTML
	if htmlStr != "" {
		l2Title, l2Price, l2Err := ScrapeLayer2(htmlStr)
		if l2Err == nil && l2Price > 0 {
			if l2Title != "" {
				res.Title = l2Title
			} else {
				res.Title = title
			}
			res.Price = l2Price
			res.LayerUsed = 2
			res.Duration = time.Since(start)

			logger.Info(fmt.Sprintf("%s | L2 OK | ₹%.2f | %q", site, l2Price, res.Title),
				slog.Int("layer", 2),
				slog.Float64("price", l2Price),
				slog.Int64("duration_ms", res.Duration.Milliseconds()),
			)
			return res
		}
	}

	// If Layer 1/2 failed to yield a price (or L1 fetch failed entirely), try Layer 3
	logger.Info(fmt.Sprintf("%s | L2 NO PX | trying Layer 3", site))

	// Check if L3 should be skipped
	if skip, _ := ctx.Value("skip_l3").(bool); skip {
		res.Error = "all layers exhausted (L3 skipped)"
		res.Duration = time.Since(start)
		logger.Warn(fmt.Sprintf("%s | L3 SKIPPED | CloakBrowser unreachable", site),
			slog.Int64("duration_ms", res.Duration.Milliseconds()))
		return res
	}

	// Acquire Layer 3 serialisation semaphore
	select {
	case <-ctx.Done():
		res.Error = ctx.Err().Error()
		res.Duration = time.Since(start)
		logger.Error(fmt.Sprintf("%s | FAILED | context cancelled before L3: %v", site, ctx.Err()),
			slog.Int64("duration_ms", res.Duration.Milliseconds()))
		return res
	case l3Sem <- struct{}{}:
		defer func() { <-l3Sem }()
	}

	// Perform Layer 3 scrape
	l3Html, l3Err := ScrapeLayer3(ctx, product.URL, cfg.CloakBrowserCDP, cfg.CDPTimeout)
	if l3Err != nil {
		res.Error = fmt.Sprintf("L3 fetch failed: %v", l3Err)
		res.Duration = time.Since(start)
		logger.Warn(fmt.Sprintf("%s | L3 FAILED | %v", site, l3Err),
			slog.Int64("duration_ms", res.Duration.Milliseconds()))
		return res
	}

	// Parse L3 HTML using Layer 1 logic first, then Layer 2
	doc, parseErr := goquery.NewDocumentFromReader(strings.NewReader(l3Html))
	if parseErr == nil {
		var l3Title string
		var l3Price float64
		var found bool

		switch DetectSite(product.URL) {
		case "amazon":
			l3Title, l3Price, found = ExtractAmazon(doc)
		case "flipkart":
			l3Title, l3Price, found = ExtractFlipkart(doc)
		default:
			l3Title, l3Price, found = ExtractGeneric(doc)
		}

		if found {
			res.Title = l3Title
			res.Price = l3Price
			res.LayerUsed = 3
			res.Duration = time.Since(start)

			logger.Info(fmt.Sprintf("%s | L3 OK (via L1 parser) | ₹%.2f | %q", site, l3Price, l3Title),
				slog.Int("layer", 3),
				slog.Float64("price", l3Price),
				slog.Int64("duration_ms", res.Duration.Milliseconds()),
			)
			return res
		}

		// Fallback to Layer 2 parser on Layer 3 HTML
		l3L2Title, l3L2Price, l3L2Err := ScrapeLayer2(l3Html)
		if l3L2Err == nil && l3L2Price > 0 {
			if l3L2Title != "" {
				res.Title = l3L2Title
			} else {
				res.Title = l3Title
			}
			res.Price = l3L2Price
			res.LayerUsed = 3
			res.Duration = time.Since(start)

			logger.Info(fmt.Sprintf("%s | L3 OK (via L2 parser) | ₹%.2f | %q", site, l3L2Price, res.Title),
				slog.Int("layer", 3),
				slog.Float64("price", l3L2Price),
				slog.Int64("duration_ms", res.Duration.Milliseconds()),
			)
			return res
		}
	}

	res.Error = "all layers exhausted"
	res.Duration = time.Since(start)
	logger.Warn(fmt.Sprintf("%s | FAILED | all layers exhausted", site),
		slog.Int64("duration_ms", res.Duration.Milliseconds()))
	return res
}
