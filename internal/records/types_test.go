package records_test

import (
	"errors"
	"net/netip"
	"reflect"
	"testing"

	"github.com/gsdevme/dnsmasq-manager/internal/records"
)

func TestParseType(t *testing.T) {
	tests := []struct {
		in      string
		want    records.Type
		wantErr bool
	}{
		{in: "A", want: records.TypeA},
		{in: "AAAA", want: records.TypeAAAA},
		{in: "a", wantErr: true},
		{in: "CNAME", wantErr: true},
		{in: "TXT", wantErr: true},
		{in: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := records.ParseType(tt.in)
			if tt.wantErr {
				if !errors.Is(err, records.ErrInvalid) {
					t.Fatalf("ParseType(%q) = %q, %v; want ErrInvalid", tt.in, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseType(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseType(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDraft(t *testing.T) {
	t.Run("last source wins across a name", func(t *testing.T) {
		d := records.State{}.Edit()
		d.Set("app.home.example.test", records.TypeA, addrs(t, "10.0.0.80"), records.SourceAPI)
		d.Set("app.home.example.test", records.TypeAAAA, addrs(t, "fd00:10::80"), records.SourceExternalDNS)

		got := d.State()
		want := records.State{Records: []records.Record{
			{Name: "app.home.example.test", Type: records.TypeA, Values: addrs(t, "10.0.0.80"), Source: records.SourceExternalDNS},
			{Name: "app.home.example.test", Type: records.TypeAAAA, Values: addrs(t, "fd00:10::80"), Source: records.SourceExternalDNS},
		}}
		if !got.Equal(want) {
			t.Errorf("State() = %+v, want %+v", got, want)
		}
		if src, ok := d.Source("app.home.example.test"); !ok || src != records.SourceExternalDNS {
			t.Errorf("Source = %q, %v; want external-dns, true", src, ok)
		}
	})

	t.Run("deleting the last record forgets the name", func(t *testing.T) {
		d := records.State{}.Edit()
		d.Set("app.home.example.test", records.TypeA, addrs(t, "10.0.0.80"), records.SourceAPI)
		d.Set("app.home.example.test", records.TypeAAAA, addrs(t, "fd00:10::80"), records.SourceAPI)
		d.Delete("app.home.example.test", records.TypeA)
		if _, ok := d.Source("app.home.example.test"); !ok {
			t.Fatal("Source after deleting A: name forgotten, want kept")
		}
		d.Delete("app.home.example.test", records.TypeAAAA)
		if src, ok := d.Source("app.home.example.test"); ok {
			t.Errorf("Source after deleting both = %q, true; want false", src)
		}
		d.Delete("absent.home.example.test", records.TypeA)
		s := d.State()
		if len(s.Records) != 0 {
			t.Errorf("Records = %+v, want none", s.Records)
		}
		if _, ok := s.Source("app.home.example.test"); ok {
			t.Error("State.Source: name present, want forgotten")
		}
	})

	t.Run("setting no values deletes", func(t *testing.T) {
		d := records.State{}.Edit()
		d.Set("app.home.example.test", records.TypeA, addrs(t, "10.0.0.80"), records.SourceAPI)
		d.Set("app.home.example.test", records.TypeA, nil, records.SourceAPI)
		if _, ok := d.Source("app.home.example.test"); ok {
			t.Error("Source: name present, want forgotten")
		}
	})

	t.Run("DeleteName removes both types", func(t *testing.T) {
		d := records.State{}.Edit()
		d.Set("app.home.example.test", records.TypeA, addrs(t, "10.0.0.80"), records.SourceAPI)
		d.Set("app.home.example.test", records.TypeAAAA, addrs(t, "fd00:10::80"), records.SourceAPI)
		d.Set("web.home.example.test", records.TypeA, addrs(t, "10.0.0.90"), records.SourceAPI)
		d.DeleteName("app.home.example.test")
		d.DeleteName("absent.home.example.test")
		want := records.State{Records: []records.Record{
			{Name: "web.home.example.test", Type: records.TypeA, Values: addrs(t, "10.0.0.90"), Source: records.SourceAPI},
		}}
		if got := d.State(); !got.Equal(want) {
			t.Errorf("State() = %+v, want %+v", got, want)
		}
	})

	t.Run("TXT set, replace and delete", func(t *testing.T) {
		d := records.State{}.Edit()
		d.SetTXT(records.TXT{Name: "a-app.home.example.test", Targets: []string{"old"}, OwnedRecord: "app.home.example.test"})
		d.SetTXT(records.TXT{Name: "a-app.home.example.test", Targets: []string{"new"}, OwnedRecord: "app.home.example.test"})
		d.SetTXT(records.TXT{Name: "aaaa-app.home.example.test", Targets: []string{"t"}, OwnedRecord: "app.home.example.test"})
		d.SetTXT(records.TXT{Name: "a-web.home.example.test", Targets: []string{"t"}, OwnedRecord: "web.home.example.test"})
		d.SetTXT(records.TXT{Name: "app.home.example.test", Targets: []string{"t"}})
		d.SetTXT(records.TXT{Name: "gone.home.example.test", Targets: []string{"t"}})
		d.DeleteTXT("gone.home.example.test")
		d.DeleteTXT("absent.home.example.test")
		d.DeleteTXTOwnedBy("app.home.example.test")

		want := []records.TXT{
			{Name: "a-web.home.example.test", Targets: []string{"t"}, OwnedRecord: "web.home.example.test"},
			{Name: "app.home.example.test", Targets: []string{"t"}},
		}
		if got := d.State().TXTs; !reflect.DeepEqual(got, want) {
			t.Errorf("TXTs = %+v, want %+v", got, want)
		}
	})

	t.Run("TXT lookup", func(t *testing.T) {
		d := records.State{}.Edit()
		d.SetTXT(records.TXT{Name: "a-app.home.example.test", Targets: []string{"t"}, OwnedRecord: "app.home.example.test"})

		got, ok := d.TXT("a-app.home.example.test")
		want := records.TXT{Name: "a-app.home.example.test", Targets: []string{"t"}, OwnedRecord: "app.home.example.test"}
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("TXT(a-app) = %+v, %v, want %+v, true", got, ok, want)
		}
		got.Targets[0] = "mutated"
		if again, _ := d.TXT("a-app.home.example.test"); again.Targets[0] != "t" {
			t.Errorf("TXT result aliased the draft: %+v", again)
		}
		if got, ok := d.TXT("absent.home.example.test"); ok || !reflect.DeepEqual(got, records.TXT{}) {
			t.Errorf("TXT(absent) = %+v, %v, want zero, false", got, ok)
		}
	})

	t.Run("State is canonical", func(t *testing.T) {
		d := records.State{}.Edit()
		d.Set("web.home.example.test", records.TypeA, addrs(t, "10.0.0.91", "10.0.0.90", "10.0.0.91"), records.SourceAPI)
		d.Set("app.home.example.test", records.TypeAAAA, addrs(t, "fd00:10::81", "fd00:10::80"), records.SourceAPI)
		d.Set("app.home.example.test", records.TypeA, addrs(t, "10.0.0.80"), records.SourceAPI)
		d.SetTXT(records.TXT{Name: "z.home.example.test", Targets: []string{"t"}})
		d.SetTXT(records.TXT{Name: "b.home.example.test", Targets: []string{"t"}})

		got := d.State()
		want := records.State{
			Records: []records.Record{
				{Name: "app.home.example.test", Type: records.TypeA, Values: addrs(t, "10.0.0.80"), Source: records.SourceAPI},
				{Name: "app.home.example.test", Type: records.TypeAAAA, Values: addrs(t, "fd00:10::80", "fd00:10::81"), Source: records.SourceAPI},
				{Name: "web.home.example.test", Type: records.TypeA, Values: addrs(t, "10.0.0.90", "10.0.0.91"), Source: records.SourceAPI},
			},
			TXTs: []records.TXT{
				{Name: "b.home.example.test", Targets: []string{"t"}},
				{Name: "z.home.example.test", Targets: []string{"t"}},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("State() = %+v, want %+v", got, want)
		}
		if err := testPolicy().Check(got); err != nil {
			t.Errorf("Check(State()): %v", err)
		}
	})

	t.Run("editing does not change the origin", func(t *testing.T) {
		orig := records.State{
			Records: []records.Record{
				{Name: "app.home.example.test", Type: records.TypeA, Values: addrs(t, "10.0.0.80"), Source: records.SourceAPI},
			},
			TXTs: []records.TXT{{Name: "a-app.home.example.test", Targets: []string{"t"}, OwnedRecord: "app.home.example.test"}},
		}
		snapshot := orig.Clone()

		values := addrs(t, "10.0.0.81")
		d := orig.Edit()
		d.Set("app.home.example.test", records.TypeA, values, records.SourceExternalDNS)
		d.Set("new.home.example.test", records.TypeA, addrs(t, "10.0.0.82"), records.SourceAPI)
		d.SetTXT(records.TXT{Name: "a-app.home.example.test", Targets: []string{"changed"}})
		values[0] = netip.MustParseAddr("10.0.0.99")

		if !orig.Equal(snapshot) {
			t.Errorf("origin changed to %+v, want %+v", orig, snapshot)
		}
		got := d.State()
		if v := got.Records[0].Values[0]; v != netip.MustParseAddr("10.0.0.81") {
			t.Errorf("draft aliased caller's slice: value = %v, want 10.0.0.81", v)
		}
		got.Records[0].Values[0] = netip.MustParseAddr("10.0.0.98")
		got.TXTs[0].Targets[0] = "mutated"
		if again := d.State(); again.Records[0].Values[0] != netip.MustParseAddr("10.0.0.81") || again.TXTs[0].Targets[0] != "changed" {
			t.Errorf("State() output aliased the draft: %+v", again)
		}
	})
}

func TestStateCloneEqualSource(t *testing.T) {
	s := records.State{
		Records: []records.Record{
			{Name: "app.home.example.test", Type: records.TypeA, Values: addrs(t, "10.0.0.80"), Source: records.SourceAPI},
			{Name: "web.home.example.test", Type: records.TypeA, Values: addrs(t, "10.0.0.90"), Source: records.SourceExternalDNS},
		},
		TXTs: []records.TXT{{Name: "a-web.home.example.test", Targets: []string{"t"}}},
	}

	c := s.Clone()
	if !c.Equal(s) {
		t.Fatalf("Clone() = %+v, not Equal to %+v", c, s)
	}
	c.Records[0].Values[0] = netip.MustParseAddr("10.0.0.81")
	c.TXTs[0].Targets[0] = "u"
	if c.Equal(s) {
		t.Error("Equal after changing the clone: true, want false")
	}
	if s.Records[0].Values[0] != netip.MustParseAddr("10.0.0.80") || s.TXTs[0].Targets[0] != "t" {
		t.Errorf("Clone shares memory with the original: %+v", s)
	}

	other := s.Clone()
	other.Records[1].Source = records.SourceAPI
	if other.Equal(s) {
		t.Error("Equal with a different source: true, want false")
	}
	other = s.Clone()
	other.TXTs[0].OwnedRecord = "web.home.example.test"
	if other.Equal(s) {
		t.Error("Equal with a different OwnedRecord: true, want false")
	}
	if !(records.State{}).Equal(records.State{Records: []records.Record{}, TXTs: []records.TXT{}}) {
		t.Error("nil and empty states: not Equal, want Equal")
	}

	if src, ok := s.Source("web.home.example.test"); !ok || src != records.SourceExternalDNS {
		t.Errorf("Source(web) = %q, %v; want external-dns, true", src, ok)
	}
	if src, ok := s.Source("absent.home.example.test"); ok {
		t.Errorf("Source(absent) = %q, true; want false", src)
	}
}

func TestDiff(t *testing.T) {
	rec := func(name string, typ records.Type, src records.Source, values ...string) records.Record {
		return records.Record{Name: name, Type: typ, Values: addrs(t, values...), Source: src}
	}
	before := records.State{
		Records: []records.Record{
			rec("changed.home.example.test", records.TypeA, records.SourceAPI, "10.0.0.1"),
			rec("removed.home.example.test", records.TypeAAAA, records.SourceAPI, "fd00:10::1"),
			rec("same.home.example.test", records.TypeA, records.SourceAPI, "10.0.0.3"),
			rec("source.home.example.test", records.TypeA, records.SourceAPI, "10.0.0.4"),
		},
		TXTs: []records.TXT{{Name: "txt.home.example.test", Targets: []string{"old"}}},
	}
	after := records.State{
		Records: []records.Record{
			rec("added.home.example.test", records.TypeA, records.SourceAPI, "10.0.0.5"),
			rec("added.home.example.test", records.TypeAAAA, records.SourceAPI, "fd00:10::5"),
			rec("changed.home.example.test", records.TypeA, records.SourceAPI, "10.0.0.1", "10.0.0.2"),
			rec("same.home.example.test", records.TypeA, records.SourceAPI, "10.0.0.3"),
			rec("source.home.example.test", records.TypeA, records.SourceExternalDNS, "10.0.0.4"),
		},
		TXTs: []records.TXT{{Name: "txt.home.example.test", Targets: []string{"new"}}},
	}

	got := records.Diff(before, after)
	want := []records.Change{
		{Name: "added.home.example.test", Type: records.TypeA, After: addrs(t, "10.0.0.5")},
		{Name: "added.home.example.test", Type: records.TypeAAAA, After: addrs(t, "fd00:10::5")},
		{Name: "changed.home.example.test", Type: records.TypeA, Before: addrs(t, "10.0.0.1"), After: addrs(t, "10.0.0.1", "10.0.0.2")},
		{Name: "removed.home.example.test", Type: records.TypeAAAA, Before: addrs(t, "fd00:10::1")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Diff = %+v, want %+v", got, want)
	}

	if got := records.Diff(before, before.Clone()); len(got) != 0 {
		t.Errorf("Diff of equal states = %+v, want none", got)
	}
}
