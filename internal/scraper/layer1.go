package scraper

import (
	"compress/flate"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// ScrapeLayer1 fetches the URL using http.Client and parses the HTML using CSS selectors.
func ScrapeLayer1(ctx context.Context, url string, client *http.Client) (string, string, float64, error) {
	var resp *http.Response
	var err error

	// Retry once on connection error
	for attempt := 1; attempt <= 2; attempt++ {
		req, reqErr := http.NewRequestWithContext(ctx, "GET", url, nil)
		if reqErr != nil {
			return "", "", 0, reqErr
		}

		// Exact browser headers
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
		req.Header.Set("Accept-Language", "en-IN,en-GB;q=0.9,en-US;q=0.8,en;q=0.7")
		req.Header.Set("Accept-Encoding", "gzip, deflate") // omit br to avoid third-party dependency issues
		req.Header.Set("DNT", "1")
		req.Header.Set("Connection", "keep-alive")
		req.Header.Set("Upgrade-Insecure-Requests", "1")
		req.Header.Set("Sec-Fetch-Dest", "document")
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		req.Header.Set("Sec-Fetch-Site", "none")
		req.Header.Set("Sec-Fetch-User", "?1")
		req.Header.Set("Cache-Control", "max-age=0")
		req.Header.Set("sec-ch-ua", `"Chromium";v="124", "Google Chrome";v="124", "Not-A.Brand";v="99"`)
		req.Header.Set("sec-ch-ua-mobile", "?0")
		req.Header.Set("sec-ch-ua-platform", `"Windows"`)

		resp, err = client.Do(req)
		if err == nil {
			break
		}

		if attempt == 1 {
			select {
			case <-ctx.Done():
				return "", "", 0, ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
	}

	if err != nil {
		return "", "", 0, fmt.Errorf("HTTP request failed after retry: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", 0, fmt.Errorf("HTTP status error: %d %s", resp.StatusCode, resp.Status)
	}

	// Handle Content-Encoding
	var bodyReader io.ReadCloser = resp.Body
	switch strings.ToLower(resp.Header.Get("Content-Encoding")) {
	case "gzip":
		gz, gzErr := gzip.NewReader(resp.Body)
		if gzErr != nil {
			return "", "", 0, fmt.Errorf("failed to create gzip reader: %w", gzErr)
		}
		defer gz.Close()
		bodyReader = gz
	case "deflate":
		df := flate.NewReader(resp.Body)
		defer df.Close()
		bodyReader = df
	}

	bodyBytes, err := io.ReadAll(bodyReader)
	if err != nil {
		return "", "", 0, fmt.Errorf("failed to read response body: %w", err)
	}
	htmlStr := string(bodyBytes)

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStr))
	if err != nil {
		return htmlStr, "", 0, fmt.Errorf("failed to parse HTML: %w", err)
	}

	site := DetectSite(url)
	var title string
	var price float64
	var found bool

	switch site {
	case "amazon":
		title, price, found = ExtractAmazon(doc)
	case "flipkart":
		title, price, found = ExtractFlipkart(doc)
	default:
		title, price, found = ExtractGeneric(doc)
	}

	if !found {
		return htmlStr, title, 0, fmt.Errorf("no price found via Layer 1 selectors")
	}

	return htmlStr, title, price, nil
}
