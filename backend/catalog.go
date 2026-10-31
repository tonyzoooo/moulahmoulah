package main

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Product is a shop item backed by an Open Food Facts entry.
// Price is in minor units (cents for EUR). Real prices come from the Open
// Prices API when available; otherwise a synthetic size-scaled price is used.
type Product struct {
	ID          string `json:"id"` // OFF barcode
	Name        string `json:"name"`
	Description string `json:"description"`
	Price       int64  `json:"price"` // minor units
	Currency    string `json:"currency"`
	ImageURL    string `json:"imageUrl,omitempty"`
	PriceSource string `json:"priceSource,omitempty"` // "open-prices" | "synthetic"
}

const (
	catalogCachePath    = "data/catalog.json"
	catalogCacheVersion = 3 // bump to invalidate the on-disk cache
	offSearchURL        = "https://world.openfoodfacts.org/api/v2/search"
	openPricesURL       = "https://prices.openfoodfacts.org/api/v1/prices"
	catalogPageSize     = 24
	userAgent           = "moulahmoulah-poc/0.1 (contact@tonyzoooo.me)"
)

// catalogCache is the on-disk cache format. A version mismatch refetches.
type catalogCache struct {
	Version  int       `json:"version"`
	Products []Product `json:"products"`
}

var (
	catalogMu sync.RWMutex
	catalog   []Product
)

// loadCatalog returns the product catalog. It uses the on-disk cache when
// present and only calls the Open Food Facts / Open Prices APIs when the
// cache is missing or outdated, so restarts don't refetch.
func loadCatalog() []Product {
	if data, err := os.ReadFile(catalogCachePath); err == nil {
		var c catalogCache
		if err := json.Unmarshal(data, &c); err == nil && c.Version == catalogCacheVersion && len(c.Products) > 0 {
			log.Printf("catalog: loaded %d products from cache %s", len(c.Products), catalogCachePath)
			return c.Products
		}
	}

	log.Printf("catalog: no usable cache at %s, fetching from Open Food Facts + Open Prices", catalogCachePath)
	products := fetchOpenFoodFacts()
	if len(products) == 0 {
		log.Printf("catalog: Open Food Facts unavailable, using built-in fallback")
		return fallbackCatalog
	}

	if err := os.MkdirAll(filepath.Dir(catalogCachePath), 0o755); err == nil {
		c := catalogCache{Version: catalogCacheVersion, Products: products}
		if data, err := json.MarshalIndent(c, "", "  "); err == nil {
			if err := os.WriteFile(catalogCachePath, data, 0o644); err == nil {
				log.Printf("catalog: cached %d products to %s", len(products), catalogCachePath)
			}
		}
	}
	return products
}

func setCatalog(products []Product) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	catalog = products
}

func getCatalog() []Product {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	return catalog
}

func findProduct(id string) (Product, bool) {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	for _, p := range catalog {
		if p.ID == id {
			return p, true
		}
	}
	return Product{}, false
}

// offProduct is the subset of the Open Food Facts search response we use.
type offProduct struct {
	Code               string `json:"code"`
	ProductName        string `json:"product_name"`
	Brands             string `json:"brands"`
	Quantity           string `json:"quantity"`
	ImageFrontSmallURL string `json:"image_front_small_url"`
}

func fetchOpenFoodFacts() []Product {
	q := url.Values{}
	q.Set("fields", "code,product_name,brands,quantity,image_front_small_url")
	q.Set("page_size", fmt.Sprintf("%d", catalogPageSize))
	q.Set("json", "1")

	client := &http.Client{Timeout: 20 * time.Second}
	var result struct {
		Products []offProduct `json:"products"`
	}
	if err := fetchJSON(client, offSearchURL+"?"+q.Encode(), &result); err != nil {
		log.Printf("catalog: Open Food Facts fetch failed: %v", err)
		return nil
	}

	var products []Product
	for _, p := range result.Products {
		if p.Code == "" || p.ProductName == "" {
			continue
		}
		price, unitLabel := priceForProduct(p.Code, p.Quantity)
		parts := []string{firstBrand(p.Brands), p.Quantity}
		if unitLabel != "" {
			parts = append(parts, unitLabel)
		}
		products = append(products, Product{
			ID:          p.Code,
			Name:        p.ProductName,
			Description: strings.TrimSpace(strings.Join(parts, " — ")),
			Price:       price,
			Currency:    "eur",
			ImageURL:    p.ImageFrontSmallURL,
			PriceSource: "synthetic",
		})
	}

	// Enrich with real prices from Open Prices (concurrently, rate-limited).
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	for i := range products {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if cents := fetchOpenPrice(client, products[i].ID); cents > 0 {
				products[i].Price = cents
				products[i].PriceSource = "open-prices"
			}
		}(i)
	}
	wg.Wait()
	return products
}

// openPriceItem is the subset of an Open Prices result item we use.
type openPriceItem struct {
	Price    float64 `json:"price"` // major units, e.g. 1.62 for €1.62
	Currency string  `json:"currency"`
	Created  string  `json:"created"`
}

// fetchOpenPrice returns the most recent EUR price in cents for a barcode,
// or 0 when Open Prices has no EUR price for it.
func fetchOpenPrice(client *http.Client, code string) int64 {
	q := url.Values{}
	q.Set("product_code", code)
	q.Set("page_size", "20")

	var result struct {
		Items []openPriceItem `json:"items"`
	}
	if err := fetchJSON(client, openPricesURL+"?"+q.Encode(), &result); err != nil {
		return 0
	}
	best, bestCreated := int64(0), ""
	for _, it := range result.Items {
		if !strings.EqualFold(it.Currency, "EUR") || it.Price <= 0 {
			continue
		}
		if it.Created >= bestCreated {
			bestCreated = it.Created
			best = int64(math.Round(it.Price * 100))
		}
	}
	return best
}

// fetchJSON GETs a JSON document with retries. The Open Food Facts /
// Open Prices APIs throttle anonymous clients with intermittent 503s.
func fetchJSON(client *http.Client, rawurl string, out any) error {
	req, err := http.NewRequest(http.MethodGet, rawurl, nil)
	if err != nil {
		return err
	}
	// Both OFF APIs ask clients to identify themselves with a User-Agent.
	req.Header.Set("User-Agent", userAgent)

	var resp *http.Response
	for attempt := 1; ; attempt++ {
		resp, err = client.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusOK ||
			(resp.StatusCode != http.StatusServiceUnavailable && resp.StatusCode != http.StatusTooManyRequests) ||
			attempt >= 3 {
			break
		}
		delay := time.Duration(attempt*2) * time.Second
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := time.ParseDuration(ra + "s"); err == nil {
				delay = secs
			}
		}
		resp.Body.Close()
		time.Sleep(delay)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned HTTP %d", rawurl, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func firstBrand(brands string) string {
	if i := strings.Index(brands, ","); i >= 0 {
		return strings.TrimSpace(brands[:i])
	}
	return strings.TrimSpace(brands)
}

// priceForProduct derives a stable synthetic price (EUR cents) from the
// barcode and the product size, used when Open Prices has no real price.
// A stable per-kg/per-L rate (derived from the barcode) is applied to the
// parsed quantity. Returns the price and a "€/kg" / "€/L" label.
func priceForProduct(code, quantity string) (int64, string) {
	h := fnv.New64a()
	h.Write([]byte(code))
	sum := h.Sum64()

	amount, unit := parseQuantity(quantity) // amount in g or ml
	if amount <= 0 {
		return 150 + int64(sum%7)*50, "" // no parseable size: flat small price
	}

	perKgOrL := 20 + int64(sum%381) // €0.20 .. €4.00 per kg / L
	base := amount / 1000           // kg or L
	price := int64(float64(perKgOrL) * base)
	price = ((price + 25) / 50) * 50 // round to 50-cent steps
	if price < 50 {
		price = 50
	}
	label := fmt.Sprintf("€%.2f/kg", float64(perKgOrL)/100)
	if unit == "ml" {
		label = fmt.Sprintf("€%.2f/L", float64(perKgOrL)/100)
	}
	return price, label
}

// parseQuantity extracts a mass/volume from an OFF quantity string such as
// "1,5 L", "500 g" or "33 cl" and returns the amount in grams or milliliters
// plus the unit. Returns (0, "") when nothing parseable is found.
func parseQuantity(q string) (float64, string) {
	q = strings.ToLower(strings.TrimSpace(q))
	i := strings.IndexAny(q, "0123456789")
	if i < 0 {
		return 0, ""
	}
	j := i
	for j < len(q) && (q[j] >= '0' && q[j] <= '9' || q[j] == ',' || q[j] == '.') {
		j++
	}
	n, err := strconv.ParseFloat(strings.ReplaceAll(q[i:j], ",", "."), 64)
	if err != nil {
		return 0, ""
	}
	unit := strings.TrimSpace(q[j:])
	switch {
	case strings.HasPrefix(unit, "kg"):
		return n * 1000, "g"
	case strings.HasPrefix(unit, "g"):
		return n, "g"
	case strings.HasPrefix(unit, "cl"):
		return n * 10, "ml"
	case strings.HasPrefix(unit, "ml"):
		return n, "ml"
	case strings.HasPrefix(unit, "l"):
		return n * 1000, "ml"
	}
	return 0, ""
}

// fallbackCatalog is used only when OFF is unreachable and no cache exists.
var fallbackCatalog = []Product{
	{ID: "0000000000000", Name: "Mystery snack", Description: "offline fallback", Price: 500, Currency: "eur", PriceSource: "synthetic"},
}
