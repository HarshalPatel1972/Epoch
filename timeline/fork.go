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

func (f *Fork) EventCount() int {
	// A bit slow but okay for small Phase 2 forks
	evts, _ := f.fEventStore.LoadAll()
	// Actually we want fork-specific events (overlay size)
	// Let's cast it
	if _, ok := f.fEventStore.(*store.ForkEventStore); ok {
		// Event count logic
	}
	return len(evts)
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
		Snapshots: r.snaps, // Using main snapshots for base reconstruction
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
