package records

import (
	"log/slog"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"sync"
)

// Saver persists a State, typically by writing the managed hosts file. Save
// must leave the previous persisted state in place when it fails.
type Saver interface {
	Save(State) error
}

// Store is the one in-memory State both APIs read and write. Writes are
// serialised by a mutex and reach memory only after the Saver has persisted
// them; reads return copies. A Store is safe for concurrent use.
type Store struct {
	mu     sync.Mutex
	state  State
	policy Policy
	saver  Saver
}

// NewStore returns a Store holding a copy of initial, validating writes with
// policy and persisting them with saver. The initial state is taken as
// already persisted and is not checked or saved.
func NewStore(initial State, policy Policy, saver Saver) *Store {
	return &Store{state: initial.Clone(), policy: policy, saver: saver}
}

// Policy returns the policy the store validates writes with, so handlers can
// normalise names and parse values the same way before calling Update.
func (s *Store) Policy() Policy {
	return s.policy
}

// State returns a copy of the current state.
func (s *Store) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Clone()
}

// Update runs fn on a draft of the current state under the lock. If fn
// returns an error, or the result fails Policy.Check, or saving fails,
// nothing changes and that error is returned unwrapped. A result equal to the
// current state is not saved. before and after are clones; on success with no
// change they are equal.
//
// fn runs while the store is locked, so it must not call the Store.
func (s *Store) Update(fn func(d *Draft) error) (before, after State, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	d := s.state.Edit()
	if err := fn(d); err != nil {
		return State{}, State{}, err
	}
	next := d.State()
	if err := s.policy.Check(next); err != nil {
		return State{}, State{}, err
	}
	if next.Equal(s.state) {
		return s.state.Clone(), s.state.Clone(), nil
	}
	if err := s.saver.Save(next.Clone()); err != nil {
		return State{}, State{}, err
	}
	before, s.state = s.state, next
	return before.Clone(), next.Clone(), nil
}

// LogChanges logs at info every difference between before and after, each
// line carrying listener and remote (who made the change) and name:
//
//   - "record changed", one per published record whose values differ, in
//     Diff order, with type, and before and after as space-joined addresses;
//   - "source changed", one per name present in both states whose source
//     differs, in name order, with before and after the two sources;
//   - "ownership changed", one per TXT added, removed or changed, in name
//     order, with before and after as space-joined targets.
//
// An absent side is "". Nothing is written when the states are equal. The
// other messages never contain "record changed", so a search for it finds
// only changes to what dnsmasq serves.
func LogChanges(logger *slog.Logger, listener, remote string, before, after State) {
	for _, c := range Diff(before, after) {
		logger.Info("record changed",
			slog.String("listener", listener),
			slog.String("remote", remote),
			slog.String("name", c.Name),
			slog.String("type", string(c.Type)),
			slog.String("before", joinAddrs(c.Before)),
			slog.String("after", joinAddrs(c.After)),
		)
	}
	for _, c := range sourceChanges(before, after) {
		c.log(logger, "source changed", listener, remote)
	}
	for _, c := range txtChanges(before, after) {
		c.log(logger, "ownership changed", listener, remote)
	}
}

// textChange is one name's before and after values as log text.
type textChange struct {
	name, before, after string
}

// log writes c at info as msg with listener, remote, name, before and after.
func (c textChange) log(logger *slog.Logger, msg, listener, remote string) {
	logger.Info(msg,
		slog.String("listener", listener),
		slog.String("remote", remote),
		slog.String("name", c.name),
		slog.String("before", c.before),
		slog.String("after", c.after),
	)
}

// sourceChanges returns, in name order, each name present in both states
// whose source differs.
func sourceChanges(before, after State) []textChange {
	sources := func(s State) map[string]Source {
		m := make(map[string]Source, len(s.Records))
		for _, r := range s.Records {
			m[r.Name] = r.Source
		}
		return m
	}
	b, a := sources(before), sources(after)
	var out []textChange
	for _, name := range slices.Sorted(maps.Keys(b)) {
		if as, ok := a[name]; ok && as != b[name] {
			out = append(out, textChange{name: name, before: string(b[name]), after: string(as)})
		}
	}
	return out
}

// txtChanges returns, in name order, each TXT added, removed, or whose
// targets or owner differ, with its targets space-joined.
func txtChanges(before, after State) []textChange {
	index := func(s State) map[string]TXT {
		m := make(map[string]TXT, len(s.TXTs))
		for _, t := range s.TXTs {
			m[t.Name] = t
		}
		return m
	}
	b, a := index(before), index(after)
	names := slices.Sorted(maps.Keys(b))
	for name := range a {
		if _, ok := b[name]; !ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	var out []textChange
	for _, name := range names {
		bt, bok := b[name]
		at, aok := a[name]
		if bok && aok && bt.OwnedRecord == at.OwnedRecord && slices.Equal(bt.Targets, at.Targets) {
			continue
		}
		out = append(out, textChange{name: name, before: strings.Join(bt.Targets, " "), after: strings.Join(at.Targets, " ")})
	}
	return out
}

// joinAddrs returns addrs as one space-separated string.
func joinAddrs(addrs []netip.Addr) string {
	s := make([]string, len(addrs))
	for i, a := range addrs {
		s[i] = a.String()
	}
	return strings.Join(s, " ")
}
