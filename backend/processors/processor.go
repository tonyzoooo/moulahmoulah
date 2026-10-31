package processors

import (
	"fmt"
	"sort"
	"sync"
)

// Status is the normalized lifecycle state of a payment across all processors.
//
//	CREATED -> AUTHORIZED -> CAPTURED (success)
//	          \-> DECLINED / FAILED
type Status string

const (
	StatusCreated    Status = "CREATED"
	StatusAuthorized Status = "AUTHORIZED"
	StatusCaptured   Status = "CAPTURED"
	StatusDeclined   Status = "DECLINED"
	StatusFailed     Status = "FAILED"
)

// Terminal reports whether the status is a final state.
func (s Status) Terminal() bool {
	return s == StatusCaptured || s == StatusDeclined || s == StatusFailed
}

// PaymentRequest describes a checkout in processor-neutral terms.
type PaymentRequest struct {
	OrderID     string
	Currency    string // ISO 4217, lowercase (e.g. "eur")
	Amount      int64  // minor units (cents)
	Description string
	SuccessURL  string
	CancelURL   string
	// Metadata is attached to the payment object the processor creates
	// (e.g. the Stripe PaymentIntent). Values must be strings.
	Metadata map[string]string
	// StatementDescriptor is the short text appended to the card statement
	// (sent as statement_descriptor_suffix, the variant cards support).
	StatementDescriptor string
	// CaptureManual requests authorize-only (manual capture), so the payment
	// moves CREATED -> AUTHORIZED and is captured later via Capture.
	CaptureManual bool
	// LineItems are the individual cart lines, used to render the hosted
	// checkout page. When empty, Description + Amount are used as one line.
	LineItems []LineItem
}

// LineItem is one cart line on a hosted checkout page.
type LineItem struct {
	Name     string
	Amount   int64 // unit price in minor units
	Quantity int64
	ImageURL string
}

// PaymentResult is what a processor returns right after creating a payment.
type PaymentResult struct {
	ProviderRef string // e.g. Stripe Checkout Session id ("cs_test_...")
	RedirectURL string // hosted page the customer should be sent to
}

// Processor is the common interface every payment provider implements.
// Adding a new provider means adding one file that registers itself here.
type Processor interface {
	ID() string
	Label() string
	CreatePayment(req PaymentRequest) (PaymentResult, error)
	GetStatus(providerRef string) (Status, error)
	// Capture captures a previously authorized payment.
	Capture(providerRef string) error
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Processor{}
)

// Register adds a processor to the global registry.
func Register(p Processor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[p.ID()] = p
}

// Get returns the processor with the given ID.
func Get(id string) (Processor, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, ok := registry[id]
	if !ok {
		return nil, fmt.Errorf("unknown processor %q", id)
	}
	return p, nil
}

// IDs returns the sorted IDs of all registered processors.
func IDs() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	ids := make([]string, 0, len(registry))
	for id := range registry {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
