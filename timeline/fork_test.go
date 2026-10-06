package timeline

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/HarshalPatel1972/epoch/aggregate"
	"github.com/HarshalPatel1972/epoch/store"
)

type stores struct {
	events store.EventStore
	snaps  store.SnapshotStore
}

func eachStore(t *testing.T, fn func(t *testing.T, s stores)) {
	t.Run("memory", func(t *testing.T) {
		fn(t, stores{store.NewMemoryEventStore(), store.NewMemorySnapshotStore()})
	})
	t.Run("badger", func(t *testing.T) {
		bs, err := store.NewBadgerEventStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { bs.Close() })
		fn(t, stores{bs, store.NewBadgerSnapshotStore(bs.DB())})
	})
}

func appendT(t *testing.T, es store.EventStore, typ store.EventType, payload any, at time.Time) store.Event {
	t.Helper()
	b, _ := json.Marshal(payload)
	e, err := es.Append(store.Event{AggregateID: "p1", Type: typ, Payload: b, OccurredAt: at})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// seedHistory creates p1 at base with price 100, then 11 hourly price updates
// (101..111), snapshotting synchronously at every 10th version like main.go.
func seedHistory(t *testing.T, s stores, base time.Time) {
	t.Helper()
	proj := &aggregate.Projector{Events: s.events, Snapshots: s.snaps}
	snap := func(e store.Event) {
		if e.Version%10 != 0 {
			return
		}
		prod, err := proj.Project("p1", e.OccurredAt)
		if err != nil {
			t.Fatal(err)
		}
		state, _ := json.Marshal(prod)
		if err := s.snaps.Save(store.Snapshot{AggregateID: "p1", State: state, AsOf: e.OccurredAt, Version: e.Version}); err != nil {
			t.Fatal(err)
		}
	}
	snap(appendT(t, s.events, store.EventProductCreated, store.ProductCreatedPayload{ID: "p1", Name: "x", Price: 100}, base))
	for i := 1; i <= 11; i++ {
		snap(appendT(t, s.events, store.EventProductPriceUpdate, store.PriceUpdatedPayload{NewPrice: float64(100 + i)}, base.Add(time.Duration(i)*time.Hour)))
	}
}

// Regression: a fork created before main's latest snapshot used to read that
// snapshot (leaking post-fork main state) and drop its own writes, because
// overlay versions restarted at 1.
func TestForkIgnoresSnapshotsAfterForkPoint(t *testing.T) {
	eachStore(t, func(t *testing.T, s stores) {
		base := time.Now().Add(-100 * time.Hour)
		seedHistory(t, s, base)

		reg := NewForkRegistry(s.events, s.snaps)
		fork, err := reg.Create("f", base.Add(2*time.Hour+30*time.Minute), "")
		if err != nil {
			t.Fatal(err)
		}

		got, err := fork.Projector().Project("p1", time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if got.Price != 102 {
			t.Fatalf("fork before writes: price = %v, want 102 (state at fork point)", got.Price)
		}

		appendT(t, fork.EventStore(), store.EventProductPriceUpdate, store.PriceUpdatedPayload{NewPrice: 5}, time.Time{})

		got, err = fork.Projector().Project("p1", time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if got.Price != 5 {
			t.Fatalf("fork after write: price = %v, want 5", got.Price)
		}
		if n := fork.EventCount(); n != 1 {
			t.Fatalf("fork EventCount = %d, want 1", n)
		}

		main := &aggregate.Projector{Events: s.events, Snapshots: s.snaps}
		got, _ = main.Project("p1", time.Time{})
		if got.Price != 111 {
			t.Fatalf("main after fork write: price = %v, want 111", got.Price)
		}
	})
}

func TestProjectAtPastTimeUsesSnapshotCorrectly(t *testing.T) {
	eachStore(t, func(t *testing.T, s stores) {
		base := time.Now().Add(-100 * time.Hour)
		seedHistory(t, s, base)
		proj := &aggregate.Projector{Events: s.events, Snapshots: s.snaps}

		for hour, want := range map[int]float64{0: 100, 5: 105, 9: 109, 10: 110, 11: 111} {
			got, err := proj.Project("p1", base.Add(time.Duration(hour)*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if got.Price != want {
				t.Errorf("at hour %d: price = %v, want %v", hour, got.Price, want)
			}
		}
	})
}

func TestLoadAfterSkipsToVersionAndStopsAtCutoff(t *testing.T) {
	eachStore(t, func(t *testing.T, s stores) {
		base := time.Now().Add(-100 * time.Hour)
		seedHistory(t, s, base)

		evts, err := s.events.LoadAfter("p1", 3, base.Add(6*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		// Version k occurred at hour k-1, so hour 6 includes version 7.
		if len(evts) != 4 || evts[0].Version != 4 || evts[3].Version != 7 {
			t.Fatalf("LoadAfter(3, hour 6) returned %d events %+v, want versions 4..7", len(evts), evts)
		}
	})
}

func TestAppendRejectsOutOfOrderEvents(t *testing.T) {
	eachStore(t, func(t *testing.T, s stores) {
		base := time.Now().Add(-100 * time.Hour)
		appendT(t, s.events, store.EventProductCreated, store.ProductCreatedPayload{ID: "p1"}, base)
		b, _ := json.Marshal(store.PriceUpdatedPayload{NewPrice: 1})
		_, err := s.events.Append(store.Event{AggregateID: "p1", Type: store.EventProductPriceUpdate, Payload: b, OccurredAt: base.Add(-time.Hour)})
		if err != store.ErrOutOfOrder {
			t.Fatalf("err = %v, want ErrOutOfOrder", err)
		}
	})
}
