package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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
	writeJSON(w, http.StatusOK, catalog)
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
		OrderID:     orderID,
		Currency:    order.Currency,
		Amount:      order.Amount,
		Description: describeItems(items),
		SuccessURL:  fmt.Sprintf("%s/success/%s?session_id={CHECKOUT_SESSION_ID}", base, orderID),
		CancelURL:   fmt.Sprintf("%s/failure/%s", base, orderID),
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

func describeItems(items []Product) string {
	if len(items) == 1 {
		return items[0].Name
	}
	return fmt.Sprintf("%d items", len(items))
}
