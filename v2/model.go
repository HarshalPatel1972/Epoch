package epoch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"
)

// DefaultSnapshotEvery is the snapshot interval used when
// Model.SnapshotEvery is 0.
const DefaultSnapshotEvery = 100

// maxRetries bounds how often Handle retries after ErrConflict.
const maxRetries = 25

// A Model is one kind of entity: an account, an order, a cart. S is its state.
//
// Declare Models as package-level pointers and do not change their fields
// after first use. To try different rules in a replay, copy the Model and
// replace Decide:
//
//	strict := *Accounts
//	strict.Decide = decideStrict
//	epoch.Replay(ctx, store, epoch.ReplayOptions{Models: []epoch.AnyModel{&strict}, ...})
type Model[S any] struct {
	// Name identifies the model in the log. Changing it orphans existing
	// history.
	Name string

	// Schema versions the state. Change it whenever S or Evolve changes in a
	// way that makes stored snapshots wrong; old snapshots are then ignored.
	Schema string

	// Commands and Events list one value of every command and event type the
	// model uses, e.g. Commands: []any{Deposit{}, Withdraw{}}.
	Commands []any
	Events   []any

	// Evolve applies one event to the state and returns the new state. It
	// must be pure and must not modify state in place (copy slices and maps).
	Evolve func(state S, event any) S

	// Decide turns a command into events, or returns an error to reject it.
	// It must be deterministic: depend only on state, command and now. The
	// returned events must be types listed in Events.
	Decide func(state S, command any, now time.Time) ([]any, error)

	// SnapshotEvery saves a snapshot every n events per stream. 0 uses
	// DefaultSnapshotEvery; a negative value disables snapshots.
	SnapshotEvery int
}

// State is an entity's state at a point in history.
type State[S any] struct {
	Value S `json:"value"`
	// Version is the number of events in the stream's history. 0 means the
	// entity does not exist yet.
	Version int64 `json:"version"`
	// Seq is the last commit that touched the stream, or 0.
	Seq int64 `json:"seq"`
}

// Exists reports whether any event has been recorded for the entity.
func (s State[S]) Exists() bool { return s.Version > 0 }

// Result is the outcome of Model.Handle.
type Result[S any] struct {
	// State is the entity's state after the command, or before it if the
	// command was rejected.
	State State[S]
	// Commit is the recorded commit.
	Commit Commit
	// Events are the events the command produced.
	Events []any
	// Duplicate is true if CommandID matched an earlier commit and the
	// command was not executed again.
	Duplicate bool
}

type modelTypes struct {
	commands, events *registry
	err              error
}

var modelCache sync.Map // *Model[S] (as any) -> *modelTypes

func (m *Model[S]) types() (*modelTypes, error) {
	if v, ok := modelCache.Load(m); ok {
		mt := v.(*modelTypes)
		return mt, mt.err
	}
	mt := &modelTypes{}
	switch {
	case m.Name == "":
		mt.err = errors.New("epoch: Model.Name is empty")
	case m.Evolve == nil || m.Decide == nil:
		mt.err = fmt.Errorf("epoch: model %q needs both Evolve and Decide", m.Name)
	}
	if mt.err == nil {
		mt.commands, mt.err = newRegistry("command", m.Commands)
	}
	if mt.err == nil {
		mt.events, mt.err = newRegistry("event", m.Events)
	}
	v, _ := modelCache.LoadOrStore(m, mt)
	mt = v.(*modelTypes)
	return mt, mt.err
}

func (m *Model[S]) snapshotKey() string { return m.Name + "@" + m.Schema }

func (m *Model[S]) snapshotEvery() int64 {
	if m.SnapshotEvery == 0 {
		return DefaultSnapshotEvery
	}
	return int64(m.SnapshotEvery)
}

// ModelName returns m.Name.
func (m *Model[S]) ModelName() string { return m.Name }

// loaded is a stream's state plus the version it inherits from ancestor
// branches, which Store.Append needs.
type loaded[S any] struct {
	State[S]
	base int64
}

func (m *Model[S]) load(ctx context.Context, s Store, l lineage, mt *modelTypes, id string, max int64) (loaded[S], error) {
	var st loaded[S]
	var after int64

	if m.snapshotEvery() > 0 {
		for i := len(l.segs) - 1; i >= 0; i-- {
			seg := l.segs[i]
			bound := min(seg.max, max)
			if bound == 0 {
				continue
			}
			snap, err := s.LoadSnapshot(ctx, seg.branch, m.Name, id, m.snapshotKey(), toStoreMax(bound))
			if err != nil {
				return st, err
			}
			if snap == nil {
				continue
			}
			if err := json.Unmarshal(snap.State, &st.Value); err != nil {
				// A snapshot that no longer decodes is ignored, not fatal.
				st = loaded[S]{}
				break
			}
			st.Version, st.Seq, st.base, after = snap.Version, snap.Seq, snap.Version, snap.Seq
			break
		}
	}

	commits, err := l.read(ctx, s, Query{Model: m.Name, Stream: id, AfterSeq: after}, max)
	if err != nil {
		return st, err
	}
	for _, c := range commits {
		for _, ed := range c.Events {
			ev, err := mt.events.decode(ed.Type, ed.Data)
			if err != nil {
				return st, fmt.Errorf("%s/%s commit %d: %w", m.Name, id, c.Seq, err)
			}
			st.Value = m.Evolve(st.Value, ev)
		}
		st.Version, st.Seq = c.Version, c.Seq
		if c.Branch != l.leaf.Name {
			st.base = c.Version
		}
	}
	return st, nil
}

// Load returns an entity's state. Use AsOf or AtSeq to read the past and On
// to read a branch.
func (m *Model[S]) Load(ctx context.Context, s Store, id string, opts ...Option) (State[S], error) {
	mt, err := m.types()
	if err != nil {
		return State[S]{}, err
	}
	o := buildOptions(opts)
	l, err := lineageOf(ctx, s, o.branch)
	if err != nil {
		return State[S]{}, err
	}
	max, err := l.maxFor(ctx, s, o)
	if err != nil {
		return State[S]{}, err
	}
	st, err := m.load(ctx, s, l, mt, id, max)
	return st.State, err
}

// History returns every commit for an entity, including rejected commands,
// oldest first. AsOf and AtSeq bound it; On selects the branch.
func (m *Model[S]) History(ctx context.Context, s Store, id string, opts ...Option) ([]Commit, error) {
	o := buildOptions(opts)
	l, err := lineageOf(ctx, s, o.branch)
	if err != nil {
		return nil, err
	}
	max, err := l.maxFor(ctx, s, o)
	if err != nil {
		return nil, err
	}
	return l.read(ctx, s, Query{Model: m.Name, Stream: id}, max)
}

// Handle decides cmd against the entity's current state and records the
// outcome. If Decide rejects the command, the rejection is recorded too and
// Handle returns an error wrapping both ErrRejected and Decide's error.
//
// Handle retries automatically when another writer changed the entity
// between loading and appending.
func (m *Model[S]) Handle(ctx context.Context, s Store, id string, cmd any, opts ...Option) (Result[S], error) {
	mt, err := m.types()
	if err != nil {
		return Result[S]{}, err
	}
	if id == "" {
		return Result[S]{}, errors.New("epoch: entity id is empty")
	}
	o := buildOptions(opts)
	l, err := lineageOf(ctx, s, o.branch)
	if err != nil {
		return Result[S]{}, err
	}
	cmdType, cmdData, err := mt.commands.encode(cmd)
	if err != nil {
		return Result[S]{}, err
	}

	if o.commandID != "" {
		if res, ok, err := m.duplicate(ctx, s, l, mt, o.commandID); ok || err != nil {
			return res, err
		}
	}

	for attempt := 0; ; attempt++ {
		st, err := m.load(ctx, s, l, mt, id, unbounded)
		if err != nil {
			return Result[S]{}, err
		}
		now := o.at
		if now.IsZero() {
			now = time.Now()
		}
		c := Commit{
			Branch:  l.leaf.Name,
			Time:    normTime(now),
			Model:   m.Name,
			Stream:  id,
			Command: &CommandData{ID: o.commandID, Type: cmdType, Data: cmdData},
		}
		res, err := m.decideAndAppend(ctx, s, l, mt, st, cmd, &c)
		if errors.Is(err, ErrConflict) && attempt < maxRetries {
			if err := backoff(ctx, attempt); err != nil {
				return Result[S]{}, err
			}
			continue
		}
		return res, err
	}
}

// Record appends events that are facts rather than decisions, such as a
// payment confirmed by an external provider. No command is recorded, so a
// replay copies recorded facts as they are instead of deciding them again.
func (m *Model[S]) Record(ctx context.Context, s Store, id string, events []any, opts ...Option) (Result[S], error) {
	mt, err := m.types()
	if err != nil {
		return Result[S]{}, err
	}
	if id == "" {
		return Result[S]{}, errors.New("epoch: entity id is empty")
	}
	o := buildOptions(opts)
	l, err := lineageOf(ctx, s, o.branch)
	if err != nil {
		return Result[S]{}, err
	}
	for attempt := 0; ; attempt++ {
		st, err := m.load(ctx, s, l, mt, id, unbounded)
		if err != nil {
			return Result[S]{}, err
		}
		now := o.at
		if now.IsZero() {
			now = time.Now()
		}
		c := Commit{Branch: l.leaf.Name, Time: normTime(now), Model: m.Name, Stream: id}
		res, err := m.appendEvents(ctx, s, l, mt, st, events, &c)
		if errors.Is(err, ErrConflict) && attempt < maxRetries {
			if err := backoff(ctx, attempt); err != nil {
				return Result[S]{}, err
			}
			continue
		}
		return res, err
	}
}

// decideAndAppend runs Decide and records the outcome. c must have
// everything but Events, Rejected, Seq and Version filled in.
func (m *Model[S]) decideAndAppend(ctx context.Context, s Store, l lineage, mt *modelTypes, st loaded[S], cmd any, c *Commit) (Result[S], error) {
	events, derr, perr := m.safeDecide(st.Value, cmd, c.Time)
	if perr != nil {
		return Result[S]{}, perr
	}
	if derr == nil {
		return m.appendEvents(ctx, s, l, mt, st, events, c)
	}
	c.Rejected = derr.Error()
	if c.Rejected == "" {
		c.Rejected = "rejected"
	}
	cond := AppendCondition{ExpectedVersion: st.Version, BaseVersion: st.base, MinTime: l.leaf.ForkTime}
	if err := s.Append(ctx, c, cond); err != nil {
		return Result[S]{}, err
	}
	return Result[S]{State: st.State, Commit: *c}, fmt.Errorf("%w: %w", ErrRejected, derr)
}

// appendEvents encodes events into c, appends it, and snapshots if due.
func (m *Model[S]) appendEvents(ctx context.Context, s Store, l lineage, mt *modelTypes, st loaded[S], events []any, c *Commit) (Result[S], error) {
	c.Events = make([]EventData, 0, len(events))
	for _, ev := range events {
		name, data, err := mt.events.encode(ev)
		if err != nil {
			return Result[S]{}, fmt.Errorf("epoch: model %s: %w", m.Name, err)
		}
		c.Events = append(c.Events, EventData{Type: name, Data: data})
	}
	cond := AppendCondition{ExpectedVersion: st.Version, BaseVersion: st.base, MinTime: l.leaf.ForkTime}
	if err := s.Append(ctx, c, cond); err != nil {
		return Result[S]{}, err
	}

	// Evolve from the decoded events so the returned state matches what a
	// later Load computes, even if an event does not round-trip through JSON.
	res := Result[S]{Commit: *c, Events: make([]any, 0, len(c.Events))}
	next := st.Value
	for _, ed := range c.Events {
		ev, err := mt.events.decode(ed.Type, ed.Data)
		if err != nil {
			return Result[S]{}, err
		}
		next = m.Evolve(next, ev)
		res.Events = append(res.Events, ev)
	}
	res.State = State[S]{Value: next, Version: c.Version, Seq: c.Seq}
	m.maybeSnapshot(ctx, s, st.Version, res.State, c)
	return res, nil
}

// backoff waits a little before retrying a conflicting append, growing with
// each attempt and jittered so that competing writers spread out.
func backoff(ctx context.Context, attempt int) error {
	if attempt < 2 {
		return ctx.Err()
	}
	d := time.Duration(rand.Int64N(int64(min(attempt, 10))*int64(time.Millisecond)) + 1)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// safeDecide runs Decide. A panic is returned as perr rather than treated as
// a rejection, so a bug in Decide is never recorded as a business outcome.
func (m *Model[S]) safeDecide(state S, cmd any, now time.Time) (events []any, derr, perr error) {
	defer func() {
		if r := recover(); r != nil {
			perr = fmt.Errorf("epoch: %s.Decide panicked: %v", m.Name, r)
		}
	}()
	events, derr = m.Decide(state, cmd, now)
	return events, derr, nil
}

// maybeSnapshot saves a snapshot when the commit crossed a SnapshotEvery
// boundary. Snapshots are an optimisation, so failures are ignored.
func (m *Model[S]) maybeSnapshot(ctx context.Context, s Store, prevVersion int64, st State[S], c *Commit) {
	every := m.snapshotEvery()
	if every <= 0 || st.Version/every == prevVersion/every {
		return
	}
	data, err := json.Marshal(st.Value)
	if err != nil {
		return
	}
	_ = s.SaveSnapshot(ctx, Snapshot{
		Branch: c.Branch, Model: m.Name, Stream: c.Stream, Key: m.snapshotKey(),
		Seq: c.Seq, Version: c.Version, State: data,
	})
}

func (m *Model[S]) duplicate(ctx context.Context, s Store, l lineage, mt *modelTypes, commandID string) (Result[S], bool, error) {
	prev, err := l.last(ctx, s, Query{CommandID: commandID}, unbounded)
	if err != nil || prev == nil {
		return Result[S]{}, false, err
	}
	if prev.Model != m.Name {
		return Result[S]{}, true, fmt.Errorf("epoch: command id %q was already used by model %q", commandID, prev.Model)
	}
	st, err := m.load(ctx, s, l, mt, prev.Stream, prev.Seq)
	if err != nil {
		return Result[S]{}, true, err
	}
	res := Result[S]{State: st.State, Commit: *prev, Duplicate: true}
	if prev.Rejected != "" {
		return res, true, fmt.Errorf("%w: %s", ErrRejected, prev.Rejected)
	}
	for _, ed := range prev.Events {
		ev, err := mt.events.decode(ed.Type, ed.Data)
		if err != nil {
			return Result[S]{}, true, err
		}
		res.Events = append(res.Events, ev)
	}
	return res, true, nil
}

// DecodeEvent decodes a recorded event into its Go type.
func (m *Model[S]) DecodeEvent(e EventData) (any, error) {
	mt, err := m.types()
	if err != nil {
		return nil, err
	}
	return mt.events.decode(e.Type, e.Data)
}

// DecodeCommand decodes a recorded command into its Go type.
func (m *Model[S]) DecodeCommand(c CommandData) (any, error) {
	mt, err := m.types()
	if err != nil {
		return nil, err
	}
	return mt.commands.decode(c.Type, c.Data)
}

// LoadAny is Load for callers that do not know S, such as the HTTP API.
func (m *Model[S]) LoadAny(ctx context.Context, s Store, id string, opts ...Option) (State[any], error) {
	st, err := m.Load(ctx, s, id, opts...)
	return State[any]{Value: st.Value, Version: st.Version, Seq: st.Seq}, err
}

// AnyModel is implemented by every *Model[S]. It lets Replay and the HTTP API
// work with models of different state types.
type AnyModel interface {
	ModelName() string
	LoadAny(ctx context.Context, s Store, id string, opts ...Option) (State[any], error)
	History(ctx context.Context, s Store, id string, opts ...Option) ([]Commit, error)
	newReplayer(l lineage) (replayer, error)
}

var _ AnyModel = (*Model[struct{}])(nil)
