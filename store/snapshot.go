package store

import (
	"sync"
	"time"
)

type Snapshot struct {
	AggregateID string    `json:"aggregate_id"`
	State       []byte    `json:"state"`
	AsOf        time.Time `json:"as_of"`
	Version     int64     `json:"version"`
}

type SnapshotStore interface {
	Save(s Snapshot) error
	LatestBefore(aggregateID string, cutoff time.Time) (*Snapshot, error)
}

type MemorySnapshotStore struct {
	mu        sync.RWMutex
	snapshots map[string][]Snapshot // aggregateID -> list of snapshots
}

func NewMemorySnapshotStore() *MemorySnapshotStore {
	return &MemorySnapshotStore{
		snapshots: make(map[string][]Snapshot),
	}
}

func (s *MemorySnapshotStore) Save(snap Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[snap.AggregateID] = append(s.snapshots[snap.AggregateID], snap)
	return nil
}

func (s *MemorySnapshotStore) LatestBefore(aggregateID string, cutoff time.Time) (*Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snaps, ok := s.snapshots[aggregateID]
	if !ok || len(snaps) == 0 {
		return nil, nil
	}

	var latest *Snapshot
	for i := range snaps {
		if snaps[i].AsOf.Before(cutoff) || snaps[i].AsOf.Equal(cutoff) {
			if latest == nil || snaps[i].Version > latest.Version {
				latest = &snaps[i]
			}
		}
	}

	return latest, nil
}
