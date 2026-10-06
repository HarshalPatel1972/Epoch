package epoch

import (
	"errors"
	"time"
)

// Main is the name of the root branch. It always exists.
const Main = "main"

var (
	// ErrConflict is returned by Store.Append when the stream's version does
	// not match the expected version. Model.Handle retries it automatically.
	ErrConflict = errors.New("epoch: stream was modified concurrently")

	// ErrRejected wraps the error returned by a Model's Decide function.
	// errors.Is(err, ErrRejected) tells rejections from failures, and
	// errors.Is / errors.As on the same error reach the Decide error.
	ErrRejected = errors.New("epoch: command rejected")

	ErrBranchExists      = errors.New("epoch: branch already exists")
	ErrBranchNotFound    = errors.New("epoch: branch not found")
	ErrBranchHasChildren = errors.New("epoch: branch has child branches")
	ErrInvalidBranchName = errors.New("epoch: invalid branch name")

	// ErrUnknownType is returned when a command or event type is not
	// registered with the Model handling it.
	ErrUnknownType = errors.New("epoch: unknown type")
)

// Option configures reads and writes. Options that do not apply to a call are
// ignored.
type Option func(*options)

type options struct {
	branch    string
	asOf      time.Time
	atSeq     int64
	at        time.Time
	commandID string
}

func buildOptions(opts []Option) options {
	o := options{branch: Main}
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// On selects the branch to read from or write to. The default is Main.
func On(branch string) Option { return func(o *options) { o.branch = branch } }

// AsOf reads the state as it was at time t (inclusive).
func AsOf(t time.Time) Option { return func(o *options) { o.asOf = t } }

// AtSeq reads the state as it was right after the commit with sequence seq.
func AtSeq(seq int64) Option { return func(o *options) { o.atSeq = seq } }

// WithTime sets the decision time for Model.Handle instead of time.Now. It is
// meant for imports and tests. Time never goes backwards on a branch, so a
// time earlier than the branch's last commit is moved forward to it.
func WithTime(t time.Time) Option { return func(o *options) { o.at = t } }

// CommandID makes Model.Handle idempotent: if a commit with this command ID
// already exists on the branch (or the history it inherits), Handle returns
// that commit's outcome instead of executing the command again.
func CommandID(id string) Option { return func(o *options) { o.commandID = id } }

// normTime strips the monotonic clock reading and location so that times
// compare and serialize identically across stores.
func normTime(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.Round(0).UTC()
}
