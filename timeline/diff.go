package timeline

import (
	"time"

	"github.com/HarshalPatel1972/epoch/aggregate"
)

type FieldDiff struct {
	Field string      `json:"field"`
	From  interface{} `json:"from"`
	To    interface{} `json:"to"`
}

type ProductDiff struct {
	AggregateID string      `json:"aggregate_id"`
	Name        string      `json:"name"`
	Status      string      `json:"status"` // added | removed | changed | unchanged
	Fields      []FieldDiff `json:"fields"`
}

type DiffSummary struct {
	ProductsChanged int `json:"products_changed"`
	ProductsAdded   int `json:"products_added"`
	ProductsRemoved int `json:"products_removed"`
}

type DiffResult struct {
	Mode     string        `json:"mode"`
	From     *time.Time    `json:"from,omitempty"`
	To       *time.Time    `json:"to,omitempty"`
	Timeline *string       `json:"timeline,omitempty"`
	Summary  DiffSummary   `json:"summary"`
	Changes  []ProductDiff `json:"changes"`
}

func Diff(before, after []*aggregate.Product) DiffResult {
	beforeMap := make(map[string]*aggregate.Product)
	for _, p := range before {
		beforeMap[p.ID] = p
	}

	afterMap := make(map[string]*aggregate.Product)
	for _, p := range after {
		afterMap[p.ID] = p
	}

	allIDs := make(map[string]bool)
	for id := range beforeMap {
		allIDs[id] = true
	}
	for id := range afterMap {
		allIDs[id] = true
	}

	var changes []ProductDiff
	var summary DiffSummary

	for id := range allIDs {
		b := beforeMap[id]
		a := afterMap[id]

		var diff ProductDiff
		diff.AggregateID = id

		if b == nil {
			// Added in 'after'
			diff.Status = "added"
			diff.Name = a.Name
			summary.ProductsAdded++
		} else if a == nil || a.Deleted {
			// Removed in 'after' (if b was NOT deleted)
			if b.Deleted {
				continue // Already deleted in 'before', so no change
			}
			diff.Status = "removed"
			diff.Name = b.Name
			summary.ProductsRemoved++
		} else {
			// Both exist, compare fields
			fields := compareProducts(b, a)
			diff.Name = a.Name // Prefer after name
			if len(fields) > 0 {
				diff.Status = "changed"
				diff.Fields = fields
				summary.ProductsChanged++
			} else {
				diff.Status = "unchanged"
			}
		}

		if diff.Status != "unchanged" {
			changes = append(changes, diff)
		}
	}

	return DiffResult{
		Changes: changes,
		Summary: summary,
	}
}

func compareProducts(before, after *aggregate.Product) []FieldDiff {
	var diffs []FieldDiff
	if before.Name != after.Name {
		diffs = append(diffs, FieldDiff{Field: "name", From: before.Name, To: after.Name})
	}
	if before.SKU != after.SKU {
		diffs = append(diffs, FieldDiff{Field: "sku", From: before.SKU, To: after.SKU})
	}
	if before.Price != after.Price {
		diffs = append(diffs, FieldDiff{Field: "price", From: before.Price, To: after.Price})
	}
	if before.Stock != after.Stock {
		diffs = append(diffs, FieldDiff{Field: "stock", From: before.Stock, To: after.Stock})
	}
	if before.Category != after.Category {
		diffs = append(diffs, FieldDiff{Field: "category", From: before.Category, To: after.Category})
	}
	return diffs
}
