package timeline

import (
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/HarshalPatel1972/epoch/aggregate"
	"github.com/HarshalPatel1972/epoch/store"
)

type Fork struct {
	Name        string    `json:"name"`
	ForkedFrom  time.Time `json:"forked_from"`
	CreatedAt   time.Time `json:"created_at"`
	Description string    `json:"description"`
	
	fEventStore store.EventStore
	fProjector  *aggregate.Projector
}

func (f *Fork) Projector() *aggregate.Projector {
	return f.fProjector
}

func (f *Fork) EventStore() store.EventStore {
	return f.fEventStore
}

// EventCount returns the number of events written to this fork (not inherited ones).
func (f *Fork) EventCount() int {
	if fs, ok := f.fEventStore.(*store.ForkEventStore); ok {
		return fs.OverlayCount()
	}
	return 0
}

type ForkRegistry struct {
	mu     sync.RWMutex
	forks  map[string]*Fork
	main   store.EventStore
	snaps  store.SnapshotStore
}

func NewForkRegistry(main store.EventStore, snaps store.SnapshotStore) *ForkRegistry {
	return &ForkRegistry{
		forks: make(map[string]*Fork),
		main:  main,
		snaps: snaps,
	}
}

var nameRegex = regexp.MustCompile("^[a-zA-Z0-9-]{1,40}$")

func (r *ForkRegistry) Create(name string, forkedFrom time.Time, description string) (*Fork, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !nameRegex.MatchString(name) {
		return nil, fmt.Errorf("invalid fork name: alphanumeric and hyphens only, max 40 chars")
	}

	if forkedFrom.After(time.Now()) {
		return nil, fmt.Errorf("forked_from must be in the past")
	}

	if _, exists := r.forks[name]; exists {
		return nil, fmt.Errorf("timeline '%s' already exists", name)
	}

	fES := store.NewForkEventStore(r.main, forkedFrom)
	fProj := &aggregate.Projector{
		Events:    fES,
		Snapshots: store.ClampedSnapshotStore{Inner: r.snaps, Until: forkedFrom},
	}

	fork := &Fork{
		Name:        name,
		ForkedFrom:  forkedFrom,
		CreatedAt:   time.Now(),
		Description: description,
		fEventStore: fES,
		fProjector:  fProj,
	}

	r.forks[name] = fork
	return fork, nil
}

func (r *ForkRegistry) Get(name string) (*Fork, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	fork, exists := r.forks[name]
	if !exists {
		return nil, fmt.Errorf("timeline '%s' not found", name)
	}
	return fork, nil
}

func (r *ForkRegistry) List() []*Fork {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var list []*Fork
	for _, f := range r.forks {
		list = append(list, f)
	}
	return list
}

func (r *ForkRegistry) Delete(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.forks[name]; !exists {
		return fmt.Errorf("timeline '%s' not found", name)
	}
	delete(r.forks, name)
	return nil
}
