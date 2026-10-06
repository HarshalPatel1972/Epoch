package epochtest

import (
	"context"
	"fmt"
	"testing"

	"github.com/HarshalPatel1972/epoch/v2"
)

// Bench runs Epoch's benchmarks against a store:
//
//	func BenchmarkStore(b *testing.B) { epochtest.Bench(b, newStore) }
func Bench(b *testing.B, newStore func(b *testing.B) epoch.Store) {
	ctx := context.Background()

	b.Run("Handle", func(b *testing.B) {
		s := newStore(b)
		m := newAccounts(0)
		if _, err := m.Handle(ctx, s, "a", openAccount{Limit: 1 << 40}); err != nil {
			b.Fatal(err)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := m.Handle(ctx, s, "a", deposit{Amount: 1}); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("HandleManyStreams", func(b *testing.B) {
		s := newStore(b)
		m := newAccounts(0)
		for i := range 1000 {
			if _, err := m.Handle(ctx, s, fmt.Sprint(i), openAccount{}); err != nil {
				b.Fatal(err)
			}
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := m.Handle(ctx, s, fmt.Sprint(i%1000), deposit{Amount: 1}); err != nil {
				b.Fatal(err)
			}
		}
	})

	for _, events := range []int{1_000, 10_000} {
		for _, snap := range []int{-1, 100} {
			name := fmt.Sprintf("Load/events=%d/snapshots=%v", events, snap > 0)
			b.Run(name, func(b *testing.B) {
				s := newStore(b)
				// Build the history with snapshots on (building it without
				// them is quadratic), then read it with the model under test,
				// which ignores snapshots when snap < 0.
				fill(b, s, newAccounts(100), "a", events)
				m := newAccounts(snap)
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := m.Load(ctx, s, "a"); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}

	b.Run("Replay/commands=10000", func(b *testing.B) {
		s := newStore(b)
		m := newAccounts(100)
		for i := range 100 {
			fill(b, s, m, fmt.Sprint(i), 100)
		}
		strict := withDecide(m, decideNoOverdraft)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rep, err := epoch.Replay(ctx, s, epoch.ReplayOptions{Name: fmt.Sprint("r", i), Models: []epoch.AnyModel{strict}})
			if err != nil {
				b.Fatal(err)
			}
			if rep.Commits != 10_000 {
				b.Fatalf("replayed %d commits", rep.Commits)
			}
		}
		b.ReportMetric(float64(b.N*10_000)/b.Elapsed().Seconds(), "commands/s")
	})

	b.Run("Projection/commits=10000", func(b *testing.B) {
		s := newStore(b)
		m := newAccounts(0)
		for i := range 100 {
			fill(b, s, m, fmt.Sprint(i), 100)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := ledgerView.Get(ctx, s); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// fill writes an open command followed by n-1 deposits and withdrawals.
func fill(b *testing.B, s epoch.Store, m *epoch.Model[account], id string, n int) {
	b.Helper()
	ctx := context.Background()
	if _, err := m.Handle(ctx, s, id, openAccount{Limit: 50}); err != nil {
		b.Fatal(err)
	}
	for i := 1; i < n; i++ {
		var cmd any = deposit{Amount: 10}
		if i%3 == 0 {
			cmd = withdraw{Amount: 15}
		}
		if _, err := m.Handle(ctx, s, id, cmd); err != nil {
			b.Fatal(err)
		}
	}
}
