// Command shop is a tour of Epoch: it generates six months of history for a
// small online shop, then answers questions a normal database cannot.
//
//	go run ./examples/shop            # print the tour
//	go run ./examples/shop -serve :8080  # then explore it in Epoch Studio
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/HarshalPatel1972/epoch/v2"
	"github.com/HarshalPatel1972/epoch/v2/epochhttp"
	"github.com/HarshalPatel1972/epoch/v2/examples/shop/shop"
	"github.com/HarshalPatel1972/epoch/v2/memstore"
)

func main() {
	serve := flag.String("serve", "", "after the tour, serve Epoch Studio on this address, e.g. :8080")
	days := flag.Int("days", 181, "days of history to generate")
	flag.Parse()

	ctx := context.Background()
	store := memstore.New()
	if err := tour(ctx, store, *days); err != nil {
		log.Fatal(err)
	}
	if *serve == "" {
		fmt.Println("\nRun again with -serve :8080 to explore these branches in Epoch Studio.")
		return
	}

	h := epochhttp.New(epochhttp.Config{
		Store:       store,
		Models:      []epoch.AnyModel{shop.Products},
		Projections: []epoch.AnyProjection{shop.SalesView},
		Policies: map[string]epoch.AnyModel{
			"backorders up to 10": shop.Model(withBackorders(10)),
			"loyalty 15% off":     shop.Model(withLoyalty(15, 12)),
			"no bulk discount":    shop.Model(shop.Policy{}),
		},
		AllowWrites: true,
	})
	addr := *serve
	if strings.HasPrefix(addr, ":") {
		addr = "localhost" + addr
	}
	fmt.Printf("\nEpoch Studio: http://%s\n", addr)
	log.Fatal(http.ListenAndServe(*serve, h))
}

func withBackorders(n int) shop.Policy {
	p := shop.Current
	p.Backorder = n
	return p
}

func withLoyalty(pct, months int) shop.Policy {
	p := shop.Current
	p.LoyaltyDiscount, p.LoyaltyMonths = pct, months
	return p
}

func tour(ctx context.Context, store epoch.Store, days int) error {
	start := time.Now()
	n, err := shop.Seed(ctx, store, days)
	if err != nil {
		return err
	}
	end := shop.Start.AddDate(0, 0, days)
	sales, err := shop.SalesView.Get(ctx, store)
	if err != nil {
		return err
	}
	heading("Epoch tour: an online shop, %s to %s", shop.Start.Format("2 Jan"), end.AddDate(0, 0, -1).Format("2 Jan 2006"))
	fmt.Printf("Recorded %s commands in %s: %s orders placed, %s turned away for lack of stock.\n",
		num(n), time.Since(start).Round(time.Millisecond), num(sales.Orders), num(sales.TurnedAway))

	// 1. Time travel.
	march := shop.Start.AddDate(0, 2, 0)
	heading("1. What was in stock on %s?", march.Format("2 January"))
	fmt.Printf("   %-22s %8s %6s\n", "product", "price", "stock")
	for _, p := range shop.Catalogue {
		st, err := shop.Products.Load(ctx, store, p.ID, epoch.AsOf(march))
		if err != nil {
			return err
		}
		fmt.Printf("   %-22s %8s %6d\n", st.Value.Name, money(st.Value.Price), st.Value.Stock)
	}

	// 2. Backtest a rule change that accepts orders we used to turn away.
	april := shop.Start.AddDate(0, 3, 0)
	heading("2. What if we had allowed up to 10 backorders per product from %s?", april.Format("2 January"))
	if err := backtest(ctx, store, "backorders", "Allow up to 10 backorders per product", april, shop.Model(withBackorders(10))); err != nil {
		return err
	}

	// 3. Backtest a pricing rule that changes accepted orders.
	heading("3. What if customers of 12+ months had had 15%% off all year?")
	return backtest(ctx, store, "loyalty", "15% off for customers of 12+ months", time.Time{}, shop.Model(withLoyalty(15, 12)))
}

func backtest(ctx context.Context, store epoch.Store, name, desc string, from time.Time, rules *epoch.Model[shop.Product]) error {
	start := time.Now()
	rep, err := epoch.Replay(ctx, store, epoch.ReplayOptions{Name: name, Description: desc, From: from, Models: []epoch.AnyModel{rules}})
	if err != nil {
		return err
	}
	fmt.Printf("   Replayed %s commands on branch %q in %s.\n", num(rep.Commits), name, time.Since(start).Round(time.Millisecond))
	fmt.Printf("   %s orders turned out differently: %s now accepted, %s now rejected, %s re-priced.\n",
		num(rep.Diverged), num(rep.NowAccepted), num(rep.NowRejected), num(rep.EventsChanged))

	cmp, err := epoch.Compare(ctx, store, shop.SalesView, epoch.Main, name)
	if err != nil {
		return err
	}
	a, b := cmp.A, cmp.B
	fmt.Printf("\n   %-20s %14s %14s %14s\n", "", "actual", name, "difference")
	row := func(label string, x, y int64, f func(int64) string) {
		fmt.Printf("   %-20s %14s %14s %14s\n", label, f(x), f(y), signed(y-x, f))
	}
	count := func(v int64) string { return num(int(v)) }
	row("orders", int64(a.Orders), int64(b.Orders), count)
	row("turned away", int64(a.TurnedAway), int64(b.TurnedAway), count)
	row("units on backorder", int64(a.Backordered), int64(b.Backordered), count)
	row("discounts given", a.Discounts, b.Discounts, money)
	row("revenue", a.Revenue, b.Revenue, money)
	if len(rep.Divergences) > 0 {
		d := rep.Divergences[0]
		fmt.Printf("\n   First change: %s, %s on %s\n", d.Kind, d.Original.Stream, d.Original.Time.Format("2 Jan 15:04"))
		fmt.Printf("     was: %s\n     now: %s\n", outcome(d.Original), outcome(d.Replayed))
	}
	return nil
}

func outcome(c epoch.Commit) string {
	if c.Rejected != "" {
		return "rejected: " + c.Rejected
	}
	if len(c.Events) == 0 {
		return "accepted"
	}
	e := c.Events[0]
	if o, ok := epoch.As[shop.OrderPlaced](e); ok {
		return fmt.Sprintf("order of %d at %s, %s off, total %s", o.Qty, money(o.UnitPrice), money(o.Discount), money(o.Total))
	}
	return e.Type
}

func heading(format string, args ...any) {
	fmt.Printf("\n"+format+"\n", args...)
}

func num(n int) string {
	s := fmt.Sprint(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		return "-" + s
	}
	return s
}

func money(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return fmt.Sprintf("%s$%s.%02d", sign, num(int(cents/100)), cents%100)
}

func signed(v int64, f func(int64) string) string {
	switch {
	case v > 0:
		return "+" + f(v)
	case v == 0:
		return "·"
	}
	return f(v)
}

func init() {
	log.SetFlags(0)
	log.SetOutput(os.Stderr)
}
