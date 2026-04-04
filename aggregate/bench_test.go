package aggregate

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/HarshalPatel1972/epoch/store"
)

func createBenchEnv(withSnapshots bool) (store.EventStore, store.SnapshotStore, *Projector) {
	es := store.NewMemoryEventStore()
	ss := store.NewMemorySnapshotStore()
	es.SetSnapshotStore(ss)

	p := &Projector{
		Events:    es,
		Snapshots: ss,
	}

	if withSnapshots {
		es.RequestSnapshot = func(aggregateID string, currentVersion int64, asOf time.Time) error {
			prod, _ := p.Project(aggregateID, asOf)
			if prod == nil {
				return nil
			}
			state, _ := json.Marshal(prod)
			return ss.Save(store.Snapshot{
				AggregateID: aggregateID,
				State:       state,
				AsOf:        asOf,
				Version:     currentVersion,
			})
		}
	}

	return es, ss, p
}

func seedEvents(es store.EventStore, id string, count int) {
	// First event: ProductCreated
	es.Append(store.Event{
		Type:        store.EventProductCreated,
		AggregateID: id,
		Payload:     marshal(store.ProductCreatedPayload{ID: id, Name: "Bench", SKU: "B-1", Price: 100, Stock: 100, Category: "test"}),
		OccurredAt:  time.Now(),
	})

	for i := 1; i < count; i++ {
		var et store.EventType
		var payload interface{}
		if i%2 == 0 {
			et = store.EventProductPriceUpdate
			payload = store.PriceUpdatedPayload{OldPrice: 100, NewPrice: 110}
		} else {
			et = store.EventProductStockUpdate
			payload = store.StockUpdatedPayload{Delta: -1, NewStock: 99}
		}
		es.Append(store.Event{
			Type:        et,
			AggregateID: id,
			Payload:     marshal(payload),
			OccurredAt:  time.Now(),
		})
	}
}

func marshal(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

func BenchmarkProjectNoSnapshot100(b *testing.B) {
	b.ReportAllocs()
	id := "bench-100"
	es, _, p := createBenchEnv(false)
	seedEvents(es, id, 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = p.Project(id, time.Time{})
	}
}

func BenchmarkProjectNoSnapshot1000(b *testing.B) {
	b.ReportAllocs()
	id := "bench-1000"
	es, _, p := createBenchEnv(false)
	seedEvents(es, id, 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = p.Project(id, time.Time{})
	}
}

func BenchmarkProjectWithSnapshot1000(b *testing.B) {
	b.ReportAllocs()
	id := "bench-snap-1000"
	es, _, p := createBenchEnv(true)
	seedEvents(es, id, 1000)
	
	// Ensure snapshots are generated (since they are async in MemoryEventStore)
	time.Sleep(100 * time.Millisecond)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = p.Project(id, time.Time{})
	}
}

func BenchmarkProjectWithSnapshot10000(b *testing.B) {
	b.ReportAllocs()
	id := "bench-snap-10000"
	es, _, p := createBenchEnv(true)
	seedEvents(es, id, 10000)
	
	// Ensure snapshots are generated
	time.Sleep(200 * time.Millisecond)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = p.Project(id, time.Time{})
	}
}
