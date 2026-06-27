package scraper

import (
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// ExtractAmazon parses Amazon product title and price.
func ExtractAmazon(doc *goquery.Document) (string, float64, bool) {
	// Title: #productTitle
	title := strings.TrimSpace(doc.Find("#productTitle").First().Text())

	// Price primary: combine span.a-price-whole + span.a-price-fraction
	var priceVal float64
	var foundPrice bool

	whole := doc.Find("span.a-price-whole").First().Text()
	fraction := doc.Find("span.a-price-fraction").First().Text()
	if whole != "" {
		combined := whole
		if fraction != "" {
			combined += "." + fraction
		}
		if p, ok := ParsePrice(combined); ok {
			priceVal = p
			foundPrice = true
		}
	}

	// Fallbacks
	if !foundPrice {
		fallbacks := []string{
			"#corePriceDisplay_desktop_feature_div .a-price .a-offscreen",
			"#apex_desktop .a-price .a-offscreen",
			"#priceblock_ourprice",
			"#priceblock_dealprice",
			".a-price:not(.a-text-price) .a-offscreen",
		}
		for _, sel := range fallbacks {
			txt := doc.Find(sel).First().Text()
			if p, ok := ParsePrice(txt); ok {
				priceVal = p
				foundPrice = true
				break
			}
		}
	}

	return title, priceVal, foundPrice
}

// ExtractFlipkart parses Flipkart product title and price.
func ExtractFlipkart(doc *goquery.Document) (string, float64, bool) {
	// Title: span.B_NuCI, h1.yhB1nd, ._35KyD6 (first match)
	var title string
	titleSels := []string{"span.B_NuCI", "h1.yhB1nd", "._35KyD6"}
	for _, sel := range titleSels {
		txt := strings.TrimSpace(doc.Find(sel).First().Text())
		if txt != "" {
			title = txt
			break
		}
	}

	// Price: div._30jeq3._16Jk6d, div._30jeq3 (first match)
	var priceVal float64
	var foundPrice bool
	priceSels := []string{"div._30jeq3._16Jk6d", "div._30jeq3"}
	for _, sel := range priceSels {
		txt := doc.Find(sel).First().Text()
		if p, ok := ParsePrice(txt); ok {
			priceVal = p
			foundPrice = true
			break
		}
	}

	return title, priceVal, foundPrice
}

// ExtractGeneric parses generic product pages using heuristics.
func ExtractGeneric(doc *goquery.Document) (string, float64, bool) {
	// Title: h1 or <title>
	title := strings.TrimSpace(doc.Find("h1").First().Text())
	if title == "" {
		title = strings.TrimSpace(doc.Find("title").First().Text())
	}

	var priceVal float64
	var foundPrice bool

	// Price: walk all elements, find one whose class or id contains "price" (case-insensitive), parse text
	doc.Find("*").EachWithBreak(func(i int, s *goquery.Selection) bool {
		class, _ := s.Attr("class")
		id, _ := s.Attr("id")
		class = strings.ToLower(class)
		id = strings.ToLower(id)

		if strings.Contains(class, "price") || strings.Contains(id, "price") {
			txt := s.Text()
			// Exclude parent divs that just wrap lots of text
			if len(txt) < 50 {
				if p, ok := ParsePrice(txt); ok {
					priceVal = p
					foundPrice = true
					return false // stop iteration
				}
			}
		}
		return true // continue
	})

	// Fallback: find <span> or <div> whose text starts with ₹
	if !foundPrice {
		doc.Find("span, div").EachWithBreak(func(i int, s *goquery.Selection) bool {
			txt := strings.TrimSpace(s.Text())
			if strings.HasPrefix(txt, "₹") || strings.Contains(txt, "₹") {
				if len(txt) < 50 {
					if p, ok := ParsePrice(txt); ok {
						priceVal = p
						foundPrice = true
						return false // stop
					}
				}
			}
			return true
		})
	}

	return title, priceVal, foundPrice
}
