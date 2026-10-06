// Package memstore is an in-memory epoch.Store for tests, examples and
// short-lived processes. Nothing is persisted.
package memstore

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/HarshalPatel1972/epoch/v2"
)

type streamKey struct{ model, stream string }

type snapKey struct{ branch, model, stream, key string }

// Store is an in-memory epoch.Store. The zero value is not usable; call New.
type Store struct {
	mu       sync.RWMutex
	seq      int64
	branches map[string]epoch.Branch
	commits  map[string][]epoch.Commit      // branch -> commits by Seq
	streams  map[string]map[streamKey][]int // branch -> stream -> indexes into commits
	snaps    map[snapKey][]epoch.Snapshot   // sorted by Seq
}

var _ epoch.Store = (*Store)(nil)

// New returns an empty store.
func New() *Store {
	return &Store{
		branches: map[string]epoch.Branch{},
		commits:  map[string][]epoch.Commit{},
		streams:  map[string]map[streamKey][]int{},
		snaps:    map[snapKey][]epoch.Snapshot{},
	}
}

func (s *Store) exists(branch string) bool {
	_, ok := s.branches[branch]
	return ok || branch == epoch.Main
}

func (s *Store) Append(_ context.Context, c *epoch.Commit, cond epoch.AppendCondition) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.exists(c.Branch) {
		return fmt.Errorf("%w: %q", epoch.ErrBranchNotFound, c.Branch)
	}
	commits := s.commits[c.Branch]
	k := streamKey{c.Model, c.Stream}
	cur := cond.BaseVersion
	if idx := s.streams[c.Branch][k]; len(idx) > 0 {
		cur = commits[idx[len(idx)-1]].Version
	}
	if cond.ExpectedVersion != epoch.Any && cond.ExpectedVersion != cur {
		return fmt.Errorf("%w: %s/%s is at version %d, expected %d", epoch.ErrConflict, c.Model, c.Stream, cur, cond.ExpectedVersion)
	}

	t := c.Time
	if t.Before(cond.MinTime) {
		t = cond.MinTime
	}
	if n := len(commits); n > 0 && t.Before(commits[n-1].Time) {
		t = commits[n-1].Time
	}
	s.seq++
	c.Seq, c.Time, c.Version = s.seq, t, cur+int64(len(c.Events))

	if s.streams[c.Branch] == nil {
		s.streams[c.Branch] = map[streamKey][]int{}
	}
	s.streams[c.Branch][k] = append(s.streams[c.Branch][k], len(commits))
	s.commits[c.Branch] = append(commits, clone(*c))
	return nil
}

func (s *Store) Read(_ context.Context, q epoch.Query) ([]epoch.Commit, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	commits := s.commits[q.Branch]
	var idx []int
	if q.Stream != "" || q.Model != "" {
		if q.Stream != "" {
			idx = s.streams[q.Branch][streamKey{q.Model, q.Stream}]
		} else {
			for i, c := range commits {
				if c.Model == q.Model {
					idx = append(idx, i)
				}
			}
		}
	} else {
		idx = make([]int, len(commits))
		for i := range idx {
			idx[i] = i
		}
	}

	// idx is in Seq order: find the window AfterSeq < Seq <= MaxSeq.
	lo := sort.Search(len(idx), func(i int) bool { return commits[idx[i]].Seq > q.AfterSeq })
	hi := len(idx)
	if q.MaxSeq > 0 {
		hi = sort.Search(len(idx), func(i int) bool { return commits[idx[i]].Seq > q.MaxSeq })
	}
	if lo >= hi {
		return nil, nil
	}
	window := idx[lo:hi]

	out := []epoch.Commit{}
	visit := func(i int) bool {
		c := commits[i]
		if q.CommandID != "" && (c.Command == nil || c.Command.ID != q.CommandID) {
			return true
		}
		out = append(out, clone(c))
		return q.Limit <= 0 || len(out) < q.Limit
	}
	if q.Reverse {
		for j := len(window) - 1; j >= 0 && visit(window[j]); j-- {
		}
	} else {
		for j := 0; j < len(window) && visit(window[j]); j++ {
		}
	}
	return out, nil
}

func (s *Store) SeqAt(_ context.Context, branch string, t time.Time, maxSeq int64) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	commits := s.commits[branch]
	// Times never decrease on a branch, so both bounds are binary searches.
	n := sort.Search(len(commits), func(i int) bool { return commits[i].Time.After(t) })
	if maxSeq > 0 {
		n = min(n, sort.Search(len(commits), func(i int) bool { return commits[i].Seq > maxSeq }))
	}
	if n == 0 {
		return 0, nil
	}
	return commits[n-1].Seq, nil
}

func (s *Store) CreateBranch(_ context.Context, b epoch.Branch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exists(b.Name) {
		return fmt.Errorf("%w: %q", epoch.ErrBranchExists, b.Name)
	}
	s.branches[b.Name] = b
	return nil
}

func (s *Store) GetBranch(_ context.Context, name string) (epoch.Branch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.branches[name]
	if !ok {
		return epoch.Branch{}, fmt.Errorf("%w: %q", epoch.ErrBranchNotFound, name)
	}
	return b, nil
}

func (s *Store) ListBranches(context.Context) ([]epoch.Branch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]epoch.Branch, 0, len(s.branches))
	for _, b := range s.branches {
		out = append(out, b)
	}
	slices.SortFunc(out, func(a, b epoch.Branch) int {
		if c := a.Created.Compare(b.Created); c != 0 {
			return c
		}
		if a.Name < b.Name {
			return -1
		}
		return 1
	})
	return out, nil
}

func (s *Store) DeleteBranch(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.branches[name]; !ok {
		return fmt.Errorf("%w: %q", epoch.ErrBranchNotFound, name)
	}
	delete(s.branches, name)
	delete(s.commits, name)
	delete(s.streams, name)
	for k := range s.snaps {
		if k.branch == name {
			delete(s.snaps, k)
		}
	}
	return nil
}

func (s *Store) SaveSnapshot(_ context.Context, snap epoch.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.exists(snap.Branch) {
		return fmt.Errorf("%w: %q", epoch.ErrBranchNotFound, snap.Branch)
	}
	k := snapKey{snap.Branch, snap.Model, snap.Stream, snap.Key}
	snap.State = slices.Clone(snap.State)
	list := s.snaps[k]
	i := sort.Search(len(list), func(i int) bool { return list[i].Seq >= snap.Seq })
	if i < len(list) && list[i].Seq == snap.Seq {
		list[i] = snap
	} else {
		list = slices.Insert(list, i, snap)
	}
	s.snaps[k] = list
	return nil
}

func (s *Store) LoadSnapshot(_ context.Context, branch, model, stream, key string, maxSeq int64) (*epoch.Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := s.snaps[snapKey{branch, model, stream, key}]
	n := len(list)
	if maxSeq > 0 {
		n = sort.Search(len(list), func(i int) bool { return list[i].Seq > maxSeq })
	}
	if n == 0 {
		return nil, nil
	}
	snap := list[n-1]
	snap.State = slices.Clone(snap.State)
	return &snap, nil
}

// clone deep-copies a commit so callers can never mutate stored data.
func clone(c epoch.Commit) epoch.Commit {
	if c.Command != nil {
		cmd := *c.Command
		cmd.Data = slices.Clone(cmd.Data)
		c.Command = &cmd
	}
	if c.Events != nil {
		evs := make([]epoch.EventData, len(c.Events))
		for i, e := range c.Events {
			evs[i] = epoch.EventData{Type: e.Type, Data: json.RawMessage(slices.Clone(e.Data))}
		}
		c.Events = evs
	}
	return c
}
