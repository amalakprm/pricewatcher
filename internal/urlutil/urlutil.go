// Package urlutil provides URL validation helpers used across PriceWatcher to
// prevent SSRF by ensuring only http:// and https:// URLs are used for outbound
// HTTP requests derived from configuration values.
package urlutil

import (
	"fmt"
	"net/url"
)

// ValidateHTTP returns an error if rawURL is not a valid http or https URL.
// Call this before using any config-supplied URL in an outbound http.Request.
func ValidateHTTP(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("URL must not be empty")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("URL %q has disallowed scheme %q (only http/https are permitted)", rawURL, parsed.Scheme)
	}
	return nil
}
