package store

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/google/uuid"
)

type BadgerEventStore struct {
	db        *badger.DB
	mu        sync.RWMutex
	seq       map[string]int64
	aggIDs    map[string]struct{}
	snapStore SnapshotStore

	// Injectable callback for creating snapshots
	RequestSnapshot func(aggregateID string, currentVersion int64, asOf time.Time) error
}

func NewBadgerEventStore(dir string) (*BadgerEventStore, error) {
	opts := badger.DefaultOptions(dir)
	opts.Logger = nil // Suppress noisy badger logs
	db, err := badger.Open(opts)
	if err != nil {
		return nil, err
	}

	s := &BadgerEventStore{
		db:     db,
		seq:    make(map[string]int64),
		aggIDs: make(map[string]struct{}),
	}

	if err := s.rehydrateSeq(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to rehydrate sequence map: %w", err)
	}

	return s, nil
}

func (s *BadgerEventStore) SetSnapshotStore(ss SnapshotStore) {
	s.snapStore = ss
}

func (s *BadgerEventStore) Close() error {
	return s.db.Close()
}

func (s *BadgerEventStore) DB() *badger.DB {
	return s.db
}

func eventKey(aggregateID string, version int64) []byte {
	return []byte(fmt.Sprintf("events:%s:%010d", aggregateID, version))
}

func (s *BadgerEventStore) rehydrateSeq() error {
	return s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte("events:")
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			key := string(item.Key())
			// Key: "events:UUID:VERSION"
			lastColon := -1
			for i := len(key) - 1; i >= 0; i-- {
				if key[i] == ':' {
					lastColon = i
					break
				}
			}
			if lastColon != -1 && lastColon > 7 {
				id := key[7:lastColon]
				var version int64
				fmt.Sscanf(key[lastColon+1:], "%d", &version)
				
				if version > s.seq[id] {
					s.seq[id] = version
				}
				s.aggIDs[id] = struct{}{}
			}
		}
		return nil
	})
}

func (s *BadgerEventStore) Append(e Event) (Event, error) {
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
	s.aggIDs[e.AggregateID] = struct{}{}

	val, _ := json.Marshal(e)
	key := eventKey(e.AggregateID, e.Version)

	err := s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, val)
	})

	if err != nil {
		s.seq[e.AggregateID]-- // Rollback in-memory
		return Event{}, err
	}

	if e.Version%10 == 0 && s.RequestSnapshot != nil {
		go s.RequestSnapshot(e.AggregateID, e.Version, e.OccurredAt)
	}

	return e, nil
}

func (s *BadgerEventStore) Load(aggregateID string) ([]Event, error) {
	var events []Event
	err := s.db.View(func(txn *badger.Txn) error {
		prefix := []byte(fmt.Sprintf("events:%s:", aggregateID))
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			err := item.Value(func(v []byte) error {
				var e Event
				if err := json.Unmarshal(v, &e); err != nil {
					return err
				}
				events = append(events, e)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	return events, err
}

func (s *BadgerEventStore) LoadBefore(aggregateID string, cutoff time.Time) ([]Event, error) {
	all, err := s.Load(aggregateID)
	if err != nil {
		return nil, err
	}

	var res []Event
	for _, e := range all {
		if e.OccurredAt.Before(cutoff) || e.OccurredAt.Equal(cutoff) {
			res = append(res, e)
		}
	}
	return res, nil
}

func (s *BadgerEventStore) LoadAll() ([]Event, error) {
	var events []Event
	err := s.db.View(func(txn *badger.Txn) error {
		prefix := []byte("events:")
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			err := item.Value(func(v []byte) error {
				var e Event
				if err := json.Unmarshal(v, &e); err != nil {
					return err
				}
				events = append(events, e)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})

	sort.Slice(events, func(i, j int) bool {
		if events[i].OccurredAt.Equal(events[j].OccurredAt) {
			return events[i].Version < events[j].Version
		}
		return events[i].OccurredAt.Before(events[j].OccurredAt)
	})

	return events, err
}

func (s *BadgerEventStore) AllAggregateIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var ids []string
	for id := range s.aggIDs {
		ids = append(ids, id)
	}
	return ids
}

// BadgerSnapshotStore

type BadgerSnapshotStore struct {
	db *badger.DB
}

func NewBadgerSnapshotStore(db *badger.DB) *BadgerSnapshotStore {
	return &BadgerSnapshotStore{db: db}
}

func snapshotKey(aggregateID string, asOf time.Time) []byte {
	return []byte(fmt.Sprintf("snapshots:%s:%020d", aggregateID, asOf.UnixNano()))
}

func (s *BadgerSnapshotStore) Save(snap Snapshot) error {
	val, _ := json.Marshal(snap)
	key := snapshotKey(snap.AggregateID, snap.AsOf)
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, val)
	})
}

func (s *BadgerSnapshotStore) LatestBefore(aggregateID string, cutoff time.Time) (*Snapshot, error) {
	var latest *Snapshot
	err := s.db.View(func(txn *badger.Txn) error {
		prefix := []byte(fmt.Sprintf("snapshots:%s:", aggregateID))
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		opts.Reverse = true // Newest to oldest
		it := txn.NewIterator(opts)
		defer it.Close()

		searchKey := snapshotKey(aggregateID, cutoff)
		// For reverse iterator, Seek(key) moves iterator to element <= key.
		for it.Seek(searchKey); it.Valid(); it.Next() {
			item := it.Item()
			err := item.Value(func(v []byte) error {
				var snap Snapshot
				if err := json.Unmarshal(v, &snap); err != nil {
					return err
				}
				// Verify exactly for this aggregate
				// Even with Reverse, it.ValidForPrefix(prefix) is safer
				if snap.AggregateID == aggregateID && (snap.AsOf.Before(cutoff) || snap.AsOf.Equal(cutoff)) {
					latest = &snap
					return nil
				}
				return nil
			})
			if err != nil {
				return err
			}
			if latest != nil {
				return nil
			}
		}
		return nil
	})
	return latest, err
}
