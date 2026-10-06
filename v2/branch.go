package epoch

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"time"
)

// unbounded is the internal "no upper Seq bound". Store queries use 0 for the
// same thing; toStoreMax converts.
const unbounded int64 = math.MaxInt64

func toStoreMax(max int64) int64 {
	if max == unbounded {
		return 0
	}
	return max
}

var branchNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// segment is one branch's contribution to a lineage: its own commits with
// Seq <= max.
type segment struct {
	branch string
	max    int64
}

// lineage is the full history visible from a branch: ancestor segments
// (bounded by fork points) followed by the branch itself, root first.
//
// Seq ranges of segments never overlap and increase root to leaf, because a
// branch's commits are all appended after it was forked.
type lineage struct {
	leaf Branch
	segs []segment
}

func mainBranch() Branch { return Branch{Name: Main} }

// GetBranch returns the named branch. Main always exists.
func GetBranch(ctx context.Context, s Store, name string) (Branch, error) {
	if name == Main || name == "" {
		return mainBranch(), nil
	}
	return s.GetBranch(ctx, name)
}

// ListBranches returns Main followed by every stored branch.
func ListBranches(ctx context.Context, s Store) ([]Branch, error) {
	bs, err := s.ListBranches(ctx)
	if err != nil {
		return nil, err
	}
	return append([]Branch{mainBranch()}, bs...), nil
}

func lineageOf(ctx context.Context, s Store, name string) (lineage, error) {
	if name == "" {
		name = Main
	}
	var l lineage
	bound := unbounded
	for depth := 0; ; depth++ {
		if depth > 1000 {
			return l, fmt.Errorf("epoch: branch %q: ancestry too deep or cyclic", name)
		}
		b, err := GetBranch(ctx, s, name)
		if err != nil {
			return l, err
		}
		if depth == 0 {
			l.leaf = b
		}
		l.segs = append(l.segs, segment{branch: b.Name, max: bound})
		if b.Name == Main {
			break
		}
		bound = min(bound, b.ForkSeq)
		name = b.Parent
	}
	for i, j := 0, len(l.segs)-1; i < j; i, j = i+1, j-1 {
		l.segs[i], l.segs[j] = l.segs[j], l.segs[i]
	}
	return l, nil
}

// read runs q against every segment, honouring fork bounds and max, and
// concatenates the results in Seq order (or reverse order if q.Reverse).
func (l lineage) read(ctx context.Context, s Store, q Query, max int64) ([]Commit, error) {
	var out []Commit
	n := len(l.segs)
	for i := range n {
		seg := l.segs[i]
		if q.Reverse {
			seg = l.segs[n-1-i]
		}
		m := min(seg.max, max)
		if m <= q.AfterSeq {
			continue
		}
		sq := q
		sq.Branch = seg.branch
		sq.MaxSeq = toStoreMax(m)
		if q.Limit > 0 {
			sq.Limit = q.Limit - len(out)
		}
		cs, err := s.Read(ctx, sq)
		if err != nil {
			return nil, err
		}
		out = append(out, cs...)
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}

// last returns the newest commit matching q with Seq <= max, or nil.
func (l lineage) last(ctx context.Context, s Store, q Query, max int64) (*Commit, error) {
	q.Reverse, q.Limit = true, 1
	cs, err := l.read(ctx, s, q, max)
	if err != nil || len(cs) == 0 {
		return nil, err
	}
	return &cs[0], nil
}

// head returns the Seq of the newest commit visible on the lineage.
func (l lineage) head(ctx context.Context, s Store) (int64, error) {
	c, err := l.last(ctx, s, Query{}, unbounded)
	if err != nil || c == nil {
		return 0, err
	}
	return c.Seq, nil
}

// seqAt returns the Seq of the last visible commit with Time <= t.
func (l lineage) seqAt(ctx context.Context, s Store, t time.Time) (int64, error) {
	for i := len(l.segs) - 1; i >= 0; i-- {
		seg := l.segs[i]
		if seg.max == 0 {
			continue
		}
		seq, err := s.SeqAt(ctx, seg.branch, normTime(t), toStoreMax(seg.max))
		if err != nil {
			return 0, err
		}
		if seq > 0 {
			return seq, nil
		}
	}
	return 0, nil
}

// commitAt returns the visible commit with the given Seq.
func (l lineage) commitAt(ctx context.Context, s Store, seq int64) (*Commit, error) {
	cs, err := l.read(ctx, s, Query{AfterSeq: seq - 1, Limit: 1}, seq)
	if err != nil {
		return nil, err
	}
	if len(cs) == 0 {
		return nil, fmt.Errorf("epoch: no commit %d on branch %q", seq, l.leaf.Name)
	}
	return &cs[0], nil
}

// maxFor turns AsOf/AtSeq options into a Seq bound.
func (l lineage) maxFor(ctx context.Context, s Store, o options) (int64, error) {
	max := unbounded
	if o.atSeq > 0 {
		max = o.atSeq
	}
	if !o.asOf.IsZero() {
		seq, err := l.seqAt(ctx, s, o.asOf)
		if err != nil {
			return 0, err
		}
		max = min(max, seq)
	}
	return max, nil
}

// ForkOptions configures Fork.
type ForkOptions struct {
	// From is the parent branch. The default is Main.
	From string
	// At forks the parent as it was at this time. Seq forks it right after
	// the commit with this Seq. If neither is set, the fork starts at the
	// parent's latest commit.
	At  time.Time
	Seq int64

	Description string
}

// Fork creates a branch that shares the parent's history up to the fork
// point. Commits written to the branch are invisible to the parent.
func Fork(ctx context.Context, s Store, name string, o ForkOptions) (Branch, error) {
	return createBranch(ctx, s, name, o, KindFork)
}

func createBranch(ctx context.Context, s Store, name string, o ForkOptions, kind string) (Branch, error) {
	if name == Main || !branchNameRE.MatchString(name) {
		return Branch{}, fmt.Errorf("%w: %q (use letters, digits, '.', '_' or '-', at most 100 characters)", ErrInvalidBranchName, name)
	}
	parent, err := lineageOf(ctx, s, o.From)
	if err != nil {
		return Branch{}, err
	}
	now := normTime(time.Now())
	b := Branch{Name: name, Parent: parent.leaf.Name, Created: now, Kind: kind, Description: o.Description}

	switch {
	case o.Seq > 0:
		c, err := parent.commitAt(ctx, s, o.Seq)
		if err != nil {
			return Branch{}, err
		}
		b.ForkSeq, b.ForkTime = c.Seq, c.Time
	case !o.At.IsZero():
		if o.At.After(now) {
			return Branch{}, fmt.Errorf("epoch: cannot fork at %s, which is in the future", o.At.Format(time.RFC3339))
		}
		if b.ForkSeq, err = parent.seqAt(ctx, s, o.At); err != nil {
			return Branch{}, err
		}
		b.ForkTime = normTime(o.At)
	default:
		c, err := parent.last(ctx, s, Query{}, unbounded)
		if err != nil {
			return Branch{}, err
		}
		if c != nil {
			b.ForkSeq, b.ForkTime = c.Seq, c.Time
		}
	}
	// A branch's commits must never be older than the history it inherits.
	if b.ForkTime.Before(parent.leaf.ForkTime) {
		b.ForkTime = parent.leaf.ForkTime
	}
	if err := s.CreateBranch(ctx, b); err != nil {
		return Branch{}, err
	}
	return b, nil
}

// DeleteBranch deletes a branch and everything written to it. Branches forked
// from it must be deleted first.
func DeleteBranch(ctx context.Context, s Store, name string) error {
	if name == Main || name == "" {
		return fmt.Errorf("%w: the main branch cannot be deleted", ErrInvalidBranchName)
	}
	bs, err := s.ListBranches(ctx)
	if err != nil {
		return err
	}
	for _, b := range bs {
		if b.Parent == name {
			return fmt.Errorf("%w: %q is forked from %q", ErrBranchHasChildren, b.Name, name)
		}
	}
	return s.DeleteBranch(ctx, name)
}

// Log returns commits visible on a branch in Seq order, starting after
// afterSeq. Use limit to page through long histories. AsOf and AtSeq bound
// the result; On selects the branch.
func Log(ctx context.Context, s Store, afterSeq int64, limit int, opts ...Option) ([]Commit, error) {
	o := buildOptions(opts)
	l, err := lineageOf(ctx, s, o.branch)
	if err != nil {
		return nil, err
	}
	max, err := l.maxFor(ctx, s, o)
	if err != nil {
		return nil, err
	}
	return l.read(ctx, s, Query{AfterSeq: afterSeq, Limit: limit}, max)
}

// Head returns the Seq of the newest commit visible on a branch, or 0.
func Head(ctx context.Context, s Store, branch string) (int64, error) {
	l, err := lineageOf(ctx, s, branch)
	if err != nil {
		return 0, err
	}
	return l.head(ctx, s)
}
