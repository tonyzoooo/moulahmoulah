package main

import (
	"sync"
	"time"

	"moulahmoulah/processors"
)

// Order is a customer order moving through the payment lifecycle.
type Order struct {
	ID          string            `json:"id"`
	Items       []Product         `json:"items"`
	Amount      int64             `json:"amount"` // minor units
	Currency    string            `json:"currency"`
	ProcessorID string            `json:"processorId"`
	ProviderRef string            `json:"providerRef,omitempty"`
	Status      processors.Status `json:"status"`
	CreatedAt   time.Time         `json:"createdAt"`
}

// orderStore is an in-memory order store guarded by a mutex.
type orderStore struct {
	mu     sync.RWMutex
	orders map[string]*Order
}

var store = &orderStore{orders: map[string]*Order{}}

func (s *orderStore) create(o *Order) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orders[o.ID] = o
}

func (s *orderStore) get(id string) (*Order, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.orders[id]
	return o, ok
}

func (s *orderStore) update(id, providerRef string, status processors.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o, ok := s.orders[id]; ok {
		o.Status = status
		if providerRef != "" {
			o.ProviderRef = providerRef
		}
	}
}
