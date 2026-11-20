package records

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"unicode/utf8"
)

// MaxValues is the most addresses one record may hold.
const MaxValues = 16

const (
	maxNameLen      = 253
	maxLabelLen     = 63
	maxTXTNameLen   = 300
	maxTXTTargets   = 4
	maxTXTTargetLen = 1024
)

// Policy is the validation both APIs apply, and the load of the managed file
// applies again. Zones are the configured zones, lowercase without a trailing
// dot; record and TXT names must be strict subdomains of one of them. Prefixes
// are the networks record addresses must fall inside.
//
// Every error a Policy returns wraps ErrInvalid.
type Policy struct {
	Zones    []string
	Prefixes []netip.Prefix
}

// NormalizeName lowercases name and removes one trailing dot, then checks the
// result is a valid record name: at most 253 characters, labels of 1 to 63
// characters from a-z, 0-9 and '-' not starting or ending with '-', and a
// strict subdomain of a configured zone. It returns the normalised name.
func (p Policy) NormalizeName(name string) (string, error) {
	n := strings.TrimSuffix(strings.ToLower(name), ".")
	if n == "" {
		return "", fmt.Errorf("%w: empty name", ErrInvalid)
	}
	if err := CheckHostName(n); err != nil {
		return "", fmt.Errorf("%w: name %q %w", ErrInvalid, name, err)
	}
	if !p.inZone(n) {
		return "", fmt.Errorf("%w: name %q is not a subdomain of a configured zone", ErrInvalid, name)
	}
	return n, nil
}

// CheckHostName reports why name is not a host name in the form records and
// zones share: at most 253 characters, every label valid as ValidLabel
// defines it. name must already be lowercase without a trailing dot; a
// trailing dot leaves an empty last label and is refused. The error says what
// is wrong, worded to follow the name, as in "is longer than 253 characters".
func CheckHostName(name string) error {
	if len(name) > maxNameLen {
		return fmt.Errorf("is longer than %d characters", maxNameLen)
	}
	for label := range strings.SplitSeq(name, ".") {
		if !ValidLabel(label) {
			return fmt.Errorf("has an invalid label %q", label)
		}
	}
	return nil
}

// ValidLabel reports whether label is 1 to 63 of a-z, 0-9 and '-', not
// starting or ending with '-'. Uppercase letters are refused, so callers
// lowercase first.
func ValidLabel(label string) bool {
	if label == "" || len(label) > maxLabelLen || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for i := range len(label) {
		c := label[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// inZone reports whether name ends in ".<zone>" for a configured zone, with
// at least one character before it.
func (p Policy) inZone(name string) bool {
	for _, zone := range p.Zones {
		z := "." + strings.TrimSuffix(strings.ToLower(zone), ".")
		if len(name) > len(z) && strings.HasSuffix(name, z) {
			return true
		}
	}
	return false
}

// ParseValues parses values as addresses of type t and returns them sorted
// and unique. There must be 1 to MaxValues of them, each in t's family, with
// no zone suffix, not IPv4-mapped, and inside one of the policy's prefixes.
// A values must be in canonical dotted-quad form; AAAA values may use any
// valid spelling and are returned canonical.
func (p Policy) ParseValues(t Type, values []string) ([]netip.Addr, error) {
	if _, err := ParseType(string(t)); err != nil {
		return nil, err
	}
	if err := checkCount(len(values)); err != nil {
		return nil, err
	}
	out := make([]netip.Addr, 0, len(values))
	for _, v := range values {
		addr, err := netip.ParseAddr(v)
		if err != nil {
			return nil, fmt.Errorf("%w: %q is not an IP address", ErrInvalid, v)
		}
		if t == TypeA && addr.String() != v {
			return nil, fmt.Errorf("%w: %q is not a canonical IPv4 address", ErrInvalid, v)
		}
		if err := p.checkAddr(t, addr); err != nil {
			return nil, err
		}
		out = append(out, addr)
	}
	slices.SortFunc(out, netip.Addr.Compare)
	return slices.Compact(out), nil
}

// checkCount refuses an address count outside 1..MaxValues.
func checkCount(n int) error {
	if n == 0 {
		return fmt.Errorf("%w: no addresses", ErrInvalid)
	}
	if n > MaxValues {
		return fmt.Errorf("%w: %d addresses, at most %d allowed", ErrInvalid, n, MaxValues)
	}
	return nil
}

// checkAddr refuses addr unless it belongs to t's family, has no zone, is not
// IPv4-mapped and lies inside an allowed prefix.
func (p Policy) checkAddr(t Type, addr netip.Addr) error {
	var family bool
	switch t {
	case TypeA:
		family = addr.Is4()
	case TypeAAAA:
		family = addr.Is6() && !addr.Is4In6() && addr.Zone() == ""
	}
	if !family {
		return fmt.Errorf("%w: %v is not a valid %s address", ErrInvalid, addr, t)
	}
	for _, prefix := range p.Prefixes {
		if prefix.Contains(addr) {
			return nil
		}
	}
	return fmt.Errorf("%w: %v is outside the allowed prefixes", ErrInvalid, addr)
}

// CheckRecord validates a record already built from normalised input: its
// name is unchanged by NormalizeName, its type is known, it has 1 to
// MaxValues addresses each in the type's family and the allowed prefixes, and
// its source is known. It does not require the values to be sorted; Check
// does.
func (p Policy) CheckRecord(r Record) error {
	n, err := p.NormalizeName(r.Name)
	if err != nil {
		return err
	}
	if n != r.Name {
		return fmt.Errorf("%w: name %q is not normalised", ErrInvalid, r.Name)
	}
	if _, err := ParseType(string(r.Type)); err != nil {
		return err
	}
	if err := checkCount(len(r.Values)); err != nil {
		return fmt.Errorf("record %s %s: %w", r.Name, r.Type, err)
	}
	for _, addr := range r.Values {
		if err := p.checkAddr(r.Type, addr); err != nil {
			return fmt.Errorf("record %s %s: %w", r.Name, r.Type, err)
		}
	}
	switch r.Source {
	case SourceAPI, SourceExternalDNS:
		return nil
	default:
		return fmt.Errorf("%w: record %s %s has unknown source %q", ErrInvalid, r.Name, r.Type, r.Source)
	}
}

// CheckTXT validates an ownership record. Its name is checked loosely, since
// it is never published: lowercase printable ASCII with no spaces, at most
// 300 characters, ending in ".<zone>" for a configured zone. It must have 1
// to 4 targets of at most 1024 bytes each, and an OwnedRecord of at most 253
// bytes, the longest record name it could own. Targets and OwnedRecord must be
// valid UTF-8, because the managed file stores them as JSON and a string
// JSON would alter could not be read back.
func (p Policy) CheckTXT(t TXT) error {
	if len(t.Name) > maxTXTNameLen {
		return fmt.Errorf("%w: TXT name is longer than %d characters", ErrInvalid, maxTXTNameLen)
	}
	for i := range len(t.Name) {
		if c := t.Name[i]; c <= ' ' || c > '~' || (c >= 'A' && c <= 'Z') {
			return fmt.Errorf("%w: TXT name %q must be lowercase printable ASCII without spaces", ErrInvalid, t.Name)
		}
	}
	if !p.inZone(t.Name) {
		return fmt.Errorf("%w: TXT name %q is not a subdomain of a configured zone", ErrInvalid, t.Name)
	}
	if len(t.Targets) == 0 || len(t.Targets) > maxTXTTargets {
		return fmt.Errorf("%w: TXT %q has %d targets, want 1 to %d", ErrInvalid, t.Name, len(t.Targets), maxTXTTargets)
	}
	for _, target := range t.Targets {
		if len(target) > maxTXTTargetLen {
			return fmt.Errorf("%w: TXT %q has a target longer than %d bytes", ErrInvalid, t.Name, maxTXTTargetLen)
		}
		if !utf8.ValidString(target) {
			return fmt.Errorf("%w: TXT %q has a target that is not valid UTF-8", ErrInvalid, t.Name)
		}
	}
	if len(t.OwnedRecord) > maxNameLen {
		return fmt.Errorf("%w: TXT %q has an owned record longer than %d bytes", ErrInvalid, t.Name, maxNameLen)
	}
	if !utf8.ValidString(t.OwnedRecord) {
		return fmt.Errorf("%w: TXT %q has an owned record that is not valid UTF-8", ErrInvalid, t.Name)
	}
	return nil
}

// Check validates every record and TXT in s and that s is canonical, as
// State documents. Use it on a State that did not come from Draft.State, such
// as one parsed from the managed file.
func (p Policy) Check(s State) error {
	for i, r := range s.Records {
		if err := p.CheckRecord(r); err != nil {
			return err
		}
		for j := 1; j < len(r.Values); j++ {
			if r.Values[j-1].Compare(r.Values[j]) >= 0 {
				return fmt.Errorf("%w: record %s %s values are not sorted and unique", ErrInvalid, r.Name, r.Type)
			}
		}
		if i == 0 {
			continue
		}
		prev := s.Records[i-1]
		if cmp.Or(strings.Compare(prev.Name, r.Name), compareType(prev.Type, r.Type)) >= 0 {
			return fmt.Errorf("%w: records are not sorted and unique at %s %s", ErrInvalid, r.Name, r.Type)
		}
		if prev.Name == r.Name && prev.Source != r.Source {
			return fmt.Errorf("%w: name %s has more than one source", ErrInvalid, r.Name)
		}
	}
	for i, t := range s.TXTs {
		if err := p.CheckTXT(t); err != nil {
			return err
		}
		if i > 0 && s.TXTs[i-1].Name >= t.Name {
			return fmt.Errorf("%w: TXTs are not sorted and unique at %s", ErrInvalid, t.Name)
		}
	}
	return nil
}
