// Package epochtest is a conformance suite for epoch.Store implementations.
//
// Call Run from a test in your store's package:
//
//	func TestConformance(t *testing.T) {
//		epochtest.Run(t, func(t *testing.T) epoch.Store { return mystore.New(...) })
//	}
//
// The suite checks the Store contract directly and then runs the epoch
// package's own behaviour (time travel, branches, replay, projections)
// against your store.
package epochtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/HarshalPatel1972/epoch/v2"
)

// Factory returns a new, empty store. Use t.Cleanup to release resources.
type Factory func(t *testing.T) epoch.Store

// Run runs the whole suite.
func Run(t *testing.T, newStore Factory) {
	t.Run("Store", func(t *testing.T) { RunStore(t, newStore) })
	t.Run("Epoch", func(t *testing.T) { RunEpoch(t, newStore) })
}

// RunStore checks the epoch.Store contract.
func RunStore(t *testing.T, newStore Factory) {
	tests := []struct {
		name string
		fn   func(t *testing.T, s epoch.Store)
	}{
		{"AppendAssignsSeqAndVersion", testAppendAssignsSeqAndVersion},
		{"AppendChecksExpectedVersion", testAppendChecksExpectedVersion},
		{"AppendUsesBaseVersion", testAppendUsesBaseVersion},
		{"AppendClampsTime", testAppendClampsTime},
		{"AppendToMissingBranch", testAppendToMissingBranch},
		{"TimesRoundTrip", testTimesRoundTrip},
		{"ReadFilters", testReadFilters},
		{"ReadReturnsCopies", testReadReturnsCopies},
		{"SeqIsGlobalAcrossBranches", testSeqIsGlobal},
		{"SeqAt", testSeqAt},
		{"Branches", testBranches},
		{"DeleteBranchRemovesData", testDeleteBranchRemovesData},
		{"Snapshots", testSnapshots},
		{"ConcurrentAppends", testConcurrentAppends},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.fn(t, newStore(t)) })
	}
}

var t0 = time.Date(2025, 1, 1, 9, 0, 0, 0, time.UTC)

func commit(branch, stream string, events int, at time.Time) *epoch.Commit {
	c := &epoch.Commit{
		Branch: branch, Time: at, Model: "m", Stream: stream,
		Command: &epoch.CommandData{Type: "cmd", Data: json.RawMessage(`{"n":1}`)},
	}
	for i := range events {
		c.Events = append(c.Events, epoch.EventData{Type: "ev", Data: json.RawMessage(fmt.Sprintf(`{"i":%d}`, i))})
	}
	return c
}

func mustAppend(t *testing.T, s epoch.Store, c *epoch.Commit, cond epoch.AppendCondition) {
	t.Helper()
	if err := s.Append(context.Background(), c, cond); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

func anyVersion() epoch.AppendCondition { return epoch.AppendCondition{ExpectedVersion: epoch.Any} }

func testAppendAssignsSeqAndVersion(t *testing.T, s epoch.Store) {
	a1 := commit(epoch.Main, "a", 2, t0)
	b1 := commit(epoch.Main, "b", 1, t0)
	a2 := commit(epoch.Main, "a", 1, t0)
	rejected := commit(epoch.Main, "a", 0, t0)
	rejected.Rejected = "no"
	for _, c := range []*epoch.Commit{a1, b1, a2, rejected} {
		mustAppend(t, s, c, anyVersion())
	}
	if !(a1.Seq > 0 && b1.Seq > a1.Seq && a2.Seq > b1.Seq && rejected.Seq > a2.Seq) {
		t.Fatalf("Seqs not increasing: %d %d %d %d", a1.Seq, b1.Seq, a2.Seq, rejected.Seq)
	}
	for _, tc := range []struct {
		c    *epoch.Commit
		want int64
	}{{a1, 2}, {b1, 1}, {a2, 3}, {rejected, 3}} {
		if tc.c.Version != tc.want {
			t.Errorf("commit %d: Version = %d, want %d", tc.c.Seq, tc.c.Version, tc.want)
		}
	}
	got, err := s.Read(context.Background(), epoch.Query{Branch: epoch.Main, Model: "m", Stream: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[2].Rejected != "no" || got[0].Version != 2 {
		t.Fatalf("Read(stream a) = %+v", got)
	}
}

func testAppendChecksExpectedVersion(t *testing.T, s epoch.Store) {
	ctx := context.Background()
	mustAppend(t, s, commit(epoch.Main, "a", 2, t0), epoch.AppendCondition{ExpectedVersion: 0})
	err := s.Append(ctx, commit(epoch.Main, "a", 1, t0), epoch.AppendCondition{ExpectedVersion: 1})
	if !errors.Is(err, epoch.ErrConflict) {
		t.Fatalf("stale ExpectedVersion: err = %v, want ErrConflict", err)
	}
	mustAppend(t, s, commit(epoch.Main, "a", 1, t0), epoch.AppendCondition{ExpectedVersion: 2})
	mustAppend(t, s, commit(epoch.Main, "a", 1, t0), anyVersion())
	if err := s.Append(ctx, commit(epoch.Main, "new", 1, t0), epoch.AppendCondition{ExpectedVersion: 1}); !errors.Is(err, epoch.ErrConflict) {
		t.Fatalf("new stream with ExpectedVersion 1: err = %v, want ErrConflict", err)
	}
}

func testAppendUsesBaseVersion(t *testing.T, s epoch.Store) {
	ctx := context.Background()
	if err := s.CreateBranch(ctx, epoch.Branch{Name: "b", Parent: epoch.Main}); err != nil {
		t.Fatal(err)
	}
	c := commit("b", "a", 1, t0)
	if err := s.Append(ctx, c, epoch.AppendCondition{ExpectedVersion: 0, BaseVersion: 5}); !errors.Is(err, epoch.ErrConflict) {
		t.Fatalf("ExpectedVersion 0 with BaseVersion 5: err = %v, want ErrConflict", err)
	}
	mustAppend(t, s, c, epoch.AppendCondition{ExpectedVersion: 5, BaseVersion: 5})
	if c.Version != 6 {
		t.Fatalf("Version = %d, want 6", c.Version)
	}
	// Once the branch has its own commit, BaseVersion is ignored.
	c2 := commit("b", "a", 1, t0)
	mustAppend(t, s, c2, epoch.AppendCondition{ExpectedVersion: 6, BaseVersion: 99})
	if c2.Version != 7 {
		t.Fatalf("Version = %d, want 7", c2.Version)
	}
}

func testAppendClampsTime(t *testing.T, s epoch.Store) {
	ctx := context.Background()
	if err := s.CreateBranch(ctx, epoch.Branch{Name: "b", Parent: epoch.Main}); err != nil {
		t.Fatal(err)
	}
	c1 := commit("b", "a", 1, t0)
	mustAppend(t, s, c1, epoch.AppendCondition{ExpectedVersion: epoch.Any, MinTime: t0.Add(time.Hour)})
	if !c1.Time.Equal(t0.Add(time.Hour)) {
		t.Fatalf("time before MinTime: got %v, want %v", c1.Time, t0.Add(time.Hour))
	}
	c2 := commit("b", "x", 1, t0.Add(30*time.Minute))
	mustAppend(t, s, c2, anyVersion())
	if !c2.Time.Equal(c1.Time) {
		t.Fatalf("time before previous commit: got %v, want %v", c2.Time, c1.Time)
	}
	c3 := commit("b", "a", 1, t0.Add(2*time.Hour))
	mustAppend(t, s, c3, anyVersion())
	if !c3.Time.Equal(t0.Add(2 * time.Hour)) {
		t.Fatalf("later time changed: got %v", c3.Time)
	}
	// Other branches have their own clocks.
	c4 := commit(epoch.Main, "a", 1, t0)
	mustAppend(t, s, c4, anyVersion())
	if !c4.Time.Equal(t0) {
		t.Fatalf("main affected by branch b's clock: got %v", c4.Time)
	}
}

func testAppendToMissingBranch(t *testing.T, s epoch.Store) {
	err := s.Append(context.Background(), commit("nope", "a", 1, t0), anyVersion())
	if !errors.Is(err, epoch.ErrBranchNotFound) {
		t.Fatalf("err = %v, want ErrBranchNotFound", err)
	}
}

func testTimesRoundTrip(t *testing.T, s epoch.Store) {
	at := time.Date(2025, 3, 4, 5, 6, 7, 123456789, time.UTC)
	c := commit(epoch.Main, "a", 1, at)
	mustAppend(t, s, c, anyVersion())
	got, err := s.Read(context.Background(), epoch.Query{Branch: epoch.Main})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Time.Equal(at) {
		t.Fatalf("time = %v, want %v", got[0].Time, at)
	}
	if string(got[0].Command.Data) != `{"n":1}` || string(got[0].Events[0].Data) != `{"i":0}` {
		t.Fatalf("payloads changed: %s %s", got[0].Command.Data, got[0].Events[0].Data)
	}
}

func testReadFilters(t *testing.T, s epoch.Store) {
	ctx := context.Background()
	var seqs []int64
	for i, stream := range []string{"a", "b", "a", "b", "a"} {
		c := commit(epoch.Main, stream, 1, t0.Add(time.Duration(i)*time.Minute))
		c.Command.ID = fmt.Sprintf("cmd-%d", i)
		mustAppend(t, s, c, anyVersion())
		seqs = append(seqs, c.Seq)
	}
	other := commit(epoch.Main, "a", 1, t0.Add(time.Hour))
	other.Model = "other"
	mustAppend(t, s, other, anyVersion())

	check := func(name string, q epoch.Query, want ...int64) {
		t.Helper()
		got, err := s.Read(ctx, q)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var gotSeqs []int64
		for _, c := range got {
			gotSeqs = append(gotSeqs, c.Seq)
		}
		if fmt.Sprint(gotSeqs) != fmt.Sprint(want) {
			t.Errorf("%s: seqs = %v, want %v", name, gotSeqs, want)
		}
	}
	q := epoch.Query{Branch: epoch.Main}
	check("all", q, append(append([]int64{}, seqs...), other.Seq)...)
	check("model", epoch.Query{Branch: epoch.Main, Model: "m"}, seqs...)
	check("stream", epoch.Query{Branch: epoch.Main, Model: "m", Stream: "a"}, seqs[0], seqs[2], seqs[4])
	check("stream other model", epoch.Query{Branch: epoch.Main, Model: "other", Stream: "a"}, other.Seq)
	check("after", epoch.Query{Branch: epoch.Main, Model: "m", AfterSeq: seqs[1]}, seqs[2], seqs[3], seqs[4])
	check("max", epoch.Query{Branch: epoch.Main, Model: "m", MaxSeq: seqs[1]}, seqs[0], seqs[1])
	check("window", epoch.Query{Branch: epoch.Main, Model: "m", Stream: "a", AfterSeq: seqs[0], MaxSeq: seqs[3]}, seqs[2])
	check("limit", epoch.Query{Branch: epoch.Main, Limit: 2}, seqs[0], seqs[1])
	check("reverse", epoch.Query{Branch: epoch.Main, Model: "m", Stream: "a", Reverse: true}, seqs[4], seqs[2], seqs[0])
	check("reverse limit max", epoch.Query{Branch: epoch.Main, Reverse: true, Limit: 2, MaxSeq: seqs[3]}, seqs[3], seqs[2])
	check("command id", epoch.Query{Branch: epoch.Main, CommandID: "cmd-3"}, seqs[3])
	check("unknown command id", epoch.Query{Branch: epoch.Main, CommandID: "zzz"})
	check("unknown branch", epoch.Query{Branch: "zzz"})
}

func testReadReturnsCopies(t *testing.T, s epoch.Store) {
	ctx := context.Background()
	c := commit(epoch.Main, "a", 1, t0)
	mustAppend(t, s, c, anyVersion())
	c.Events[0].Data[1] = 'X'
	got, _ := s.Read(ctx, epoch.Query{Branch: epoch.Main})
	got[0].Events[0].Data[1] = 'Y'
	got[0].Command.Type = "changed"
	again, _ := s.Read(ctx, epoch.Query{Branch: epoch.Main})
	if string(again[0].Events[0].Data) != `{"i":0}` || again[0].Command.Type != "cmd" {
		t.Fatalf("stored commit was modified through a returned or appended value: %+v", again[0])
	}
}

func testSeqIsGlobal(t *testing.T, s epoch.Store) {
	ctx := context.Background()
	if err := s.CreateBranch(ctx, epoch.Branch{Name: "b", Parent: epoch.Main}); err != nil {
		t.Fatal(err)
	}
	var last int64
	for i := range 6 {
		branch := epoch.Main
		if i%2 == 1 {
			branch = "b"
		}
		c := commit(branch, "a", 1, t0)
		mustAppend(t, s, c, anyVersion())
		if c.Seq <= last {
			t.Fatalf("Seq %d not greater than previous %d", c.Seq, last)
		}
		last = c.Seq
	}
	// Seqs are never reused, even after deleting a branch.
	if err := s.DeleteBranch(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	c := commit(epoch.Main, "a", 1, t0)
	mustAppend(t, s, c, anyVersion())
	if c.Seq <= last {
		t.Fatalf("Seq %d reused after DeleteBranch (last was %d)", c.Seq, last)
	}
}

func testSeqAt(t *testing.T, s epoch.Store) {
	ctx := context.Background()
	var seqs []int64
	for i := range 4 {
		c := commit(epoch.Main, "a", 1, t0.Add(time.Duration(i)*time.Hour))
		mustAppend(t, s, c, anyVersion())
		seqs = append(seqs, c.Seq)
	}
	for _, tc := range []struct {
		at   time.Time
		max  int64
		want int64
	}{
		{t0.Add(-time.Second), 0, 0},
		{t0, 0, seqs[0]},
		{t0.Add(90 * time.Minute), 0, seqs[1]},
		{t0.Add(100 * time.Hour), 0, seqs[3]},
		{t0.Add(100 * time.Hour), seqs[2], seqs[2]},
		{t0.Add(time.Hour), seqs[0], seqs[0]},
	} {
		got, err := s.SeqAt(ctx, epoch.Main, tc.at, tc.max)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("SeqAt(%v, %d) = %d, want %d", tc.at, tc.max, got, tc.want)
		}
	}
	if got, err := s.SeqAt(ctx, "nope", t0.Add(time.Hour), 0); err != nil || got != 0 {
		t.Errorf("SeqAt on unknown branch = %d, %v; want 0, nil", got, err)
	}
}

func testBranches(t *testing.T, s epoch.Store) {
	ctx := context.Background()
	b := epoch.Branch{
		Name: "feature", Parent: epoch.Main, ForkSeq: 7, ForkTime: t0, Created: t0.Add(time.Minute),
		Kind: epoch.KindReplay, Description: "what if",
	}
	if err := s.CreateBranch(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateBranch(ctx, b); !errors.Is(err, epoch.ErrBranchExists) {
		t.Fatalf("duplicate CreateBranch: err = %v, want ErrBranchExists", err)
	}
	got, err := s.GetBranch(ctx, "feature")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != b.Name || got.Parent != b.Parent || got.ForkSeq != 7 || !got.ForkTime.Equal(t0) ||
		!got.Created.Equal(b.Created) || got.Kind != b.Kind || got.Description != b.Description {
		t.Fatalf("GetBranch = %+v, want %+v", got, b)
	}
	if _, err := s.GetBranch(ctx, "nope"); !errors.Is(err, epoch.ErrBranchNotFound) {
		t.Fatalf("GetBranch(missing): err = %v, want ErrBranchNotFound", err)
	}
	if err := s.CreateBranch(ctx, epoch.Branch{Name: "child", Parent: "feature", Created: t0.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListBranches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "feature" || list[1].Name != "child" {
		t.Fatalf("ListBranches = %+v, want [feature child]", list)
	}
	if err := s.DeleteBranch(ctx, "nope"); !errors.Is(err, epoch.ErrBranchNotFound) {
		t.Fatalf("DeleteBranch(missing): err = %v, want ErrBranchNotFound", err)
	}
}

func testDeleteBranchRemovesData(t *testing.T, s epoch.Store) {
	ctx := context.Background()
	if err := s.CreateBranch(ctx, epoch.Branch{Name: "b", Parent: epoch.Main}); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, s, commit("b", "a", 1, t0), anyVersion())
	if err := s.SaveSnapshot(ctx, epoch.Snapshot{Branch: "b", Model: "m", Stream: "a", Key: "k", Seq: 1, Version: 1, State: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBranch(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateBranch(ctx, epoch.Branch{Name: "b", Parent: epoch.Main}); err != nil {
		t.Fatalf("re-creating a deleted branch: %v", err)
	}
	cs, _ := s.Read(ctx, epoch.Query{Branch: "b"})
	snap, _ := s.LoadSnapshot(ctx, "b", "m", "a", "k", 0)
	if len(cs) != 0 || snap != nil {
		t.Fatalf("deleted branch data survived: %d commits, snapshot %v", len(cs), snap)
	}
}

func testSnapshots(t *testing.T, s epoch.Store) {
	ctx := context.Background()
	save := func(seq int64, key, state string) {
		t.Helper()
		err := s.SaveSnapshot(ctx, epoch.Snapshot{Branch: epoch.Main, Model: "m", Stream: "a", Key: key, Seq: seq, Version: seq * 10, State: json.RawMessage(state)})
		if err != nil {
			t.Fatal(err)
		}
	}
	save(5, "k1", `{"v":5}`)
	save(10, "k1", `{"v":10}`)
	save(20, "k1", `{"v":20}`)
	save(15, "k2", `{"v":15}`)
	save(10, "k1", `{"v":"10b"}`) // replaces

	for _, tc := range []struct {
		key  string
		max  int64
		want string
	}{
		{"k1", 0, `{"v":20}`},
		{"k1", 19, `{"v":"10b"}`},
		{"k1", 4, ""},
		{"k2", 0, `{"v":15}`},
		{"k3", 0, ""},
	} {
		snap, err := s.LoadSnapshot(ctx, epoch.Main, "m", "a", tc.key, tc.max)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case tc.want == "" && snap != nil:
			t.Errorf("LoadSnapshot(%s, %d) = %s, want none", tc.key, tc.max, snap.State)
		case tc.want != "" && (snap == nil || string(snap.State) != tc.want):
			t.Errorf("LoadSnapshot(%s, %d) = %v, want %s", tc.key, tc.max, snap, tc.want)
		case snap != nil && snap.Version != snap.Seq*10:
			t.Errorf("snapshot Version = %d, want %d", snap.Version, snap.Seq*10)
		}
	}
	if snap, _ := s.LoadSnapshot(ctx, epoch.Main, "m", "b", "k1", 0); snap != nil {
		t.Errorf("snapshot leaked across streams")
	}
}

func testConcurrentAppends(t *testing.T, s epoch.Store) {
	ctx := context.Background()
	const workers, each = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				for {
					cs, err := s.Read(ctx, epoch.Query{Branch: epoch.Main, Model: "m", Stream: "a", Reverse: true, Limit: 1})
					if err != nil {
						errs <- err
						return
					}
					var v int64
					if len(cs) > 0 {
						v = cs[0].Version
					}
					err = s.Append(ctx, commit(epoch.Main, "a", 1, t0), epoch.AppendCondition{ExpectedVersion: v})
					if errors.Is(err, epoch.ErrConflict) {
						continue
					}
					if err != nil {
						errs <- err
						return
					}
					break
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	cs, err := s.Read(ctx, epoch.Query{Branch: epoch.Main})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != workers*each {
		t.Fatalf("%d commits, want %d", len(cs), workers*each)
	}
	for i, c := range cs {
		if c.Version != int64(i+1) {
			t.Fatalf("commit %d has Version %d, want %d: a lost update got through", i, c.Version, i+1)
		}
	}
}
