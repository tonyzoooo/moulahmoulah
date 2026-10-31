package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"moulahmoulah/processors"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// handleCatalog returns the product catalog.
func handleCatalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, getCatalog())
}

type checkoutRequest struct {
	ProcessorID string         `json:"processorId"`
	Items       map[string]int `json:"items"` // product id -> quantity
}

type checkoutResponse struct {
	OrderID string `json:"orderId"`
	URL     string `json:"url"`
}

// handleCheckout creates an order and a payment with the chosen processor,
// returning the hosted payment URL the customer should be redirected to.
func handleCheckout(w http.ResponseWriter, r *http.Request) {
	var req checkoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	proc, err := processors.Get(req.ProcessorID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Build the order server-side from the catalog; never trust client prices.
	var items []Product
	var amount int64
	for id, qty := range req.Items {
		if qty <= 0 {
			continue
		}
		p, ok := findProduct(id)
		if !ok {
			writeError(w, http.StatusBadRequest, "unknown product: "+id)
			return
		}
		for i := 0; i < qty; i++ {
			items = append(items, p)
		}
		amount += p.Price * int64(qty)
	}
	if len(items) == 0 {
		writeError(w, http.StatusBadRequest, "cart is empty")
		return
	}

	orderID := newOrderID()
	order := &Order{
		ID:          orderID,
		Items:       items,
		Amount:      amount,
		Currency:    "eur",
		ProcessorID: proc.ID(),
		Status:      processors.StatusCreated,
		CreatedAt:   time.Now().UTC(),
	}
	store.create(order)

	base := baseURL(r)
	result, err := proc.CreatePayment(processors.PaymentRequest{
		OrderID:             orderID,
		Currency:            order.Currency,
		Amount:              order.Amount,
		Description:         describeItems(items),
		SuccessURL:          fmt.Sprintf("%s/success/%s?session_id={CHECKOUT_SESSION_ID}", base, orderID),
		CancelURL:           fmt.Sprintf("%s/failure/%s", base, orderID),
		Metadata:            metadataForOrder(orderID, items, amount, order.Currency),
		StatementDescriptor: "MOULAHMOULAH",
		// Automatic capture: the hosted page redirects to success_url right
		// after payment. (Manual capture leaves the session "complete" but
		// unpaid and Checkout does not redirect.)
		CaptureManual: false,
		LineItems:     lineItemsFor(req.Items),
	})
	if err != nil {
		store.update(orderID, "", processors.StatusFailed)
		writeError(w, http.StatusBadGateway, "processor error: "+err.Error())
		return
	}

	store.update(orderID, result.ProviderRef, processors.StatusCreated)
	writeJSON(w, http.StatusOK, checkoutResponse{OrderID: orderID, URL: result.RedirectURL})
}

// handleOrder returns an order, refreshing its status from the processor.
func handleOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	order, ok := store.get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "order not found")
		return
	}

	if order.ProviderRef != "" {
		if proc, err := processors.Get(order.ProcessorID); err == nil {
			if status, err := proc.GetStatus(order.ProviderRef); err == nil {
				store.update(id, order.ProviderRef, status)
				order, _ = store.get(id)
			}
		}
	}

	writeJSON(w, http.StatusOK, order)
}

// handleCapture captures an authorized payment (acquirer capture step).
func handleCapture(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	order, ok := store.get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "order not found")
		return
	}
	if order.ProviderRef == "" {
		writeError(w, http.StatusBadRequest, "order has no payment yet")
		return
	}
	proc, err := processors.Get(order.ProcessorID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := proc.Capture(order.ProviderRef); err != nil {
		writeError(w, http.StatusBadGateway, "capture failed: "+err.Error())
		return
	}
	if status, err := proc.GetStatus(order.ProviderRef); err == nil {
		store.update(id, order.ProviderRef, status)
		order, _ = store.get(id)
	}
	writeJSON(w, http.StatusOK, order)
}

func newOrderID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// baseURL returns the public base URL used to build processor redirect URLs.
// It prefers the Origin header of the incoming request, so the customer is
// redirected back to the exact origin they checked out from (localhost vs
// 127.0.0.1 vs a LAN address). Falls back to the BASE_URL env var.
func baseURL(r *http.Request) string {
	if origin := r.Header.Get("Origin"); origin != "" {
		return strings.TrimRight(origin, "/")
	}
	if v := os.Getenv("BASE_URL"); v != "" {
		return v
	}
	return "http://localhost:5173"
}

// lineItemsFor groups the cart (product id -> quantity) into checkout line
// items, sorted by product id for stable ordering.
func lineItemsFor(cart map[string]int) []processors.LineItem {
	ids := make([]string, 0, len(cart))
	for id, qty := range cart {
		if qty > 0 {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	items := make([]processors.LineItem, 0, len(ids))
	for _, id := range ids {
		p, ok := findProduct(id)
		if !ok {
			continue
		}
		items = append(items, processors.LineItem{
			Name:     p.Name,
			Amount:   p.Price,
			Quantity: int64(cart[id]),
			ImageURL: p.ImageURL,
		})
	}
	return items
}

func describeItems(items []Product) string {
	if len(items) == 1 {
		return items[0].Name
	}
	return fmt.Sprintf("%d items", len(items))
}

// metadataForOrder builds the key-value metadata attached to the payment
// object (e.g. the Stripe PaymentIntent). Stripe metadata values are strings
// and capped at 500 chars, so the item list is truncated if needed.
func metadataForOrder(orderID string, items []Product, amount int64, currency string) map[string]string {
	counts := map[string]int{}
	for _, it := range items {
		counts[it.ID]++
	}
	parts := make([]string, 0, len(counts))
	for id, n := range counts {
		parts = append(parts, fmt.Sprintf("%s x%d", id, n))
	}
	sort.Strings(parts)
	itemList := strings.Join(parts, ",")
	if len(itemList) > 450 {
		itemList = itemList[:450]
	}
	return map[string]string{
		"order_id":   orderID,
		"items":      itemList,
		"item_count": strconv.Itoa(len(items)),
		"amount":     strconv.FormatInt(amount, 10),
		"currency":   currency,
	}
}
