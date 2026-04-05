package seed

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/HarshalPatel1972/epoch/store"
	"github.com/google/uuid"
)

func Run(es store.EventStore) {
	now := time.Now()

	// Product 1: MacBook Pro — price drops over 6 months
	mbpID := uuid.New().String()
	appendEvent(es, store.Event{
		Type:        store.EventProductCreated,
		AggregateID: mbpID,
		Payload: marshal(store.ProductCreatedPayload{
			ID:       mbpID,
			Name:     "MacBook Pro M3",
			SKU:      "MBP-001",
			Price:    2499.99,
			Stock:    10,
			Category: "laptops",
		}),
		OccurredAt: now.AddDate(0, -6, 0),
	})

	appendEvent(es, store.Event{
		Type:        store.EventProductPriceUpdate,
		AggregateID: mbpID,
		Payload: marshal(store.PriceUpdatedPayload{
			OldPrice: 2499.99,
			NewPrice: 2299.99,
		}),
		OccurredAt: now.AddDate(0, -4, 0),
	})

	appendEvent(es, store.Event{
		Type:        store.EventProductPriceUpdate,
		AggregateID: mbpID,
		Payload: marshal(store.PriceUpdatedPayload{
			OldPrice: 2299.99,
			NewPrice: 1999.99,
		}),
		OccurredAt: now.AddDate(0, -2, 0),
	})

	// Product 2: USB-C Hub — stock fluctuates
	hubID := uuid.New().String()
	appendEvent(es, store.Event{
		Type:        store.EventProductCreated,
		AggregateID: hubID,
		Payload: marshal(store.ProductCreatedPayload{
			ID:       hubID,
			Name:     "USB-C Hub",
			SKU:      "HUB-001",
			Price:    49.99,
			Stock:    50,
			Category: "accessories",
		}),
		OccurredAt: now.AddDate(0, -5, 0),
	})

	for i := 1; i <= 5; i++ {
		appendEvent(es, store.Event{
			Type:        store.EventProductStockUpdate,
			AggregateID: hubID,
			Payload: marshal(store.StockUpdatedPayload{
				Delta:    -5,
				NewStock: 50 - (5 * i),
			}),
			OccurredAt: now.AddDate(0, -5, i*7),
		})
	}

	// Product 3: Keyboard — gets deleted after 3 months
	kbID := uuid.New().String()
	appendEvent(es, store.Event{
		Type:        store.EventProductCreated,
		AggregateID: kbID,
		Payload: marshal(store.ProductCreatedPayload{
			ID:       kbID,
			Name:     "Mechanical Keyboard",
			SKU:      "KB-001",
			Price:    129.99,
			Stock:    20,
			Category: "peripherals",
		}),
		OccurredAt: now.AddDate(0, -6, 0),
	})

	appendEvent(es, store.Event{
		Type:        store.EventProductDeleted,
		AggregateID: kbID,
		Payload:     []byte("{}"),
		OccurredAt:  now.AddDate(0, -3, 0),
	})

	// Product 4: Monitor — high volume stock events (triggers snapshot at version 10, 20...)
	monID := uuid.New().String()
	appendEvent(es, store.Event{
		Type:        store.EventProductCreated,
		AggregateID: monID,
		Payload: marshal(store.ProductCreatedPayload{
			ID:       monID,
			Name:     "4K Monitor",
			SKU:      "MON-001",
			Price:    599.99,
			Stock:    100,
			Category: "peripherals",
		}),
		OccurredAt: now.AddDate(0, -6, 0),
	})

	for i := 1; i <= 25; i++ {
		appendEvent(es, store.Event{
			Type:        store.EventProductStockUpdate,
			AggregateID: monID,
			Payload: marshal(store.StockUpdatedPayload{
				Delta:    -1,
				NewStock: 100 - i,
			}),
			OccurredAt: now.AddDate(0, -6, i), // Day i
		})
	}

	slog.Info("demo data seeded", "mbp_id", mbpID, "hub_id", hubID, "kb_id", kbID, "mon_id", monID)
}

func marshal(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

func appendEvent(es store.EventStore, e store.Event) {
	_, err := es.Append(e)
	if err != nil {
		slog.Error("error seeding event", "err", err)
	}
}
