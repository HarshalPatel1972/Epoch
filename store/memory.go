package store

import (
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

type MemoryEventStore struct {
	mu     sync.RWMutex
	events map[string][]Event
	seq    map[string]int64
	snaps  SnapshotStore

	// Injectable callback for creating snapshots
	RequestSnapshot func(aggregateID string, currentVersion int64, asOf time.Time) error
}

func NewMemoryEventStore() *MemoryEventStore {
	return &MemoryEventStore{
		events: make(map[string][]Event),
		seq:    make(map[string]int64),
	}
}

func (s *MemoryEventStore) SetSnapshotStore(snaps SnapshotStore) {
	s.snaps = snaps
}

func (s *MemoryEventStore) Append(e Event) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if e.ID == "" {
		e.ID = uuid.New().String()
	}

	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now()
	}

	if prev := s.events[e.AggregateID]; len(prev) > 0 && e.OccurredAt.Before(prev[len(prev)-1].OccurredAt) {
		return Event{}, ErrOutOfOrder
	}

	s.seq[e.AggregateID]++
	e.Version = s.seq[e.AggregateID]

	s.events[e.AggregateID] = append(s.events[e.AggregateID], e)

	if e.Version%10 == 0 && s.RequestSnapshot != nil {
		go func(id string, ver int64, t time.Time) {
			s.RequestSnapshot(id, ver, t)
		}(e.AggregateID, e.Version, e.OccurredAt)
	}

	return e, nil
}

func (s *MemoryEventStore) Load(aggregateID string) ([]Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events, ok := s.events[aggregateID]
	if !ok {
		return nil, nil
	}
	// Copy to avoid race if modified in other places
	res := make([]Event, len(events))
	copy(res, events)
	return res, nil
}

func (s *MemoryEventStore) LoadBefore(aggregateID string, cutoff time.Time) ([]Event, error) {
	return s.LoadAfter(aggregateID, 0, cutoff)
}

func (s *MemoryEventStore) LoadAfter(aggregateID string, afterVersion int64, cutoff time.Time) ([]Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events := s.events[aggregateID]
	// Versions are contiguous but may start above 1 (fork overlays), so locate
	// the first event past afterVersion by search rather than by index.
	start := sort.Search(len(events), func(i int) bool { return events[i].Version > afterVersion })

	var res []Event
	for _, e := range events[start:] {
		if e.OccurredAt.After(cutoff) {
			break
		}
		res = append(res, e)
	}
	return res, nil
}

// ensureSeq makes the next appended version for aggregateID start after base.
// Fork overlays use it so their versions continue from the main timeline.
func (s *MemoryEventStore) ensureSeq(aggregateID string, base int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seq[aggregateID] < base {
		s.seq[aggregateID] = base
	}
}

func (s *MemoryEventStore) LoadAll() ([]Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var res []Event
	for _, events := range s.events {
		res = append(res, events...)
	}

	sort.Slice(res, func(i, j int) bool {
		return res[i].OccurredAt.Before(res[j].OccurredAt)
	})

	return res, nil
}

func (s *MemoryEventStore) AllAggregateIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var ids []string
	for id := range s.events {
		ids = append(ids, id)
	}
	return ids
}

func (s *MemoryEventStore) IsReady() bool {
	return true
}
