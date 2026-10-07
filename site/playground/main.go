//go:build js && wasm

// Command playground runs the Epoch shop example inside the browser, so the
// website's playground uses the real engine, not a simulation. It is built
// with GOOS=js GOARCH=wasm and driven from a Web Worker through the global
// epochPlayground object. Every function takes and returns JSON strings.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"syscall/js"
	"time"

	"github.com/HarshalPatel1972/epoch/v2"
	"github.com/HarshalPatel1972/epoch/v2/examples/shop/shop"
	"github.com/HarshalPatel1972/epoch/v2/memstore"
)

const days = 181

var (
	ctx      = context.Background()
	store    epoch.Store
	names    = map[string]string{}
	replays  int
	lastName string
)

func main() {
	api := map[string]any{
		"init":    fn(initShop),
		"replay":  fn(replay),
		"stockAt": fn(stockAt),
	}
	js.Global().Set("epochPlayground", js.ValueOf(api))
	select {}
}

// fn adapts func(json) (any, error) to a JS function returning a JSON string
// of {ok, data} or {ok:false, error}.
func fn(f func(arg string) (any, error)) js.Func {
	return js.FuncOf(func(_ js.Value, args []js.Value) any {
		arg := ""
		if len(args) > 0 && args[0].Type() == js.TypeString {
			arg = args[0].String()
		}
		out := map[string]any{"ok": true}
		func() {
			defer func() {
				if r := recover(); r != nil {
					out = map[string]any{"ok": false, "error": fmt.Sprint(r)}
				}
			}()
			data, err := f(arg)
			if err != nil {
				out = map[string]any{"ok": false, "error": err.Error()}
				return
			}
			out["data"] = data
		}()
		b, _ := json.Marshal(out)
		return string(b)
	})
}

type daily struct {
	Revenue  []int64 `json:"revenue"`  // per day, cents
	Accepted []int   `json:"accepted"` // orders per day
	Rejected []int   `json:"rejected"` // turned away per day
}

func dayIndex(t time.Time) int {
	d := int(t.Sub(shop.Start).Hours() / 24)
	return max(0, min(days-1, d))
}

var dailyView = &epoch.Projection[daily]{
	Name: "daily",
	Init: func() daily {
		return daily{Revenue: make([]int64, days), Accepted: make([]int, days), Rejected: make([]int, days)}
	},
	Event: func(v daily, r epoch.Record) daily {
		if e, ok := epoch.As[shop.OrderPlaced](r); ok {
			d := dayIndex(r.Time)
			v.Revenue[d] += e.Total
			v.Accepted[d]++
		}
		return v
	},
	Rejected: func(v daily, r epoch.Rejection) daily {
		if _, ok := epoch.As[shop.PlaceOrder](r); ok {
			v.Rejected[dayIndex(r.Time)]++
		}
		return v
	},
}

func initShop(string) (any, error) {
	start := time.Now()
	store = memstore.New()
	n, err := shop.Seed(ctx, store, days)
	if err != nil {
		return nil, err
	}
	seedMS := time.Since(start).Milliseconds()
	for _, p := range shop.Catalogue {
		names[p.ID] = p.Name
	}
	sales, err := shop.SalesView.Get(ctx, store)
	if err != nil {
		return nil, err
	}
	d, err := dailyView.Get(ctx, store)
	if err != nil {
		return nil, err
	}
	type product struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Price int64  `json:"price"`
	}
	var cat []product
	for _, p := range shop.Catalogue {
		cat = append(cat, product{p.ID, p.Name, p.Price})
	}
	return map[string]any{
		"commands": n, "seed_ms": seedMS, "sales": sales, "daily": d,
		"start": shop.Start, "days": days, "catalogue": cat, "policy": shop.Current,
	}, nil
}

type replayRequest struct {
	Policy shop.Policy `json:"policy"`
	From   string      `json:"from"` // YYYY-MM-DD; empty replays everything
}

type change struct {
	Time    time.Time `json:"time"`
	Product string    `json:"product"`
	Kind    string    `json:"kind"`
	Was     string    `json:"was"`
	Now     string    `json:"now"`
	Delta   int64     `json:"delta"` // revenue change in cents
}

func replay(arg string) (any, error) {
	if store == nil {
		return nil, fmt.Errorf("call init first")
	}
	var req replayRequest
	if err := json.Unmarshal([]byte(arg), &req); err != nil {
		return nil, err
	}
	var from time.Time
	if req.From != "" {
		t, err := time.Parse(time.DateOnly, req.From)
		if err != nil {
			return nil, err
		}
		from = t
	}
	// One replay branch at a time keeps memory flat however long people play.
	if lastName != "" {
		_ = epoch.DeleteBranch(ctx, store, lastName)
	}
	replays++
	lastName = fmt.Sprintf("play-%d", replays)

	start := time.Now()
	rep, err := epoch.Replay(ctx, store, epoch.ReplayOptions{
		Name: lastName, From: from, Models: []epoch.AnyModel{shop.Model(req.Policy)}, MaxDivergences: -1,
	})
	if err != nil {
		return nil, err
	}
	replayMS := float64(time.Since(start).Microseconds()) / 1000

	cmp, err := epoch.Compare(ctx, store, shop.SalesView, epoch.Main, lastName)
	if err != nil {
		return nil, err
	}
	da, err := dailyView.Get(ctx, store)
	if err != nil {
		return nil, err
	}
	db, err := dailyView.Get(ctx, store, epoch.On(lastName))
	if err != nil {
		return nil, err
	}

	changes := make([]change, 0, min(len(rep.Divergences), 400))
	for _, d := range rep.Divergences {
		if len(changes) == cap(changes) {
			break
		}
		was, wasTotal := describe(d.Original)
		now, nowTotal := describe(d.Replayed)
		changes = append(changes, change{
			Time: d.Original.Time, Product: names[d.Original.Stream], Kind: d.Kind,
			Was: was, Now: now, Delta: nowTotal - wasTotal,
		})
	}
	return map[string]any{
		"ms": replayMS, "commits": rep.Commits, "diverged": rep.Diverged,
		"now_accepted": rep.NowAccepted, "now_rejected": rep.NowRejected, "events_changed": rep.EventsChanged,
		"a": cmp.A, "b": cmp.B, "daily_a": da, "daily_b": db, "changes": changes,
	}, nil
}

// describe summarises a commit's outcome for people, plus the order total.
func describe(c epoch.Commit) (string, int64) {
	if c.Rejected != "" {
		return "turned away: " + c.Rejected, 0
	}
	for _, e := range c.Events {
		if o, ok := epoch.As[shop.OrderPlaced](e); ok {
			s := fmt.Sprintf("%d × %s = %s", o.Qty, money(o.UnitPrice), money(o.Total))
			if o.Discount > 0 {
				s += fmt.Sprintf(" (−%s)", money(o.Discount))
			}
			return s, o.Total
		}
	}
	return "accepted", 0
}

func money(c int64) string {
	return fmt.Sprintf("$%d.%02d", c/100, c%100)
}

func stockAt(arg string) (any, error) {
	if store == nil {
		return nil, fmt.Errorf("call init first")
	}
	t, err := time.Parse(time.RFC3339, arg)
	if err != nil {
		return nil, err
	}
	type row struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Price int64  `json:"price"`
		Stock int    `json:"stock"`
		Seq   int64  `json:"seq"`
	}
	rows := []row{}
	for _, p := range shop.Catalogue {
		st, err := shop.Products.Load(ctx, store, p.ID, epoch.AsOf(t))
		if err != nil {
			return nil, err
		}
		rows = append(rows, row{p.ID, p.Name, st.Value.Price, st.Value.Stock, st.Seq})
	}
	return rows, nil
}
