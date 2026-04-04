package aggregate

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/HarshalPatel1972/epoch/store"
)

type Product struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	SKU       string    `json:"sku"`
	Price     float64   `json:"price"`
	Stock     int       `json:"stock"`
	Category  string    `json:"category"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Deleted   bool      `json:"deleted"`
}

func (p *Product) Apply(e store.Event) error {
	switch e.Type {
	case store.EventProductCreated:
		var payload store.ProductCreatedPayload
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("failed to unmarshal product created payload: %w", err)
		}
		p.ID = payload.ID
		p.Name = payload.Name
		p.SKU = payload.SKU
		p.Price = payload.Price
		p.Stock = payload.Stock
		p.Category = payload.Category
		p.CreatedAt = e.OccurredAt
		p.UpdatedAt = e.OccurredAt

	case store.EventProductPriceUpdate:
		var payload store.PriceUpdatedPayload
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("failed to unmarshal price updated payload: %w", err)
		}
		p.Price = payload.NewPrice
		p.UpdatedAt = e.OccurredAt

	case store.EventProductStockUpdate:
		var payload store.StockUpdatedPayload
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("failed to unmarshal stock updated payload: %w", err)
		}
		p.Stock = payload.NewStock
		p.UpdatedAt = e.OccurredAt

	case store.EventProductDeleted:
		p.Deleted = true
		p.UpdatedAt = e.OccurredAt
	}

	return nil
}
