package aggregate

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/HarshalPatel1972/epoch/store"
)

func seedEvents(es store.EventStore, aggregateID string, n int) {
	// Append 1 ProductCreated + (n-1) alternating PriceUpdated/StockUpdated events
	es.Append(store.Event{
		Type:        store.EventProductCreated,
		AggregateID: aggregateID,
		Payload:     marshal(store.ProductCreatedPayload{ID: aggregateID, Name: "bench", SKU: "B", Price: 100, Stock: 100, Category: "c"}),
		OccurredAt:  time.Now().AddDate(-1, 0, 0),
	})

	for i := 1; i < n; i++ {
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
			AggregateID: aggregateID,
			Payload:     marshal(payload),
			OccurredAt:  time.Now().AddDate(-1, 0, 0).Add(time.Duration(i) * time.Second),
		})
	}
}

func marshal(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

func BenchmarkProjectNoSnapshot_100(b *testing.B) {
	es := store.NewMemoryEventStore()
	seedEvents(es, "bench-product", 100)
	proj := &Projector{Events: es}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		proj.Project("bench-product", time.Time{})
	}
}

func BenchmarkProjectNoSnapshot_1000(b *testing.B) {
	es := store.NewMemoryEventStore()
	seedEvents(es, "bench-product", 1000)
	proj := &Projector{Events: es}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		proj.Project("bench-product", time.Time{})
	}
}

func BenchmarkProjectWithSnapshot_1000(b *testing.B) {
	es := store.NewMemoryEventStore()
	ss := store.NewMemorySnapshotStore()
	es.SetSnapshotStore(ss)

	proj := &Projector{Events: es, Snapshots: ss}
	es.RequestSnapshot = func(aggregateID string, currentVersion int64, asOf time.Time) error {
		prod, _ := proj.Project(aggregateID, asOf)
		state, _ := json.Marshal(prod)
		return ss.Save(store.Snapshot{
			AggregateID: aggregateID,
			State:       state,
			AsOf:        asOf,
			Version:     currentVersion,
		})
	}

	seedEvents(es, "bench-product", 1000)
	time.Sleep(100 * time.Millisecond)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		proj.Project("bench-product", time.Time{})
	}
}

func BenchmarkProjectWithSnapshot_10000(b *testing.B) {
	es := store.NewMemoryEventStore()
	ss := store.NewMemorySnapshotStore()
	es.SetSnapshotStore(ss)

	proj := &Projector{Events: es, Snapshots: ss}
	es.RequestSnapshot = func(aggregateID string, currentVersion int64, asOf time.Time) error {
		prod, _ := proj.Project(aggregateID, asOf)
		state, _ := json.Marshal(prod)
		return ss.Save(store.Snapshot{
			AggregateID: aggregateID,
			State:       state,
			AsOf:        asOf,
			Version:     currentVersion,
		})
	}

	seedEvents(es, "bench-product", 10000)
	time.Sleep(200 * time.Millisecond)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		proj.Project("bench-product", time.Time{})
	}
}

func BenchmarkProjectAll_100Products(b *testing.B) {
	es := store.NewMemoryEventStore()
	for i := 0; i < 100; i++ {
		seedEvents(es, fmt.Sprintf("prod-%d", i), 10)
	}
	proj := &Projector{Events: es}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		proj.ProjectAll(time.Time{})
	}
}
