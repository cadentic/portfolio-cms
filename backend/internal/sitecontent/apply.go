package sitecontent

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// sectionField maps a section key to its field index in Content.
var sectionField = func() map[string]int {
	m := map[string]int{}
	t := reflect.TypeFor[Content]()
	for i := range t.NumField() {
		m[jsonKey(t.Field(i))] = i
	}
	return m
}()

// FromStored decodes a stored document over Default(), section by section
// (D4). Unknown keys are ignored and a section that is not an object keeps
// its default. Lists are never nil in the result.
func FromStored(raw []byte) (Content, error) {
	c := Default()
	if len(bytes.TrimSpace(raw)) == 0 {
		return c, nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return c, fmt.Errorf("site content: %w", err)
	}
	v := reflect.ValueOf(&c).Elem()
	for _, key := range Sections {
		b, ok := doc[key]
		if !ok {
			continue
		}
		sec := v.Field(sectionField[key])
		next := reflect.New(sec.Type())
		next.Elem().Set(sec)
		if _, err := merge(next.Elem(), b, false); err != nil {
			slog.Warn("site content: stored section ignored", "section", key, "err", err)
			continue
		}
		sec.Set(next.Elem())
	}
	fillNil(v)
	return c, nil
}

// ApplySections merges sections over a copy of base (D5): scalars present
// overwrite, a list or nested object present replaces the whole value, and
// sections left out are kept. Errors are per section; the result is not
// normalised.
func ApplySections(base Content, sections map[string]json.RawMessage) (Content, []FieldError) {
	out := clone(base)
	v := reflect.ValueOf(&out).Elem()
	var errs []FieldError
	keys := slices.Collect(maps.Keys(sections))
	slices.SortFunc(keys, func(a, b string) int { return cmp.Or(cmp.Compare(sectionOrder(a), sectionOrder(b)), cmp.Compare(a, b)) })
	for _, key := range keys {
		i, ok := sectionField[key]
		if !ok {
			errs = append(errs, FieldError{Message: fmt.Sprintf("unknown section %q", key)})
			continue
		}
		if fe, err := merge(v.Field(i), sections[key], true); err != nil {
			fe.Path = joinPath(key, fe.Path)
			errs = append(errs, fe)
		}
	}
	return out, errs
}

func sectionOrder(k string) int {
	if i, ok := sectionField[k]; ok {
		return i
	}
	return len(Sections)
}

func joinPath(a, b string) string {
	if b == "" {
		return a
	}
	return a + "." + b
}

var errMerge = errors.New("site content: section rejected")

// merge decodes one section object over sec. Top-level lists and nested
// objects whose keys are present are zeroed first, so a stored or patched
// list never decodes into existing elements. strict rejects unknown fields
// and reports type errors as a FieldError relative to the section.
func merge(sec reflect.Value, b json.RawMessage, strict bool) (FieldError, error) {
	b = bytes.TrimSpace(b)
	var present map[string]json.RawMessage
	if len(b) == 0 || b[0] != '{' || json.Unmarshal(b, &present) != nil {
		return FieldError{Message: "must be an object"}, errMerge
	}
	t := sec.Type()
	known := map[string]int{}
	for i := range t.NumField() {
		known[jsonKey(t.Field(i))] = i
	}
	if strict {
		for _, k := range slices.Sorted(maps.Keys(present)) {
			// Exact match: encoding/json would accept "Title" for "title".
			if _, ok := known[k]; !ok {
				return FieldError{Message: fmt.Sprintf("unknown field %q", k)}, errMerge
			}
		}
	}
	for k := range present {
		if i, ok := known[k]; ok {
			if f := sec.Field(i); f.Kind() == reflect.Slice || f.Kind() == reflect.Struct {
				f.Set(reflect.Zero(f.Type()))
			}
		}
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(sec.Addr().Interface()); err != nil {
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) {
			return FieldError{Path: te.Field, Message: typeMessage(te.Type)}, err
		}
		if msg, ok := strings.CutPrefix(err.Error(), "json: "); ok {
			return FieldError{Message: msg}, err
		}
		return FieldError{Message: "is not valid JSON"}, err
	}
	return FieldError{}, nil
}

func typeMessage(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Int, reflect.Int64, reflect.Int32:
		return "must be a whole number"
	case reflect.String:
		return "must be text"
	case reflect.Bool:
		return "must be true or false"
	case reflect.Slice:
		return "must be a list"
	case reflect.Struct:
		return "must be an object"
	}
	return "has the wrong type"
}

// Reset returns a copy of base with the given sections copied from Default().
func Reset(base Content, keys []string) (Content, error) {
	out := clone(base)
	def := Default()
	v, d := reflect.ValueOf(&out).Elem(), reflect.ValueOf(def)
	for _, k := range keys {
		i, ok := sectionField[k]
		if !ok {
			return base, &ValidationError{Errors: []FieldError{{Message: fmt.Sprintf("unknown section %q", k)}}}
		}
		v.Field(i).Set(d.Field(i))
	}
	return out, nil
}

// Changed lists the sections whose JSON differs, in Sections order. A nil
// list and an empty one compare equal. It is never nil.
func Changed(a, b Content) []string {
	a, b = clone(a), clone(b)
	av, bv := reflect.ValueOf(&a).Elem(), reflect.ValueOf(&b).Elem()
	fillNil(av)
	fillNil(bv)
	out := []string{}
	for _, k := range Sections {
		i := sectionField[k]
		x, _ := json.Marshal(av.Field(i).Interface())
		y, _ := json.Marshal(bv.Field(i).Interface())
		if !bytes.Equal(x, y) {
			out = append(out, k)
		}
	}
	return out
}

// clone deep-copies c so that callers never share list storage.
func clone(c Content) Content {
	b, err := json.Marshal(c)
	if err != nil {
		panic(err) // Content holds only strings, ints, bools, lists and structs
	}
	var out Content
	if err := json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return out
}
