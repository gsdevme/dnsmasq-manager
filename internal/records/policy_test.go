package records_test

import (
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/gsdevme/dnsmasq-manager/internal/records"
)

// testPolicy is the policy every test in this package validates against.
func testPolicy() records.Policy {
	return records.Policy{
		Zones: []string{"home.example.test"},
		Prefixes: []netip.Prefix{
			netip.MustParsePrefix("10.0.0.0/8"),
			netip.MustParsePrefix("172.16.0.0/12"),
			netip.MustParsePrefix("192.168.0.0/16"),
			netip.MustParsePrefix("fd00:10::/64"),
		},
	}
}

// addrs parses each string as an address, failing the test on a bad one.
func addrs(t *testing.T, ss ...string) []netip.Addr {
	t.Helper()
	out := make([]netip.Addr, 0, len(ss))
	for _, s := range ss {
		a, err := netip.ParseAddr(s)
		if err != nil {
			t.Fatalf("ParseAddr(%q): %v", s, err)
		}
		out = append(out, a)
	}
	return out
}

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "lowercases and drops trailing dot", in: "App.Home.Example.Test.", want: "app.home.example.test"},
		{name: "deeper subdomain", in: "a.b.home.example.test", want: "a.b.home.example.test"},
		{name: "63-character label", in: strings.Repeat("a", 63) + ".home.example.test", want: strings.Repeat("a", 63) + ".home.example.test"},
		{name: "zone apex", in: "home.example.test", wantErr: true},
		{name: "wildcard", in: "*.home.example.test", wantErr: true},
		{name: "underscore", in: "_x.home.example.test", wantErr: true},
		{name: "leading hyphen", in: "-a.home.example.test", wantErr: true},
		{name: "trailing hyphen", in: "a-.home.example.test", wantErr: true},
		{name: "empty label", in: "a..home.example.test", wantErr: true},
		{name: "other zone", in: "app.other.test", wantErr: true},
		{name: "zone suffix without dot", in: "xhome.example.test", wantErr: true},
		{name: "two trailing dots", in: "app.home.example.test..", wantErr: true},
		{name: "64-character label", in: strings.Repeat("a", 64) + ".home.example.test", wantErr: true},
		{name: "254-character name", in: strings.Repeat(strings.Repeat("a", 49)+".", 4) + strings.Repeat("a", 36) + ".home.example.test", wantErr: true},
		{name: "empty", in: "", wantErr: true},
		{name: "non-ASCII", in: "ä.home.example.test", wantErr: true},
	}

	p := testPolicy()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.NormalizeName(tt.in)
			if tt.wantErr {
				if !errors.Is(err, records.ErrInvalid) {
					t.Fatalf("NormalizeName(%q) = %q, %v; want ErrInvalid", tt.in, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeName(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("NormalizeName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeNameLengthBoundary(t *testing.T) {
	p := testPolicy()
	name := strings.Repeat(strings.Repeat("a", 49)+".", 4) + strings.Repeat("a", 35) + ".home.example.test"
	if len(name) != 253 {
		t.Fatalf("test name is %d characters, want 253", len(name))
	}
	if _, err := p.NormalizeName(name); err != nil {
		t.Errorf("253-character name: %v", err)
	}
	if _, err := p.NormalizeName("a" + name); !errors.Is(err, records.ErrInvalid) {
		t.Errorf("254-character name: err = %v, want ErrInvalid", err)
	}
}

func TestValidLabel(t *testing.T) {
	for _, tt := range []struct {
		label string
		want  bool
	}{
		{"a", true},
		{"app-1", true},
		{strings.Repeat("a", 63), true},
		{"", false},
		{strings.Repeat("a", 64), false},
		{"-app", false},
		{"app-", false},
		{"App", false},
		{"a_b", false},
		{"*", false},
	} {
		if got := records.ValidLabel(tt.label); got != tt.want {
			t.Errorf("ValidLabel(%q) = %v, want %v", tt.label, got, tt.want)
		}
	}
}

func TestCheckHostName(t *testing.T) {
	label := strings.Repeat("a", 49)
	for _, tt := range []struct {
		name string
		ok   bool
	}{
		{"home.example.test", true},
		{"test", true},
		{strings.Repeat(label+".", 5) + "tes", true},
		{strings.Repeat(label+".", 5) + "test", false},
		{"", false},
		{"home..test", false},
		{"home.example.test.", false},
		{"Home.example.test", false},
		{"home_lan.example.test", false},
	} {
		err := records.CheckHostName(tt.name)
		if (err == nil) != tt.ok {
			t.Errorf("CheckHostName(%q) = %v, want ok %v", tt.name, err, tt.ok)
		}
	}
}

func TestParseValues(t *testing.T) {
	seventeen := make([]string, 17)
	for i := range seventeen {
		seventeen[i] = netip.AddrFrom4([4]byte{10, 0, 0, byte(i + 1)}).String()
	}

	tests := []struct {
		name    string
		typ     records.Type
		in      []string
		want    []string
		wantErr bool
	}{
		{name: "A sorted and deduplicated", typ: records.TypeA, in: []string{"10.0.0.81", "10.0.0.80", "10.0.0.80"}, want: []string{"10.0.0.80", "10.0.0.81"}},
		{name: "AAAA canonicalised", typ: records.TypeAAAA, in: []string{"fd00:10:0:0::80"}, want: []string{"fd00:10::80"}},
		{name: "sixteen values", typ: records.TypeA, in: seventeen[:16], want: seventeen[:16]},
		{name: "empty list", typ: records.TypeA, in: nil, wantErr: true},
		{name: "seventeen values", typ: records.TypeA, in: seventeen, wantErr: true},
		{name: "A outside prefixes", typ: records.TypeA, in: []string{"8.8.8.8"}, wantErr: true},
		{name: "A leading zeros", typ: records.TypeA, in: []string{"010.0.0.80"}, wantErr: true},
		{name: "A given IPv6", typ: records.TypeA, in: []string{"fd00:10::80"}, wantErr: true},
		{name: "AAAA given IPv4", typ: records.TypeAAAA, in: []string{"10.0.0.80"}, wantErr: true},
		{name: "AAAA IPv4-mapped", typ: records.TypeAAAA, in: []string{"::ffff:10.0.0.80"}, wantErr: true},
		{name: "AAAA zone suffix", typ: records.TypeAAAA, in: []string{"fe80::1%eth0"}, wantErr: true},
		{name: "AAAA outside prefixes", typ: records.TypeAAAA, in: []string{"2001:db8::1"}, wantErr: true},
		{name: "not an address", typ: records.TypeA, in: []string{"x"}, wantErr: true},
		{name: "unknown type", typ: records.Type("CNAME"), in: []string{"10.0.0.80"}, wantErr: true},
	}

	p := testPolicy()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.ParseValues(tt.typ, tt.in)
			if tt.wantErr {
				if !errors.Is(err, records.ErrInvalid) {
					t.Fatalf("ParseValues(%s, %q) = %v, %v; want ErrInvalid", tt.typ, tt.in, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseValues(%s, %q): %v", tt.typ, tt.in, err)
			}
			if want := addrs(t, tt.want...); !slices.Equal(got, want) {
				t.Errorf("ParseValues(%s, %q) = %v, want %v", tt.typ, tt.in, got, want)
			}
		})
	}
}

func TestCheckRecord(t *testing.T) {
	tests := []struct {
		name    string
		rec     records.Record
		wantErr bool
	}{
		{name: "valid A", rec: records.Record{Name: "app.home.example.test", Type: records.TypeA, Values: addrs(t, "10.0.0.80"), Source: records.SourceAPI}},
		{name: "valid AAAA", rec: records.Record{Name: "app.home.example.test", Type: records.TypeAAAA, Values: addrs(t, "fd00:10::80"), Source: records.SourceExternalDNS}},
		{name: "name not normal", rec: records.Record{Name: "App.home.example.test", Type: records.TypeA, Values: addrs(t, "10.0.0.80"), Source: records.SourceAPI}, wantErr: true},
		{name: "unknown type", rec: records.Record{Name: "app.home.example.test", Type: "CNAME", Values: addrs(t, "10.0.0.80"), Source: records.SourceAPI}, wantErr: true},
		{name: "no values", rec: records.Record{Name: "app.home.example.test", Type: records.TypeA, Source: records.SourceAPI}, wantErr: true},
		{name: "wrong family", rec: records.Record{Name: "app.home.example.test", Type: records.TypeA, Values: addrs(t, "fd00:10::80"), Source: records.SourceAPI}, wantErr: true},
		{name: "IPv4-mapped AAAA", rec: records.Record{Name: "app.home.example.test", Type: records.TypeAAAA, Values: addrs(t, "::ffff:10.0.0.80"), Source: records.SourceAPI}, wantErr: true},
		{name: "outside prefixes", rec: records.Record{Name: "app.home.example.test", Type: records.TypeA, Values: addrs(t, "8.8.8.8"), Source: records.SourceAPI}, wantErr: true},
		{name: "zero address", rec: records.Record{Name: "app.home.example.test", Type: records.TypeA, Values: []netip.Addr{{}}, Source: records.SourceAPI}, wantErr: true},
		{name: "unknown source", rec: records.Record{Name: "app.home.example.test", Type: records.TypeA, Values: addrs(t, "10.0.0.80"), Source: "manual"}, wantErr: true},
	}

	p := testPolicy()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := p.CheckRecord(tt.rec)
			if tt.wantErr {
				if !errors.Is(err, records.ErrInvalid) {
					t.Fatalf("CheckRecord = %v, want ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckRecord: %v", err)
			}
		})
	}

	t.Run("seventeen values", func(t *testing.T) {
		values := make([]netip.Addr, records.MaxValues+1)
		for i := range values {
			values[i] = netip.AddrFrom4([4]byte{10, 0, 0, byte(i + 1)})
		}
		rec := records.Record{Name: "app.home.example.test", Type: records.TypeA, Values: values, Source: records.SourceAPI}
		if err := p.CheckRecord(rec); !errors.Is(err, records.ErrInvalid) {
			t.Fatalf("CheckRecord = %v, want ErrInvalid", err)
		}
	})
}

func TestCheckTXT(t *testing.T) {
	target := `"heritage=external-dns,external-dns/owner=default"`
	tests := []struct {
		name    string
		txt     records.TXT
		wantErr bool
	}{
		{name: "typical", txt: records.TXT{Name: "a-app.home.example.test", Targets: []string{`"heritage=external-dns"`}}},
		{name: "underscore", txt: records.TXT{Name: "_a-app.home.example.test", Targets: []string{target}}},
		{name: "70-character first label", txt: records.TXT{Name: strings.Repeat("a", 70) + ".home.example.test", Targets: []string{target}}},
		{name: "owned record", txt: records.TXT{Name: "a-app.home.example.test", Targets: []string{target}, OwnedRecord: "app.home.example.test"}},
		{name: "four targets", txt: records.TXT{Name: "a-app.home.example.test", Targets: []string{"1", "2", "3", "4"}}},
		{name: "1024-byte target", txt: records.TXT{Name: "a-app.home.example.test", Targets: []string{strings.Repeat("x", 1024)}}},
		{name: "300 characters", txt: records.TXT{Name: strings.Repeat("a", 300-len(".home.example.test")) + ".home.example.test", Targets: []string{target}}},
		{name: "uppercase", txt: records.TXT{Name: "A-app.home.example.test", Targets: []string{target}}, wantErr: true},
		{name: "space in name", txt: records.TXT{Name: "a app.home.example.test", Targets: []string{target}}, wantErr: true},
		{name: "301 characters", txt: records.TXT{Name: strings.Repeat("a", 301-len(".home.example.test")) + ".home.example.test", Targets: []string{target}}, wantErr: true},
		{name: "zone apex", txt: records.TXT{Name: "home.example.test", Targets: []string{target}}, wantErr: true},
		{name: "other zone", txt: records.TXT{Name: "a-app.other.test", Targets: []string{target}}, wantErr: true},
		{name: "non-ASCII", txt: records.TXT{Name: "ä.home.example.test", Targets: []string{target}}, wantErr: true},
		{name: "zero targets", txt: records.TXT{Name: "a-app.home.example.test"}, wantErr: true},
		{name: "five targets", txt: records.TXT{Name: "a-app.home.example.test", Targets: []string{"1", "2", "3", "4", "5"}}, wantErr: true},
		{name: "1025-byte target", txt: records.TXT{Name: "a-app.home.example.test", Targets: []string{strings.Repeat("x", 1025)}}, wantErr: true},
		{name: "target not UTF-8", txt: records.TXT{Name: "a-app.home.example.test", Targets: []string{"\xff"}}, wantErr: true},
		{name: "owned record not UTF-8", txt: records.TXT{Name: "a-app.home.example.test", Targets: []string{target}, OwnedRecord: "\xff"}, wantErr: true},
		{name: "253-byte owned record", txt: records.TXT{Name: "a-app.home.example.test", Targets: []string{target}, OwnedRecord: strings.Repeat("o", 253)}},
		{name: "254-byte owned record", txt: records.TXT{Name: "a-app.home.example.test", Targets: []string{target}, OwnedRecord: strings.Repeat("o", 254)}, wantErr: true},
		{name: "1 MiB owned record", txt: records.TXT{Name: "a-app.home.example.test", Targets: []string{target}, OwnedRecord: strings.Repeat("o", 1<<20)}, wantErr: true},
	}

	p := testPolicy()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := p.CheckTXT(tt.txt)
			if tt.wantErr {
				if !errors.Is(err, records.ErrInvalid) {
					t.Fatalf("CheckTXT = %v, want ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckTXT: %v", err)
			}
		})
	}
}

func TestPolicyCheck(t *testing.T) {
	valid := func() records.State {
		return records.State{
			Records: []records.Record{
				{Name: "app.home.example.test", Type: records.TypeA, Values: addrs(t, "10.0.0.80", "10.0.0.81"), Source: records.SourceAPI},
				{Name: "app.home.example.test", Type: records.TypeAAAA, Values: addrs(t, "fd00:10::80"), Source: records.SourceAPI},
				{Name: "web.home.example.test", Type: records.TypeA, Values: addrs(t, "10.0.0.90"), Source: records.SourceExternalDNS},
			},
			TXTs: []records.TXT{
				{Name: "a-web.home.example.test", Targets: []string{`"heritage=external-dns"`}, OwnedRecord: "web.home.example.test"},
				{Name: "web.home.example.test", Targets: []string{`"heritage=external-dns"`}},
			},
		}
	}

	tests := []struct {
		name   string
		mutate func(s *records.State)
		ok     bool
	}{
		{name: "canonical", mutate: func(*records.State) {}, ok: true},
		{name: "empty", mutate: func(s *records.State) { *s = records.State{} }, ok: true},
		{name: "unsorted records", mutate: func(s *records.State) { s.Records[0], s.Records[2] = s.Records[2], s.Records[0] }},
		{name: "AAAA before A", mutate: func(s *records.State) { s.Records[0], s.Records[1] = s.Records[1], s.Records[0] }},
		{name: "duplicate name and type", mutate: func(s *records.State) { s.Records[1].Type = records.TypeA; s.Records[1].Values = addrs(t, "10.0.0.82") }},
		{name: "two sources under one name", mutate: func(s *records.State) { s.Records[1].Source = records.SourceExternalDNS }},
		{name: "out-of-prefix value", mutate: func(s *records.State) { s.Records[2].Values = addrs(t, "8.8.8.8") }},
		{name: "unsorted values", mutate: func(s *records.State) { s.Records[0].Values = addrs(t, "10.0.0.81", "10.0.0.80") }},
		{name: "duplicate values", mutate: func(s *records.State) { s.Records[0].Values = addrs(t, "10.0.0.80", "10.0.0.80") }},
		{name: "unsorted TXTs", mutate: func(s *records.State) { s.TXTs[0], s.TXTs[1] = s.TXTs[1], s.TXTs[0] }},
		{name: "duplicate TXT name", mutate: func(s *records.State) { s.TXTs[1].Name = s.TXTs[0].Name }},
		{name: "invalid TXT", mutate: func(s *records.State) { s.TXTs[0].Targets = nil }},
	}

	p := testPolicy()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := valid()
			tt.mutate(&s)
			err := p.Check(s)
			if tt.ok {
				if err != nil {
					t.Fatalf("Check: %v", err)
				}
				return
			}
			if !errors.Is(err, records.ErrInvalid) {
				t.Fatalf("Check = %v, want ErrInvalid", err)
			}
		})
	}
}
