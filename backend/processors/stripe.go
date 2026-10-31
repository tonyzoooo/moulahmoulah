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
	if len(req.LineItems) > 0 {
		// One line per cart item, with product name and image, so the hosted
		// checkout page shows the actual basket.
		for i, item := range req.LineItems {
			prefix := fmt.Sprintf("line_items[%d]", i)
			form.Set(prefix+"[quantity]", fmt.Sprintf("%d", item.Quantity))
			form.Set(prefix+"[price_data][currency]", req.Currency)
			form.Set(prefix+"[price_data][unit_amount]", fmt.Sprintf("%d", item.Amount))
			form.Set(prefix+"[price_data][product_data][name]", item.Name)
			if item.ImageURL != "" {
				form.Set(prefix+"[price_data][product_data][images][0]", item.ImageURL)
			}
		}
	} else {
		form.Set("line_items[0][quantity]", "1")
		form.Set("line_items[0][price_data][currency]", req.Currency)
		form.Set("line_items[0][price_data][unit_amount]", fmt.Sprintf("%d", req.Amount))
		form.Set("line_items[0][price_data][product_data][name]", req.Description)
	}
	form.Set("success_url", req.SuccessURL)
	form.Set("cancel_url", req.CancelURL)
	// Fill the PaymentIntent that Checkout creates: description, statement
	// descriptor, capture method, and metadata all go through
	// payment_intent_data so they land on the PI itself.
	form.Set("payment_intent_data[description]", req.Description)
	// Note: return_url is a confirm-time parameter for API-confirmed
	// PaymentIntents (Payment Element), not a Checkout Session parameter —
	// hosted Checkout redirects via success_url instead.
	if req.StatementDescriptor != "" {
		// Card payments only allow the suffix variant on a PaymentIntent.
		form.Set("payment_intent_data[statement_descriptor_suffix]", req.StatementDescriptor)
	}
	if req.CaptureManual {
		form.Set("payment_intent_data[capture_method]", "manual")
	}
	// Metadata goes on both the Checkout Session and the PaymentIntent.
	for k, v := range req.Metadata {
		form.Set("metadata["+k+"]", v)
		form.Set("payment_intent_data[metadata]["+k+"]", v)
	}

	var session struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := p.call(http.MethodPost, "/checkout/sessions", form, &session); err != nil {
		return PaymentResult{}, err
	}
	return PaymentResult{ProviderRef: session.ID, RedirectURL: session.URL}, nil
}

// GetStatus retrieves the Checkout Session and its PaymentIntent, mapping the
// PaymentIntent status to a normalized Status (the PI is the source of truth).
func (p *StripeProcessor) GetStatus(providerRef string) (Status, error) {
	var session struct {
		Status        string `json:"status"`         // open | complete | expired
		PaymentStatus string `json:"payment_status"` // paid | unpaid | no_payment_required
		PaymentIntent string `json:"payment_intent"` // pi_...
	}
	path := "/checkout/sessions/" + url.PathEscape(providerRef)
	if err := p.call(http.MethodGet, path, nil, &session); err != nil {
		return "", err
	}
	if session.Status == "expired" {
		return StatusFailed, nil
	}

	if session.PaymentIntent != "" {
		var pi struct {
			Status string `json:"status"`
		}
		piPath := "/payment_intents/" + url.PathEscape(session.PaymentIntent)
		if err := p.call(http.MethodGet, piPath, nil, &pi); err == nil {
			switch pi.Status {
			case "succeeded":
				return StatusCaptured, nil
			case "requires_capture":
				return StatusAuthorized, nil
			case "canceled":
				return StatusFailed, nil
			default: // requires_payment_method, requires_confirmation, processing
				return StatusCreated, nil
			}
		}
	}

	if session.Status == "complete" && session.PaymentStatus == "paid" {
		return StatusCaptured, nil
	}
	return StatusCreated, nil
}

// Capture captures the PaymentIntent behind a Checkout Session
// (providerRef is the session id "cs_...").
func (p *StripeProcessor) Capture(providerRef string) error {
	var session struct {
		PaymentIntent string `json:"payment_intent"`
	}
	path := "/checkout/sessions/" + url.PathEscape(providerRef)
	if err := p.call(http.MethodGet, path, nil, &session); err != nil {
		return err
	}
	if session.PaymentIntent == "" {
		return fmt.Errorf("no payment intent on session %s", providerRef)
	}
	var pi struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	capturePath := "/payment_intents/" + url.PathEscape(session.PaymentIntent) + "/capture"
	return p.call(http.MethodPost, capturePath, url.Values{}, &pi)
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
