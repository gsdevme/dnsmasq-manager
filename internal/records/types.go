// Package records holds the DNS records dnsmasq-manager serves: their types,
// the canonical State both APIs share, the Draft used to edit it, and the
// Policy that validates names, addresses and TXT ownership records.
//
// A State is a value. Callers never edit one in place: they call Edit, change
// the Draft, and take the canonical result from Draft.State.
package records

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"
)

// ErrInvalid is wrapped by every validation error, so callers can map any
// refusal of bad input to one response with errors.Is.
var ErrInvalid = errors.New("invalid")

// ErrConflict is wrapped by refusals to change a name another source owns.
var ErrConflict = errors.New("conflict")

// Type is a published record type. Only A and AAAA exist.
type Type string

// The supported record types.
const (
	TypeA    Type = "A"
	TypeAAAA Type = "AAAA"
)

// ParseType returns the Type spelled exactly s. Any other spelling,
// including lowercase, is an error wrapping ErrInvalid.
func ParseType(s string) (Type, error) {
	switch t := Type(s); t {
	case TypeA, TypeAAAA:
		return t, nil
	default:
		return "", fmt.Errorf("%w: unsupported record type %q", ErrInvalid, s)
	}
}

// Source says who created a name and so who may change it.
type Source string

// The known sources.
const (
	SourceAPI         Source = "api"
	SourceExternalDNS Source = "external-dns"
)

// Record is the addresses published for one name and type.
type Record struct {
	Name   string
	Type   Type
	Values []netip.Addr
	Source Source
}

// TXT is an external-dns ownership record. It is kept as state and never
// published. Targets are kept byte for byte; OwnedRecord is the
// ownedRecord label external-dns attached, lowercase without a trailing dot
// so that it compares equal to a record name, or empty.
type TXT struct {
	Name        string
	Targets     []string
	OwnedRecord string
}

// State is the full set of managed records.
//
// A canonical State has Records sorted by Name (byte order) then Type (A
// before AAAA), one per (Name, Type), Values sorted and unique, and every
// record of a name sharing one Source; and TXTs sorted by Name, one per Name.
// Draft.State always returns a canonical State; Policy.Check verifies one.
type State struct {
	Records []Record
	TXTs    []TXT
}

// Clone returns a deep copy of s that shares no memory with it.
func (s State) Clone() State {
	var c State
	if s.Records != nil {
		c.Records = make([]Record, len(s.Records))
		for i, r := range s.Records {
			r.Values = slices.Clone(r.Values)
			c.Records[i] = r
		}
	}
	if s.TXTs != nil {
		c.TXTs = make([]TXT, len(s.TXTs))
		for i, t := range s.TXTs {
			t.Targets = slices.Clone(t.Targets)
			c.TXTs[i] = t
		}
	}
	return c
}

// Equal reports whether s and o hold the same records and TXTs in the same
// order. A nil slice equals an empty one.
func (s State) Equal(o State) bool {
	return slices.EqualFunc(s.Records, o.Records, func(a, b Record) bool {
		return a.Name == b.Name && a.Type == b.Type && a.Source == b.Source && slices.Equal(a.Values, b.Values)
	}) && slices.EqualFunc(s.TXTs, o.TXTs, func(a, b TXT) bool {
		return a.Name == b.Name && a.OwnedRecord == b.OwnedRecord && slices.Equal(a.Targets, b.Targets)
	})
}

// Source returns the source of name, and false when s has no record for it.
func (s State) Source(name string) (Source, bool) {
	for _, r := range s.Records {
		if r.Name == name {
			return r.Source, true
		}
	}
	return "", false
}

// Edit returns a Draft holding a copy of s. Changing the Draft never changes s.
func (s State) Edit() *Draft {
	d := &Draft{names: map[string]*entry{}, txts: map[string]TXT{}}
	for _, r := range s.Records {
		d.Set(r.Name, r.Type, r.Values, r.Source)
	}
	for _, t := range s.TXTs {
		d.SetTXT(t)
	}
	return d
}

// entry is the records of one name in a Draft.
type entry struct {
	source Source
	values map[Type][]netip.Addr
}

// Draft is a mutable copy of a State, made by State.Edit. It does not
// validate: callers check names and values with a Policy before setting them.
// A Draft is not safe for concurrent use.
type Draft struct {
	names map[string]*entry
	txts  map[string]TXT
}

// Source returns the source of name, and false when the draft has no record
// for it.
func (d *Draft) Source(name string) (Source, bool) {
	e, ok := d.names[name]
	if !ok {
		return "", false
	}
	return e.source, true
}

// Set replaces the values of (name, t) with a copy of values and sets the
// name's source to src, which also applies to the name's record of the other
// type. Setting no values is the same as Delete.
func (d *Draft) Set(name string, t Type, values []netip.Addr, src Source) {
	if len(values) == 0 {
		d.Delete(name, t)
		return
	}
	e, ok := d.names[name]
	if !ok {
		e = &entry{values: map[Type][]netip.Addr{}}
		d.names[name] = e
	}
	e.source = src
	e.values[t] = slices.Clone(values)
}

// Delete removes the record (name, t). When it was the name's last record the
// name and its source are forgotten. Deleting an absent record does nothing.
func (d *Draft) Delete(name string, t Type) {
	e, ok := d.names[name]
	if !ok {
		return
	}
	delete(e.values, t)
	if len(e.values) == 0 {
		delete(d.names, name)
	}
}

// DeleteName removes every record of name and forgets its source.
func (d *Draft) DeleteName(name string) {
	delete(d.names, name)
}

// SetTXT stores a copy of t, replacing any TXT with the same name.
func (d *Draft) SetTXT(t TXT) {
	t.Targets = slices.Clone(t.Targets)
	d.txts[t.Name] = t
}

// TXT returns a copy of the TXT called name, and false when there is none.
func (d *Draft) TXT(name string) (TXT, bool) {
	t, ok := d.txts[name]
	t.Targets = slices.Clone(t.Targets)
	return t, ok
}

// DeleteTXT removes the TXT called name, if there is one.
func (d *Draft) DeleteTXT(name string) {
	delete(d.txts, name)
}

// DeleteTXTOwnedBy removes every TXT whose OwnedRecord is recordName. Both
// are compared byte for byte, so recordName must be a normalised name and
// OwnedRecord stored normalised, as TXT documents.
func (d *Draft) DeleteTXTOwnedBy(recordName string) {
	maps.DeleteFunc(d.txts, func(_ string, t TXT) bool {
		return t.OwnedRecord == recordName
	})
}

// State returns the draft as a canonical State, sorting and deduplicating
// each record's values. The result shares no memory with the draft.
func (d *Draft) State() State {
	var s State
	for _, name := range slices.Sorted(maps.Keys(d.names)) {
		e := d.names[name]
		for _, t := range []Type{TypeA, TypeAAAA} {
			values, ok := e.values[t]
			if !ok {
				continue
			}
			values = slices.Clone(values)
			slices.SortFunc(values, netip.Addr.Compare)
			s.Records = append(s.Records, Record{Name: name, Type: t, Values: slices.Compact(values), Source: e.source})
		}
	}
	for _, name := range slices.Sorted(maps.Keys(d.txts)) {
		t := d.txts[name]
		t.Targets = slices.Clone(t.Targets)
		s.TXTs = append(s.TXTs, t)
	}
	return s
}

// Change is the difference in one published record between two states.
// Before is nil when the record was absent and After is nil when it is
// removed.
type Change struct {
	Name          string
	Type          Type
	Before, After []netip.Addr
}

// Diff returns the A and AAAA records whose values differ between before and
// after, sorted by Name then Type. TXTs and source-only changes are not
// published, so they yield no Change.
func Diff(before, after State) []Change {
	type key struct {
		name string
		typ  Type
	}
	index := func(s State) map[key][]netip.Addr {
		m := make(map[key][]netip.Addr, len(s.Records))
		for _, r := range s.Records {
			m[key{r.Name, r.Type}] = r.Values
		}
		return m
	}
	b, a := index(before), index(after)

	var changes []Change
	for k, bv := range b {
		av, ok := a[k]
		if !ok {
			changes = append(changes, Change{Name: k.name, Type: k.typ, Before: slices.Clone(bv)})
		} else if !slices.Equal(bv, av) {
			changes = append(changes, Change{Name: k.name, Type: k.typ, Before: slices.Clone(bv), After: slices.Clone(av)})
		}
	}
	for k, av := range a {
		if _, ok := b[k]; !ok {
			changes = append(changes, Change{Name: k.name, Type: k.typ, After: slices.Clone(av)})
		}
	}
	slices.SortFunc(changes, func(x, y Change) int {
		return cmp.Or(strings.Compare(x.Name, y.Name), compareType(x.Type, y.Type))
	})
	return changes
}

// compareType orders A before AAAA.
func compareType(a, b Type) int {
	return strings.Compare(string(a), string(b))
}
