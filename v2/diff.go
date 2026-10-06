package epoch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
)

// Change kinds.
const (
	Added    = "added"
	Removed  = "removed"
	Modified = "modified"
)

// A Change is one difference found by Diff.
type Change struct {
	// Path locates the value, e.g. `revenue.total` or `items[2].qty`. Keys
	// that are not identifiers are quoted: `byCountry["en-GB"]`.
	Path string `json:"path"`
	Kind string `json:"kind"`
	From any    `json:"from,omitempty"`
	To   any    `json:"to,omitempty"`
}

// Diff compares the JSON encodings of a and b and returns every difference,
// ordered by path. Object keys are compared by name and arrays by index.
// Numbers keep their exact JSON text, so From and To are json.Number values.
func Diff(a, b any) ([]Change, error) {
	av, err := toJSONValue(a)
	if err != nil {
		return nil, err
	}
	bv, err := toJSONValue(b)
	if err != nil {
		return nil, err
	}
	changes := []Change{}
	diffValues("", av, bv, &changes)
	return changes, nil
}

func toJSONValue(v any) (any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("epoch: diff: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var out any
	err = dec.Decode(&out)
	return out, err
}

func diffValues(path string, a, b any, out *[]Change) {
	switch av := a.(type) {
	case map[string]any:
		if bv, ok := b.(map[string]any); ok {
			keys := make([]string, 0, len(av)+len(bv))
			for k := range av {
				keys = append(keys, k)
			}
			for k := range bv {
				if _, ok := av[k]; !ok {
					keys = append(keys, k)
				}
			}
			slices.Sort(keys)
			for _, k := range keys {
				x, inA := av[k]
				y, inB := bv[k]
				p := joinKey(path, k)
				switch {
				case !inA:
					*out = append(*out, Change{Path: p, Kind: Added, To: y})
				case !inB:
					*out = append(*out, Change{Path: p, Kind: Removed, From: x})
				default:
					diffValues(p, x, y, out)
				}
			}
			return
		}
	case []any:
		if bv, ok := b.([]any); ok {
			for i := range max(len(av), len(bv)) {
				p := path + "[" + strconv.Itoa(i) + "]"
				switch {
				case i >= len(av):
					*out = append(*out, Change{Path: p, Kind: Added, To: bv[i]})
				case i >= len(bv):
					*out = append(*out, Change{Path: p, Kind: Removed, From: av[i]})
				default:
					diffValues(p, av[i], bv[i], out)
				}
			}
			return
		}
	}
	if !scalarEqual(a, b) {
		*out = append(*out, Change{Path: path, Kind: Modified, From: a, To: b})
	}
}

// scalarEqual compares leaves, and containers of different kinds (which are
// never equal).
func scalarEqual(a, b any) bool {
	switch av := a.(type) {
	case map[string]any, []any:
		return false
	case json.Number:
		bv, ok := b.(json.Number)
		if !ok {
			return false
		}
		if av == bv {
			return true
		}
		// 1 and 1.0 are the same number.
		x, err1 := av.Float64()
		y, err2 := bv.Float64()
		return err1 == nil && err2 == nil && x == y
	default:
		return a == b
	}
}

var identRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func joinKey(path, key string) string {
	if !identRE.MatchString(key) {
		return path + "[" + strconv.Quote(key) + "]"
	}
	if path == "" {
		return key
	}
	return path + "." + key
}
