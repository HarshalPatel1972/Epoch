package epochtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HarshalPatel1972/epoch/v2"
)

// RunEpoch runs the epoch package's behaviour against a store.
func RunEpoch(t *testing.T, newStore Factory) {
	tests := []struct {
		name string
		fn   func(t *testing.T, s epoch.Store)
	}{
		{"HandleAndLoad", testHandleAndLoad},
		{"RejectionsAreRecorded", testRejectionsAreRecorded},
		{"DecidePanicIsNotRecorded", testDecidePanicIsNotRecorded},
		{"TimeTravel", testTimeTravel},
		{"SnapshotsMatchFullReplay", testSnapshotsMatchFullReplay},
		{"SchemaChangeIgnoresSnapshots", testSchemaChangeIgnoresSnapshots},
		{"CommandIDIsIdempotent", testCommandIDIsIdempotent},
		{"RecordFacts", testRecordFacts},
		{"ForkIsolation", testForkIsolation},
		{"ForkAtTimeIgnoresLaterSnapshots", testForkAtTimeIgnoresLaterSnapshots},
		{"NestedForks", testNestedForks},
		{"DeleteBranch", testDeleteBranch},
		{"BranchNames", testBranchNames},
		{"ReplayWithNewRules", testReplayWithNewRules},
		{"ReplayCopiesOtherModels", testReplayCopiesOtherModels},
		{"ReplayWindow", testReplayWindow},
		{"ReplayWholeHistory", testReplayWholeHistory},
		{"ReplayOfReplay", testReplayOfReplay},
		{"ReplayFailureRemovesBranch", testReplayFailureRemovesBranch},
		{"ReplayReportFromStore", testReplayReportFromStore},
		{"ProjectionAndCompare", testProjectionAndCompare},
		{"ConcurrentHandle", testConcurrentHandle},
		{"Log", testLog},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.fn(t, newStore(t)) })
	}
}

var ctx = context.Background()

func day(n int) time.Time { return t0.AddDate(0, 0, n) }

func handle(t *testing.T, s epoch.Store, m *epoch.Model[account], id string, cmd any, opts ...epoch.Option) epoch.Result[account] {
	t.Helper()
	res, err := m.Handle(ctx, s, id, cmd, opts...)
	if err != nil {
		t.Fatalf("Handle(%s, %T%+v): %v", id, cmd, cmd, err)
	}
	return res
}

func balance(t *testing.T, s epoch.Store, m *epoch.Model[account], id string, opts ...epoch.Option) int64 {
	t.Helper()
	st, err := m.Load(ctx, s, id, opts...)
	if err != nil {
		t.Fatalf("Load(%s): %v", id, err)
	}
	return st.Value.Balance
}

// seedAccounts writes one command per day:
//
//	day 0  open alice, limit 50     day 3  withdraw 120 (ok, -20)
//	day 1  deposit 100              day 4  withdraw 50  (rejected: -70 < -50)
//	day 2  open bob, limit 0        day 5  bob deposit 10
func seedAccounts(t *testing.T, s epoch.Store, m *epoch.Model[account]) {
	t.Helper()
	handle(t, s, m, "alice", openAccount{Limit: 50}, epoch.WithTime(day(0)))
	handle(t, s, m, "alice", deposit{Amount: 100}, epoch.WithTime(day(1)))
	handle(t, s, m, "bob", openAccount{Limit: 0}, epoch.WithTime(day(2)))
	handle(t, s, m, "alice", withdraw{Amount: 120}, epoch.WithTime(day(3)))
	if _, err := m.Handle(ctx, s, "alice", withdraw{Amount: 50}, epoch.WithTime(day(4))); !errors.Is(err, epoch.ErrRejected) {
		t.Fatalf("expected day-4 withdrawal to be rejected, got %v", err)
	}
	handle(t, s, m, "bob", deposit{Amount: 10}, epoch.WithTime(day(5)))
}

func testHandleAndLoad(t *testing.T, s epoch.Store) {
	m := newAccounts(0)
	res := handle(t, s, m, "alice", openAccount{Limit: 10})
	if !res.State.Exists() || res.State.Version != 1 || !res.State.Value.Open || len(res.Events) != 1 {
		t.Fatalf("after open: %+v", res)
	}
	if _, ok := res.Events[0].(opened); !ok {
		t.Fatalf("event type %T, want opened", res.Events[0])
	}
	res = handle(t, s, m, "alice", deposit{Amount: 30})
	if res.State.Value.Balance != 30 || res.State.Version != 2 || res.Commit.Seq == 0 {
		t.Fatalf("after deposit: %+v", res)
	}
	st, err := m.Load(ctx, s, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if st.Value != res.State.Value || st.Version != 2 || st.Seq != res.Commit.Seq {
		t.Fatalf("Load = %+v, want %+v", st, res.State)
	}
	none, err := m.Load(ctx, s, "nobody")
	if err != nil || none.Exists() {
		t.Fatalf("Load(nobody) = %+v, %v", none, err)
	}
	if _, err := m.Handle(ctx, s, "alice", struct{ X int }{}); !errors.Is(err, epoch.ErrUnknownType) {
		t.Fatalf("unregistered command: err = %v, want ErrUnknownType", err)
	}
}

func testRejectionsAreRecorded(t *testing.T, s epoch.Store) {
	m := newAccounts(0)
	_, err := m.Handle(ctx, s, "alice", deposit{Amount: 5})
	if !errors.Is(err, epoch.ErrRejected) || !errors.Is(err, errNotOpen) {
		t.Fatalf("err = %v, want ErrRejected wrapping errNotOpen", err)
	}
	hist, err := m.History(ctx, s, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || hist[0].Rejected != errNotOpen.Error() || len(hist[0].Events) != 0 || hist[0].Command.Type != "deposit" {
		t.Fatalf("History = %+v", hist)
	}
	if st, _ := m.Load(ctx, s, "alice"); st.Exists() {
		t.Fatalf("rejected command changed state: %+v", st)
	}
}

func testDecidePanicIsNotRecorded(t *testing.T, s epoch.Store) {
	m := newAccounts(0)
	_, err := m.Handle(ctx, s, "alice", explode{})
	if err == nil || errors.Is(err, epoch.ErrRejected) || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("err = %v, want a non-rejection panic error", err)
	}
	if hist, _ := m.History(ctx, s, "alice"); len(hist) != 0 {
		t.Fatalf("a panicking Decide was recorded: %+v", hist)
	}
}

func testTimeTravel(t *testing.T, s epoch.Store) {
	m := newAccounts(0)
	seedAccounts(t, s, m)
	for _, tc := range []struct {
		at   time.Time
		want int64
	}{
		{day(0).Add(-time.Hour), 0},
		{day(0), 0},
		{day(1), 100},
		{day(2).Add(12 * time.Hour), 100},
		{day(3), -20},
		{day(10), -20},
	} {
		if got := balance(t, s, m, "alice", epoch.AsOf(tc.at)); got != tc.want {
			t.Errorf("balance as of %s = %d, want %d", tc.at.Format(time.DateTime), got, tc.want)
		}
	}
	if st, _ := m.Load(ctx, s, "bob", epoch.AsOf(day(1))); st.Exists() {
		t.Errorf("bob exists before he opened an account")
	}
	hist, _ := m.History(ctx, s, "alice")
	if got := balance(t, s, m, "alice", epoch.AtSeq(hist[1].Seq)); got != 100 {
		t.Errorf("balance at seq %d = %d, want 100", hist[1].Seq, got)
	}
	if h, _ := m.History(ctx, s, "alice", epoch.AsOf(day(1))); len(h) != 2 {
		t.Errorf("History as of day 1 has %d commits, want 2", len(h))
	}
}

func testSnapshotsMatchFullReplay(t *testing.T, s epoch.Store) {
	snapped := newAccounts(3)
	plain := newAccounts(-1)
	handle(t, s, snapped, "a", openAccount{Limit: 1000}, epoch.WithTime(day(0)))
	for i := 1; i <= 25; i++ {
		var cmd any = deposit{Amount: int64(i)}
		if i%4 == 0 {
			cmd = withdraw{Amount: int64(2 * i)}
		}
		handle(t, s, snapped, "a", cmd, epoch.WithTime(day(i)))
	}
	for d := -1; d <= 26; d++ {
		got := balance(t, s, snapped, "a", epoch.AsOf(day(d)))
		want := balance(t, s, plain, "a", epoch.AsOf(day(d)))
		if got != want {
			t.Fatalf("day %d: with snapshots %d, without %d", d, got, want)
		}
	}
}

func testSchemaChangeIgnoresSnapshots(t *testing.T, s epoch.Store) {
	v1 := newAccounts(2)
	handle(t, s, v1, "a", openAccount{}, epoch.WithTime(day(0)))
	for i := 1; i <= 5; i++ {
		handle(t, s, v1, "a", deposit{Amount: 10}, epoch.WithTime(day(i)))
	}
	// v2 counts every deposit twice. With the same schema it would trust
	// v1's snapshots; with a new schema it must rebuild from events.
	v2 := newAccounts(2)
	v2.Schema = "2"
	v2.Evolve = func(s account, e any) account {
		if d, ok := e.(deposited); ok {
			s.Balance += 2 * d.Amount
			return s
		}
		return evolveAccount(s, e)
	}
	if got := balance(t, s, v2, "a"); got != 100 {
		t.Fatalf("v2 balance = %d, want 100 (v1 snapshots must be ignored)", got)
	}
}

func testCommandIDIsIdempotent(t *testing.T, s epoch.Store) {
	m := newAccounts(0)
	handle(t, s, m, "a", openAccount{})
	first := handle(t, s, m, "a", deposit{Amount: 10}, epoch.CommandID("pay-1"))
	again := handle(t, s, m, "a", deposit{Amount: 10}, epoch.CommandID("pay-1"))
	if !again.Duplicate || again.Commit.Seq != first.Commit.Seq || again.State.Value.Balance != 10 || len(again.Events) != 1 {
		t.Fatalf("duplicate result = %+v", again)
	}
	if got := balance(t, s, m, "a"); got != 10 {
		t.Fatalf("balance = %d after duplicate command, want 10", got)
	}
	_, err := m.Handle(ctx, s, "a", withdraw{Amount: 999}, epoch.CommandID("w-1"))
	_, err2 := m.Handle(ctx, s, "a", withdraw{Amount: 999}, epoch.CommandID("w-1"))
	if !errors.Is(err, epoch.ErrRejected) || !errors.Is(err2, epoch.ErrRejected) {
		t.Fatalf("rejected duplicate: errs = %v / %v", err, err2)
	}
	if hist, _ := m.History(ctx, s, "a"); len(hist) != 3 {
		t.Fatalf("history has %d commits, want 3", len(hist))
	}
}

func testRecordFacts(t *testing.T, s epoch.Store) {
	m := newAccounts(0)
	handle(t, s, m, "a", openAccount{})
	res, err := m.Record(ctx, s, "a", []any{deposited{Amount: 7}, deposited{Amount: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if res.State.Value.Balance != 10 || res.State.Version != 3 || res.Commit.Command != nil {
		t.Fatalf("Record result = %+v", res)
	}
	if _, err := m.Record(ctx, s, "a", []any{struct{}{}}); !errors.Is(err, epoch.ErrUnknownType) {
		t.Fatalf("unregistered fact: err = %v", err)
	}
}

func testForkIsolation(t *testing.T, s epoch.Store) {
	m := newAccounts(2)
	seedAccounts(t, s, m)
	b, err := epoch.Fork(ctx, s, "what-if", epoch.ForkOptions{Description: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Parent != epoch.Main || b.Kind != epoch.KindFork || b.ForkSeq == 0 {
		t.Fatalf("branch = %+v", b)
	}
	handle(t, s, m, "alice", deposit{Amount: 1000}, epoch.On("what-if"))
	handle(t, s, m, "carol", openAccount{}, epoch.On("what-if"))
	handle(t, s, m, "alice", deposit{Amount: 1})

	if got := balance(t, s, m, "alice", epoch.On("what-if")); got != 980 {
		t.Errorf("fork balance = %d, want 980", got)
	}
	if got := balance(t, s, m, "alice"); got != -19 {
		t.Errorf("main balance = %d, want -19", got)
	}
	if st, _ := m.Load(ctx, s, "carol"); st.Exists() {
		t.Errorf("fork-only entity visible on main")
	}
	if _, err := m.Handle(ctx, s, "a", openAccount{}, epoch.On("missing")); !errors.Is(err, epoch.ErrBranchNotFound) {
		t.Errorf("Handle on missing branch: err = %v", err)
	}
}

// The v1 bug: a fork must not read main's snapshots from after the fork
// point, and its own writes must not be shadowed by them.
func testForkAtTimeIgnoresLaterSnapshots(t *testing.T, s epoch.Store) {
	m := newAccounts(2)
	handle(t, s, m, "a", openAccount{Limit: 0}, epoch.WithTime(day(0)))
	for i := 1; i <= 10; i++ {
		handle(t, s, m, "a", deposit{Amount: 1}, epoch.WithTime(day(i)))
	}
	if _, err := epoch.Fork(ctx, s, "past", epoch.ForkOptions{At: day(2).Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if got := balance(t, s, m, "a", epoch.On("past")); got != 2 {
		t.Fatalf("fork balance = %d, want 2 (state at fork point)", got)
	}
	res := handle(t, s, m, "a", withdraw{Amount: 2}, epoch.On("past"))
	if res.State.Value.Balance != 0 || res.State.Version != 4 {
		t.Fatalf("fork after write = %+v, want balance 0 version 4", res.State)
	}
	if !res.Commit.Time.After(day(2)) {
		t.Fatalf("fork commit time %v is before the fork point", res.Commit.Time)
	}
	if got := balance(t, s, m, "a", epoch.On("past")); got != 0 {
		t.Fatalf("fork balance after write = %d, want 0", got)
	}
	if got := balance(t, s, m, "a"); got != 10 {
		t.Fatalf("main balance = %d, want 10", got)
	}
}

func testNestedForks(t *testing.T, s epoch.Store) {
	m := newAccounts(2)
	handle(t, s, m, "a", openAccount{}, epoch.WithTime(day(0)))
	handle(t, s, m, "a", deposit{Amount: 1}, epoch.WithTime(day(1)))
	if _, err := epoch.Fork(ctx, s, "b1", epoch.ForkOptions{}); err != nil {
		t.Fatal(err)
	}
	handle(t, s, m, "a", deposit{Amount: 10}, epoch.On("b1"))
	handle(t, s, m, "a", deposit{Amount: 100}) // main, after b1 forked
	if _, err := epoch.Fork(ctx, s, "b2", epoch.ForkOptions{From: "b1"}); err != nil {
		t.Fatal(err)
	}
	handle(t, s, m, "a", deposit{Amount: 1000}, epoch.On("b2"))
	handle(t, s, m, "a", deposit{Amount: 10000}, epoch.On("b1"))

	// b3 forks b1 at a point before b1's own commits, so it inherits main up
	// to b1's fork point only.
	if _, err := epoch.Fork(ctx, s, "b3", epoch.ForkOptions{From: "b1", At: day(1)}); err != nil {
		t.Fatal(err)
	}
	for branch, want := range map[string]int64{epoch.Main: 101, "b1": 10011, "b2": 1011, "b3": 1} {
		if got := balance(t, s, m, "a", epoch.On(branch)); got != want {
			t.Errorf("%s balance = %d, want %d", branch, got, want)
		}
	}
	bs, err := epoch.ListBranches(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 4 || bs[0].Name != epoch.Main {
		t.Fatalf("ListBranches = %+v", bs)
	}
}

func testDeleteBranch(t *testing.T, s epoch.Store) {
	m := newAccounts(0)
	handle(t, s, m, "a", openAccount{})
	for _, name := range []string{"parent", "child"} {
		from := epoch.Main
		if name == "child" {
			from = "parent"
		}
		if _, err := epoch.Fork(ctx, s, name, epoch.ForkOptions{From: from}); err != nil {
			t.Fatal(err)
		}
	}
	if err := epoch.DeleteBranch(ctx, s, "parent"); !errors.Is(err, epoch.ErrBranchHasChildren) {
		t.Fatalf("deleting a branch with children: err = %v", err)
	}
	if err := epoch.DeleteBranch(ctx, s, epoch.Main); err == nil {
		t.Fatalf("deleting main succeeded")
	}
	for _, name := range []string{"child", "parent"} {
		if err := epoch.DeleteBranch(ctx, s, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := epoch.GetBranch(ctx, s, "parent"); !errors.Is(err, epoch.ErrBranchNotFound) {
		t.Fatalf("deleted branch still exists: %v", err)
	}
}

func testBranchNames(t *testing.T, s epoch.Store) {
	for _, name := range []string{"", "main", "-x", "a b", "a/b", strings.Repeat("x", 101)} {
		if _, err := epoch.Fork(ctx, s, name, epoch.ForkOptions{}); !errors.Is(err, epoch.ErrInvalidBranchName) {
			t.Errorf("Fork(%q): err = %v, want ErrInvalidBranchName", name, err)
		}
	}
	if _, err := epoch.Fork(ctx, s, "ok-1.2_x", epoch.ForkOptions{}); err != nil {
		t.Errorf("valid name rejected: %v", err)
	}
	if _, err := epoch.Fork(ctx, s, "ok-1.2_x", epoch.ForkOptions{}); !errors.Is(err, epoch.ErrBranchExists) {
		t.Errorf("duplicate name: err = %v", err)
	}
	if _, err := epoch.Fork(ctx, s, "future", epoch.ForkOptions{At: time.Now().Add(time.Hour)}); err == nil {
		t.Errorf("fork in the future succeeded")
	}
}

func testReplayWithNewRules(t *testing.T, s epoch.Store) {
	m := newAccounts(2)
	seedAccounts(t, s, m)
	head, _ := epoch.Head(ctx, s, epoch.Main)

	// Stricter: day 3's overdraft withdrawal is now rejected.
	strict, err := epoch.Replay(ctx, s, epoch.ReplayOptions{
		Name: "strict", From: day(1).Add(time.Hour), Models: []epoch.AnyModel{withDecide(m, decideNoOverdraft)},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Day 3's overdraft is now rejected, which leaves enough money for day 4's
	// withdrawal, which was rejected originally.
	if strict.Commits != 4 || strict.Replayed != 4 || strict.Diverged != 2 || strict.NowRejected != 1 ||
		strict.NowAccepted != 1 || strict.ToSeq != head {
		t.Fatalf("strict report = %+v", strict)
	}
	d := strict.Divergences[0]
	if d.Kind != epoch.NowRejected || !d.Replayed.Time.Equal(day(3)) || !d.Original.Time.Equal(day(3)) || d.Replayed.Origin != d.Original.Seq {
		t.Fatalf("first divergence = %+v", d)
	}
	if strict.Divergences[1].Kind != epoch.NowAccepted {
		t.Fatalf("second divergence = %+v", strict.Divergences[1])
	}
	if got := balance(t, s, m, "alice", epoch.On("strict")); got != 50 {
		t.Errorf("strict alice balance = %d, want 50", got)
	}
	if got := balance(t, s, m, "alice", epoch.On("strict"), epoch.AsOf(day(3))); got != 100 {
		t.Errorf("strict alice as of day 3 = %d, want 100", got)
	}
	if got := balance(t, s, m, "alice"); got != -20 {
		t.Errorf("main changed by replay: %d", got)
	}

	// Generous: day 4's withdrawal is now accepted.
	gen, err := epoch.Replay(ctx, s, epoch.ReplayOptions{Name: "generous", Models: []epoch.AnyModel{withDecide(m, decideGenerous)}})
	if err != nil {
		t.Fatal(err)
	}
	if gen.Diverged != 1 || gen.NowAccepted != 1 || gen.Divergences[0].Original.Rejected == "" {
		t.Fatalf("generous report = %+v", gen)
	}

	// Fees: every accepted withdrawal produces different events.
	fee, err := epoch.Replay(ctx, s, epoch.ReplayOptions{Name: "fee", Models: []epoch.AnyModel{withDecide(m, decideWithFee)}})
	if err != nil {
		t.Fatal(err)
	}
	if fee.Diverged != 1 || fee.EventsChanged != 1 {
		t.Fatalf("fee report = %+v", fee)
	}
	if st, _ := m.Load(ctx, s, "alice", epoch.On("fee")); st.Value.Fees != 1 || st.Value.Balance != -21 {
		t.Fatalf("fee alice = %+v", st.Value)
	}
}

func testReplayCopiesOtherModels(t *testing.T, s epoch.Store) {
	m := newAccounts(0)
	handle(t, s, m, "a", openAccount{}, epoch.WithTime(day(0)))
	if _, err := notes.Handle(ctx, s, "a", addNote{Text: "hello"}, epoch.WithTime(day(1))); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Record(ctx, s, "a", []any{deposited{Amount: 5}}, epoch.WithTime(day(2))); err != nil {
		t.Fatal(err)
	}
	handle(t, s, m, "a", withdraw{Amount: 5}, epoch.WithTime(day(3)))

	rep, err := epoch.Replay(ctx, s, epoch.ReplayOptions{Name: "r", Models: []epoch.AnyModel{withDecide(m, decideWithFee)}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Commits != 4 || rep.Replayed != 2 || rep.EventsChanged != 1 {
		t.Fatalf("report = %+v", rep)
	}
	n, err := notes.Load(ctx, s, "a", epoch.On("r"))
	if err != nil || len(n.Value) != 1 || n.Value[0] != "hello" {
		t.Fatalf("copied notes = %+v, %v", n, err)
	}
	if got := balance(t, s, m, "a", epoch.On("r")); got != -1 {
		t.Fatalf("balance = %d, want -1 (fact copied, fee charged)", got)
	}
}

func testReplayWindow(t *testing.T, s epoch.Store) {
	m := newAccounts(0)
	seedAccounts(t, s, m)
	rep, err := epoch.Replay(ctx, s, epoch.ReplayOptions{
		Name: "window", From: day(2), To: day(3), Models: []epoch.AnyModel{withDecide(m, decideNoOverdraft)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Commits != 1 || rep.NowRejected != 1 {
		t.Fatalf("report = %+v", rep)
	}
	// The branch stops at day 3: day 4 and 5 were not replayed.
	if hist, _ := m.History(ctx, s, "bob", epoch.On("window")); len(hist) != 1 {
		t.Fatalf("bob has %d commits on the branch, want 1", len(hist))
	}
	if _, err := epoch.Replay(ctx, s, epoch.ReplayOptions{Name: "bad", FromSeq: rep.ToSeq + 1, ToSeq: rep.ToSeq}); err == nil {
		t.Fatalf("replay with From after To succeeded")
	}
	if _, err := epoch.GetBranch(ctx, s, "bad"); !errors.Is(err, epoch.ErrBranchNotFound) {
		t.Fatalf("failed replay left its branch behind")
	}
}

func testReplayWholeHistory(t *testing.T, s epoch.Store) {
	m := newAccounts(0)
	seedAccounts(t, s, m)
	rep, err := epoch.Replay(ctx, s, epoch.ReplayOptions{Name: "all", Models: []epoch.AnyModel{m}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.FromSeq != 0 || rep.Commits != 6 || rep.Diverged != 0 {
		t.Fatalf("replaying with unchanged rules: %+v", rep)
	}
	for _, id := range []string{"alice", "bob"} {
		a, _ := m.Load(ctx, s, id)
		b, _ := m.Load(ctx, s, id, epoch.On("all"))
		if a.Value != b.Value || a.Version != b.Version {
			t.Errorf("%s differs after identical replay: %+v vs %+v", id, a, b)
		}
	}
}

func testReplayOfReplay(t *testing.T, s epoch.Store) {
	m := newAccounts(2)
	seedAccounts(t, s, m)
	if _, err := epoch.Replay(ctx, s, epoch.ReplayOptions{Name: "r1", From: day(1), Models: []epoch.AnyModel{withDecide(m, decideGenerous)}}); err != nil {
		t.Fatal(err)
	}
	genFee := withDecide(m, func(st account, c any, now time.Time) ([]any, error) {
		evs, err := decideGenerous(st, c, now)
		if _, ok := c.(withdraw); ok && err == nil {
			evs = append(evs, feeCharged{Amount: 1})
		}
		return evs, err
	})
	rep, err := epoch.Replay(ctx, s, epoch.ReplayOptions{Name: "r2", Source: "r1", From: day(2), Models: []epoch.AnyModel{genFee}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Branch.Parent != "r1" || rep.Commits != 3 {
		t.Fatalf("report = %+v", rep)
	}
	// r1 accepted both withdrawals (-20, then -70). r2 replays r1 from day 2
	// and charges a fee on each.
	if got := balance(t, s, m, "alice", epoch.On("r1")); got != -70 {
		t.Fatalf("r1 alice = %d, want -70", got)
	}
	if got := balance(t, s, m, "alice", epoch.On("r2")); got != -72 {
		t.Fatalf("r2 alice = %d, want -72", got)
	}
}

func testReplayFailureRemovesBranch(t *testing.T, s epoch.Store) {
	m := newAccounts(0)
	seedAccounts(t, s, m)
	broken := withDecide(m, func(account, any, time.Time) ([]any, error) { panic("bug in new rules") })
	_, err := epoch.Replay(ctx, s, epoch.ReplayOptions{Name: "broken", Models: []epoch.AnyModel{broken}})
	if err == nil || !strings.Contains(err.Error(), "bug in new rules") {
		t.Fatalf("err = %v", err)
	}
	if _, err := epoch.GetBranch(ctx, s, "broken"); !errors.Is(err, epoch.ErrBranchNotFound) {
		t.Fatalf("failed replay left its branch behind: %v", err)
	}
	if _, err := epoch.Replay(ctx, s, epoch.ReplayOptions{Name: "dup", Models: []epoch.AnyModel{m, m}}); err == nil {
		t.Fatalf("duplicate models accepted")
	}
}

func testReplayReportFromStore(t *testing.T, s epoch.Store) {
	m := newAccounts(0)
	seedAccounts(t, s, m)
	live, err := epoch.Replay(ctx, s, epoch.ReplayOptions{Name: "r", From: day(0), Models: []epoch.AnyModel{withDecide(m, decideNoOverdraft)}})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := epoch.ReplayReport(ctx, s, "r", 0)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Commits != live.Commits || stored.Diverged != live.Diverged || stored.NowRejected != live.NowRejected ||
		stored.ToSeq != live.ToSeq || len(stored.Divergences) != len(live.Divergences) {
		t.Fatalf("stored report %+v\ndiffers from live %+v", stored, live)
	}
	if _, err := epoch.ReplayReport(ctx, s, epoch.Main, 0); err == nil {
		t.Fatalf("ReplayReport(main) succeeded")
	}
	capped, _ := epoch.ReplayReport(ctx, s, "r", -1)
	if capped.Truncated {
		t.Fatalf("uncapped report truncated")
	}
}

func testProjectionAndCompare(t *testing.T, s epoch.Store) {
	m := newAccounts(0)
	seedAccounts(t, s, m)
	v, err := ledgerView.Get(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if v.Total != -10 || v.Balances["alice"] != -20 || v.Balances["bob"] != 10 || v.Rejections != 1 || v.Denied != 50 {
		t.Fatalf("ledger = %+v", v)
	}
	past, _ := ledgerView.Get(ctx, s, epoch.AsOf(day(1)))
	if past.Total != 100 || past.Rejections != 0 {
		t.Fatalf("ledger as of day 1 = %+v", past)
	}
	if _, err := epoch.Replay(ctx, s, epoch.ReplayOptions{Name: "gen", Models: []epoch.AnyModel{withDecide(m, decideGenerous)}}); err != nil {
		t.Fatal(err)
	}
	cmp, err := epoch.Compare(ctx, s, ledgerView, epoch.Main, "gen")
	if err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprint(cmp.Changes)
	want := fmt.Sprint([]epoch.Change{
		{Path: "balances.alice", Kind: epoch.Modified, From: jsonNum("-20"), To: jsonNum("-70")},
		{Path: "denied", Kind: epoch.Modified, From: jsonNum("50"), To: jsonNum("0")},
		{Path: "rejections", Kind: epoch.Modified, From: jsonNum("1"), To: jsonNum("0")},
		{Path: "total", Kind: epoch.Modified, From: jsonNum("-10"), To: jsonNum("-60")},
	})
	if got != want {
		t.Fatalf("Compare changes:\n got %s\nwant %s", got, want)
	}
}

func testConcurrentHandle(t *testing.T, s epoch.Store) {
	m := newAccounts(5)
	handle(t, s, m, "a", openAccount{})
	const workers, each = 6, 15
	var wg sync.WaitGroup
	errs := make(chan error, workers*each)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				if _, err := m.Handle(ctx, s, "a", deposit{Amount: 1}); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	st, _ := m.Load(ctx, s, "a")
	if st.Value.Balance != workers*each || st.Version != workers*each+1 {
		t.Fatalf("state = %+v, want balance %d", st, workers*each)
	}
}

func testLog(t *testing.T, s epoch.Store) {
	m := newAccounts(0)
	seedAccounts(t, s, m)
	all, err := epoch.Log(ctx, s, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 6 {
		t.Fatalf("Log has %d commits, want 6", len(all))
	}
	page, _ := epoch.Log(ctx, s, all[1].Seq, 2)
	if len(page) != 2 || page[0].Seq != all[2].Seq {
		t.Fatalf("Log page = %+v", page)
	}
	past, _ := epoch.Log(ctx, s, 0, 0, epoch.AsOf(day(2)))
	if len(past) != 3 {
		t.Fatalf("Log as of day 2 has %d commits, want 3", len(past))
	}
}

func jsonNum(s string) json.Number { return json.Number(s) }
