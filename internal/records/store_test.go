package records_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/gsdevme/dnsmasq-manager/internal/records"
)

// fakeSaver records every state it is asked to save and fails while err is
// set. It is safe for concurrent use.
type fakeSaver struct {
	mu    sync.Mutex
	err   error
	saved []records.State
}

func (f *fakeSaver) Save(s records.State) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.saved = append(f.saved, s.Clone())
	return nil
}

func (f *fakeSaver) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeSaver) calls() []records.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.saved
}

func storePolicy() records.Policy {
	return records.Policy{
		Zones:    []string{"home.example.test"},
		Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24"), netip.MustParsePrefix("fd00:10::/64")},
	}
}

func mustAddrs(s ...string) []netip.Addr {
	out := make([]netip.Addr, len(s))
	for i, v := range s {
		out[i] = netip.MustParseAddr(v)
	}
	return out
}

func initialState() records.State {
	d := records.State{}.Edit()
	d.Set("nas.home.example.test", records.TypeA, mustAddrs("10.0.0.40"), records.SourceAPI)
	return d.State()
}

func setApp(d *records.Draft) error {
	d.Set("app.home.example.test", records.TypeA, mustAddrs("10.0.0.80"), records.SourceExternalDNS)
	return nil
}

func TestStoreUpdateSavesAndSwaps(t *testing.T) {
	saver := &fakeSaver{}
	s := records.NewStore(initialState(), storePolicy(), saver)

	before, after, err := s.Update(setApp)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !before.Equal(initialState()) {
		t.Errorf("before = %+v, want the initial state", before)
	}
	want := initialState().Edit()
	_ = setApp(want)
	if !after.Equal(want.State()) {
		t.Errorf("after = %+v, want %+v", after, want.State())
	}
	if got := s.State(); !got.Equal(after) {
		t.Errorf("State() = %+v, want %+v", got, after)
	}
	calls := saver.calls()
	if len(calls) != 1 || !calls[0].Equal(after) {
		t.Errorf("saver calls = %+v, want one call with %+v", calls, after)
	}
}

func TestStoreNoSaveWhenUnchanged(t *testing.T) {
	tests := []struct {
		name string
		fn   func(d *records.Draft) error
	}{
		{name: "no edit", fn: func(*records.Draft) error { return nil }},
		{name: "same values", fn: func(d *records.Draft) error {
			d.Set("nas.home.example.test", records.TypeA, mustAddrs("10.0.0.40"), records.SourceAPI)
			return nil
		}},
		{name: "delete absent", fn: func(d *records.Draft) error {
			d.Delete("absent.home.example.test", records.TypeA)
			return nil
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saver := &fakeSaver{}
			s := records.NewStore(initialState(), storePolicy(), saver)

			before, after, err := s.Update(tt.fn)
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			if !before.Equal(after) || !after.Equal(initialState()) {
				t.Errorf("before = %+v, after = %+v, want both the initial state", before, after)
			}
			if n := len(saver.calls()); n != 0 {
				t.Errorf("saver called %d times, want 0", n)
			}
		})
	}
}

func TestStoreFnErrorChangesNothing(t *testing.T) {
	saver := &fakeSaver{}
	s := records.NewStore(initialState(), storePolicy(), saver)
	wantErr := errors.New("refused")

	_, _, err := s.Update(func(d *records.Draft) error {
		_ = setApp(d)
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if got := s.State(); !got.Equal(initialState()) {
		t.Errorf("State() = %+v, want the initial state", got)
	}
	if n := len(saver.calls()); n != 0 {
		t.Errorf("saver called %d times, want 0", n)
	}
}

func TestStoreInvalidResultChangesNothing(t *testing.T) {
	saver := &fakeSaver{}
	s := records.NewStore(initialState(), storePolicy(), saver)

	_, _, err := s.Update(func(d *records.Draft) error {
		d.Set("app.other.example.test", records.TypeA, mustAddrs("10.0.0.80"), records.SourceAPI)
		return nil
	})
	if !errors.Is(err, records.ErrInvalid) {
		t.Fatalf("err = %v, want one wrapping ErrInvalid", err)
	}
	if got := s.State(); !got.Equal(initialState()) {
		t.Errorf("State() = %+v, want the initial state", got)
	}
	if n := len(saver.calls()); n != 0 {
		t.Errorf("saver called %d times, want 0", n)
	}
}

func TestStoreKeepsStateWhenSaveFails(t *testing.T) {
	saveErr := errors.New("hosts directory is gone")
	saver := &fakeSaver{err: saveErr}
	s := records.NewStore(initialState(), storePolicy(), saver)

	_, _, err := s.Update(setApp)
	if !errors.Is(err, saveErr) {
		t.Fatalf("err = %v, want %v", err, saveErr)
	}
	if got := s.State(); !got.Equal(initialState()) {
		t.Errorf("State() after failed save = %+v, want the initial state", got)
	}

	saver.setErr(nil)
	_, after, err := s.Update(setApp)
	if err != nil {
		t.Fatalf("Update after the saver recovered: %v", err)
	}
	if _, ok := after.Source("app.home.example.test"); !ok {
		t.Errorf("after = %+v, want app.home.example.test present", after)
	}
	if got := s.State(); !got.Equal(after) {
		t.Errorf("State() = %+v, want %+v", got, after)
	}
	if n := len(saver.calls()); n != 1 {
		t.Errorf("successful saves = %d, want 1", n)
	}
}

func TestStoreStateIsACopy(t *testing.T) {
	d := initialState().Edit()
	d.SetTXT(records.TXT{Name: "a-nas.home.example.test", Targets: []string{"heritage=external-dns"}, OwnedRecord: "nas.home.example.test"})
	initial := d.State()
	s := records.NewStore(initial, storePolicy(), &fakeSaver{})

	initial.Records[0].Values[0] = netip.MustParseAddr("10.0.0.99")

	got := s.State()
	got.Records[0].Values[0] = netip.MustParseAddr("10.0.0.98")
	got.Records[0].Name = "changed.home.example.test"
	got.TXTs[0].Targets[0] = "changed"

	before, after, err := s.Update(func(*records.Draft) error { return nil })
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	before.Records[0].Values[0] = netip.MustParseAddr("10.0.0.97")
	after.TXTs[0].Targets[0] = "changed again"

	again := s.State()
	if v := again.Records[0].Values[0]; v != netip.MustParseAddr("10.0.0.40") {
		t.Errorf("stored value = %v, want 10.0.0.40", v)
	}
	if n := again.Records[0].Name; n != "nas.home.example.test" {
		t.Errorf("stored name = %q, want nas.home.example.test", n)
	}
	if tg := again.TXTs[0].Targets[0]; tg != "heritage=external-dns" {
		t.Errorf("stored TXT target = %q, want heritage=external-dns", tg)
	}
}

func TestStoreConcurrentUpdates(t *testing.T) {
	const writers = 50
	saver := &fakeSaver{}
	s := records.NewStore(records.State{}, storePolicy(), saver)

	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Go(func() {
			name := fmt.Sprintf("host%d.home.example.test", i)
			_, _, err := s.Update(func(d *records.Draft) error {
				d.Set(name, records.TypeA, mustAddrs(fmt.Sprintf("10.0.0.%d", i+1)), records.SourceAPI)
				return nil
			})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("Update: %v", err)
		}
	}

	if n := len(s.State().Records); n != writers {
		t.Errorf("records = %d, want %d", n, writers)
	}
	calls := saver.calls()
	if len(calls) != writers {
		t.Fatalf("saver calls = %d, want %d", len(calls), writers)
	}
	for i, c := range calls {
		if n := len(c.Records); n != i+1 {
			t.Errorf("save %d held %d records, want %d", i, n, i+1)
		}
	}
}

func TestLogChanges(t *testing.T) {
	b := initialState().Edit()
	b.Set("app.home.example.test", records.TypeA, mustAddrs("10.0.0.80"), records.SourceExternalDNS)
	b.Set("old.home.example.test", records.TypeAAAA, mustAddrs("fd00:10::1"), records.SourceAPI)
	before := b.State()

	a := before.Edit()
	a.Set("app.home.example.test", records.TypeA, mustAddrs("10.0.0.81", "10.0.0.80"), records.SourceExternalDNS)
	a.DeleteName("old.home.example.test")
	a.Set("new.home.example.test", records.TypeA, mustAddrs("10.0.0.5"), records.SourceAPI)
	after := a.State()

	var buf bytes.Buffer
	records.LogChanges(timelessLogger(&buf), "api", "10.0.0.7", before, after)

	want := []string{
		`level=INFO msg="record changed" listener=api remote=10.0.0.7 name=app.home.example.test type=A before=10.0.0.80 after="10.0.0.80 10.0.0.81"`,
		`level=INFO msg="record changed" listener=api remote=10.0.0.7 name=new.home.example.test type=A before="" after=10.0.0.5`,
		`level=INFO msg="record changed" listener=api remote=10.0.0.7 name=old.home.example.test type=AAAA before=fd00:10::1 after=""`,
	}
	got := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("log lines = %q, want %d lines", got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %s\nwant      %s", i, got[i], want[i])
		}
	}
}

func TestLogChangesSourceAndOwnership(t *testing.T) {
	b := initialState().Edit()
	b.Set("app.home.example.test", records.TypeA, mustAddrs("10.0.0.80"), records.SourceExternalDNS)
	b.SetTXT(records.TXT{Name: "a-app.home.example.test", Targets: []string{"heritage=external-dns"}, OwnedRecord: "app.home.example.test"})
	b.SetTXT(records.TXT{Name: "a-web.home.example.test", Targets: []string{"old"}, OwnedRecord: "web.home.example.test"})
	before := b.State()

	a := before.Edit()
	a.Set("app.home.example.test", records.TypeA, mustAddrs("10.0.0.80"), records.SourceAPI)
	a.DeleteTXT("a-app.home.example.test")
	a.SetTXT(records.TXT{Name: "a-web.home.example.test", Targets: []string{"new", "two"}, OwnedRecord: "web.home.example.test"})
	a.SetTXT(records.TXT{Name: "a-new.home.example.test", Targets: []string{"t"}, OwnedRecord: "new.home.example.test"})
	after := a.State()

	var buf bytes.Buffer
	records.LogChanges(timelessLogger(&buf), "api", "10.0.0.7", before, after)

	want := []string{
		`level=INFO msg="source changed" listener=api remote=10.0.0.7 name=app.home.example.test before=external-dns after=api`,
		`level=INFO msg="ownership changed" listener=api remote=10.0.0.7 name=a-app.home.example.test before="heritage=external-dns" after=""`,
		`level=INFO msg="ownership changed" listener=api remote=10.0.0.7 name=a-new.home.example.test before="" after=t`,
		`level=INFO msg="ownership changed" listener=api remote=10.0.0.7 name=a-web.home.example.test before=old after="new two"`,
	}
	got := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("log lines = %q, want %d lines", got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %s\nwant      %s", i, got[i], want[i])
		}
	}
	for _, line := range got {
		if strings.Contains(line, "record changed") {
			t.Errorf("line %s contains \"record changed\", which the e2e test reads as a published change", line)
		}
	}
}

// timelessLogger returns a text logger writing to w without the time attribute.
func timelessLogger(w *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if len(groups) == 0 && attr.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return attr
		},
	}))
}

func TestLogChangesNothingWhenUnchanged(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	records.LogChanges(logger, "webhook", "10.0.0.7", initialState(), initialState())

	if buf.Len() != 0 {
		t.Errorf("log = %q, want empty", buf.String())
	}
}
