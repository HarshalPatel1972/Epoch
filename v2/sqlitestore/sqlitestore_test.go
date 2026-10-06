package sqlitestore_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/HarshalPatel1972/epoch/v2"
	"github.com/HarshalPatel1972/epoch/v2/epochtest"
	"github.com/HarshalPatel1972/epoch/v2/sqlitestore"
)

func open(t *testing.T, path string) *sqlitestore.Store {
	t.Helper()
	s, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestConformanceFile(t *testing.T) {
	epochtest.Run(t, func(t *testing.T) epoch.Store {
		// t.TempDir paths often contain spaces on Windows, which is worth testing.
		return open(t, filepath.Join(t.TempDir(), "epoch test.db"))
	})
}

func TestConformanceMemory(t *testing.T) {
	epochtest.Run(t, func(t *testing.T) epoch.Store { return open(t, ":memory:") })
}

func TestDataSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "epoch.db")
	s, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2025, 5, 6, 7, 8, 9, 10, time.UTC)
	c := &epoch.Commit{Branch: epoch.Main, Time: at, Model: "m", Stream: "a", Events: []epoch.EventData{{Type: "e", Data: []byte(`{"x":1}`)}}}
	if err := s.Append(ctx, c, epoch.AppendCondition{ExpectedVersion: 0}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateBranch(ctx, epoch.Branch{Name: "b", Parent: epoch.Main, ForkSeq: c.Seq, ForkTime: at}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s = open(t, path)
	got, err := s.Read(ctx, epoch.Query{Branch: epoch.Main})
	if err != nil || len(got) != 1 || !got[0].Time.Equal(at) || string(got[0].Events[0].Data) != `{"x":1}` {
		t.Fatalf("after reopen: %+v, %v", got, err)
	}
	b, err := s.GetBranch(ctx, "b")
	if err != nil || b.ForkSeq != c.Seq || !b.ForkTime.Equal(at) {
		t.Fatalf("branch after reopen: %+v, %v", b, err)
	}
	c2 := &epoch.Commit{Branch: epoch.Main, Time: at, Model: "m", Stream: "a"}
	if err := s.Append(ctx, c2, epoch.AppendCondition{ExpectedVersion: 1}); err != nil || c2.Seq <= c.Seq {
		t.Fatalf("append after reopen: seq %d, %v", c2.Seq, err)
	}
}

func BenchmarkSQLite(b *testing.B) {
	epochtest.Bench(b, func(b *testing.B) epoch.Store {
		s, err := sqlitestore.Open(filepath.Join(b.TempDir(), "bench.db"))
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { s.Close() })
		return s
	})
}
