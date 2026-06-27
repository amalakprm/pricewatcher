package scraper

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

var (
	jsBlobRegexes = []*regexp.Regexp{
		regexp.MustCompile(`__INITIAL_STATE__\s*=\s*(\{.+?\})\s*;`),
		regexp.MustCompile(`window\.__STATE__\s*=\s*(\{.+?\})\s*;`),
		regexp.MustCompile(`window\.__INITIAL_STATE__\s*=\s*(\{.+?\})\s*;`),
	}
)

// ScrapeLayer2 extracts the price from the already fetched HTML using:
// 1. JSON-LD structured data
// 2. OpenGraph metadata
// 3. Embedded Javascript state blobs
func ScrapeLayer2(htmlStr string) (string, float64, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStr))
	if err != nil {
		return "", 0, err
	}

	// 1. JSON-LD
	if title, price, ok := extractJSONLD(doc); ok {
		return title, price, nil
	}

	// 2. OpenGraph meta tags
	if title, price, ok := extractOpenGraph(doc); ok {
		return title, price, nil
	}

	// 3. JS State blobs
	if title, price, ok := extractJSBlobs(doc); ok {
		return title, price, nil
	}

	return "", 0, nil
}

func extractJSONLD(doc *goquery.Document) (string, float64, bool) {
	var foundTitle string
	var foundPrice float64
	var ok bool

	doc.Find(`script[type="application/ld+json"]`).EachWithBreak(func(i int, s *goquery.Selection) bool {
		jsText := strings.TrimSpace(s.Text())
		if jsText == "" {
			return true
		}

		var parsed interface{}
		if err := json.Unmarshal([]byte(jsText), &parsed); err != nil {
			return true // ignore invalid JSON
		}

		t, p, found := findProductJSONLD(parsed)
		if found {
			if t != "" {
				foundTitle = t
			}
			foundPrice = p
			ok = true
			return false // stop iteration
		}
		return true
	})

	return foundTitle, foundPrice, ok
}

func findProductJSONLD(val interface{}) (string, float64, bool) {
	switch v := val.(type) {
	case map[string]interface{}:
		// Check for @type == "Product"
		t, hasType := v["@type"].(string)
		if hasType && strings.EqualFold(t, "Product") {
			title, _ := v["name"].(string)
			// Check offers
			if offers, ok := v["offers"]; ok {
				if p, found := extractPriceFromOffers(offers); found {
					return title, p, true
				}
			}
		}

		// Also check @graph
		if graph, ok := v["@graph"]; ok {
			if title, p, found := findProductJSONLD(graph); found {
				return title, p, true
			}
		}

		// Recurse into other keys
		for _, item := range v {
			if title, p, found := findProductJSONLD(item); found {
				return title, p, true
			}
		}

	case []interface{}:
		for _, item := range v {
			if title, p, found := findProductJSONLD(item); found {
				return title, p, true
			}
		}
	}
	return "", 0, false
}

func extractPriceFromOffers(offers interface{}) (float64, bool) {
	switch o := offers.(type) {
	case map[string]interface{}:
		// check price or lowPrice
		if p, ok := toFloat(o["price"]); ok && p > 0 {
			return p, true
		}
		if lp, ok := toFloat(o["lowPrice"]); ok && lp > 0 {
			return lp, true
		}
	case []interface{}:
		for _, item := range o {
			if p, ok := extractPriceFromOffers(item); ok {
				return p, true
			}
		}
	}
	return 0, false
}

func toFloat(val interface{}) (float64, bool) {
	switch v := val.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		return ParsePrice(v)
	}
	return 0, false
}

func extractOpenGraph(doc *goquery.Document) (string, float64, bool) {
	var title string
	var price float64
	var foundPrice bool

	// og:title
	title, _ = doc.Find(`meta[property="og:title"]`).Attr("content")
	if title == "" {
		title, _ = doc.Find(`meta[name="og:title"]`).Attr("content")
	}

	// og:price:amount
	pStr, _ := doc.Find(`meta[property="og:price:amount"]`).Attr("content")
	if pStr == "" {
		pStr, _ = doc.Find(`meta[name="og:price:amount"]`).Attr("content")
	}

	if p, ok := ParsePrice(pStr); ok {
		price = p
		foundPrice = true
	}

	return strings.TrimSpace(title), price, foundPrice
}

func extractJSBlobs(doc *goquery.Document) (string, float64, bool) {
	var foundPrice float64
	var ok bool

	doc.Find("script").EachWithBreak(func(i int, s *goquery.Selection) bool {
		scriptText := s.Text()
		for _, re := range jsBlobRegexes {
			match := re.FindStringSubmatch(scriptText)
			if len(match) > 1 {
				var parsed interface{}
				if err := json.Unmarshal([]byte(match[1]), &parsed); err == nil {
					if p, found := walkJSONForPrice(parsed, 1); found {
						foundPrice = p
						ok = true
						return false // stop
					}
				}
			}
		}
		return true
	})

	// For title in this fallback, we just get title or h1
	var title string
	if ok {
		title = strings.TrimSpace(doc.Find("h1").First().Text())
		if title == "" {
			title = strings.TrimSpace(doc.Find("title").First().Text())
		}
	}

	return title, foundPrice, ok
}

func walkJSONForPrice(val interface{}, depth int) (float64, bool) {
	if depth > 6 {
		return 0, false
	}
	switch v := val.(type) {
	case map[string]interface{}:
		// Check keys first
		for k, item := range v {
			kl := strings.ToLower(k)
			// Match exact or contains price but avoid price currency or price symbol if it's not a number
			if strings.Contains(kl, "price") && !strings.Contains(kl, "currency") && !strings.Contains(kl, "symbol") {
				if num, ok := toFloat(item); ok && num > 0 {
					return num, true
				}
			}
		}
		// Recurse keys
		for _, item := range v {
			if num, ok := walkJSONForPrice(item, depth+1); ok {
				return num, true
			}
		}
	case []interface{}:
		limit := len(v)
		if limit > 10 {
			limit = 10
		}
		for i := 0; i < limit; i++ {
			if num, ok := walkJSONForPrice(v[i], depth+1); ok {
				return num, true
			}
		}
	}
	return 0, false
}
