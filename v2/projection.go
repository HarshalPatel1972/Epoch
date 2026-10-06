package epoch

import (
	"context"
	"encoding/json"
	"time"
)

// A Record is one event as seen by a Projection.
type Record struct {
	Seq    int64     `json:"seq"`
	Time   time.Time `json:"time"`
	Branch string    `json:"branch"`
	Model  string    `json:"model"`
	Stream string    `json:"stream"`
	// Version is the stream's version right after this event.
	Version int64 `json:"version"`
	// Command is the type of the command that produced the event, or "" for
	// a recorded fact.
	Command string          `json:"command,omitempty"`
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data"`
}

func (r Record) PayloadType() string          { return r.Type }
func (r Record) PayloadData() json.RawMessage { return r.Data }

// A Rejection is a command that a Model's Decide rejected.
type Rejection struct {
	Seq     int64       `json:"seq"`
	Time    time.Time   `json:"time"`
	Branch  string      `json:"branch"`
	Model   string      `json:"model"`
	Stream  string      `json:"stream"`
	Command CommandData `json:"command"`
	Reason  string      `json:"reason"`
}

// PayloadType and PayloadData expose the rejected command, so As works on
// a Rejection.
func (r Rejection) PayloadType() string          { return r.Command.Type }
func (r Rejection) PayloadData() json.RawMessage { return r.Command.Data }

// A Projection folds the whole log into a read model V: a report, a
// dashboard, a search index. Because it reads the log, it can be computed for
// any branch and any point in time.
//
// Event and Rejected must be pure and must not modify v in place when V
// contains maps or slices that an earlier result might still reference.
type Projection[V any] struct {
	Name string
	// Init returns the empty read model. If nil, the zero V is used.
	Init func() V
	// Event applies one event.
	Event func(v V, r Record) V
	// Rejected, if set, applies one rejected command. Use it to track demand
	// your rules turned away.
	Rejected func(v V, r Rejection) V
}

// projectionPage is how many commits Get reads from the store at once.
const projectionPage = 1000

// Get computes the read model. Use AsOf or AtSeq to compute it for the past
// and On to compute it for a branch.
//
// Get reads every visible commit, so its cost grows with history. Cache the
// result if you call it often.
func (p *Projection[V]) Get(ctx context.Context, s Store, opts ...Option) (V, error) {
	var v V
	if p.Init != nil {
		v = p.Init()
	}
	o := buildOptions(opts)
	l, err := lineageOf(ctx, s, o.branch)
	if err != nil {
		return v, err
	}
	max, err := l.maxFor(ctx, s, o)
	if err != nil {
		return v, err
	}
	for cursor := int64(0); ; {
		page, err := l.read(ctx, s, Query{AfterSeq: cursor, Limit: projectionPage}, max)
		if err != nil {
			return v, err
		}
		if len(page) == 0 {
			return v, nil
		}
		for _, c := range page {
			v = p.apply(v, c)
		}
		cursor = page[len(page)-1].Seq
	}
}

func (p *Projection[V]) apply(v V, c Commit) V {
	if c.Rejected != "" {
		if p.Rejected != nil && c.Command != nil {
			v = p.Rejected(v, Rejection{
				Seq: c.Seq, Time: c.Time, Branch: c.Branch, Model: c.Model, Stream: c.Stream,
				Command: *c.Command, Reason: c.Rejected,
			})
		}
		return v
	}
	if p.Event == nil {
		return v
	}
	var cmd string
	if c.Command != nil {
		cmd = c.Command.Type
	}
	first := c.Version - int64(len(c.Events))
	for i, e := range c.Events {
		v = p.Event(v, Record{
			Seq: c.Seq, Time: c.Time, Branch: c.Branch, Model: c.Model, Stream: c.Stream,
			Version: first + int64(i) + 1, Command: cmd, Type: e.Type, Data: e.Data,
		})
	}
	return v
}

// ProjectionName returns p.Name.
func (p *Projection[V]) ProjectionName() string { return p.Name }

// GetAny is Get for callers that do not know V, such as the HTTP API.
func (p *Projection[V]) GetAny(ctx context.Context, s Store, opts ...Option) (any, error) {
	return p.Get(ctx, s, opts...)
}

// AnyProjection is implemented by every *Projection[V].
type AnyProjection interface {
	ProjectionName() string
	GetAny(ctx context.Context, s Store, opts ...Option) (any, error)
}

var _ AnyProjection = (*Projection[struct{}])(nil)

// A Comparison holds a read model computed on two branches and the
// differences between them.
type Comparison[V any] struct {
	A       V        `json:"a"`
	B       V        `json:"b"`
	Changes []Change `json:"changes"`
}

// Compare computes p on branches a and b and diffs the results. AsOf and
// AtSeq apply to both branches; On is ignored.
func Compare[V any](ctx context.Context, s Store, p *Projection[V], a, b string, opts ...Option) (Comparison[V], error) {
	var cmp Comparison[V]
	var err error
	if cmp.A, err = p.Get(ctx, s, append(opts, On(a))...); err != nil {
		return cmp, err
	}
	if cmp.B, err = p.Get(ctx, s, append(opts, On(b))...); err != nil {
		return cmp, err
	}
	cmp.Changes, err = Diff(cmp.A, cmp.B)
	return cmp, err
}
