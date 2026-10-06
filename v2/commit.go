package epoch

import (
	"encoding/json"
	"time"
)

// A Commit is one entry in the log: a command handled by a Model, the events
// it produced, or the reason it was rejected.
type Commit struct {
	// Seq is assigned by the store. It is unique across all branches and
	// increases with every append, so a branch's history sorts by it.
	Seq    int64     `json:"seq"`
	Branch string    `json:"branch"`
	Time   time.Time `json:"time"`

	// Model and Stream identify the entity: Model is the Model's Name, Stream
	// is the entity ID passed to Handle.
	Model  string `json:"model"`
	Stream string `json:"stream"`

	// Version is the stream's version after this commit: the number of events
	// in the stream's history, including history inherited by a branch.
	Version int64 `json:"version"`

	Command  *CommandData `json:"command,omitempty"`
	Events   []EventData  `json:"events,omitempty"`
	Rejected string       `json:"rejected,omitempty"`

	// Origin is the Seq of the commit this one was replayed or copied from,
	// or 0 for commits written directly.
	Origin int64 `json:"origin,omitempty"`
}

// CommandData is a recorded command.
type CommandData struct {
	ID   string          `json:"id,omitempty"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// EventData is a recorded event.
type EventData struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func (c CommandData) PayloadType() string          { return c.Type }
func (c CommandData) PayloadData() json.RawMessage { return c.Data }
func (e EventData) PayloadType() string            { return e.Type }
func (e EventData) PayloadData() json.RawMessage   { return e.Data }

// Branch describes a line of history. Every branch except Main has a Parent
// and inherits the parent's commits up to and including ForkSeq.
type Branch struct {
	Name        string    `json:"name"`
	Parent      string    `json:"parent,omitempty"`
	ForkSeq     int64     `json:"fork_seq"`
	ForkTime    time.Time `json:"fork_time"`
	Created     time.Time `json:"created"`
	Kind        string    `json:"kind,omitempty"`
	Description string    `json:"description,omitempty"`
}

// Branch kinds.
const (
	KindFork   = "fork"
	KindReplay = "replay"
)

// A Snapshot caches a stream's state at a commit so that loads only replay
// the commits after it.
type Snapshot struct {
	Branch  string          `json:"branch"`
	Model   string          `json:"model"`
	Stream  string          `json:"stream"`
	Key     string          `json:"key"` // Model.Name + "@" + Model.Schema
	Seq     int64           `json:"seq"`
	Version int64           `json:"version"`
	State   json.RawMessage `json:"state"`
}
