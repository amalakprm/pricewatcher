package notify

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"time"
)

// SendNotification sends a price drop or scrape failure alert via the Apprise API.
// It uses a 10s timeout and is non-blocking (does not abort the scraping loop on failure).
func SendNotification(ctx context.Context, appriseURL, title, body string) {
	// Create context with 10s timeout
	notifyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	if err := writer.WriteField("title", title); err != nil {
		slog.Warn("Failed to write title field in notification", "error", err)
		return
	}
	if err := writer.WriteField("body", body); err != nil {
		slog.Warn("Failed to write body field in notification", "error", err)
		return
	}
	if err := writer.Close(); err != nil {
		slog.Warn("Failed to close multipart writer in notification", "error", err)
		return
	}

	req, err := http.NewRequestWithContext(notifyCtx, "POST", appriseURL, &buf)
	if err != nil {
		slog.Warn("Failed to create notification request", "error", err)
		return
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("Notification request failed", "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		slog.Warn("Notification API returned non-2xx status", "status", resp.Status, "body", string(bodyBytes))
	} else {
		slog.Info("Notification sent successfully", "title", title)
	}
}
