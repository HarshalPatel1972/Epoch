package epoch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ReplayOptions configures Replay.
type ReplayOptions struct {
	// Name of the branch to create. Required.
	Name string
	// Source is the branch whose history is replayed. The default is Main.
	Source string

	// From is the fork point: history up to and including it is kept as is,
	// everything after it is replayed. Set From (a time) or FromSeq (a commit
	// Seq). If neither is set, the whole history is replayed.
	From    time.Time
	FromSeq int64

	// To ends the replay at this time (inclusive) or ToSeq. If neither is set,
	// the replay runs to the source's latest commit.
	To    time.Time
	ToSeq int64

	// Models re-decide their recorded commands, normally with changed Decide
	// rules. Commits of other models are copied as recorded. A model's
	// Name selects which commits it handles.
	Models []AnyModel

	Description string

	// MaxDivergences caps Report.Divergences; the counters still count every
	// divergence. 0 means 1000; negative means no cap.
	MaxDivergences int
}

// Divergence kinds.
const (
	// NowAccepted: the original command was rejected, the replay accepted it.
	NowAccepted = "now_accepted"
	// NowRejected: the original command was accepted, the replay rejected it.
	NowRejected = "now_rejected"
	// EventsChanged: both accepted the command but produced different events.
	EventsChanged = "events_changed"
)

// A Divergence is a command whose outcome changed in a replay.
type Divergence struct {
	Kind     string `json:"kind"`
	Original Commit `json:"original"`
	Replayed Commit `json:"replayed"`
}

// Report summarises a replay.
type Report struct {
	Branch  Branch `json:"branch"`
	FromSeq int64  `json:"from_seq"`
	ToSeq   int64  `json:"to_seq"`
	// Commits is the number of commits replayed or copied.
	Commits int `json:"commits"`
	// Replayed is the number of commands re-decided. Only Replay sets it;
	// ReplayReport cannot tell re-decided from copied commits.
	Replayed int `json:"replayed"`

	Diverged      int          `json:"diverged"`
	NowAccepted   int          `json:"now_accepted"`
	NowRejected   int          `json:"now_rejected"`
	EventsChanged int          `json:"events_changed"`
	Divergences   []Divergence `json:"divergences"`
	// Truncated is true when Divergences was capped by MaxDivergences.
	Truncated bool `json:"truncated,omitempty"`

	max int
}

func (r *Report) add(orig, rep Commit) {
	kind := divergence(orig, rep)
	if kind == "" {
		return
	}
	r.Diverged++
	switch kind {
	case NowAccepted:
		r.NowAccepted++
	case NowRejected:
		r.NowRejected++
	case EventsChanged:
		r.EventsChanged++
	}
	if r.max >= 0 && len(r.Divergences) >= r.max {
		r.Truncated = true
		return
	}
	r.Divergences = append(r.Divergences, Divergence{Kind: kind, Original: orig, Replayed: rep})
}

func divergence(orig, rep Commit) string {
	origOK, repOK := orig.Rejected == "", rep.Rejected == ""
	switch {
	case !origOK && repOK:
		return NowAccepted
	case origOK && !repOK:
		return NowRejected
	case !origOK && !repOK:
		return ""
	}
	if len(orig.Events) != len(rep.Events) {
		return EventsChanged
	}
	for i := range orig.Events {
		if orig.Events[i].Type != rep.Events[i].Type || !jsonEqual(orig.Events[i].Data, rep.Events[i].Data) {
			return EventsChanged
		}
	}
	return ""
}

func jsonEqual(a, b json.RawMessage) bool {
	if bytes.Equal(a, b) {
		return true
	}
	var ca, cb bytes.Buffer
	if json.Compact(&ca, a) != nil || json.Compact(&cb, b) != nil {
		return false
	}
	return bytes.Equal(ca.Bytes(), cb.Bytes())
}

func newReport(max int) *Report {
	if max == 0 {
		max = 1000
	}
	return &Report{max: max, Divergences: []Divergence{}}
}

// Replay creates a branch from Source at the From point and re-runs every
// commit after it, up to To, on that branch:
//
//   - commands of the given Models are decided again by those Models, with
//     the decision time set to the original commit's time;
//   - everything else is copied as recorded.
//
// Replayed commits keep their original times, so time-travel reads work on
// the branch exactly as on the source. If Replay fails, the partial branch is
// deleted.
func Replay(ctx context.Context, s Store, o ReplayOptions) (*Report, error) {
	if o.Source == "" {
		o.Source = Main
	}
	src, err := lineageOf(ctx, s, o.Source)
	if err != nil {
		return nil, err
	}
	toSeq := o.ToSeq
	switch {
	case toSeq > 0:
	case !o.To.IsZero():
		toSeq, err = src.seqAt(ctx, s, o.To)
	default:
		toSeq, err = src.head(ctx, s)
	}
	if err != nil {
		return nil, err
	}

	fork := ForkOptions{From: o.Source, At: o.From, Seq: o.FromSeq, Description: o.Description}
	var b Branch
	if o.From.IsZero() && o.FromSeq == 0 {
		b, err = createBranchAtStart(ctx, s, o.Name, fork)
	} else {
		b, err = createBranch(ctx, s, o.Name, fork, KindReplay)
	}
	if err != nil {
		return nil, err
	}
	done := false
	defer func() {
		if !done {
			_ = s.DeleteBranch(context.WithoutCancel(ctx), b.Name)
		}
	}()
	if b.ForkSeq > toSeq {
		return nil, fmt.Errorf("epoch: replay starts at commit %d, after its end at commit %d", b.ForkSeq, toSeq)
	}

	l, err := lineageOf(ctx, s, b.Name)
	if err != nil {
		return nil, err
	}
	replayers := map[string]replayer{}
	for _, m := range o.Models {
		if _, dup := replayers[m.ModelName()]; dup {
			return nil, fmt.Errorf("epoch: model %q passed to Replay twice", m.ModelName())
		}
		if replayers[m.ModelName()], err = m.newReplayer(l); err != nil {
			return nil, err
		}
	}

	rep := newReport(o.MaxDivergences)
	rep.Branch, rep.FromSeq, rep.ToSeq = b, b.ForkSeq, toSeq
	bases := map[[2]string]int64{}
	for cursor := b.ForkSeq; ; {
		page, err := src.read(ctx, s, Query{AfterSeq: cursor, Limit: 500}, toSeq)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		// Each page is written in one transaction when the store supports it.
		err = batch(ctx, s, func(bs Store) error {
			for _, orig := range page {
				if err := ctx.Err(); err != nil {
					return err
				}
				var out Commit
				var err error
				rp, ok := replayers[orig.Model]
				switch {
				case ok && orig.Command != nil:
					out, err = rp.redo(ctx, bs, orig)
					rep.Replayed++
				case ok:
					out, err = rp.copy(ctx, bs, orig)
				default:
					out, err = copyCommit(ctx, bs, l, orig, bases)
				}
				if err != nil {
					return fmt.Errorf("epoch: replaying commit %d (%s/%s): %w", orig.Seq, orig.Model, orig.Stream, err)
				}
				rep.Commits++
				rep.add(orig, out)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		cursor = page[len(page)-1].Seq
	}
	done = true
	return rep, nil
}

// createBranchAtStart creates a replay branch that inherits nothing from its
// parent, so that the parent's whole history is replayed.
func createBranchAtStart(ctx context.Context, s Store, name string, o ForkOptions) (Branch, error) {
	if name == Main || !branchNameRE.MatchString(name) {
		return Branch{}, fmt.Errorf("%w: %q", ErrInvalidBranchName, name)
	}
	parent, err := lineageOf(ctx, s, o.From)
	if err != nil {
		return Branch{}, err
	}
	b := Branch{
		Name: name, Parent: parent.leaf.Name, ForkTime: parent.leaf.ForkTime,
		Created: normTime(time.Now()), Kind: KindReplay, Description: o.Description,
	}
	return b, s.CreateBranch(ctx, b)
}

func copyCommit(ctx context.Context, s Store, l lineage, orig Commit, bases map[[2]string]int64) (Commit, error) {
	key := [2]string{orig.Model, orig.Stream}
	base, ok := bases[key]
	if !ok {
		// Before the first copy for this stream, the newest visible commit is
		// inherited, so its Version is the base.
		prev, err := l.last(ctx, s, Query{Model: orig.Model, Stream: orig.Stream}, unbounded)
		if err != nil {
			return Commit{}, err
		}
		if prev != nil {
			base = prev.Version
		}
		bases[key] = base
	}
	c := replica(orig, l.leaf.Name)
	err := s.Append(ctx, &c, AppendCondition{ExpectedVersion: Any, BaseVersion: base, MinTime: l.leaf.ForkTime})
	return c, err
}

func replica(orig Commit, branch string) Commit {
	return Commit{
		Branch: branch, Time: orig.Time, Model: orig.Model, Stream: orig.Stream,
		Command: orig.Command, Events: orig.Events, Rejected: orig.Rejected, Origin: orig.Seq,
	}
}

// replayer re-executes or copies one model's commits on a replay branch,
// keeping stream states in memory so each command does not reload history.
type replayer interface {
	redo(ctx context.Context, s Store, orig Commit) (Commit, error)
	copy(ctx context.Context, s Store, orig Commit) (Commit, error)
}

type modelReplayer[S any] struct {
	m      *Model[S]
	l      lineage
	mt     *modelTypes
	states map[string]loaded[S]
}

func (m *Model[S]) newReplayer(l lineage) (replayer, error) {
	mt, err := m.types()
	if err != nil {
		return nil, err
	}
	return &modelReplayer[S]{m: m, l: l, mt: mt, states: map[string]loaded[S]{}}, nil
}

func (r *modelReplayer[S]) state(ctx context.Context, s Store, id string) (loaded[S], error) {
	if st, ok := r.states[id]; ok {
		return st, nil
	}
	return r.m.load(ctx, s, r.l, r.mt, id, unbounded)
}

func (r *modelReplayer[S]) redo(ctx context.Context, s Store, orig Commit) (Commit, error) {
	st, err := r.state(ctx, s, orig.Stream)
	if err != nil {
		return Commit{}, err
	}
	cmd, err := r.mt.commands.decode(orig.Command.Type, orig.Command.Data)
	if err != nil {
		return Commit{}, err
	}
	c := replica(orig, r.l.leaf.Name)
	c.Events, c.Rejected = nil, ""
	res, err := r.m.decideAndAppend(ctx, s, r.l, r.mt, st, cmd, &c)
	switch {
	case errors.Is(err, ErrRejected):
		st.Seq = c.Seq
	case err != nil:
		return Commit{}, err
	default:
		st.State = res.State
	}
	r.states[orig.Stream] = st
	return c, nil
}

func (r *modelReplayer[S]) copy(ctx context.Context, s Store, orig Commit) (Commit, error) {
	st, err := r.state(ctx, s, orig.Stream)
	if err != nil {
		return Commit{}, err
	}
	c := replica(orig, r.l.leaf.Name)
	if err := s.Append(ctx, &c, AppendCondition{ExpectedVersion: Any, BaseVersion: st.base, MinTime: r.l.leaf.ForkTime}); err != nil {
		return Commit{}, err
	}
	for _, ed := range c.Events {
		ev, err := r.mt.events.decode(ed.Type, ed.Data)
		if err != nil {
			return Commit{}, err
		}
		st.Value = r.m.Evolve(st.Value, ev)
	}
	st.Version, st.Seq = c.Version, c.Seq
	r.states[orig.Stream] = st
	return c, nil
}

// ReplayReport rebuilds the Report of a replay branch from stored commits, by
// comparing every commit on the branch with the commit it was replayed from.
// Report.Replayed is always 0, since stored commits do not record whether a
// command was re-decided or copied.
func ReplayReport(ctx context.Context, s Store, branch string, maxDivergences int) (*Report, error) {
	b, err := GetBranch(ctx, s, branch)
	if err != nil {
		return nil, err
	}
	if b.Kind != KindReplay {
		return nil, fmt.Errorf("epoch: branch %q is not a replay branch", branch)
	}
	src, err := lineageOf(ctx, s, b.Parent)
	if err != nil {
		return nil, err
	}
	rep := newReport(maxDivergences)
	rep.Branch, rep.FromSeq, rep.ToSeq = b, b.ForkSeq, b.ForkSeq
	for cursor := int64(0); ; {
		page, err := s.Read(ctx, Query{Branch: b.Name, AfterSeq: cursor, Limit: 500})
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		cursor = page[len(page)-1].Seq
		var lo, hi int64
		for _, c := range page {
			if c.Origin == 0 {
				continue
			}
			if lo == 0 || c.Origin < lo {
				lo = c.Origin
			}
			hi = max(hi, c.Origin)
		}
		if hi == 0 {
			continue
		}
		origs, err := src.read(ctx, s, Query{AfterSeq: lo - 1}, hi)
		if err != nil {
			return nil, err
		}
		bySeq := make(map[int64]Commit, len(origs))
		for _, c := range origs {
			bySeq[c.Seq] = c
		}
		for _, c := range page {
			orig, ok := bySeq[c.Origin]
			if c.Origin == 0 || !ok {
				continue
			}
			rep.Commits++
			rep.ToSeq = max(rep.ToSeq, orig.Seq)
			rep.add(orig, c)
		}
	}
	return rep, nil
}
