package epochtest

import (
	"errors"
	"time"

	"github.com/HarshalPatel1972/epoch/v2"
)

// The suite's test domain: bank accounts with an overdraft limit, and notes.

type account struct {
	Open    bool  `json:"open"`
	Balance int64 `json:"balance"`
	Limit   int64 `json:"limit"`
	Fees    int64 `json:"fees"`
}

type (
	openAccount struct{ Limit int64 }
	deposit     struct{ Amount int64 }
	withdraw    struct{ Amount int64 }
	explode     struct{}

	opened     struct{ Limit int64 }
	deposited  struct{ Amount int64 }
	withdrawn  struct{ Amount int64 }
	feeCharged struct{ Amount int64 }
)

var (
	errNotOpen      = errors.New("account is not open")
	errAlreadyOpen  = errors.New("account is already open")
	errInsufficient = errors.New("insufficient funds")
)

func evolveAccount(s account, e any) account {
	switch e := e.(type) {
	case opened:
		s.Open, s.Limit = true, e.Limit
	case deposited:
		s.Balance += e.Amount
	case withdrawn:
		s.Balance -= e.Amount
	case feeCharged:
		s.Balance -= e.Amount
		s.Fees += e.Amount
	}
	return s
}

// decideAccount is the "production" rule: withdrawals may use the overdraft.
func decideAccount(s account, c any, _ time.Time) ([]any, error) {
	switch c := c.(type) {
	case openAccount:
		if s.Open {
			return nil, errAlreadyOpen
		}
		return []any{opened(c)}, nil
	case deposit:
		if !s.Open {
			return nil, errNotOpen
		}
		return []any{deposited(c)}, nil
	case withdraw:
		if !s.Open {
			return nil, errNotOpen
		}
		if s.Balance-c.Amount < -s.Limit {
			return nil, errInsufficient
		}
		return []any{withdrawn(c)}, nil
	case explode:
		panic("boom")
	}
	return nil, errors.New("unknown command")
}

// decideNoOverdraft is a stricter rule: no overdraft at all.
func decideNoOverdraft(s account, c any, now time.Time) ([]any, error) {
	if w, ok := c.(withdraw); ok && s.Open && s.Balance < w.Amount {
		return nil, errInsufficient
	}
	return decideAccount(s, c, now)
}

// decideWithFee charges a fee of 1 on every withdrawal.
func decideWithFee(s account, c any, now time.Time) ([]any, error) {
	evs, err := decideAccount(s, c, now)
	if _, ok := c.(withdraw); ok && err == nil {
		evs = append(evs, feeCharged{Amount: 1})
	}
	return evs, err
}

// decideGenerous doubles every overdraft limit.
func decideGenerous(s account, c any, now time.Time) ([]any, error) {
	if w, ok := c.(withdraw); ok && s.Open && s.Balance-w.Amount >= -2*s.Limit {
		return []any{withdrawn(w)}, nil
	}
	return decideAccount(s, c, now)
}

func newAccounts(snapshotEvery int) *epoch.Model[account] {
	return &epoch.Model[account]{
		Name:          "account",
		Schema:        "1",
		Commands:      []any{openAccount{}, deposit{}, withdraw{}, explode{}},
		Events:        []any{opened{}, deposited{}, withdrawn{}, feeCharged{}},
		Evolve:        evolveAccount,
		Decide:        decideAccount,
		SnapshotEvery: snapshotEvery,
	}
}

func withDecide(m *epoch.Model[account], d func(account, any, time.Time) ([]any, error)) *epoch.Model[account] {
	v := *m
	v.Decide = d
	return &v
}

type (
	addNote struct{ Text string }
	noted   struct{ Text string }
)

var notes = &epoch.Model[[]string]{
	Name:     "notes",
	Commands: []any{addNote{}},
	Events:   []any{noted{}},
	Evolve: func(s []string, e any) []string {
		return append(append([]string(nil), s...), e.(noted).Text)
	},
	Decide: func(_ []string, c any, _ time.Time) ([]any, error) {
		return []any{noted{Text: c.(addNote).Text}}, nil
	},
}

// ledger is a projection over all accounts.
type ledger struct {
	Balances   map[string]int64 `json:"balances"`
	Total      int64            `json:"total"`
	Rejections int              `json:"rejections"`
	Denied     int64            `json:"denied"`
}

var ledgerView = &epoch.Projection[ledger]{
	Name: "ledger",
	Init: func() ledger { return ledger{Balances: map[string]int64{}} },
	Event: func(v ledger, r epoch.Record) ledger {
		var delta int64
		if e, ok := epoch.As[deposited](r); ok {
			delta = e.Amount
		}
		if e, ok := epoch.As[withdrawn](r); ok {
			delta = -e.Amount
		}
		if e, ok := epoch.As[feeCharged](r); ok {
			delta = -e.Amount
		}
		if delta != 0 {
			v.Balances[r.Stream] += delta
			v.Total += delta
		}
		return v
	},
	Rejected: func(v ledger, r epoch.Rejection) ledger {
		v.Rejections++
		if w, ok := epoch.As[withdraw](r); ok {
			v.Denied += w.Amount
		}
		return v
	},
}
