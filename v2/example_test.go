package epoch_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/HarshalPatel1972/epoch/v2"
	"github.com/HarshalPatel1972/epoch/v2/memstore"
)

// A bank account with an overdraft limit.
type Account struct {
	Balance int64
	Limit   int64
}

// Commands are what the application is asked to do.
type (
	Open     struct{ Limit int64 }
	Deposit  struct{ Amount int64 }
	Withdraw struct{ Amount int64 }
)

// Events are what happened.
type (
	Opened    struct{ Limit int64 }
	Deposited struct{ Amount int64 }
	Withdrew  struct{ Amount int64 }
)

var ErrInsufficientFunds = errors.New("insufficient funds")

var Accounts = &epoch.Model[Account]{
	Name:     "account",
	Commands: []any{Open{}, Deposit{}, Withdraw{}},
	Events:   []any{Opened{}, Deposited{}, Withdrew{}},
	Evolve: func(a Account, e any) Account {
		switch e := e.(type) {
		case Opened:
			a.Limit = e.Limit
		case Deposited:
			a.Balance += e.Amount
		case Withdrew:
			a.Balance -= e.Amount
		}
		return a
	},
	Decide: func(a Account, c any, _ time.Time) ([]any, error) {
		switch c := c.(type) {
		case Open:
			return []any{Opened(c)}, nil
		case Deposit:
			return []any{Deposited(c)}, nil
		case Withdraw:
			if a.Balance-c.Amount < -a.Limit {
				return nil, ErrInsufficientFunds
			}
			return []any{Withdrew(c)}, nil
		}
		return nil, fmt.Errorf("unknown command %T", c)
	},
}

func day(n int) time.Time { return time.Date(2026, 1, n, 12, 0, 0, 0, time.UTC) }

// seed records a short history. The last withdrawal is rejected, and the
// rejection is recorded too.
func seed(ctx context.Context, store epoch.Store) {
	Accounts.Handle(ctx, store, "alice", Open{Limit: 100}, epoch.WithTime(day(1)))
	Accounts.Handle(ctx, store, "alice", Deposit{Amount: 50}, epoch.WithTime(day(2)))
	Accounts.Handle(ctx, store, "alice", Withdraw{Amount: 120}, epoch.WithTime(day(3)))
	Accounts.Handle(ctx, store, "alice", Withdraw{Amount: 40}, epoch.WithTime(day(4)))
}

func Example() {
	ctx := context.Background()
	store := memstore.New()

	Accounts.Handle(ctx, store, "alice", Open{Limit: 100})
	Accounts.Handle(ctx, store, "alice", Deposit{Amount: 50})
	_, err := Accounts.Handle(ctx, store, "alice", Withdraw{Amount: 500})
	fmt.Println(err)
	fmt.Println(errors.Is(err, epoch.ErrRejected), errors.Is(err, ErrInsufficientFunds))

	st, _ := Accounts.Load(ctx, store, "alice")
	fmt.Println("balance:", st.Value.Balance)
	// Output:
	// epoch: command rejected: insufficient funds
	// true true
	// balance: 50
}

func ExampleModel_Load_asOf() {
	ctx := context.Background()
	store := memstore.New()
	seed(ctx, store)

	for _, d := range []int{1, 2, 3, 4} {
		st, _ := Accounts.Load(ctx, store, "alice", epoch.AsOf(day(d)))
		fmt.Printf("day %d: %d\n", d, st.Value.Balance)
	}
	// Output:
	// day 1: 0
	// day 2: 50
	// day 3: -70
	// day 4: -70
}

func ExampleFork() {
	ctx := context.Background()
	store := memstore.New()
	seed(ctx, store)

	// Branch off as things were on day 2 and try something different.
	epoch.Fork(ctx, store, "what-if", epoch.ForkOptions{At: day(2)})
	Accounts.Handle(ctx, store, "alice", Deposit{Amount: 1000}, epoch.On("what-if"))

	main, _ := Accounts.Load(ctx, store, "alice")
	fork, _ := Accounts.Load(ctx, store, "alice", epoch.On("what-if"))
	fmt.Println("main:", main.Value.Balance)
	fmt.Println("what-if:", fork.Value.Balance)
	// Output:
	// main: -70
	// what-if: 1050
}

func ExampleReplay() {
	ctx := context.Background()
	store := memstore.New()
	seed(ctx, store)

	// What if overdrafts had been capped at 20 instead of 100?
	stricter := *Accounts
	stricter.Decide = func(a Account, c any, now time.Time) ([]any, error) {
		if w, ok := c.(Withdraw); ok && a.Balance-w.Amount < -20 {
			return nil, ErrInsufficientFunds
		}
		return Accounts.Decide(a, c, now)
	}

	report, _ := epoch.Replay(ctx, store, epoch.ReplayOptions{
		Name:   "overdraft-20",
		Models: []epoch.AnyModel{&stricter},
	})
	fmt.Printf("replayed %d commands, %d outcomes changed\n", report.Commits, report.Diverged)
	for _, d := range report.Divergences {
		fmt.Printf("%s: %s %s\n", d.Original.Time.Format("Jan 2"), d.Original.Command.Type, d.Kind)
	}

	st, _ := Accounts.Load(ctx, store, "alice", epoch.On("overdraft-20"))
	fmt.Println("balance on the replay branch:", st.Value.Balance)
	// Output:
	// replayed 4 commands, 2 outcomes changed
	// Jan 3: Withdraw now_rejected
	// Jan 4: Withdraw now_accepted
	// balance on the replay branch: 10
}

func ExampleCompare() {
	ctx := context.Background()
	store := memstore.New()
	seed(ctx, store)

	type Totals struct {
		Deposited, Withdrawn int64
		Declined             int
	}
	totals := &epoch.Projection[Totals]{
		Name: "totals",
		Event: func(t Totals, r epoch.Record) Totals {
			if e, ok := epoch.As[Deposited](r); ok {
				t.Deposited += e.Amount
			}
			if e, ok := epoch.As[Withdrew](r); ok {
				t.Withdrawn += e.Amount
			}
			return t
		},
		Rejected: func(t Totals, r epoch.Rejection) Totals { t.Declined++; return t },
	}

	generous := *Accounts
	generous.Decide = func(a Account, c any, now time.Time) ([]any, error) {
		if w, ok := c.(Withdraw); ok {
			return []any{Withdrew(w)}, nil // no limit at all
		}
		return Accounts.Decide(a, c, now)
	}
	epoch.Replay(ctx, store, epoch.ReplayOptions{Name: "no-limit", Models: []epoch.AnyModel{&generous}})

	cmp, _ := epoch.Compare(ctx, store, totals, epoch.Main, "no-limit")
	for _, c := range cmp.Changes {
		fmt.Printf("%s: %v -> %v\n", c.Path, c.From, c.To)
	}
	// Output:
	// Declined: 1 -> 0
	// Withdrawn: 120 -> 160
}
