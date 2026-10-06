package epoch

import (
	"context"
	"time"
)

// Any disables the expected-version check in AppendCondition.
const Any int64 = -1

// AppendCondition guards Store.Append.
type AppendCondition struct {
	// ExpectedVersion is the stream version the caller based its decision
	// on, or Any.
	ExpectedVersion int64

	// BaseVersion is the stream's version inherited from parent branches.
	// The store uses it as the current version while the branch itself has
	// no commits for the stream.
	BaseVersion int64

	// MinTime is the earliest time the commit may carry, normally the
	// branch's ForkTime.
	MinTime time.Time
}

// Query selects commits on a single branch. Inherited history is resolved by
// the epoch package, never by stores.
type Query struct {
	Branch string
	// Model and Stream filter by entity. Stream requires Model.
	Model  string
	Stream string
	// CommandID filters by Commit.Command.ID.
	CommandID string
	// AfterSeq and MaxSeq bound Seq: AfterSeq < Seq <= MaxSeq. A MaxSeq of 0
	// means no upper bound.
	AfterSeq int64
	MaxSeq   int64
	// Limit caps the number of commits returned. 0 means no limit.
	Limit int
	// Reverse returns the newest commits first.
	Reverse bool
}

// Store persists commits, branches and snapshots. Implementations must be
// safe for concurrent use and should pass the conformance suite in package
// epochtest.
//
// A Store only ever deals with one branch at a time; inheritance between
// branches is handled by the epoch package.
type Store interface {
	// Append atomically writes c to c.Branch. It must:
	//   - fail with ErrConflict unless cond.ExpectedVersion is Any or equals
	//     the stream's current version: the Version of the branch's latest
	//     commit for (c.Model, c.Stream), or cond.BaseVersion if there is none;
	//   - set c.Version to the current version plus len(c.Events);
	//   - set c.Time to the latest of c.Time, cond.MinTime and the time of the
	//     branch's previous commit, so time never goes backwards on a branch;
	//   - set c.Seq greater than every Seq the store assigned before.
	Append(ctx context.Context, c *Commit, cond AppendCondition) error

	// Read returns the commits on q.Branch that match q, ordered by Seq.
	Read(ctx context.Context, q Query) ([]Commit, error)

	// SeqAt returns the Seq of the last commit on branch with Time <= t and
	// Seq <= maxSeq (0 means no bound), or 0 if there is none.
	SeqAt(ctx context.Context, branch string, t time.Time, maxSeq int64) (int64, error)

	// CreateBranch stores b, failing with ErrBranchExists if the name is taken.
	CreateBranch(ctx context.Context, b Branch) error
	// GetBranch fails with ErrBranchNotFound if there is no such branch.
	GetBranch(ctx context.Context, name string) (Branch, error)
	ListBranches(ctx context.Context) ([]Branch, error)
	// DeleteBranch removes the branch with its commits and snapshots. It fails
	// with ErrBranchNotFound if there is no such branch.
	DeleteBranch(ctx context.Context, name string) error

	SaveSnapshot(ctx context.Context, s Snapshot) error
	// LoadSnapshot returns the snapshot with the highest Seq <= maxSeq (0
	// means no bound) for the branch, model, stream and key, or nil.
	LoadSnapshot(ctx context.Context, branch, model, stream, key string, maxSeq int64) (*Snapshot, error)
}

// Batcher is an optional Store extension for writing many commits in one
// transaction. Replay uses it when available.
//
// Batch calls fn with a Store whose reads see the batch's own writes. If fn
// returns an error, nothing it wrote is kept.
type Batcher interface {
	Batch(ctx context.Context, fn func(Store) error) error
}

// batch runs fn in a Batch if s supports it, and directly otherwise.
func batch(ctx context.Context, s Store, fn func(Store) error) error {
	if b, ok := s.(Batcher); ok {
		return b.Batch(ctx, fn)
	}
	return fn(s)
}
