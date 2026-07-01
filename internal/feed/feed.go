package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"pricewatcher/internal/urlutil"
)

type FeedItem struct {
	URL   string  `json:"url"`
	Price float64 `json:"price"` // Target price
}

// FetchFeed downloads the product list feed from the Google Apps Script JSON endpoint.
func FetchFeed(ctx context.Context, feedURL string, client *http.Client) ([]FeedItem, error) {
	if err := urlutil.ValidateHTTP(feedURL); err != nil {
		return nil, fmt.Errorf("feed URL validation failed: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", feedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create feed request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute feed request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("feed response status error: %d %s", resp.StatusCode, resp.Status)
	}

	var items []FeedItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("failed to decode feed JSON: %w", err)
	}

	return items, nil
}
