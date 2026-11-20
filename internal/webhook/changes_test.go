package webhook_test

import (
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/gsdevme/dnsmasq-manager/internal/records"
	"github.com/gsdevme/dnsmasq-manager/internal/webhook"
)

// testPolicy returns the policy every webhook test uses: one zone, an IPv4
// and an IPv6 prefix.
func testPolicy() records.Policy {
	return records.Policy{
		Zones:    []string{"home.example.test"},
		Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24"), netip.MustParsePrefix("fd00:10::/64")},
	}
}

// addrs parses each of s as an address.
func addrs(s ...string) []netip.Addr {
	out := make([]netip.Addr, len(s))
	for i, v := range s {
		out[i] = netip.MustParseAddr(v)
	}
	return out
}

// ownership is the target external-dns writes in a TXT registry record,
// literal quotes included.
const ownership = `"heritage=external-dns,external-dns/owner=default,external-dns/resource=ingress/default/app"`

// apiState holds one name owned by the REST API and one by external-dns.
func apiState() records.State {
	return records.State{
		Records: []records.Record{
			{Name: "api.home.example.test", Type: records.TypeA, Values: addrs("10.0.0.10"), Source: records.SourceAPI},
			{Name: "app.home.example.test", Type: records.TypeA, Values: addrs("10.0.0.80"), Source: records.SourceExternalDNS},
		},
		TXTs: []records.TXT{
			{Name: "a-app.home.example.test", Targets: []string{ownership}, OwnedRecord: "app.home.example.test"},
		},
	}
}

// orphanState is apiState plus nas.home.example.test owned by the REST API
// and a TXT a-nas.home.example.test stored as owning it, the state an
// ownership TXT created before the REST API took the name would leave.
func orphanState() records.State {
	d := apiState().Edit()
	d.Set("nas.home.example.test", records.TypeA, addrs("10.0.0.40"), records.SourceAPI)
	d.SetTXT(records.TXT{Name: "a-nas.home.example.test", Targets: []string{ownership}, OwnedRecord: "nas.home.example.test"})
	return d.State()
}

// apply runs Apply on a draft of s and returns the resulting state.
func apply(t *testing.T, s records.State, c webhook.Changes) (records.State, error) {
	t.Helper()
	d := s.Edit()
	err := webhook.Apply(testPolicy(), d, c)
	return d.State(), err
}

func TestAdjustKeepsAndNormalises(t *testing.T) {
	in := []webhook.Endpoint{{
		DNSName:          "App.Home.Example.Test",
		Targets:          []string{"10.0.0.81", "10.0.0.80"},
		RecordType:       "A",
		RecordTTL:        300,
		Labels:           map[string]string{"owner": "default"},
		ProviderSpecific: []webhook.Property{{Name: "alias", Value: "false"}},
	}}
	kept, dropped := webhook.Adjust(testPolicy(), in)
	want := []webhook.Endpoint{{
		DNSName:    "app.home.example.test",
		Targets:    []string{"10.0.0.80", "10.0.0.81"},
		RecordType: "A",
		Labels:     map[string]string{"owner": "default"},
	}}
	if !reflect.DeepEqual(kept, want) {
		t.Errorf("kept = %+v, want %+v", kept, want)
	}
	if len(dropped) != 0 {
		t.Errorf("dropped = %q, want none", dropped)
	}
}

func TestAdjustCanonicalIPv6(t *testing.T) {
	in := []webhook.Endpoint{{
		DNSName:    "app.home.example.test",
		Targets:    []string{"fd00:10:0:0::80", "FD00:10::81", "fd00:10::80"},
		RecordType: "AAAA",
	}}
	kept, dropped := webhook.Adjust(testPolicy(), in)
	if len(dropped) != 0 {
		t.Fatalf("dropped = %q, want none", dropped)
	}
	if len(kept) != 1 {
		t.Fatalf("kept = %+v, want one endpoint", kept)
	}
	if want := []string{"fd00:10::80", "fd00:10::81"}; !reflect.DeepEqual(kept[0].Targets, want) {
		t.Errorf("targets = %q, want %q", kept[0].Targets, want)
	}
}

func TestAdjustDrops(t *testing.T) {
	tests := []struct {
		name     string
		endpoint webhook.Endpoint
		reason   string
	}{
		{name: "CNAME", endpoint: webhook.Endpoint{DNSName: "c.home.example.test", Targets: []string{"app.home.example.test"}, RecordType: "CNAME"}, reason: "CNAME"},
		{name: "TXT", endpoint: webhook.Endpoint{DNSName: "t.home.example.test", Targets: []string{ownership}, RecordType: "TXT"}, reason: "TXT"},
		{name: "set identifier", endpoint: webhook.Endpoint{DNSName: "s.home.example.test", Targets: []string{"10.0.0.80"}, RecordType: "A", SetIdentifier: "blue"}, reason: "set identifier"},
		{name: "outside the zone", endpoint: webhook.Endpoint{DNSName: "app.other.example.test", Targets: []string{"10.0.0.80"}, RecordType: "A"}, reason: "zone"},
		{name: "wildcard", endpoint: webhook.Endpoint{DNSName: "*.home.example.test", Targets: []string{"10.0.0.80"}, RecordType: "A"}, reason: "label"},
		{name: "address outside the prefixes", endpoint: webhook.Endpoint{DNSName: "p.home.example.test", Targets: []string{"8.8.8.8"}, RecordType: "A"}, reason: "outside the allowed prefixes"},
		{name: "one good and one bad target", endpoint: webhook.Endpoint{DNSName: "m.home.example.test", Targets: []string{"10.0.0.80", "10.0.1.80"}, RecordType: "A"}, reason: "10.0.1.80"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kept, dropped := webhook.Adjust(testPolicy(), []webhook.Endpoint{tt.endpoint})
			if len(kept) != 0 {
				t.Errorf("kept = %+v, want none", kept)
			}
			if len(dropped) != 1 {
				t.Fatalf("dropped = %q, want one description", dropped)
			}
			if !strings.Contains(dropped[0], tt.endpoint.DNSName) || !strings.Contains(dropped[0], tt.endpoint.RecordType) {
				t.Errorf("dropped = %q, want it to name %s %s", dropped[0], tt.endpoint.DNSName, tt.endpoint.RecordType)
			}
			if !strings.Contains(dropped[0], tt.reason) {
				t.Errorf("dropped = %q, want it to contain %q", dropped[0], tt.reason)
			}
		})
	}
}

func TestAdjustEmptyInput(t *testing.T) {
	for _, in := range [][]webhook.Endpoint{nil, {}} {
		kept, dropped := webhook.Adjust(testPolicy(), in)
		if kept == nil || len(kept) != 0 {
			t.Errorf("Adjust(%v) kept = %#v, want empty non-nil", in, kept)
		}
		if len(dropped) != 0 {
			t.Errorf("Adjust(%v) dropped = %q, want none", in, dropped)
		}
	}
}

func TestApplyCreate(t *testing.T) {
	got, err := apply(t, records.State{}, webhook.Changes{Create: []webhook.Endpoint{
		{DNSName: "new.home.example.test", Targets: []string{"10.0.0.81", "10.0.0.80"}, RecordType: "A", RecordTTL: 300},
		{DNSName: "new.home.example.test", Targets: []string{"fd00:10::80"}, RecordType: "AAAA"},
		{DNSName: "a-new.home.example.test", Targets: []string{ownership}, RecordType: "TXT", Labels: map[string]string{"ownedRecord": "new.home.example.test"}},
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := records.State{
		Records: []records.Record{
			{Name: "new.home.example.test", Type: records.TypeA, Values: addrs("10.0.0.80", "10.0.0.81"), Source: records.SourceExternalDNS},
			{Name: "new.home.example.test", Type: records.TypeAAAA, Values: addrs("fd00:10::80"), Source: records.SourceExternalDNS},
		},
		TXTs: []records.TXT{{Name: "a-new.home.example.test", Targets: []string{ownership}, OwnedRecord: "new.home.example.test"}},
	}
	if !got.Equal(want) {
		t.Errorf("state = %+v, want %+v", got, want)
	}
}

func TestApplyNormalisesOwnedRecord(t *testing.T) {
	for _, label := range []string{"App.home.example.test", "app.home.example.test.", "APP.Home.Example.Test."} {
		t.Run(label, func(t *testing.T) {
			got, err := apply(t, records.State{}, webhook.Changes{Create: []webhook.Endpoint{
				{DNSName: "a-app.home.example.test", Targets: []string{ownership}, RecordType: "TXT", Labels: map[string]string{"ownedRecord": label}},
			}})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			want := records.State{TXTs: []records.TXT{{Name: "a-app.home.example.test", Targets: []string{ownership}, OwnedRecord: "app.home.example.test"}}}
			if !got.Equal(want) {
				t.Errorf("state = %+v, want %+v", got, want)
			}
		})
	}
}

func TestApplyUpdate(t *testing.T) {
	got, err := apply(t, apiState(), webhook.Changes{
		UpdateOld: []webhook.Endpoint{{DNSName: "app.home.example.test", Targets: []string{"10.0.0.80"}, RecordType: "A"}},
		UpdateNew: []webhook.Endpoint{{DNSName: "app.home.example.test", Targets: []string{"10.0.0.90"}, RecordType: "A"}},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := apiState()
	want.Records[1].Values = addrs("10.0.0.90")
	if !got.Equal(want) {
		t.Errorf("state = %+v, want %+v", got, want)
	}
}

func TestApplyDelete(t *testing.T) {
	got, err := apply(t, apiState(), webhook.Changes{Delete: []webhook.Endpoint{
		{DNSName: "app.home.example.test", Targets: []string{"10.0.0.80"}, RecordType: "A"},
		{DNSName: "a-app.home.example.test", Targets: []string{"something else"}, RecordType: "TXT", Labels: map[string]string{"ownedRecord": "app.home.example.test"}},
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := records.State{Records: apiState().Records[:1]}
	if !got.Equal(want) {
		t.Errorf("state = %+v, want %+v", got, want)
	}
	d := got.Edit()
	if _, ok := d.Source("app.home.example.test"); ok {
		t.Error("app.home.example.test still has a source, want it forgotten")
	}
}

func TestApplyChangeSetShapes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want func() records.State
	}{
		{
			name: "omitted lists",
			body: `{}`,
			want: apiState,
		},
		{
			name: "null lists",
			body: `{"create":null,"updateOld":null,"updateNew":null,"delete":null}`,
			want: apiState,
		},
		{
			name: "updateOld without a matching updateNew",
			body: `{"updateOld":[{"dnsName":"app.home.example.test","targets":["10.0.0.80"],"recordType":"A"}]}`,
			want: func() records.State {
				s := apiState()
				s.Records = s.Records[:1]
				return s
			},
		},
		{
			name: "same name in delete and create",
			body: `{"delete":[{"dnsName":"app.home.example.test","targets":["10.0.0.80"],"recordType":"A"}],` +
				`"create":[{"dnsName":"app.home.example.test","targets":["10.0.0.91"],"recordType":"A"}]}`,
			want: func() records.State {
				s := apiState()
				s.Records[1].Values = addrs("10.0.0.91")
				return s
			},
		},
		{
			name: "delete of something absent",
			body: `{"delete":[{"dnsName":"gone.home.example.test","targets":["10.0.0.70"],"recordType":"A"},` +
				`{"dnsName":"app.home.example.test","targets":["fd00:10::80"],"recordType":"AAAA"},` +
				`{"dnsName":"a-gone.home.example.test","targets":["x"],"recordType":"TXT"}]}`,
			want: apiState,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c webhook.Changes
			if err := json.Unmarshal([]byte(tt.body), &c); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			got, err := apply(t, apiState(), c)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if want := tt.want(); !got.Equal(want) {
				t.Errorf("state = %+v, want %+v", got, want)
			}
		})
	}
}

func TestApplyRefusals(t *testing.T) {
	a := func(name string, targets ...string) webhook.Endpoint {
		return webhook.Endpoint{DNSName: name, Targets: targets, RecordType: "A"}
	}
	txt := func(name, owned string) webhook.Endpoint {
		return webhook.Endpoint{DNSName: name, Targets: []string{ownership}, RecordType: "TXT", Labels: map[string]string{"ownedRecord": owned}}
	}
	withID := a("new.home.example.test", "10.0.0.80")
	withID.SetIdentifier = "blue"
	tests := []struct {
		name    string
		changes webhook.Changes
		want    error
		entry   string
	}{
		{name: "CNAME create", changes: webhook.Changes{Create: []webhook.Endpoint{{DNSName: "c.home.example.test", Targets: []string{"app.home.example.test"}, RecordType: "CNAME"}}}, want: records.ErrInvalid, entry: "c.home.example.test"},
		{name: "set identifier", changes: webhook.Changes{Create: []webhook.Endpoint{withID}}, want: records.ErrInvalid, entry: "new.home.example.test"},
		{name: "bad address", changes: webhook.Changes{Create: []webhook.Endpoint{a("new.home.example.test", "8.8.8.8")}}, want: records.ErrInvalid, entry: "new.home.example.test"},
		{name: "create on an api name", changes: webhook.Changes{Create: []webhook.Endpoint{a("api.home.example.test", "10.0.0.11")}}, want: records.ErrConflict, entry: "api.home.example.test"},
		{name: "update on an api name", changes: webhook.Changes{UpdateNew: []webhook.Endpoint{a("api.home.example.test", "10.0.0.11")}}, want: records.ErrConflict, entry: "api.home.example.test"},
		{name: "updateOld on an api name", changes: webhook.Changes{UpdateOld: []webhook.Endpoint{a("api.home.example.test", "10.0.0.10")}}, want: records.ErrConflict, entry: "api.home.example.test"},
		{name: "delete on an api name", changes: webhook.Changes{Delete: []webhook.Endpoint{a("api.home.example.test", "10.0.0.10")}}, want: records.ErrConflict, entry: "api.home.example.test"},
		{name: "TXT create owned by an api name", changes: webhook.Changes{Create: []webhook.Endpoint{txt("a-api.home.example.test", "api.home.example.test")}}, want: records.ErrConflict, entry: "a-api.home.example.test"},
		{name: "TXT delete owned by an api name", changes: webhook.Changes{Delete: []webhook.Endpoint{txt("a-api.home.example.test", "api.home.example.test")}}, want: records.ErrConflict, entry: "a-api.home.example.test"},
		{name: "unlabelled TXT delete of a stored TXT owning an api name", changes: webhook.Changes{Delete: []webhook.Endpoint{{DNSName: "a-nas.home.example.test", Targets: []string{ownership}, RecordType: "TXT"}}}, want: records.ErrConflict, entry: "a-nas.home.example.test"},
		{name: "TXT set over a stored TXT owning an api name", changes: webhook.Changes{Create: []webhook.Endpoint{txt("a-nas.home.example.test", "app.home.example.test")}}, want: records.ErrConflict, entry: "a-nas.home.example.test"},
		{name: "TXT name outside the zone", changes: webhook.Changes{Create: []webhook.Endpoint{txt("a-app.other.example.test", "app.other.example.test")}}, want: records.ErrInvalid, entry: "a-app.other.example.test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := apply(t, orphanState(), tt.changes)
			if err == nil {
				t.Fatal("Apply = nil, want an error")
			}
			if !errors.Is(err, tt.want) {
				t.Errorf("Apply = %v, want it to wrap %v", err, tt.want)
			}
			if !strings.Contains(err.Error(), tt.entry) {
				t.Errorf("Apply = %v, want it to name %s", err, tt.entry)
			}
		})
	}
}

func TestApplyTXTLongName(t *testing.T) {
	name := "aaaa-" + strings.Repeat("x", 62) + ".home.example.test"
	got, err := apply(t, records.State{}, webhook.Changes{Create: []webhook.Endpoint{
		{DNSName: name, Targets: []string{ownership}, RecordType: "TXT"},
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(got.TXTs) != 1 || got.TXTs[0].Name != name {
		t.Errorf("TXTs = %+v, want one called %s", got.TXTs, name)
	}
}

func TestEndpoints(t *testing.T) {
	s := records.State{
		Records: []records.Record{
			{Name: "api.home.example.test", Type: records.TypeA, Values: addrs("10.0.0.10"), Source: records.SourceAPI},
			{Name: "app.home.example.test", Type: records.TypeA, Values: addrs("10.0.0.80", "10.0.0.81"), Source: records.SourceExternalDNS},
			{Name: "app.home.example.test", Type: records.TypeAAAA, Values: addrs("fd00:10::80"), Source: records.SourceExternalDNS},
		},
		TXTs: []records.TXT{{Name: "a-app.home.example.test", Targets: []string{ownership}, OwnedRecord: "app.home.example.test"}},
	}
	got, err := json.Marshal(webhook.Endpoints(s))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `[{"dnsName":"api.home.example.test","targets":["10.0.0.10"],"recordType":"A"},` +
		`{"dnsName":"app.home.example.test","targets":["10.0.0.80","10.0.0.81"],"recordType":"A"},` +
		`{"dnsName":"app.home.example.test","targets":["fd00:10::80"],"recordType":"AAAA"},` +
		`{"dnsName":"a-app.home.example.test","targets":[` + mustJSON(t, ownership) + `],"recordType":"TXT"}]`
	if string(got) != want {
		t.Errorf("JSON =\n%s\nwant\n%s", got, want)
	}
}

func TestEndpointsEmpty(t *testing.T) {
	got, err := json.Marshal(webhook.Endpoints(records.State{}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(got) != "[]" {
		t.Errorf("JSON = %s, want []", got)
	}
}

// mustJSON returns s encoded as a JSON string.
func mustJSON(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
