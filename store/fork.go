package store

import (
	"sort"
	"sync"
	"time"
)

type ForkEventStore struct {
	main       EventStore        // read-only main
	overlay    *MemoryEventStore // fork-specific overlay (always Memory)
	forkedFrom time.Time
	appendMu   sync.Mutex
}

func NewForkEventStore(main EventStore, forkedFrom time.Time) *ForkEventStore {
	return &ForkEventStore{
		main:       main,
		overlay:    NewMemoryEventStore(),
		forkedFrom: forkedFrom,
	}
}

// Append writes to the overlay. Overlay versions continue from the main
// timeline's last version at the fork point, so version-based filtering
// (e.g. "events after this snapshot") stays meaningful across both streams.
func (f *ForkEventStore) Append(e Event) (Event, error) {
	f.appendMu.Lock()
	defer f.appendMu.Unlock()

	mainEvts, err := f.main.LoadBefore(e.AggregateID, f.forkedFrom)
	if err != nil {
		return Event{}, err
	}
	if n := len(mainEvts); n > 0 {
		f.overlay.ensureSeq(e.AggregateID, mainEvts[n-1].Version)
		if !e.OccurredAt.IsZero() && e.OccurredAt.Before(mainEvts[n-1].OccurredAt) {
			return Event{}, ErrOutOfOrder
		}
	}
	return f.overlay.Append(e)
}

// OverlayCount returns the number of events written to this fork.
func (f *ForkEventStore) OverlayCount() int {
	all, _ := f.overlay.LoadAll()
	return len(all)
}

func (f *ForkEventStore) Load(aggregateID string) ([]Event, error) {
	mainEvts, _ := f.main.LoadBefore(aggregateID, f.forkedFrom)
	forkEvts, _ := f.overlay.Load(aggregateID)
	return mergeEventSlices(mainEvts, forkEvts), nil
}

func (f *ForkEventStore) LoadBefore(aggregateID string, cutoff time.Time) ([]Event, error) {
	return f.LoadAfter(aggregateID, 0, cutoff)
}

func (f *ForkEventStore) LoadAfter(aggregateID string, afterVersion int64, cutoff time.Time) ([]Event, error) {
	mainCutoff := cutoff
	if f.forkedFrom.Before(cutoff) {
		mainCutoff = f.forkedFrom
	}
	mainEvts, err := f.main.LoadAfter(aggregateID, afterVersion, mainCutoff)
	if err != nil {
		return nil, err
	}
	forkEvts, err := f.overlay.LoadAfter(aggregateID, afterVersion, cutoff)
	if err != nil {
		return nil, err
	}
	return mergeEventSlices(mainEvts, forkEvts), nil
}

func (f *ForkEventStore) LoadAll() ([]Event, error) {
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
		if !seen[id] {
			seen[id] = true
			res = append(res, id)
		}
	}
	for _, id := range overlayIDs {
		if !seen[id] {
			seen[id] = true
			res = append(res, id)
		}
	}
	return res
}

func (f *ForkEventStore) IsReady() bool {
	return f.main.IsReady()
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

// ClampedSnapshotStore hides snapshots taken after a fork point. Main-timeline
// snapshots after forkedFrom include main events the fork must not see.
type ClampedSnapshotStore struct {
	Inner SnapshotStore
	Until time.Time
}

func (c ClampedSnapshotStore) Save(s Snapshot) error { return nil } // forks never write main snapshots

func (c ClampedSnapshotStore) LatestBefore(aggregateID string, cutoff time.Time) (*Snapshot, error) {
	if c.Until.Before(cutoff) {
		cutoff = c.Until
	}
	return c.Inner.LatestBefore(aggregateID, cutoff)
}
