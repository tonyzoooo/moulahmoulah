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
	catalogCachePath = "data/catalog.json"
	offSearchURL     = "https://world.openfoodfacts.org/api/v2/search"
	catalogPageSize  = 24
)

var (
	catalogMu sync.RWMutex
	catalog   []Product
)

// loadCatalog returns the product catalog. It uses the on-disk cache when
// present and only calls the Open Food Facts API when the cache is missing,
// so restarts don't refetch.
func loadCatalog() []Product {
	if data, err := os.ReadFile(catalogCachePath); err == nil {
		var cached []Product
		if err := json.Unmarshal(data, &cached); err == nil && len(cached) > 0 {
			log.Printf("catalog: loaded %d products from cache %s", len(cached), catalogCachePath)
			return cached
		}
	}

	log.Printf("catalog: no cache at %s, fetching from Open Food Facts", catalogCachePath)
	products := fetchOpenFoodFacts()
	if len(products) == 0 {
		log.Printf("catalog: Open Food Facts unavailable, using built-in fallback")
		return fallbackCatalog
	}

	if err := os.MkdirAll(filepath.Dir(catalogCachePath), 0o755); err == nil {
		if data, err := json.MarshalIndent(products, "", "  "); err == nil {
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
		desc := strings.TrimSpace(strings.Join([]string{firstBrand(p.Brands), p.Quantity}, " — "))
		products = append(products, Product{
			ID:          p.Code,
			Name:        p.ProductName,
			Description: desc,
			Price:       priceForBarcode(p.Code),
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

// priceForBarcode derives a stable fake price (EUR cents) from the barcode, so
// a product keeps the same price across restarts and refetches.
func priceForBarcode(code string) int64 {
	h := fnv.New64a()
	h.Write([]byte(code))
	return 150 + int64(h.Sum64()%171)*50 // €1.50 .. €86.50 in 50-cent steps
}

// fallbackCatalog is used only when OFF is unreachable and no cache exists.
var fallbackCatalog = []Product{
	{ID: "0000000000000", Name: "Mystery snack", Description: "offline fallback", Price: 500, Currency: "eur"},
}
