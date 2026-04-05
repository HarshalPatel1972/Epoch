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
	s.mu.RLock()
	defer s.mu.RUnlock()

	events, ok := s.events[aggregateID]
	if !ok {
		return nil, nil
	}

	var res []Event
	for _, e := range events {
		if e.OccurredAt.Before(cutoff) || e.OccurredAt.Equal(cutoff) {
			res = append(res, e)
		}
	}
	return res, nil
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
