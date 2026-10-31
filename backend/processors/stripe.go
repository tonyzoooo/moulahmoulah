package processors

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const stripeAPI = "https://api.stripe.com/v1"

// StripeProcessor implements Processor using Stripe Checkout in test mode,
// called via plain REST (no SDK). The customer pays on Stripe's hosted page
// and is redirected back to the merchant's success/cancel URLs.
type StripeProcessor struct {
	secretKey string
	client    *http.Client
}

// NewStripeProcessor creates the Stripe processor and registers it.
// The secret key is read from STRIPE_SECRET_KEY (use a sk_test_ key).
func NewStripeProcessor() *StripeProcessor {
	p := &StripeProcessor{
		secretKey: os.Getenv("STRIPE_SECRET_KEY"),
		client:    &http.Client{Timeout: 15 * time.Second},
	}
	Register(p)
	return p
}

func (p *StripeProcessor) ID() string    { return "stripe" }
func (p *StripeProcessor) Label() string { return "Stripe (test mode)" }

// CreatePayment creates a Stripe Checkout Session and returns its hosted URL.
func (p *StripeProcessor) CreatePayment(req PaymentRequest) (PaymentResult, error) {
	if p.secretKey == "" {
		return PaymentResult{}, fmt.Errorf("STRIPE_SECRET_KEY is not set")
	}

	form := url.Values{}
	form.Set("mode", "payment")
	form.Set("line_items[0][quantity]", "1")
	form.Set("line_items[0][price_data][currency]", req.Currency)
	form.Set("line_items[0][price_data][unit_amount]", fmt.Sprintf("%d", req.Amount))
	form.Set("line_items[0][price_data][product_data][name]", req.Description)
	form.Set("success_url", req.SuccessURL)
	form.Set("cancel_url", req.CancelURL)
	form.Set("metadata[order_id]", req.OrderID)

	var session struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := p.call(http.MethodPost, "/checkout/sessions", form, &session); err != nil {
		return PaymentResult{}, err
	}
	return PaymentResult{ProviderRef: session.ID, RedirectURL: session.URL}, nil
}

// GetStatus retrieves the Checkout Session and maps it to a normalized Status.
func (p *StripeProcessor) GetStatus(providerRef string) (Status, error) {
	var session struct {
		Status        string `json:"status"`         // open | complete | expired
		PaymentStatus string `json:"payment_status"` // paid | unpaid | no_payment_required
	}
	path := "/checkout/sessions/" + url.PathEscape(providerRef)
	if err := p.call(http.MethodGet, path, nil, &session); err != nil {
		return "", err
	}
	switch {
	case session.Status == "complete" && session.PaymentStatus == "paid":
		return StatusCaptured, nil
	case session.Status == "expired":
		return StatusFailed, nil
	default:
		return StatusCreated, nil
	}
}

// call performs an authenticated Stripe API request. Form values are sent
// application/x-www-form-urlencoded, which is what the Stripe API expects.
func (p *StripeProcessor) call(method, path string, form url.Values, out any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}

	req, err := http.NewRequest(method, stripeAPI+path, body)
	if err != nil {
		return err
	}
	// Stripe authenticates the secret key as the Basic-auth username.
	req.SetBasicAuth(p.secretKey, "")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("stripe request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("stripe API error (HTTP %d): %s", resp.StatusCode, stripeErrorMessage(data))
	}
	return json.Unmarshal(data, out)
}

// stripeErrorMessage extracts the human-readable message from a Stripe error body.
func stripeErrorMessage(data []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &e); err == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	return string(data)
}
