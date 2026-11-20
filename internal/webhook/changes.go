package webhook

import (
	"fmt"
	"maps"
	"net/netip"
	"strings"

	"github.com/gsdevme/dnsmasq-manager/internal/records"
)

// typeTXT is the record type of external-dns ownership records.
const typeTXT = "TXT"

// ownedRecordLabel is the label external-dns puts on an ownership TXT to name
// the record it owns.
const ownedRecordLabel = "ownedRecord"

// Endpoint is external-dns's endpoint as it travels over the webhook.
type Endpoint struct {
	DNSName          string            `json:"dnsName,omitempty"`
	Targets          []string          `json:"targets,omitempty"`
	RecordType       string            `json:"recordType,omitempty"`
	SetIdentifier    string            `json:"setIdentifier,omitempty"`
	RecordTTL        int64             `json:"recordTTL,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
	ProviderSpecific []Property        `json:"providerSpecific,omitempty"`
}

// Property is one provider-specific setting of an Endpoint. The service
// accepts none and strips them in Adjust.
type Property struct {
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

// Changes is the change set external-dns sends to POST /records. Any list
// may be omitted or null.
type Changes struct {
	Create    []Endpoint `json:"create,omitempty"`
	UpdateOld []Endpoint `json:"updateOld,omitempty"`
	UpdateNew []Endpoint `json:"updateNew,omitempty"`
	Delete    []Endpoint `json:"delete,omitempty"`
}

// empty reports whether c holds no entries at all.
func (c Changes) empty() bool {
	return len(c.Create) == 0 && len(c.UpdateOld) == 0 && len(c.UpdateNew) == 0 && len(c.Delete) == 0
}

// Adjust returns the endpoints the service would accept, in the form
// GET /records returns them, and a description of each one dropped.
//
// An endpoint is kept only if it is A or AAAA, has no set identifier, and its
// name and every target pass policy. A kept endpoint has a normalised name,
// canonical sorted unique targets, its labels, and no TTL or provider-specific
// properties. Each description names the endpoint and the reason. kept is
// never nil, so it encodes as [] when empty.
func Adjust(policy records.Policy, in []Endpoint) (kept []Endpoint, dropped []string) {
	kept = make([]Endpoint, 0, len(in))
	for _, e := range in {
		name, t, values, err := parseRecord(policy, e)
		if err != nil {
			dropped = append(dropped, fmt.Sprintf("%s %s: %v", e.DNSName, e.RecordType, err))
			continue
		}
		kept = append(kept, Endpoint{
			DNSName:    name,
			Targets:    addrStrings(values),
			RecordType: string(t),
			Labels:     maps.Clone(e.Labels),
		})
	}
	return kept, dropped
}

// Apply applies c to d in the order delete, updateOld, updateNew, create.
// Any refusal returns an error naming the entry; d must then be discarded.
//
// Delete and updateOld entries remove a record by name and type, or a TXT by
// name, ignoring their targets; removing something absent is not an error.
// UpdateNew and create entries set the name and type to their targets, giving
// the name source external-dns. An entry of another type, with a set
// identifier, or failing policy is refused with an error wrapping
// records.ErrInvalid. An entry that would touch a name whose source is api,
// or a TXT whose ownedRecord label or stored owner is such a name, is refused
// with an error wrapping records.ErrConflict.
func Apply(policy records.Policy, d *records.Draft, c Changes) error {
	steps := []struct {
		list   string
		in     []Endpoint
		remove bool
	}{
		{"delete", c.Delete, true},
		{"updateOld", c.UpdateOld, true},
		{"updateNew", c.UpdateNew, false},
		{"create", c.Create, false},
	}
	for _, step := range steps {
		for _, e := range step.in {
			if err := applyOne(policy, d, e, step.remove); err != nil {
				return fmt.Errorf("%s %s %s: %w", step.list, e.DNSName, e.RecordType, err)
			}
		}
	}
	return nil
}

// applyOne removes or sets the record or TXT that e describes.
func applyOne(policy records.Policy, d *records.Draft, e Endpoint, remove bool) error {
	if e.SetIdentifier != "" {
		return fmt.Errorf("%w: set identifier %q is not supported", records.ErrInvalid, e.SetIdentifier)
	}
	if e.RecordType == typeTXT {
		return applyTXT(policy, d, e, remove)
	}
	t, err := records.ParseType(e.RecordType)
	if err != nil {
		return err
	}
	name, err := policy.NormalizeName(e.DNSName)
	if err != nil {
		return err
	}
	if src, ok := d.Source(name); ok && src == records.SourceAPI {
		return fmt.Errorf("%w: %s belongs to the api", records.ErrConflict, name)
	}
	if remove {
		d.Delete(name, t)
		return nil
	}
	values, err := policy.ParseValues(t, e.Targets)
	if err != nil {
		return err
	}
	d.Set(name, t, values, records.SourceExternalDNS)
	return nil
}

// applyTXT removes or sets the ownership TXT that e describes. Its name and
// targets are kept exactly as received; its ownedRecord label is stored
// normalised, as records.TXT requires. It is refused when either the
// incoming ownedRecord label or the TXT already stored under that name owns
// a name whose source is api, so a label-less removal cannot take a TXT the
// api's name depends on.
func applyTXT(policy records.Policy, d *records.Draft, e Endpoint, remove bool) error {
	owned := normalizeOwner(e.Labels[ownedRecordLabel])
	if err := refuseAPIOwner(d, owned); err != nil {
		return err
	}
	if stored, ok := d.TXT(e.DNSName); ok {
		if err := refuseAPIOwner(d, stored.OwnedRecord); err != nil {
			return err
		}
	}
	if remove {
		d.DeleteTXT(e.DNSName)
		return nil
	}
	txt := records.TXT{Name: e.DNSName, Targets: e.Targets, OwnedRecord: owned}
	if err := policy.CheckTXT(txt); err != nil {
		return err
	}
	d.SetTXT(txt)
	return nil
}

// normalizeOwner returns an ownedRecord label lowercase with one trailing dot
// removed, the form record names are stored in.
func normalizeOwner(owned string) string {
	return strings.TrimSuffix(strings.ToLower(owned), ".")
}

// refuseAPIOwner returns an error wrapping records.ErrConflict when owned,
// compared after normalizeOwner, is a name whose source is api. An empty
// owned is never refused.
func refuseAPIOwner(d *records.Draft, owned string) error {
	key := normalizeOwner(owned)
	if key == "" {
		return nil
	}
	if src, ok := d.Source(key); ok && src == records.SourceAPI {
		return fmt.Errorf("%w: TXT owns %s, which belongs to the api", records.ErrConflict, key)
	}
	return nil
}

// parseRecord validates e as an A or AAAA endpoint without a set identifier
// and returns its normalised name, type and canonical values.
func parseRecord(policy records.Policy, e Endpoint) (string, records.Type, []netip.Addr, error) {
	if e.SetIdentifier != "" {
		return "", "", nil, fmt.Errorf("%w: set identifier %q is not supported", records.ErrInvalid, e.SetIdentifier)
	}
	t, err := records.ParseType(e.RecordType)
	if err != nil {
		return "", "", nil, err
	}
	name, err := policy.NormalizeName(e.DNSName)
	if err != nil {
		return "", "", nil, err
	}
	values, err := policy.ParseValues(t, e.Targets)
	if err != nil {
		return "", "", nil, err
	}
	return name, t, values, nil
}

// Endpoints returns the state as GET /records serves it.
//
// Every A and AAAA record of either source comes first, in state order, then
// every TXT with its targets exactly as stored. No TTL, labels or
// provider-specific properties are set. The result is never nil, so it
// encodes as [] when empty.
func Endpoints(s records.State) []Endpoint {
	out := make([]Endpoint, 0, len(s.Records)+len(s.TXTs))
	for _, r := range s.Records {
		out = append(out, Endpoint{DNSName: r.Name, Targets: addrStrings(r.Values), RecordType: string(r.Type)})
	}
	for _, t := range s.TXTs {
		out = append(out, Endpoint{DNSName: t.Name, Targets: append([]string(nil), t.Targets...), RecordType: typeTXT})
	}
	return out
}

// addrStrings returns each address in canonical text form.
func addrStrings(addrs []netip.Addr) []string {
	out := make([]string, len(addrs))
	for i, a := range addrs {
		out[i] = a.String()
	}
	return out
}
