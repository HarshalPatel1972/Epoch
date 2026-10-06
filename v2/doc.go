// Package epoch records what your application was asked to do, not just what
// it did, so you can travel back to any moment, branch history, and re-run
// the past under new business rules.
//
// # Model
//
// Application logic is written as a [Model]: a pure Decide function that turns
// a command into events (or rejects it), and a pure Evolve function that folds
// events into state. Every call to [Model.Handle] appends one [Commit] to the
// log. A commit stores the command, the resulting events, and, when Decide
// returned an error, the rejection reason. Recording rejected commands is
// what lets a replay discover work that new rules would have accepted.
//
// # Time travel
//
// Reads take [AsOf] or [AtSeq] options. [Model.Load] rebuilds one entity;
// [Projection.Get] folds the whole log into a read model.
//
// # Branches
//
// [Fork] creates a copy-on-write branch of the log at any point. Writes to a
// branch never touch its parent. Branches are stored, can be nested, and are
// read with the same API as the main timeline by passing [On].
//
// # Replay
//
// [Replay] forks a branch at a point in the past and re-executes every
// recorded command after it through the Models you pass, which typically
// carry new Decide rules. Commits from other models are copied as recorded.
// The [Report] lists every command whose outcome changed, and [Compare]
// diffs any projection between the original and the replayed branch.
//
// Replay re-runs the inputs your system actually received. It cannot predict
// how people would have behaved differently under different rules, so it
// answers "what would our system have done", not "what would have happened".
//
// # Determinism
//
// Replay is only meaningful if Decide is deterministic: it should depend on
// the current state, the command, and the decision time passed to it, and
// nothing else. Data fetched from elsewhere (prices, scores, exchange rates)
// belongs in the command so that it is recorded with it.
package epoch
