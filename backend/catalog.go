package main

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
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
// Price is in minor units (cents for EUR); OFF has no prices, so we derive a
// stable one from the barcode.
type Product struct {
	ID          string `json:"id"` // OFF barcode
	Name        string `json:"name"`
	Description string `json:"description"`
	Price       int64  `json:"price"` // minor units
	Currency    string `json:"currency"`
	ImageURL    string `json:"imageUrl,omitempty"`
}

const (
	catalogCachePath    = "data/catalog.json"
	catalogCacheVersion = 2 // bump to invalidate the on-disk cache
	offSearchURL        = "https://world.openfoodfacts.org/api/v2/search"
	catalogPageSize     = 24
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
// present and only calls the Open Food Facts API when the cache is missing,
// so restarts don't refetch.
func loadCatalog() []Product {
	if data, err := os.ReadFile(catalogCachePath); err == nil {
		var c catalogCache
		if err := json.Unmarshal(data, &c); err == nil && c.Version == catalogCacheVersion && len(c.Products) > 0 {
			log.Printf("catalog: loaded %d products from cache %s", len(c.Products), catalogCachePath)
			return c.Products
		}
	}

	log.Printf("catalog: no usable cache at %s, fetching from Open Food Facts", catalogCachePath)
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

	req, err := http.NewRequest(http.MethodGet, offSearchURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil
	}
	// OFF asks clients to identify themselves with a User-Agent.
	req.Header.Set("User-Agent", "moulahmoulah-poc/0.1 (contact@tonyzoooo.me)")

	client := &http.Client{Timeout: 20 * time.Second}
	// OFF throttles anonymous search requests with intermittent 503s, so retry.
	var resp *http.Response
	for attempt := 1; ; attempt++ {
		resp, err = client.Do(req)
		if err != nil {
			log.Printf("catalog: Open Food Facts request failed: %v", err)
			return nil
		}
		if resp.StatusCode == http.StatusOK || (resp.StatusCode != http.StatusServiceUnavailable && resp.StatusCode != http.StatusTooManyRequests) || attempt >= 3 {
			break
		}
		delay := time.Duration(attempt*2) * time.Second
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := time.ParseDuration(ra + "s"); err == nil {
				delay = secs
			}
		}
		log.Printf("catalog: Open Food Facts returned HTTP %d, retrying in %s (attempt %d)", resp.StatusCode, delay, attempt+1)
		resp.Body.Close()
		time.Sleep(delay)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("catalog: Open Food Facts returned HTTP %d", resp.StatusCode)
		return nil
	}

	var result struct {
		Products []offProduct `json:"products"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("catalog: Open Food Facts decode failed: %v", err)
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
		desc := strings.TrimSpace(strings.Join(parts, " — "))
		products = append(products, Product{
			ID:          p.Code,
			Name:        p.ProductName,
			Description: desc,
			Price:       price,
			Currency:    "eur",
			ImageURL:    p.ImageFrontSmallURL,
		})
	}
	return products
}

func firstBrand(brands string) string {
	if i := strings.Index(brands, ","); i >= 0 {
		return strings.TrimSpace(brands[:i])
	}
	return strings.TrimSpace(brands)
}

// priceForProduct derives a stable synthetic price (EUR cents) from the
// barcode and the product size. Open Food Facts has no prices, so we apply a
// stable per-kg/per-L rate (derived from the barcode) to the parsed quantity —
// a 33 cl water ends up around €0.50–1.50, a 1 L drink around €0.50–4.00.
// Returns the price and a "€/kg" / "€/L" label for the description.
func priceForProduct(code, quantity string) (int64, string) {
	h := fnv.New64a()
	h.Write([]byte(code))
	sum := h.Sum64()

	amount, unit := parseQuantity(quantity) // amount in g or ml
	if amount <= 0 {
		// No parseable size: flat small price.
		return 150 + int64(sum%7)*50, ""
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
	{ID: "0000000000000", Name: "Mystery snack", Description: "offline fallback", Price: 500, Currency: "eur"},
}
