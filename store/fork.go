package store

import (
	"sort"
	"time"
)

type ForkEventStore struct {
	main       EventStore      // read-only main
	overlay    *MemoryEventStore // fork-specific overlay (always Memory)
	forkedFrom time.Time
}

func NewForkEventStore(main EventStore, forkedFrom time.Time) *ForkEventStore {
	return &ForkEventStore{
		main:       main,
		overlay:    NewMemoryEventStore(),
		forkedFrom: forkedFrom,
	}
}

func (f *ForkEventStore) Append(e Event) (Event, error) {
	// Always append to overlay
	return f.overlay.Append(e)
}

func (f *ForkEventStore) Load(aggregateID string) ([]Event, error) {
	mainEvts, _ := f.main.LoadBefore(aggregateID, f.forkedFrom)
	forkEvts, _ := f.overlay.Load(aggregateID)
	return mergeEventSlices(mainEvts, forkEvts), nil
}

func (f *ForkEventStore) LoadBefore(aggregateID string, cutoff time.Time) ([]Event, error) {
	mainCutoff := cutoff
	if f.forkedFrom.Before(cutoff) {
		mainCutoff = f.forkedFrom
	}
	mainEvts, _ := f.main.LoadBefore(aggregateID, mainCutoff)
	forkEvts, _ := f.overlay.LoadBefore(aggregateID, cutoff)
	return mergeEventSlices(mainEvts, forkEvts), nil
}

func (f *ForkEventStore) LoadAll() ([]Event, error) {
	// This is a bit expensive for a fork, but needed for /events
	// We'll just load all from main (before forkedFrom) and all from overlay
	// Note: AllAggregateIDs union might be needed.
	
	ids := f.AllAggregateIDs()
	var all []Event
	for _, id := range ids {
		evts, _ := f.Load(id)
		all = append(all, evts...)
	}
	
	sort.Slice(all, func(i, j int) bool {
		if all[i].OccurredAt.Equal(all[j].OccurredAt) {
			return all[i].Version < all[j].Version
		}
		return all[i].OccurredAt.Before(all[j].OccurredAt)
	})
	
	return all, nil
}

func (f *ForkEventStore) AllAggregateIDs() []string {
	mainIDs := f.main.AllAggregateIDs()
	overlayIDs := f.overlay.AllAggregateIDs()
	
	seen := make(map[string]bool)
	var res []string
	for _, id := range mainIDs {
		if _, ok := seen[id]; !ok {
			seen[id] = true
			res = append(res, id)
		}
	}
	for _, id := range overlayIDs {
		if _, ok := seen[id]; !ok {
			seen[id] = true
			res = append(res, id)
		}
	}
	return res
}

func mergeEventSlices(main, fork []Event) []Event {
	res := append([]Event{}, main...)
	res = append(res, fork...)
	
	sort.Slice(res, func(i, j int) bool {
		if res[i].OccurredAt.Equal(res[j].OccurredAt) {
			return res[i].Version < res[j].Version
		}
		return res[i].OccurredAt.Before(res[j].OccurredAt)
	})
	
	return res
}
