package aggregate

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/HarshalPatel1972/epoch/store"
)

type Projector struct {
	Events    store.EventStore
	Snapshots store.SnapshotStore
}

func (p *Projector) Project(aggregateID string, asOf time.Time) (*Product, error) {
	cutoff := asOf
	if cutoff.IsZero() {
		cutoff = time.Now().Add(24 * 365 * 100 * time.Hour) // Sentinel "far future"
	}

	snap, err := p.Snapshots.LatestBefore(aggregateID, cutoff)
	if err != nil {
		log.Printf("error loading snapshot for %s: %v", aggregateID, err)
	}

	product := &Product{}
	var events []store.Event

	if snap != nil {
		if err := json.Unmarshal(snap.State, product); err != nil {
			return nil, fmt.Errorf("failed to unmarshal snapshot state: %w", err)
		}
		// Load only events that happened after the snapshot's recorded version
		allEvents, err := p.Events.LoadBefore(aggregateID, cutoff)
		if err != nil {
			return nil, err
		}
		for _, e := range allEvents {
			if e.Version > snap.Version {
				events = append(events, e)
			}
		}
	} else {
		events, err = p.Events.LoadBefore(aggregateID, cutoff)
		if err != nil {
			return nil, err
		}
	}

	// Sort events by Version just in case
	sort.Slice(events, func(i, j int) bool {
		return events[i].Version < events[j].Version
	})

	for _, e := range events {
		if err := product.Apply(e); err != nil {
			return nil, err
		}
	}

	if product.ID == "" {
		return nil, nil // Never created before asOf
	}

	return product, nil
}

func (p *Projector) ProjectAll(asOf time.Time) ([]*Product, error) {
	ids := p.Events.AllAggregateIDs()
	var products []*Product

	for _, id := range ids {
		prod, err := p.Project(id, asOf)
		if err != nil {
			return nil, err
		}
		if prod != nil && !prod.Deleted {
			products = append(products, prod)
		}
	}

	// Stable sort for consistency
	sort.Slice(products, func(i, j int) bool {
		return products[i].ID < products[j].ID
	})

	return products, nil
}
