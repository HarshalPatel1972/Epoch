package epoch

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// Named lets a command or event type choose the name it is recorded under.
// Without it, the Go type name is used (e.g. "Deposited"). Implement it when
// you rename a type but need old records to keep decoding, or when two
// packages use the same type name.
type Named interface {
	EpochType() string
}

// A Payload is anything carrying a recorded type name and JSON data:
// EventData, CommandData, Record and Rejection.
type Payload interface {
	PayloadType() string
	PayloadData() json.RawMessage
}

// As decodes p into T if p was recorded as type T. It reports false if the
// type names differ or the data does not decode into T.
//
//	if d, ok := epoch.As[Deposited](rec); ok { total += d.Amount }
func As[T any](p Payload) (T, bool) {
	var v T
	name, err := typeName(reflect.TypeFor[T]())
	if err != nil || p.PayloadType() != name {
		return v, false
	}
	if err := json.Unmarshal(p.PayloadData(), &v); err != nil {
		return v, false
	}
	return v, true
}

// TypeName returns the name v's type is recorded under.
func TypeName(v any) (string, error) {
	if v == nil {
		return "", fmt.Errorf("%w: nil value", ErrUnknownType)
	}
	return typeName(reflect.TypeOf(v))
}

var namedType = reflect.TypeFor[Named]()

func typeName(t reflect.Type) (string, error) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t.Implements(namedType):
		return reflect.Zero(t).Interface().(Named).EpochType(), nil
	case reflect.PointerTo(t).Implements(namedType):
		return reflect.New(t).Interface().(Named).EpochType(), nil
	case t.Name() == "":
		return "", fmt.Errorf("%w: %s has no name; use a named type", ErrUnknownType, t)
	}
	return t.Name(), nil
}

// registry maps recorded type names to Go types for one kind of payload.
type registry struct {
	kind   string
	byName map[string]reflect.Type
	byType map[reflect.Type]string
}

func newRegistry(kind string, samples []any) (*registry, error) {
	r := &registry{kind: kind, byName: map[string]reflect.Type{}, byType: map[reflect.Type]string{}}
	for _, s := range samples {
		if s == nil {
			return nil, fmt.Errorf("epoch: nil %s sample", kind)
		}
		t := reflect.TypeOf(s)
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		name, err := typeName(t)
		if err != nil {
			return nil, err
		}
		if prev, ok := r.byName[name]; ok && prev != t {
			return nil, fmt.Errorf("epoch: %s name %q is used by both %s and %s", kind, name, prev, t)
		}
		r.byName[name] = t
		r.byType[t] = name
	}
	return r, nil
}

func (r *registry) encode(v any) (string, json.RawMessage, error) {
	if v == nil {
		return "", nil, fmt.Errorf("%w: nil %s", ErrUnknownType, r.kind)
	}
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	name, ok := r.byType[t]
	if !ok {
		return "", nil, fmt.Errorf("%w: %s %s is not registered", ErrUnknownType, r.kind, t)
	}
	data, err := json.Marshal(v)
	if err != nil {
		return "", nil, fmt.Errorf("epoch: encoding %s %s: %w", r.kind, name, err)
	}
	return name, data, nil
}

// decode returns a value (not a pointer) of the registered type.
func (r *registry) decode(name string, data json.RawMessage) (any, error) {
	t, ok := r.byName[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s %q is not registered", ErrUnknownType, r.kind, name)
	}
	p := reflect.New(t)
	if len(data) > 0 {
		if err := json.Unmarshal(data, p.Interface()); err != nil {
			return nil, fmt.Errorf("epoch: decoding %s %s: %w", r.kind, name, err)
		}
	}
	return p.Elem().Interface(), nil
}
