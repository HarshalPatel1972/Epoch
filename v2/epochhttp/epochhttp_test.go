package epochhttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/HarshalPatel1972/epoch/v2"
	"github.com/HarshalPatel1972/epoch/v2/epochhttp"
	"github.com/HarshalPatel1972/epoch/v2/memstore"
)

type counter struct{ N int }

type (
	add   struct{ N int }
	added struct{ N int }
)

var errTooBig = errors.New("too big")

func counterModel(limit int) *epoch.Model[counter] {
	return &epoch.Model[counter]{
		Name:     "counter",
		Commands: []any{add{}},
		Events:   []any{added{}},
		Evolve:   func(s counter, e any) counter { s.N += e.(added).N; return s },
		Decide: func(s counter, c any, _ time.Time) ([]any, error) {
			if c.(add).N > limit {
				return nil, errTooBig
			}
			return []any{added(c.(add))}, nil
		},
	}
}

var total = &epoch.Projection[map[string]int]{
	Name: "total",
	Init: func() map[string]int { return map[string]int{} },
	Event: func(v map[string]int, r epoch.Record) map[string]int {
		e, _ := epoch.As[added](r)
		v["sum"] += e.N
		return v
	},
	Rejected: func(v map[string]int, _ epoch.Rejection) map[string]int {
		v["rejected"]++
		return v
	},
}

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func setup(t *testing.T, allowWrites bool) *httptest.Server {
	t.Helper()
	s := memstore.New()
	m := counterModel(5)
	for i, n := range []int{1, 2, 9, 3} {
		_, err := m.Handle(context.Background(), s, "c1", add{N: n}, epoch.WithTime(t0.AddDate(0, 0, i)))
		if err != nil && !errors.Is(err, epoch.ErrRejected) {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(http.StripPrefix("/epoch", epochhttp.New(epochhttp.Config{
		Store:       s,
		Models:      []epoch.AnyModel{m},
		Projections: []epoch.AnyProjection{total},
		Policies:    map[string]epoch.AnyModel{"limit 10": counterModel(10)},
		AllowWrites: allowWrites,
	})))
	t.Cleanup(srv.Close)
	return srv
}

func call(t *testing.T, srv *httptest.Server, method, path, body string, header map[string]string, wantStatus int, out any) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, srv.URL+"/epoch"+path, r)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode != wantStatus {
		t.Fatalf("%s %s: status %d, want %d: %s", method, path, res.StatusCode, wantStatus, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, data)
		}
	}
}

var jsonCT = map[string]string{"Content-Type": "application/json"}

func TestStudioPage(t *testing.T) {
	srv := setup(t, false)
	res, err := http.Get(srv.URL + "/epoch/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.Contains(string(body), "Epoch Studio") {
		t.Fatalf("studio: %d", res.StatusCode)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("missing CSP: %q", csp)
	}
}

func TestReadEndpoints(t *testing.T) {
	srv := setup(t, false)

	var info struct {
		Models, Projections []string
		AllowWrites         bool `json:"allow_writes"`
	}
	call(t, srv, "GET", "/api/info", "", nil, 200, &info)
	if len(info.Models) != 1 || info.Projections[0] != "total" || info.AllowWrites {
		t.Fatalf("info = %+v", info)
	}

	var log []epoch.Commit
	call(t, srv, "GET", "/api/log?limit=2", "", nil, 200, &log)
	if len(log) != 2 {
		t.Fatalf("log = %+v", log)
	}
	call(t, srv, "GET", "/api/log?at=2026-01-02", "", nil, 200, &log)
	if len(log) != 2 {
		t.Fatalf("log as of 2 Jan has %d commits, want 2", len(log))
	}

	var ent struct {
		State   epoch.State[counter] `json:"state"`
		History []epoch.Commit       `json:"history"`
	}
	call(t, srv, "GET", "/api/entities/counter/c1", "", nil, 200, &ent)
	if ent.State.Value.N != 6 || len(ent.History) != 4 || ent.History[2].Rejected == "" {
		t.Fatalf("entity = %+v", ent)
	}
	call(t, srv, "GET", "/api/entities/counter/c1?at=2026-01-01", "", nil, 200, &ent)
	if ent.State.Value.N != 1 {
		t.Fatalf("entity as of 1 Jan = %+v", ent.State)
	}

	var v map[string]int
	call(t, srv, "GET", "/api/views/total", "", nil, 200, &v)
	if v["sum"] != 6 || v["rejected"] != 1 {
		t.Fatalf("view = %v", v)
	}

	var tl struct {
		Commits int
		Buckets []struct{ Accepted, Rejected int }
	}
	call(t, srv, "GET", "/api/timeline?buckets=4", "", nil, 200, &tl)
	acc, rej := 0, 0
	for _, b := range tl.Buckets {
		acc, rej = acc+b.Accepted, rej+b.Rejected
	}
	if tl.Commits != 4 || len(tl.Buckets) != 4 || acc != 3 || rej != 1 {
		t.Fatalf("timeline = %+v", tl)
	}

	call(t, srv, "GET", "/api/views/nope", "", nil, 404, nil)
	call(t, srv, "GET", "/api/entities/nope/x", "", nil, 404, nil)
	call(t, srv, "GET", "/api/log?branch=nope", "", nil, 404, nil)
	call(t, srv, "GET", "/api/log?at=yesterday", "", nil, 400, nil)
	call(t, srv, "GET", "/api/replays/main", "", nil, 400, nil)
}

func TestWritesDisabledByDefault(t *testing.T) {
	srv := setup(t, false)
	call(t, srv, "POST", "/api/branches", `{"name":"x"}`, jsonCT, 403, nil)
	call(t, srv, "POST", "/api/replays", `{"name":"x","policies":["limit 10"]}`, jsonCT, 403, nil)
	call(t, srv, "DELETE", "/api/branches/x", "", map[string]string{"X-Epoch-Studio": "1"}, 403, nil)
}

func TestWritesRequireCSRFSafeRequests(t *testing.T) {
	srv := setup(t, true)
	// A form post is what a malicious page can send cross-site.
	call(t, srv, "POST", "/api/branches", `name=x`, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, 415, nil)
	call(t, srv, "POST", "/api/branches", `{"name":"x"}`, map[string]string{"Content-Type": "text/plain"}, 415, nil)
	call(t, srv, "POST", "/api/branches", `{"name":"x"}`, jsonCT, 201, nil)
	call(t, srv, "DELETE", "/api/branches/x", "", nil, 403, nil)
	call(t, srv, "DELETE", "/api/branches/x", "", map[string]string{"X-Epoch-Studio": "1"}, 204, nil)
	call(t, srv, "POST", "/api/branches", `{"name":"y","bogus":1}`, jsonCT, 400, nil)
	call(t, srv, "POST", "/api/branches", `{"name":"bad name"}`, jsonCT, 400, nil)
}

func TestForkReplayCompare(t *testing.T) {
	srv := setup(t, true)

	var b epoch.Branch
	call(t, srv, "POST", "/api/branches", `{"name":"f","at":"2026-01-02","description":"d"}`, jsonCT, 201, &b)
	if b.Parent != epoch.Main || b.Description != "d" {
		t.Fatalf("fork = %+v", b)
	}
	call(t, srv, "POST", "/api/branches", `{"name":"f"}`, jsonCT, 409, nil)

	var rep epoch.Report
	call(t, srv, "POST", "/api/replays", `{"name":"r","policies":["limit 10"]}`, jsonCT, 201, &rep)
	if rep.Commits != 4 || rep.NowAccepted != 1 {
		t.Fatalf("replay = %+v", rep)
	}
	call(t, srv, "POST", "/api/replays", `{"name":"r2","policies":["nope"]}`, jsonCT, 400, nil)
	call(t, srv, "POST", "/api/replays", `{"name":"r3","policies":[]}`, jsonCT, 400, nil)

	var stored epoch.Report
	call(t, srv, "GET", "/api/replays/r", "", nil, 200, &stored)
	if stored.Diverged != 1 || stored.Branch.Kind != epoch.KindReplay {
		t.Fatalf("stored report = %+v", stored)
	}

	var cmp struct {
		Changes []epoch.Change `json:"changes"`
	}
	call(t, srv, "GET", "/api/compare/total?a=main&b=r", "", nil, 200, &cmp)
	if len(cmp.Changes) != 2 || cmp.Changes[0].Path != "rejected" || cmp.Changes[1].Path != "sum" {
		t.Fatalf("compare = %+v", cmp.Changes)
	}

	var branches []struct {
		Name string `json:"name"`
		Head int64  `json:"head"`
	}
	call(t, srv, "GET", "/api/branches", "", nil, 200, &branches)
	if len(branches) != 3 || branches[0].Name != epoch.Main || branches[0].Head == 0 {
		t.Fatalf("branches = %+v", branches)
	}
	call(t, srv, "DELETE", "/api/branches/main", "", map[string]string{"X-Epoch-Studio": "1"}, 400, nil)
}
