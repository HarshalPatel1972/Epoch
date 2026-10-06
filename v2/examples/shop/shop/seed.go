package shop

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"github.com/HarshalPatel1972/epoch/v2"
)

// Catalogue is the shop's range: id, name, price in cents, starting stock,
// typical orders per day, and the weekly restock in units.
var Catalogue = []struct {
	ID          string
	Name        string
	Price       int64
	Stock       int
	DailyDemand float64
	Restock     int
}{
	{"kb-01", "Mechanical keyboard", 129_00, 40, 2.2, 36},
	{"ms-01", "Wireless mouse", 49_00, 70, 4.0, 64},
	{"mn-27", "27\" 4K monitor", 399_00, 16, 0.9, 14},
	{"hd-01", "USB-C dock", 189_00, 22, 1.3, 21},
	{"cm-01", "1080p webcam", 79_00, 28, 1.6, 26},
	{"ch-01", "Ergonomic chair", 549_00, 9, 0.5, 7},
}

// Start is the first day of the generated history.
var Start = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Seed writes `days` days of deterministic shop history to s: orders arriving
// through the day, weekly restocks that sometimes come too late, and a few
// price changes. It returns the number of commands handled.
func Seed(ctx context.Context, s epoch.Store, days int) (int, error) {
	rng := rand.New(rand.NewPCG(42, 1972))
	n := 0
	handle := func(id string, cmd any, at time.Time) error {
		n++
		_, err := Products.Handle(ctx, s, id, cmd, epoch.WithTime(at))
		if errors.Is(err, epoch.ErrRejected) {
			return nil // a turned-away order is part of history, not a failure
		}
		return err
	}

	for _, p := range Catalogue {
		if err := handle(p.ID, AddProduct{Name: p.Name, Price: p.Price, Stock: p.Stock}, Start); err != nil {
			return n, err
		}
	}

	customers := make([]int, 400) // loyalty in months at Start
	for i := range customers {
		customers[i] = rng.IntN(36)
	}

	order := 0
	for d := range days {
		day := Start.AddDate(0, 0, d)
		weekend := day.Weekday() == time.Saturday || day.Weekday() == time.Sunday
		for _, p := range Catalogue {
			demand := p.DailyDemand
			if weekend {
				demand *= 1.4
			}
			// A spring promotion triples monitor demand in April, and the
			// fixed weekly restock cannot keep up.
			if p.ID == "mn-27" && day.Month() == time.April {
				demand *= 3
			}
			for range poisson(rng, demand) {
				order++
				c := rng.IntN(len(customers))
				qty := 1
				if rng.Float64() < 0.12 {
					qty = 5 + rng.IntN(6)
				} else if rng.Float64() < 0.3 {
					qty = 2
				}
				at := day.Add(time.Duration(8*60+rng.IntN(14*60)) * time.Minute)
				cmd := PlaceOrder{
					OrderID: fmt.Sprintf("o-%05d", order), Customer: fmt.Sprintf("c-%03d", c),
					Qty: qty, LoyaltyMonths: customers[c] + d/30,
				}
				if err := handle(p.ID, cmd, at); err != nil {
					return n, err
				}
			}
		}
		// Restocks arrive on Mondays, just before midnight.
		if day.Weekday() == time.Monday {
			for _, p := range Catalogue {
				if err := handle(p.ID, Restock{Qty: p.Restock}, day.Add(23*time.Hour)); err != nil {
					return n, err
				}
			}
		}
		if d == 59 {
			if err := handle("kb-01", ChangePrice{Price: 119_00}, day.Add(23*time.Hour+30*time.Minute)); err != nil {
				return n, err
			}
		}
		if d == 120 {
			if err := handle("ch-01", ChangePrice{Price: 499_00}, day.Add(23*time.Hour+30*time.Minute)); err != nil {
				return n, err
			}
		}
	}
	return n, nil
}

// poisson draws from a Poisson distribution (Knuth's method; fine for small
// means).
func poisson(rng *rand.Rand, mean float64) int {
	l, k, p := math.Exp(-mean), 0, 1.0
	for {
		p *= rng.Float64()
		if p <= l {
			return k
		}
		k++
	}
}
