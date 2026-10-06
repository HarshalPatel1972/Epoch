// Package epochhttp serves Epoch Studio, a web UI for exploring history,
// branches and replays, and the JSON API behind it.
//
//	mux.Handle("/epoch/", http.StripPrefix("/epoch", epochhttp.New(epochhttp.Config{
//		Store:       store,
//		Models:      []epoch.AnyModel{Accounts},
//		Projections: []epoch.AnyProjection{Ledger},
//	})))
//
// Mount it at a path ending in "/" (Studio uses relative URLs). The handler
// has no authentication of its own. It exposes your application's
// full history, so mount it behind your existing auth, or only on an internal
// address. It is read-only unless Config.AllowWrites is set.
package epochhttp

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/HarshalPatel1972/epoch/v2"
)

//go:embed studio.html
var studioHTML []byte

// Config configures the handler.
type Config struct {
	Store epoch.Store

	// Models can be inspected entity by entity.
	Models []epoch.AnyModel

	// Projections can be viewed at any time on any branch, and compared
	// between branches.
	Projections []epoch.AnyProjection

	// Policies are alternative rules that Studio offers for replays, keyed by
	// a label shown to the user, e.g. "no overdraft". Each is a copy of one
	// of your Models with a different Decide.
	Policies map[string]epoch.AnyModel

	// AllowWrites enables creating and deleting branches and running
	// replays. Replays never modify the main branch, but they do write to
	// the store.
	AllowWrites bool

	// MaxTimelineCommits caps how many commits the activity chart reads.
	// 0 means 200,000.
	MaxTimelineCommits int
}

type handler struct {
	cfg         Config
	models      map[string]epoch.AnyModel
	projections map[string]epoch.AnyProjection
	mux         *http.ServeMux
}

// New returns the Studio handler. It panics if c.Store is nil or names are
// duplicated, since both are programming errors.
func New(c Config) http.Handler {
	if c.Store == nil {
		panic("epochhttp: Config.Store is nil")
	}
	if c.MaxTimelineCommits == 0 {
		c.MaxTimelineCommits = 200_000
	}
	h := &handler{cfg: c, models: map[string]epoch.AnyModel{}, projections: map[string]epoch.AnyProjection{}, mux: http.NewServeMux()}
	for _, m := range c.Models {
		if _, dup := h.models[m.ModelName()]; dup {
			panic("epochhttp: duplicate model " + m.ModelName())
		}
		h.models[m.ModelName()] = m
	}
	for _, p := range c.Projections {
		if _, dup := h.projections[p.ProjectionName()]; dup {
			panic("epochhttp: duplicate projection " + p.ProjectionName())
		}
		h.projections[p.ProjectionName()] = p
	}

	h.mux.HandleFunc("GET /{$}", h.studio)
	h.mux.HandleFunc("GET /api/info", handle(h.info))
	h.mux.HandleFunc("GET /api/branches", handle(h.branches))
	h.mux.HandleFunc("POST /api/branches", h.write(h.fork))
	h.mux.HandleFunc("DELETE /api/branches/{name}", h.write(h.deleteBranch))
	h.mux.HandleFunc("GET /api/log", handle(h.log))
	h.mux.HandleFunc("GET /api/timeline", handle(h.timeline))
	h.mux.HandleFunc("GET /api/entities/{model}/{id}", handle(h.entity))
	h.mux.HandleFunc("GET /api/views/{name}", handle(h.view))
	h.mux.HandleFunc("GET /api/compare/{name}", handle(h.compare))
	h.mux.HandleFunc("POST /api/replays", h.write(h.replay))
	h.mux.HandleFunc("GET /api/replays/{name}", handle(h.replayReport))
	return h
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	h.mux.ServeHTTP(w, r)
}

func (h *handler) studio(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = w.Write(studioHTML)
}

// httpError carries a status code to writeError.
type httpError struct {
	status int
	msg    string
}

func (e httpError) Error() string { return e.msg }

func badRequest(format string, args ...any) error {
	return httpError{http.StatusBadRequest, fmt.Sprintf(format, args...)}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	var he httpError
	switch {
	case errors.As(err, &he):
		status = he.status
	case errors.Is(err, epoch.ErrBranchNotFound):
		status = http.StatusNotFound
	case errors.Is(err, epoch.ErrBranchExists), errors.Is(err, epoch.ErrBranchHasChildren), errors.Is(err, epoch.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, epoch.ErrInvalidBranchName), errors.Is(err, epoch.ErrUnknownType):
		status = http.StatusBadRequest
	case errors.Is(err, context.Canceled):
		status = 499
	}
	msg := err.Error()
	if status == http.StatusInternalServerError {
		msg = "internal error: " + msg
	}
	writeJSON(w, status, map[string]string{"error": msg})
}

// write guards state-changing endpoints. They must be enabled, and requests
// must be ones a browser cannot send cross-site without a CORS preflight
// (which this handler never approves): JSON bodies, or a custom header.
func (h *handler) write(fn func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.cfg.AllowWrites {
			writeError(w, httpError{http.StatusForbidden, "writes are disabled (epochhttp.Config.AllowWrites)"})
			return
		}
		if r.Method == http.MethodDelete {
			// A custom header cannot be sent cross-site without a preflight.
			if r.Header.Get("X-Epoch-Studio") == "" {
				writeError(w, httpError{http.StatusForbidden, "DELETE requires the X-Epoch-Studio header"})
				return
			}
		} else if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
			writeError(w, httpError{http.StatusUnsupportedMediaType, "send a JSON body with Content-Type: application/json"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := fn(w, r); err != nil {
			writeError(w, err)
		}
	}
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return badRequest("invalid JSON body: %v", err)
	}
	return nil
}

// parseTime accepts RFC 3339 timestamps and plain dates (end of that day).
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t.Add(24*time.Hour - time.Nanosecond), nil
	}
	return time.Time{}, badRequest("invalid time %q: use RFC 3339 (2026-03-01T12:00:00Z) or a date (2026-03-01)", s)
}

func parseInt(s, name string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, badRequest("invalid %s %q", name, s)
	}
	return n, nil
}

// readOptions turns ?branch=, ?at= and ?seq= into epoch options.
func readOptions(r *http.Request) ([]epoch.Option, error) {
	q := r.URL.Query()
	opts := []epoch.Option{}
	if b := q.Get("branch"); b != "" {
		opts = append(opts, epoch.On(b))
	}
	at, err := parseTime(q.Get("at"))
	if err != nil {
		return nil, err
	}
	if !at.IsZero() {
		opts = append(opts, epoch.AsOf(at))
	}
	seq, err := parseInt(q.Get("seq"), "seq")
	if err != nil {
		return nil, err
	}
	if seq > 0 {
		opts = append(opts, epoch.AtSeq(seq))
	}
	return opts, nil
}

// handle adapts an endpoint that returns an error.
func handle(fn func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			writeError(w, err)
		}
	}
}

func (h *handler) info(w http.ResponseWriter, _ *http.Request) error {
	type policy struct {
		Label string `json:"label"`
		Model string `json:"model"`
	}
	out := struct {
		Models      []string `json:"models"`
		Projections []string `json:"projections"`
		Policies    []policy `json:"policies"`
		AllowWrites bool     `json:"allow_writes"`
	}{Models: []string{}, Projections: []string{}, Policies: []policy{}, AllowWrites: h.cfg.AllowWrites}
	for _, m := range h.cfg.Models {
		out.Models = append(out.Models, m.ModelName())
	}
	for _, p := range h.cfg.Projections {
		out.Projections = append(out.Projections, p.ProjectionName())
	}
	for label, m := range h.cfg.Policies {
		out.Policies = append(out.Policies, policy{label, m.ModelName()})
	}
	slices.SortFunc(out.Policies, func(a, b policy) int { return strings.Compare(a.Label, b.Label) })
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (h *handler) branches(w http.ResponseWriter, r *http.Request) error {
	bs, err := epoch.ListBranches(r.Context(), h.cfg.Store)
	if err != nil {
		return err
	}
	type branch struct {
		epoch.Branch
		Head int64 `json:"head"`
	}
	out := make([]branch, 0, len(bs))
	for _, b := range bs {
		head, err := epoch.Head(r.Context(), h.cfg.Store, b.Name)
		if err != nil {
			return err
		}
		out = append(out, branch{b, head})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (h *handler) fork(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name        string `json:"name"`
		From        string `json:"from"`
		At          string `json:"at"`
		Seq         int64  `json:"seq"`
		Description string `json:"description"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	at, err := parseTime(req.At)
	if err != nil {
		return err
	}
	b, err := epoch.Fork(r.Context(), h.cfg.Store, req.Name, epoch.ForkOptions{From: req.From, At: at, Seq: req.Seq, Description: req.Description})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, b)
	return nil
}

func (h *handler) deleteBranch(w http.ResponseWriter, r *http.Request) error {
	if err := epoch.DeleteBranch(r.Context(), h.cfg.Store, r.PathValue("name")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *handler) log(w http.ResponseWriter, r *http.Request) error {
	opts, err := readOptions(r)
	if err != nil {
		return err
	}
	after, err := parseInt(r.URL.Query().Get("after"), "after")
	if err != nil {
		return err
	}
	limit, err := parseInt(r.URL.Query().Get("limit"), "limit")
	if err != nil {
		return err
	}
	if limit == 0 || limit > 1000 {
		limit = 100
	}
	cs, err := epoch.Log(r.Context(), h.cfg.Store, after, int(limit), opts...)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, cs)
	return nil
}

// timeline buckets a branch's commits by time for the activity chart.
func (h *handler) timeline(w http.ResponseWriter, r *http.Request) error {
	opts, err := readOptions(r)
	if err != nil {
		return err
	}
	n, err := parseInt(r.URL.Query().Get("buckets"), "buckets")
	if err != nil {
		return err
	}
	if n == 0 || n > 500 {
		n = 90
	}
	var commits []epoch.Commit
	truncated := false
	for after := int64(0); ; {
		page, err := epoch.Log(r.Context(), h.cfg.Store, after, 5000, opts...)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			break
		}
		commits = append(commits, page...)
		after = page[len(page)-1].Seq
		if len(commits) >= h.cfg.MaxTimelineCommits {
			truncated = true
			break
		}
	}
	type bucket struct {
		Start    time.Time `json:"start"`
		Accepted int       `json:"accepted"`
		Rejected int       `json:"rejected"`
	}
	out := struct {
		First     time.Time `json:"first"`
		Last      time.Time `json:"last"`
		Commits   int       `json:"commits"`
		Buckets   []bucket  `json:"buckets"`
		Truncated bool      `json:"truncated,omitempty"`
	}{Buckets: []bucket{}, Commits: len(commits), Truncated: truncated}
	if len(commits) > 0 {
		out.First, out.Last = commits[0].Time, commits[len(commits)-1].Time
		span := out.Last.Sub(out.First)
		width := max(span/time.Duration(n)+1, time.Second)
		out.Buckets = make([]bucket, n)
		for i := range out.Buckets {
			out.Buckets[i].Start = out.First.Add(time.Duration(i) * width)
		}
		for _, c := range commits {
			i := min(int(c.Time.Sub(out.First)/width), int(n)-1)
			if c.Rejected != "" {
				out.Buckets[i].Rejected++
			} else {
				out.Buckets[i].Accepted++
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (h *handler) entity(w http.ResponseWriter, r *http.Request) error {
	m, ok := h.models[r.PathValue("model")]
	if !ok {
		return httpError{http.StatusNotFound, fmt.Sprintf("unknown model %q", r.PathValue("model"))}
	}
	opts, err := readOptions(r)
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	st, err := m.LoadAny(r.Context(), h.cfg.Store, id, opts...)
	if err != nil {
		return err
	}
	hist, err := m.History(r.Context(), h.cfg.Store, id, opts...)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": st, "history": hist})
	return nil
}

func (h *handler) projection(name string) (epoch.AnyProjection, error) {
	p, ok := h.projections[name]
	if !ok {
		return nil, httpError{http.StatusNotFound, fmt.Sprintf("unknown projection %q", name)}
	}
	return p, nil
}

func (h *handler) view(w http.ResponseWriter, r *http.Request) error {
	p, err := h.projection(r.PathValue("name"))
	if err != nil {
		return err
	}
	opts, err := readOptions(r)
	if err != nil {
		return err
	}
	v, err := p.GetAny(r.Context(), h.cfg.Store, opts...)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, v)
	return nil
}

func (h *handler) compare(w http.ResponseWriter, r *http.Request) error {
	p, err := h.projection(r.PathValue("name"))
	if err != nil {
		return err
	}
	q := r.URL.Query()
	a, b := q.Get("a"), q.Get("b")
	if a == "" {
		a = epoch.Main
	}
	if b == "" {
		return badRequest("missing branch b")
	}
	opts, err := readOptions(r)
	if err != nil {
		return err
	}
	va, err := p.GetAny(r.Context(), h.cfg.Store, append(opts, epoch.On(a))...)
	if err != nil {
		return err
	}
	vb, err := p.GetAny(r.Context(), h.cfg.Store, append(opts, epoch.On(b))...)
	if err != nil {
		return err
	}
	changes, err := epoch.Diff(va, vb)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"a": va, "b": vb, "changes": changes})
	return nil
}

func (h *handler) replay(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name        string   `json:"name"`
		Source      string   `json:"source"`
		From        string   `json:"from"`
		To          string   `json:"to"`
		Policies    []string `json:"policies"`
		Description string   `json:"description"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	from, err := parseTime(req.From)
	if err != nil {
		return err
	}
	to, err := parseTime(req.To)
	if err != nil {
		return err
	}
	if len(req.Policies) == 0 {
		return badRequest("choose at least one policy")
	}
	var models []epoch.AnyModel
	for _, label := range req.Policies {
		m, ok := h.cfg.Policies[label]
		if !ok {
			return badRequest("unknown policy %q", label)
		}
		models = append(models, m)
	}
	desc := req.Description
	if desc == "" {
		desc = "Replay with " + strings.Join(req.Policies, ", ")
	}
	rep, err := epoch.Replay(r.Context(), h.cfg.Store, epoch.ReplayOptions{
		Name: req.Name, Source: req.Source, From: from, To: to, Models: models, Description: desc, MaxDivergences: 500,
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, rep)
	return nil
}

func (h *handler) replayReport(w http.ResponseWriter, r *http.Request) error {
	b, err := epoch.GetBranch(r.Context(), h.cfg.Store, r.PathValue("name"))
	if err != nil {
		return err
	}
	if b.Kind != epoch.KindReplay {
		return badRequest("branch %q is not a replay", b.Name)
	}
	rep, err := epoch.ReplayReport(r.Context(), h.cfg.Store, b.Name, 500)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, rep)
	return nil
}
