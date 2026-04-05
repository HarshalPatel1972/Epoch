package store

import (
	"time"
)

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
	LoadAll() ([]Event, error)
	AllAggregateIDs() []string
	IsReady() bool
}
