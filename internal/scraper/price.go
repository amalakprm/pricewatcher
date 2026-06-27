package scraper

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var priceRegex = regexp.MustCompile(`\d+(?:\.\d+)?`)

// ParsePrice cleans a price string and extracts the first valid positive float64 value.
func ParsePrice(raw string) (float64, bool) {
	// Strip symbols
	raw = strings.ReplaceAll(raw, "₹", "")
	raw = strings.ReplaceAll(raw, "$", "")
	raw = strings.ReplaceAll(raw, "€", "")
	raw = strings.ReplaceAll(raw, "\u00a0", "")
	
	// Strip all whitespaces
	raw = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, raw)

	// Remove all commas
	raw = strings.ReplaceAll(raw, ",", "")

	// Find first float-like match
	match := priceRegex.FindString(raw)
	if match == "" {
		return 0, false
	}

	val, err := strconv.ParseFloat(match, 64)
	if err != nil || val <= 0 {
		return 0, false
	}

	return val, true
}

// DetectSite checks the domain of the URL to classify the site type.
func DetectSite(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "generic"
	}
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, "amazon.in") {
		return "amazon"
	}
	if strings.Contains(host, "flipkart.com") {
		return "flipkart"
	}
	return "generic"
}
