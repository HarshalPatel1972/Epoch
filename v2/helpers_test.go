package epoch_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/HarshalPatel1972/epoch/v2"
)

func TestDiff(t *testing.T) {
	type inner struct {
		N  int      `json:"n"`
		Xs []string `json:"xs"`
	}
	type doc struct {
		A    int            `json:"a"`
		M    map[string]int `json:"m"`
		In   inner          `json:"in"`
		Opt  *int           `json:"opt,omitempty"`
		Rate float64        `json:"rate"`
		Any  map[string]any `json:"any"`
	}
	one := 1
	a := doc{A: 1, M: map[string]int{"x": 1, "en-GB": 2, "gone": 3}, In: inner{N: 1, Xs: []string{"a", "b"}}, Rate: 1, Any: map[string]any{"k": []any{1}}}
	b := doc{A: 1, M: map[string]int{"x": 2, "en-GB": 2, "new": 4}, In: inner{N: 1, Xs: []string{"a"}}, Opt: &one, Rate: 1.0, Any: map[string]any{"k": map[string]any{}}}

	got, err := epoch.Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`any.k modified [1] -> map[]`,
		`in.xs[1] removed b -> <nil>`,
		`m.gone removed 3 -> <nil>`,
		`m.new added <nil> -> 4`,
		`m.x modified 1 -> 2`,
		`opt added <nil> -> 1`,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d changes: %+v", len(got), got)
	}
	for i, c := range got {
		if s := fmt.Sprintf("%s %s %v -> %v", c.Path, c.Kind, c.From, c.To); s != want[i] {
			t.Errorf("change %d = %q, want %q", i, s, want[i])
		}
	}

	same, _ := epoch.Diff(a, a)
	if len(same) != 0 {
		t.Errorf("Diff(a, a) = %+v", same)
	}
	quoted, _ := epoch.Diff(map[string]int{"en-GB": 1}, map[string]int{"en-GB": 2})
	if quoted[0].Path != `["en-GB"]` {
		t.Errorf("quoted path = %q", quoted[0].Path)
	}
	root, _ := epoch.Diff(1, 2)
	if len(root) != 1 || root[0].Path != "" {
		t.Errorf("root diff = %+v", root)
	}
	numbers, _ := epoch.Diff(json.RawMessage(`{"n":1}`), json.RawMessage(`{"n":1.0}`))
	if len(numbers) != 0 {
		t.Errorf("1 and 1.0 should be equal: %+v", numbers)
	}
	if _, err := epoch.Diff(make(chan int), 1); err == nil {
		t.Errorf("Diff of an unencodable value succeeded")
	}
}

type renamed struct{ V int }

func (renamed) EpochType() string { return "legacy.v1" }

type ptrRenamed struct{ V int }

func (*ptrRenamed) EpochType() string { return "ptr.v1" }

func TestTypeNames(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want string
	}{
		{Deposited{}, "Deposited"},
		{&Deposited{}, "Deposited"},
		{renamed{}, "legacy.v1"},
		{ptrRenamed{}, "ptr.v1"},
	} {
		got, err := epoch.TypeName(tc.v)
		if err != nil || got != tc.want {
			t.Errorf("TypeName(%T) = %q, %v; want %q", tc.v, got, err, tc.want)
		}
	}
	for _, v := range []any{nil, struct{}{}, 3} {
		_, err := epoch.TypeName(v)
		if v == 3 {
			if err != nil {
				t.Errorf("TypeName(int) failed: %v", err) // named builtin types are allowed
			}
			continue
		}
		if !errors.Is(err, epoch.ErrUnknownType) {
			t.Errorf("TypeName(%#v): err = %v, want ErrUnknownType", v, err)
		}
	}
}

func TestAs(t *testing.T) {
	e := epoch.EventData{Type: "Deposited", Data: json.RawMessage(`{"Amount":5}`)}
	if d, ok := epoch.As[Deposited](e); !ok || d.Amount != 5 {
		t.Fatalf("As[Deposited] = %+v, %v", d, ok)
	}
	if _, ok := epoch.As[Withdrew](e); ok {
		t.Fatalf("As[Withdrew] matched a Deposited event")
	}
	bad := epoch.EventData{Type: "Deposited", Data: json.RawMessage(`{"Amount":"x"}`)}
	if _, ok := epoch.As[Deposited](bad); ok {
		t.Fatalf("As decoded invalid data")
	}
	r := epoch.EventData{Type: "legacy.v1", Data: json.RawMessage(`{"V":2}`)}
	if v, ok := epoch.As[renamed](r); !ok || v.V != 2 {
		t.Fatalf("As[renamed] = %+v, %v", v, ok)
	}
}

func TestModelValidation(t *testing.T) {
	ctx := context.Background()
	for name, m := range map[string]*epoch.Model[Account]{
		"no name":       {Evolve: Accounts.Evolve, Decide: Accounts.Decide},
		"no decide":     {Name: "x", Evolve: Accounts.Evolve},
		"dup names":     {Name: "x", Evolve: Accounts.Evolve, Decide: Accounts.Decide, Events: []any{Opened{}, renamed{}, struct{ renamed }{}}},
		"anonymous cmd": {Name: "x", Evolve: Accounts.Evolve, Decide: Accounts.Decide, Commands: []any{struct{ A int }{}}},
	} {
		if _, err := m.Load(ctx, nil, "id"); err == nil {
			t.Errorf("%s: Load succeeded", name)
		}
	}
}
