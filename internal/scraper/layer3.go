package scraper

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
)

// ScrapeLayer3 connects to an existing CloakBrowser CDP endpoint, navigates to the URL,
// waits for the body to be ready, and returns the outer HTML.
func ScrapeLayer3(ctx context.Context, url string, cdpAddr string, timeout time.Duration) (string, error) {
	// Create context with timeout
	ctx, cancelTimeout := context.WithTimeout(ctx, timeout)
	defer cancelTimeout()

	// Connect to remote allocator
	allocCtx, cancelAlloc := chromedp.NewRemoteAllocator(ctx, cdpAddr)
	defer cancelAlloc()

	// Connect to existing browser and create a tab
	tabCtx, cancelTab := chromedp.NewContext(allocCtx)
	defer cancelTab()

	var html string
	err := chromedp.Run(tabCtx,
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
		chromedp.Sleep(2*time.Second), // short sleep after load to allow scripts to run
		chromedp.OuterHTML("html", &html),
	)
	if err != nil {
		return "", fmt.Errorf("chromedp run failed: %w", err)
	}

	return html, nil
}
