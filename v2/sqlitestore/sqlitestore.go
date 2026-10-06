// Package sqlitestore is an epoch.Store backed by SQLite, using the pure-Go
// modernc.org/sqlite driver, so it needs no cgo and cross-compiles anywhere.
//
//	store, err := sqlitestore.Open("epoch.db")
//	if err != nil { ... }
//	defer store.Close()
//
// The database runs in WAL mode: reads proceed in parallel and writes are
// serialised, which suits a single application node. All tables are prefixed
// with "epoch_", so the database can be shared with application tables.
package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/HarshalPatel1972/epoch/v2"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS epoch_commits (
	seq          INTEGER PRIMARY KEY AUTOINCREMENT,
	branch       TEXT    NOT NULL,
	time         INTEGER NOT NULL,
	model        TEXT    NOT NULL,
	stream       TEXT    NOT NULL,
	version      INTEGER NOT NULL,
	command_id   TEXT,
	command_type TEXT,
	command_data BLOB,
	events       BLOB,
	rejected     TEXT    NOT NULL DEFAULT '',
	origin       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS epoch_commits_branch  ON epoch_commits (branch, seq);
CREATE INDEX IF NOT EXISTS epoch_commits_stream  ON epoch_commits (branch, model, stream, seq);
CREATE INDEX IF NOT EXISTS epoch_commits_time    ON epoch_commits (branch, time, seq);
CREATE INDEX IF NOT EXISTS epoch_commits_command ON epoch_commits (branch, command_id) WHERE command_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS epoch_branches (
	name        TEXT PRIMARY KEY,
	parent      TEXT    NOT NULL,
	fork_seq    INTEGER NOT NULL,
	fork_time   INTEGER,
	created     INTEGER,
	kind        TEXT    NOT NULL,
	description TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS epoch_snapshots (
	branch  TEXT    NOT NULL,
	model   TEXT    NOT NULL,
	stream  TEXT    NOT NULL,
	key     TEXT    NOT NULL,
	seq     INTEGER NOT NULL,
	version INTEGER NOT NULL,
	state   BLOB    NOT NULL,
	PRIMARY KEY (branch, model, stream, key, seq)
) WITHOUT ROWID;
`

// Store is a SQLite-backed epoch.Store. It is safe for concurrent use.
type Store struct {
	w  *sql.DB // single connection; every write is an IMMEDIATE transaction
	r  *sql.DB // read pool (the same handle as w for in-memory databases)
	tx *sql.Tx // set on the Store passed to a Batch function
	st *stmts
}

// stmts are the append path's statements, prepared once on the writer
// connection. Parsing them on every append would dominate replay time.
type stmts struct {
	branchExists, streamVersion, lastTime, insert *sql.Stmt
}

func prepare(db *sql.DB) (*stmts, error) {
	var st stmts
	for _, p := range []struct {
		dst   **sql.Stmt
		query string
	}{
		{&st.branchExists, `SELECT 1 FROM epoch_branches WHERE name = ?`},
		{&st.streamVersion, `SELECT version FROM epoch_commits WHERE branch = ? AND model = ? AND stream = ? ORDER BY seq DESC LIMIT 1`},
		{&st.lastTime, `SELECT time FROM epoch_commits WHERE branch = ? ORDER BY seq DESC LIMIT 1`},
		{&st.insert, `INSERT INTO epoch_commits (branch, time, model, stream, version, command_id, command_type, command_data, events, rejected, origin)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`},
	} {
		stmt, err := db.Prepare(p.query)
		if err != nil {
			return nil, err
		}
		*p.dst = stmt
	}
	return &st, nil
}

var (
	_ epoch.Store   = (*Store)(nil)
	_ epoch.Batcher = (*Store)(nil)
)

// dbtx is what *sql.DB and *sql.Tx have in common.
type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// rd returns the handle for reads: the batch transaction, so a batch sees its
// own writes, or else the read pool.
func (s *Store) rd() dbtx {
	if s.tx != nil {
		return s.tx
	}
	return s.r
}

// wr returns the handle for single-statement writes.
func (s *Store) wr() dbtx {
	if s.tx != nil {
		return s.tx
	}
	return s.w
}

// inTx runs fn in the batch transaction if there is one, or else in a new
// IMMEDIATE transaction.
func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	if s.tx != nil {
		return fn(s.tx)
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Batch runs fn in one transaction: everything fn writes through the Store it
// is given is committed together, or not at all if fn returns an error. Other
// writers wait until the batch ends. epoch.Replay uses it to replay thousands
// of commands per second.
func (s *Store) Batch(ctx context.Context, fn func(epoch.Store) error) error {
	if s.tx != nil {
		return fn(s)
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		return fn(&Store{w: s.w, r: s.r, tx: tx, st: s.st})
	})
}

// Open opens or creates the database at path and creates Epoch's tables. Use
// ":memory:" for a private in-memory database.
func Open(path string) (*Store, error) {
	memory := path == ":memory:"
	dsn := func(txlock string) string {
		q := url.Values{}
		q.Add("_pragma", "busy_timeout(10000)")
		q.Add("_pragma", "foreign_keys(1)")
		q.Add("_pragma", "synchronous(NORMAL)")
		if !memory {
			q.Add("_pragma", "journal_mode(WAL)")
		}
		if txlock != "" {
			q.Set("_txlock", txlock)
		}
		if memory {
			return ":memory:?" + q.Encode()
		}
		return "file:" + path + "?" + q.Encode()
	}

	w, err := sql.Open("sqlite", dsn("immediate"))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetConnMaxLifetime(0)
	w.SetConnMaxIdleTime(0)
	if _, err := w.Exec(schema); err != nil {
		w.Close()
		return nil, fmt.Errorf("sqlitestore: creating schema: %w", err)
	}
	st, err := prepare(w)
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("sqlitestore: preparing statements: %w", err)
	}
	s := &Store{w: w, r: w, st: st}
	if !memory {
		if s.r, err = sql.Open("sqlite", dsn("")); err != nil {
			w.Close()
			return nil, err
		}
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	err := s.w.Close()
	if s.r != s.w {
		err = errors.Join(err, s.r.Close())
	}
	return err
}

// DB returns the underlying write handle, for backups and maintenance.
func (s *Store) DB() *sql.DB { return s.w }

func toNanos(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.UnixNano(), Valid: true}
}

func fromNanos(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return time.Unix(0, n.Int64).UTC()
}

func (s *Store) Append(ctx context.Context, c *epoch.Commit, cond epoch.AppendCondition) error {
	var seq, version int64
	var t time.Time
	err := s.inTx(ctx, func(tx *sql.Tx) (err error) {
		seq, version, t, err = s.appendTx(ctx, tx, c, cond)
		return err
	})
	if err != nil {
		return err
	}
	c.Seq, c.Time, c.Version = seq, time.Unix(0, t.UnixNano()).UTC(), version
	return nil
}

func (s *Store) appendTx(ctx context.Context, tx *sql.Tx, c *epoch.Commit, cond epoch.AppendCondition) (seq, version int64, t time.Time, err error) {
	// The writer pool has one connection, so these reuse the statements
	// prepared on it instead of parsing again.
	stmt := func(st *sql.Stmt) *sql.Stmt { return tx.StmtContext(ctx, st) }
	ok := c.Branch == epoch.Main
	if !ok {
		var one int
		err = stmt(s.st.branchExists).QueryRowContext(ctx, c.Branch).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
		ok = one == 1
	}
	if err != nil {
		return
	}
	if !ok {
		err = fmt.Errorf("%w: %q", epoch.ErrBranchNotFound, c.Branch)
		return
	}

	cur := cond.BaseVersion
	err = stmt(s.st.streamVersion).QueryRowContext(ctx, c.Branch, c.Model, c.Stream).Scan(&cur)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return
	}
	if cond.ExpectedVersion != epoch.Any && cond.ExpectedVersion != cur {
		err = fmt.Errorf("%w: %s/%s is at version %d, expected %d", epoch.ErrConflict, c.Model, c.Stream, cur, cond.ExpectedVersion)
		return
	}

	t = c.Time
	if t.Before(cond.MinTime) {
		t = cond.MinTime
	}
	var last sql.NullInt64
	err = stmt(s.st.lastTime).QueryRowContext(ctx, c.Branch).Scan(&last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return
	}
	if prev := fromNanos(last); last.Valid && t.Before(prev) {
		t = prev
	}

	events, err := json.Marshal(c.Events)
	if err != nil {
		return
	}
	var cmdID, cmdType sql.NullString
	var cmdData []byte
	if c.Command != nil {
		cmdID = sql.NullString{String: c.Command.ID, Valid: c.Command.ID != ""}
		cmdType = sql.NullString{String: c.Command.Type, Valid: true}
		cmdData = c.Command.Data
	}
	version = cur + int64(len(c.Events))
	res, err := stmt(s.st.insert).ExecContext(ctx,
		c.Branch, t.UnixNano(), c.Model, c.Stream, version, cmdID, cmdType, cmdData, events, c.Rejected, c.Origin)
	if err != nil {
		return
	}
	seq, err = res.LastInsertId()
	return
}

const commitColumns = `seq, branch, time, model, stream, version, command_id, command_type, command_data, events, rejected, origin`

func (s *Store) Read(ctx context.Context, q epoch.Query) ([]epoch.Commit, error) {
	var where []string
	var args []any
	add := func(cond string, arg any) {
		where = append(where, cond)
		args = append(args, arg)
	}
	add("branch = ?", q.Branch)
	if q.Model != "" {
		add("model = ?", q.Model)
	}
	if q.Stream != "" {
		add("stream = ?", q.Stream)
	}
	if q.CommandID != "" {
		add("command_id = ?", q.CommandID)
	}
	if q.AfterSeq > 0 {
		add("seq > ?", q.AfterSeq)
	}
	if q.MaxSeq > 0 {
		add("seq <= ?", q.MaxSeq)
	}
	query := `SELECT ` + commitColumns + ` FROM epoch_commits WHERE ` + strings.Join(where, " AND ") + ` ORDER BY seq`
	if q.Reverse {
		query += ` DESC`
	}
	if q.Limit > 0 {
		query += fmt.Sprintf(` LIMIT %d`, q.Limit)
	}

	rows, err := s.rd().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []epoch.Commit{}
	for rows.Next() {
		var c epoch.Commit
		var t int64
		var cmdID, cmdType sql.NullString
		var cmdData, events []byte
		if err := rows.Scan(&c.Seq, &c.Branch, &t, &c.Model, &c.Stream, &c.Version, &cmdID, &cmdType, &cmdData, &events, &c.Rejected, &c.Origin); err != nil {
			return nil, err
		}
		c.Time = time.Unix(0, t).UTC()
		if cmdType.Valid {
			c.Command = &epoch.CommandData{ID: cmdID.String, Type: cmdType.String, Data: cmdData}
		}
		if len(events) > 0 && string(events) != "null" {
			if err := json.Unmarshal(events, &c.Events); err != nil {
				return nil, fmt.Errorf("sqlitestore: commit %d: %w", c.Seq, err)
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) SeqAt(ctx context.Context, branch string, t time.Time, maxSeq int64) (int64, error) {
	query := `SELECT seq FROM epoch_commits WHERE branch = ? AND time <= ?`
	args := []any{branch, t.UnixNano()}
	if maxSeq > 0 {
		query += ` AND seq <= ?`
		args = append(args, maxSeq)
	}
	query += ` ORDER BY time DESC, seq DESC LIMIT 1`
	var seq int64
	err := s.rd().QueryRowContext(ctx, query, args...).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return seq, err
}

func (s *Store) CreateBranch(ctx context.Context, b epoch.Branch) error {
	if b.Name == epoch.Main {
		return fmt.Errorf("%w: %q", epoch.ErrBranchExists, b.Name)
	}
	res, err := s.wr().ExecContext(ctx, `
		INSERT INTO epoch_branches (name, parent, fork_seq, fork_time, created, kind, description)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (name) DO NOTHING`,
		b.Name, b.Parent, b.ForkSeq, toNanos(b.ForkTime), toNanos(b.Created), b.Kind, b.Description)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return fmt.Errorf("%w: %q", epoch.ErrBranchExists, b.Name)
	}
	return nil
}

const branchColumns = `name, parent, fork_seq, fork_time, created, kind, description`

func scanBranch(sc interface{ Scan(...any) error }) (epoch.Branch, error) {
	var b epoch.Branch
	var forkTime, created sql.NullInt64
	err := sc.Scan(&b.Name, &b.Parent, &b.ForkSeq, &forkTime, &created, &b.Kind, &b.Description)
	b.ForkTime, b.Created = fromNanos(forkTime), fromNanos(created)
	return b, err
}

func (s *Store) GetBranch(ctx context.Context, name string) (epoch.Branch, error) {
	b, err := scanBranch(s.rd().QueryRowContext(ctx, `SELECT `+branchColumns+` FROM epoch_branches WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return epoch.Branch{}, fmt.Errorf("%w: %q", epoch.ErrBranchNotFound, name)
	}
	return b, err
}

func (s *Store) ListBranches(ctx context.Context) ([]epoch.Branch, error) {
	rows, err := s.rd().QueryContext(ctx, `SELECT `+branchColumns+` FROM epoch_branches ORDER BY created, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []epoch.Branch{}
	for rows.Next() {
		b, err := scanBranch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) DeleteBranch(ctx context.Context, name string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM epoch_branches WHERE name = ?`, name)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return fmt.Errorf("%w: %q", epoch.ErrBranchNotFound, name)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM epoch_commits WHERE branch = ?`, name); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM epoch_snapshots WHERE branch = ?`, name)
		return err
	})
}

func (s *Store) SaveSnapshot(ctx context.Context, snap epoch.Snapshot) error {
	// Check and write in one statement, so a snapshot can never outlive a
	// concurrently deleted branch and attach to a re-created one.
	res, err := s.wr().ExecContext(ctx, `
		INSERT OR REPLACE INTO epoch_snapshots (branch, model, stream, key, seq, version, state)
		SELECT ?, ?, ?, ?, ?, ?, ?
		WHERE ? = ? OR EXISTS (SELECT 1 FROM epoch_branches WHERE name = ?)`,
		snap.Branch, snap.Model, snap.Stream, snap.Key, snap.Seq, snap.Version, []byte(snap.State),
		snap.Branch, epoch.Main, snap.Branch)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return fmt.Errorf("%w: %q", epoch.ErrBranchNotFound, snap.Branch)
	}
	return nil
}

func (s *Store) LoadSnapshot(ctx context.Context, branch, model, stream, key string, maxSeq int64) (*epoch.Snapshot, error) {
	query := `SELECT seq, version, state FROM epoch_snapshots WHERE branch = ? AND model = ? AND stream = ? AND key = ?`
	args := []any{branch, model, stream, key}
	if maxSeq > 0 {
		query += ` AND seq <= ?`
		args = append(args, maxSeq)
	}
	query += ` ORDER BY seq DESC LIMIT 1`
	snap := epoch.Snapshot{Branch: branch, Model: model, Stream: stream, Key: key}
	var state []byte
	err := s.rd().QueryRowContext(ctx, query, args...).Scan(&snap.Seq, &snap.Version, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	snap.State = state
	return &snap, nil
}
