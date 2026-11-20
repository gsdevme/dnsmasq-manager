// Package config loads the serve command's settings from DNSMASQ_MANAGER_*
// environment variables, applying the defaults and rules of the serve design.
// Load reports every problem at once, so one run shows everything to fix.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gsdevme/dnsmasq-manager/internal/records"
)

const (
	prefix      = "DNSMASQ_MANAGER_"
	minTokenLen = 32
	off         = "off"
)

const (
	varZones           = prefix + "ZONES"
	varAllowedCIDRs    = prefix + "ALLOWED_CIDRS"
	varHostsDir        = prefix + "HOSTS_DIR"
	varDNSProbeAddr    = prefix + "DNS_PROBE_ADDR"
	varAPIAddr         = prefix + "API_ADDR"
	varAPIToken        = prefix + "API_TOKEN"
	varAPITokenFile    = prefix + "API_TOKEN_FILE"
	varAPITLSCert      = prefix + "API_TLS_CERT"
	varAPITLSKey       = prefix + "API_TLS_KEY"
	varAPIPlainHTTP    = prefix + "API_PLAIN_HTTP"
	varWebhookAddr     = prefix + "WEBHOOK_ADDR"
	varWebhookSources  = prefix + "WEBHOOK_ALLOWED_SOURCES"
	varWebAddr         = prefix + "WEB_ADDR"
	varAllowWildcard   = prefix + "ALLOW_WILDCARD_BIND"
	varLogLevel        = prefix + "LOG_LEVEL"
	defaultCIDRs       = "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"
	defaultHostsDir    = "/etc/dnsmasq.hosts.d"
	defaultDNSProbe    = "127.0.0.1:53"
	defaultAPIAddr     = "127.0.0.1:8443"
	defaultWebhookAddr = "127.0.0.1:8888"
	defaultSources     = "127.0.0.1/32"
	defaultWebAddr     = "127.0.0.1:80"
	defaultLogLevel    = "info"
)

var known = []string{
	varZones, varAllowedCIDRs, varHostsDir, varDNSProbeAddr, varAPIAddr,
	varAPIToken, varAPITokenFile, varAPITLSCert, varAPITLSKey, varAPIPlainHTTP,
	varWebhookAddr, varWebhookSources, varWebAddr, varAllowWildcard, varLogLevel,
}

// installer lists the variables that belong to install.sh, not to serve. Load
// ignores them, so an operator who exported one for the installer can run
// serve in the same shell; they are not unknown, and not read either.
var installer = []string{
	prefix + "VERSION", prefix + "BASE_URL", prefix + "INSTALL_DIR", prefix + "OS_RELEASE",
}

// Config is the validated configuration of the serve command. A Config from
// Load satisfies every rule of the design; one built by hand, as in tests, is
// trusted as it is.
type Config struct {
	// Policy holds the zones, lowercase without a trailing dot, and the
	// prefixes record addresses must fall in.
	Policy records.Policy
	// HostsDir is dnsmasq's hostsdir, an existing directory.
	HostsDir string
	// DNSProbeAddr is where the health check asks dnsmasq a question. It may
	// be a wildcard and may equal a listener's address.
	DNSProbeAddr netip.AddrPort
	// APIAddr is the REST listener's address.
	APIAddr netip.AddrPort
	// APIToken is the bearer token, at least 32 characters, read from
	// API_TOKEN or API_TOKEN_FILE.
	APIToken string
	// APITLSCert and APITLSKey are the PEM paths for REST over TLS; both are
	// set exactly when APIPlainHTTP is false. Load does not read them.
	APITLSCert, APITLSKey string
	// APIPlainHTTP serves REST without TLS.
	APIPlainHTTP bool
	// WebhookEnabled is false when WEBHOOK_ADDR is off; WebhookAddr is then
	// the zero value.
	WebhookEnabled bool
	WebhookAddr    netip.AddrPort
	// WebhookAllowedSources are the prefixes allowed to call the webhook; a
	// bare IP is held as a /32 or /128.
	WebhookAllowedSources []netip.Prefix
	// WebEnabled is false when WEB_ADDR is off; WebAddr is then the zero
	// value.
	WebEnabled bool
	WebAddr    netip.AddrPort
	// LogLevel is the minimum level logged.
	LogLevel slog.Level
}

// Load reads DNSMASQ_MANAGER_* from environ (os.Environ form). Every problem
// is reported, joined with errors.Join, each naming its variable. A prefixed
// variable that is neither serve's nor install.sh's is a problem.
//
// A variable set to the empty string counts as unset. Entries without '=' and
// variables without the prefix are ignored; when a variable appears more than
// once, the first wins, as in os.Getenv.
func Load(environ []string) (Config, error) {
	l := newLoader(environ)
	var c Config

	c.Policy.Zones = l.zones()
	c.Policy.Prefixes = l.prefixes(varAllowedCIDRs, defaultCIDRs, false)
	c.HostsDir = l.hostsDir()
	c.APIToken = l.token()
	c.APITLSCert, c.APITLSKey, c.APIPlainHTTP = l.transport()
	c.WebhookAllowedSources = l.prefixes(varWebhookSources, defaultSources, true)
	c.LogLevel = l.logLevel()

	wildcard := l.boolean(varAllowWildcard)
	if probe, ok := l.addrPort(varDNSProbeAddr, defaultDNSProbe); ok {
		c.DNSProbeAddr = probe
	}
	c.APIAddr, _ = l.listener(varAPIAddr, defaultAPIAddr, false, wildcard)
	c.WebhookAddr, c.WebhookEnabled = l.listener(varWebhookAddr, defaultWebhookAddr, true, wildcard)
	c.WebAddr, c.WebEnabled = l.listener(varWebAddr, defaultWebAddr, true, wildcard)
	l.distinct()

	l.unknown()
	if err := errors.Join(l.errs...); err != nil {
		return Config{}, err
	}
	return c, nil
}

// loader holds the prefixed variables being read and the problems found.
type loader struct {
	env       map[string]string
	errs      []error
	listeners []listenerAddr
}

// listenerAddr is a parsed, enabled listener address and its variable.
type listenerAddr struct {
	name string
	addr netip.AddrPort
}

func newLoader(environ []string) *loader {
	l := &loader{env: map[string]string{}}
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(k, prefix) {
			continue
		}
		if _, seen := l.env[k]; !seen {
			l.env[k] = v
		}
	}
	return l
}

// get returns the value of name, or def when it is unset or empty.
func (l *loader) get(name, def string) string {
	if v := l.env[name]; v != "" {
		return v
	}
	return def
}

// fail records a problem with the variable name.
func (l *loader) fail(name, format string, args ...any) {
	l.errs = append(l.errs, fmt.Errorf("%s: %s", name, fmt.Sprintf(format, args...)))
}

// list splits a comma-separated value, trimming spaces around each item. It
// reports an empty item and returns false.
func (l *loader) list(name, value string) ([]string, bool) {
	items := strings.Split(value, ",")
	for i, item := range items {
		items[i] = strings.TrimSpace(item)
		if items[i] == "" {
			l.fail(name, "empty item in %q", value)
			return nil, false
		}
	}
	return items, true
}

func (l *loader) zones() []string {
	v := l.get(varZones, "")
	if v == "" {
		l.fail(varZones, "required")
		return nil
	}
	items, ok := l.list(varZones, v)
	if !ok {
		return nil
	}
	var zones []string
	for _, item := range items {
		zone := strings.TrimSuffix(strings.ToLower(item), ".")
		if err := checkZone(zone); err != nil {
			l.fail(varZones, "zone %q %v", item, err)
			continue
		}
		if !slices.Contains(zones, zone) {
			zones = append(zones, zone)
		}
	}
	return zones
}

// checkZone reports why zone, already lowercase without a trailing dot, is
// not a host name of at least two labels. The host name rules are the ones
// record names follow, from records.CheckHostName.
func checkZone(zone string) error {
	if err := records.CheckHostName(zone); err != nil {
		return err
	}
	if !strings.Contains(zone, ".") {
		return errors.New("needs at least two labels")
	}
	return nil
}

// prefixes parses a comma-separated list of prefixes. When bareIP is true an
// address without a length is accepted as a single-address prefix. Prefixes
// must be in masked form, carry no zone and not be IPv4-mapped IPv6: record
// addresses are never IPv4-mapped and webhook source addresses are unmapped
// before matching, so such a prefix would never match anything.
func (l *loader) prefixes(name, def string, bareIP bool) []netip.Prefix {
	items, ok := l.list(name, l.get(name, def))
	if !ok {
		return nil
	}
	var out []netip.Prefix
	for _, item := range items {
		p, err := parsePrefix(item, bareIP)
		if err != nil {
			l.fail(name, "%v", err)
			continue
		}
		out = append(out, p)
	}
	return out
}

func parsePrefix(s string, bareIP bool) (netip.Prefix, error) {
	if bareIP && !strings.Contains(s, "/") {
		addr, err := netip.ParseAddr(s)
		if err != nil || addr.Zone() != "" {
			return netip.Prefix{}, fmt.Errorf("%q is not an IP address or prefix", s)
		}
		if addr.Is4In6() {
			return netip.Prefix{}, fmt.Errorf("%q is an IPv4-mapped IPv6 address; use %v", s, addr.Unmap())
		}
		return netip.PrefixFrom(addr, addr.BitLen()), nil
	}
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q is not a prefix", s)
	}
	if p != p.Masked() {
		return netip.Prefix{}, fmt.Errorf("%q has host bits set; did you mean %v?", s, p.Masked())
	}
	if p.Addr().Is4In6() {
		return netip.Prefix{}, fmt.Errorf("%q is an IPv4-mapped IPv6 prefix; write it as an IPv4 prefix", s)
	}
	return p, nil
}

func (l *loader) hostsDir() string {
	dir := l.get(varHostsDir, defaultHostsDir)
	info, err := os.Stat(dir)
	switch {
	case err != nil:
		l.fail(varHostsDir, "%v", err)
	case !info.IsDir():
		l.fail(varHostsDir, "%s is not a directory", dir)
	}
	return dir
}

// token reads the bearer token from exactly one of API_TOKEN and
// API_TOKEN_FILE and checks its length and characters. A token holding any
// whitespace or control character is refused: the REST listener trims the
// presented token, so one with surrounding whitespace could never match.
func (l *loader) token() string {
	value, path := l.get(varAPIToken, ""), l.get(varAPITokenFile, "")
	switch {
	case value != "" && path != "":
		l.fail(varAPIToken, "set only one of %s and %s", varAPIToken, varAPITokenFile)
		return ""
	case value == "" && path == "":
		l.fail(varAPIToken, "set one of %s and %s", varAPIToken, varAPITokenFile)
		return ""
	}
	name := varAPIToken
	if path != "" {
		name = varAPITokenFile
		var err error
		if value, err = readTokenFile(path); err != nil {
			l.fail(name, "%v", err)
			return ""
		}
	}
	if strings.ContainsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		l.fail(name, "token contains whitespace or a control character")
		return ""
	}
	if n := utf8.RuneCountInString(value); n < minTokenLen {
		l.fail(name, "token is %d characters, want at least %d", n, minTokenLen)
		return ""
	}
	return value
}

// readTokenFile returns the contents of path without trailing line endings,
// refusing a file that group or others may read.
func readTokenFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return "", fmt.Errorf("%s has mode %04o; it must not be readable by group or others", path, perm)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}

// transport checks that exactly one of the TLS pair and plain HTTP is chosen.
func (l *loader) transport() (cert, key string, plain bool) {
	cert, key = l.get(varAPITLSCert, ""), l.get(varAPITLSKey, "")
	plain = l.boolean(varAPIPlainHTTP)
	switch {
	case cert != "" && key == "":
		l.fail(varAPITLSKey, "required when %s is set", varAPITLSCert)
	case cert == "" && key != "":
		l.fail(varAPITLSCert, "required when %s is set", varAPITLSKey)
	case cert != "" && plain:
		l.fail(varAPIPlainHTTP, "must not be true when %s and %s are set", varAPITLSCert, varAPITLSKey)
	case cert == "" && !plain:
		l.fail(varAPIPlainHTTP, "set %s=true, or set %s and %s", varAPIPlainHTTP, varAPITLSCert, varAPITLSKey)
	}
	return cert, key, plain
}

// boolean parses name as exactly "true" or "false", defaulting to false.
func (l *loader) boolean(name string) bool {
	switch v := l.get(name, "false"); v {
	case "true":
		return true
	case "false":
		return false
	default:
		l.fail(name, "%q is not true or false", v)
		return false
	}
}

func (l *loader) logLevel() slog.Level {
	switch v := l.get(varLogLevel, defaultLogLevel); v {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		l.fail(varLogLevel, "%q is not debug, info, warn or error", v)
		return slog.LevelInfo
	}
}

// addrPort parses name as a literal IP and port. An IPv4-mapped IPv6 address
// is returned as the plain IPv4 address it maps, which is what binding or
// dialling it reaches, so the wildcard and must-differ rules see through it.
func (l *loader) addrPort(name, def string) (netip.AddrPort, bool) {
	v := l.get(name, def)
	ap, err := netip.ParseAddrPort(v)
	if err != nil {
		l.fail(name, "%q is not a literal IP and port", v)
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()), true
}

// listener parses a listener address, accepting "off" when canBeOff is set,
// and refusing a wildcard unless wildcard is allowed. It returns whether the
// listener is enabled and remembers it for the distinct check.
func (l *loader) listener(name, def string, canBeOff, wildcard bool) (netip.AddrPort, bool) {
	if canBeOff && l.get(name, def) == off {
		return netip.AddrPort{}, false
	}
	ap, ok := l.addrPort(name, def)
	if !ok {
		return netip.AddrPort{}, true
	}
	if ap.Addr().IsUnspecified() && !wildcard {
		l.fail(name, "%v is a wildcard address; set %s=true to allow it", ap, varAllowWildcard)
	}
	l.listeners = append(l.listeners, listenerAddr{name: name, addr: ap})
	return ap, true
}

// distinct reports each enabled listener whose address an earlier one uses.
// Port 0 never clashes: the kernel picks a different free port for each.
func (l *loader) distinct() {
	for i, a := range l.listeners {
		if a.addr.Port() == 0 {
			continue
		}
		for _, b := range l.listeners[:i] {
			if a.addr == b.addr {
				l.fail(a.name, "%v is also %s; the listener addresses must differ", a.addr, b.name)
				break
			}
		}
	}
}

// unknown reports, in name order, every prefixed variable Load does not know
// and that is not one of the installer's.
func (l *loader) unknown() {
	var names []string
	for name := range l.env {
		if !slices.Contains(known, name) && !slices.Contains(installer, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		l.fail(name, "unknown variable")
	}
}
