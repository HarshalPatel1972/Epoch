package store

import (
	"errors"
	"time"
)

// ErrOutOfOrder is returned when an event's OccurredAt is earlier than the
// previous event of the same aggregate.
var ErrOutOfOrder = errors.New("event occurred_at is before the aggregate's previous event")

type EventType string

const (
	EventProductCreated     EventType = "product.created"
	EventProductPriceUpdate EventType = "product.price_updated"
	EventProductStockUpdate EventType = "product.stock_updated"
	EventProductDeleted     EventType = "product.deleted"
)

type Event struct {
	ID          string    `json:"id"`
	Type        EventType `json:"type"`
	AggregateID string    `json:"aggregate_id"`
	Payload     []byte    `json:"payload"`
	OccurredAt  time.Time `json:"occurred_at"`
	Version     int64     `json:"version"`
}

// Payload shapes (JSON)

type ProductCreatedPayload struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	SKU      string  `json:"sku"`
	Price    float64 `json:"price"`
	Stock    int     `json:"stock"`
	Category string  `json:"category"`
}

type PriceUpdatedPayload struct {
	OldPrice float64 `json:"old_price"`
	NewPrice float64 `json:"new_price"`
}

type StockUpdatedPayload struct {
	Delta    int `json:"delta"`
	NewStock int `json:"new_stock"`
}

type EventStore interface {
	Append(e Event) (Event, error)
	Load(aggregateID string) ([]Event, error)
	LoadBefore(aggregateID string, cutoff time.Time) ([]Event, error)
	// LoadAfter returns events with Version > afterVersion and OccurredAt <= cutoff,
	// in version order. Stores rely on per-aggregate timestamps being monotonic so
	// the scan can stop at the first event past the cutoff.
	LoadAfter(aggregateID string, afterVersion int64, cutoff time.Time) ([]Event, error)
	LoadAll() ([]Event, error)
	AllAggregateIDs() []string
	IsReady() bool
}
