// Package shop is the domain of the Epoch example: a small online shop whose
// inventory model decides which orders it can fulfil and at what price.
package shop

import (
	"errors"
	"fmt"
	"time"

	"github.com/HarshalPatel1972/epoch/v2"
)

// Product is the state of one product: its catalogue data and stock level.
type Product struct {
	Name  string `json:"name"`
	Price int64  `json:"price"` // cents
	Stock int    `json:"stock"`
}

// Commands.
type (
	AddProduct struct {
		Name  string
		Price int64
		Stock int
	}
	Restock     struct{ Qty int }
	ChangePrice struct{ Price int64 }
	// PlaceOrder carries everything the pricing rules need, including the
	// customer's loyalty, so a replay can re-price it without lookups.
	PlaceOrder struct {
		OrderID       string
		Customer      string
		Qty           int
		LoyaltyMonths int
	}
)

// Events.
type (
	ProductAdded struct {
		Name  string
		Price int64
		Stock int
	}
	Restocked    struct{ Qty int }
	PriceChanged struct{ Price int64 }
	OrderPlaced  struct {
		OrderID   string
		Customer  string
		Qty       int
		UnitPrice int64
		Discount  int64 // cents off the whole order
		Total     int64 // cents
	}
)

var (
	ErrUnknownProduct = errors.New("unknown product")
	ErrOutOfStock     = errors.New("out of stock")
)

func evolve(p Product, e any) Product {
	switch e := e.(type) {
	case ProductAdded:
		p = Product{Name: e.Name, Price: e.Price, Stock: e.Stock}
	case Restocked:
		p.Stock += e.Qty
	case PriceChanged:
		p.Price = e.Price
	case OrderPlaced:
		p.Stock -= e.Qty
	}
	return p
}

// Policy is the set of business rules that a Decide function applies. The
// example backtests changes to it.
type Policy struct {
	// Backorder lets stock go this far below zero instead of turning orders
	// away.
	Backorder int
	// BulkDiscount is the percentage off orders of at least BulkQty units.
	BulkDiscount int
	BulkQty      int
	// LoyaltyDiscount is the percentage off for customers of at least
	// LoyaltyMonths months. Discounts do not stack; the larger one wins.
	LoyaltyDiscount int
	LoyaltyMonths   int
}

// Current is the policy the shop actually ran with.
var Current = Policy{BulkDiscount: 10, BulkQty: 5}

// Decide returns a Decide function that applies the policy.
func (pol Policy) Decide(p Product, cmd any, _ time.Time) ([]any, error) {
	switch c := cmd.(type) {
	case AddProduct:
		if p.Name != "" {
			return nil, fmt.Errorf("product %q already exists", p.Name)
		}
		return []any{ProductAdded(c)}, nil
	}
	if p.Name == "" {
		return nil, ErrUnknownProduct
	}
	switch c := cmd.(type) {
	case Restock:
		return []any{Restocked(c)}, nil
	case ChangePrice:
		return []any{PriceChanged(c)}, nil
	case PlaceOrder:
		if p.Stock-c.Qty < -pol.Backorder {
			return nil, fmt.Errorf("%w: %d left, %d ordered", ErrOutOfStock, p.Stock, c.Qty)
		}
		pct := 0
		if pol.BulkQty > 0 && c.Qty >= pol.BulkQty {
			pct = pol.BulkDiscount
		}
		if pol.LoyaltyMonths > 0 && c.LoyaltyMonths >= pol.LoyaltyMonths {
			pct = max(pct, pol.LoyaltyDiscount)
		}
		gross := p.Price * int64(c.Qty)
		discount := gross * int64(pct) / 100
		return []any{OrderPlaced{
			OrderID: c.OrderID, Customer: c.Customer, Qty: c.Qty,
			UnitPrice: p.Price, Discount: discount, Total: gross - discount,
		}}, nil
	}
	return nil, fmt.Errorf("unknown command %T", cmd)
}

// Model returns the inventory model running the given policy.
func Model(pol Policy) *epoch.Model[Product] {
	return &epoch.Model[Product]{
		Name:     "product",
		Schema:   "1",
		Commands: []any{AddProduct{}, Restock{}, ChangePrice{}, PlaceOrder{}},
		Events:   []any{ProductAdded{}, Restocked{}, PriceChanged{}, OrderPlaced{}},
		Evolve:   evolve,
		Decide:   pol.Decide,
	}
}

// Products is the model the shop runs in production.
var Products = Model(Current)

// Sales is a read model of the shop's trading.
type Sales struct {
	Orders      int              `json:"orders"`
	Units       int              `json:"units"`
	Revenue     int64            `json:"revenue"`
	Discounts   int64            `json:"discounts"`
	TurnedAway  int              `json:"turned_away"`
	LostUnits   int              `json:"lost_units"`
	ByProduct   map[string]int64 `json:"revenue_by_product"`
	Backordered int              `json:"backordered_units"`
	stock       map[string]int
}

// SalesView computes Sales from the log.
var SalesView = &epoch.Projection[Sales]{
	Name: "sales",
	Init: func() Sales { return Sales{ByProduct: map[string]int64{}, stock: map[string]int{}} },
	Event: func(s Sales, r epoch.Record) Sales {
		if e, ok := epoch.As[ProductAdded](r); ok {
			s.stock[r.Stream] = e.Stock
		}
		if e, ok := epoch.As[Restocked](r); ok {
			s.stock[r.Stream] += e.Qty
		}
		if e, ok := epoch.As[OrderPlaced](r); ok {
			// Units sold beyond the stock on hand are backorders.
			before := s.stock[r.Stream]
			s.stock[r.Stream] -= e.Qty
			s.Backordered += max(0, min(e.Qty, e.Qty-before))
			s.Orders++
			s.Units += e.Qty
			s.Revenue += e.Total
			s.Discounts += e.Discount
			s.ByProduct[r.Stream] += e.Total
		}
		return s
	},
	Rejected: func(s Sales, r epoch.Rejection) Sales {
		if c, ok := epoch.As[PlaceOrder](r); ok {
			s.TurnedAway++
			s.LostUnits += c.Qty
		}
		return s
	},
}
