package config_test

import (
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gsdevme/dnsmasq-manager/internal/config"
	"github.com/gsdevme/dnsmasq-manager/internal/records"
)

var token = strings.Repeat("x", 32)

// minimal returns the smallest valid environ: zones, a token, plain HTTP and a
// temporary hosts directory, followed by extra.
func minimal(t *testing.T, extra ...string) []string {
	t.Helper()
	env := []string{
		"DNSMASQ_MANAGER_ZONES=home.example.test",
		"DNSMASQ_MANAGER_API_TOKEN=" + token,
		"DNSMASQ_MANAGER_API_PLAIN_HTTP=true",
		"DNSMASQ_MANAGER_HOSTS_DIR=" + t.TempDir(),
	}
	return append(env, extra...)
}

// without returns env with every entry for the variable key removed.
func without(env []string, key string) []string {
	return slices.DeleteFunc(slices.Clone(env), func(kv string) bool {
		return strings.HasPrefix(kv, key+"=")
	})
}

// writeFile writes content to a new file in a temporary directory with mode
// perm and returns its path.
func writeFile(t *testing.T, content string, perm os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, perm); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMinimal(t *testing.T) {
	env := minimal(t)
	dir := strings.TrimPrefix(env[3], "DNSMASQ_MANAGER_HOSTS_DIR=")

	got, err := config.Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := config.Config{
		Policy: records.Policy{
			Zones: []string{"home.example.test"},
			Prefixes: []netip.Prefix{
				netip.MustParsePrefix("10.0.0.0/8"),
				netip.MustParsePrefix("172.16.0.0/12"),
				netip.MustParsePrefix("192.168.0.0/16"),
			},
		},
		HostsDir:              dir,
		DNSProbeAddr:          netip.MustParseAddrPort("127.0.0.1:53"),
		APIAddr:               netip.MustParseAddrPort("127.0.0.1:8443"),
		APIToken:              token,
		APIPlainHTTP:          true,
		WebhookEnabled:        true,
		WebhookAddr:           netip.MustParseAddrPort("127.0.0.1:8888"),
		WebhookAllowedSources: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		WebEnabled:            true,
		WebAddr:               netip.MustParseAddrPort("127.0.0.1:80"),
		LogLevel:              slog.LevelInfo,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load =\n%+v\nwant\n%+v", got, want)
	}
}

func TestLoadReportsEveryProblem(t *testing.T) {
	_, err := config.Load([]string{"DNSMASQ_MANAGER_ZONE=x"})
	if err == nil {
		t.Fatal("Load: err = nil, want an error")
	}
	msg := err.Error()
	for _, want := range []string{
		"DNSMASQ_MANAGER_ZONES",
		"DNSMASQ_MANAGER_API_TOKEN",
		"DNSMASQ_MANAGER_API_TOKEN_FILE",
		"DNSMASQ_MANAGER_API_PLAIN_HTTP",
		"DNSMASQ_MANAGER_API_TLS_CERT",
		"DNSMASQ_MANAGER_ZONE:",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("err = %q, want it to mention %s", msg, want)
		}
	}
	for line := range strings.SplitSeq(msg, "\n") {
		if !strings.HasPrefix(line, "DNSMASQ_MANAGER_") {
			t.Errorf("error line %q does not start with the variable name", line)
		}
	}
}

func TestLoadErrors(t *testing.T) {
	plain := "DNSMASQ_MANAGER_API_PLAIN_HTTP"
	tests := []struct {
		name    string
		env     func(t *testing.T) []string
		wantErr string
	}{
		{
			name: "both token variables",
			env: func(t *testing.T) []string {
				return minimal(t, "DNSMASQ_MANAGER_API_TOKEN_FILE="+writeFile(t, token, 0o600))
			},
			wantErr: "DNSMASQ_MANAGER_API_TOKEN",
		},
		{
			name: "neither token variable",
			env: func(t *testing.T) []string {
				return without(minimal(t), "DNSMASQ_MANAGER_API_TOKEN")
			},
			wantErr: "DNSMASQ_MANAGER_API_TOKEN",
		},
		{
			name: "31-character token",
			env: func(t *testing.T) []string {
				env := without(minimal(t), "DNSMASQ_MANAGER_API_TOKEN")
				return append(env, "DNSMASQ_MANAGER_API_TOKEN="+strings.Repeat("x", 31))
			},
			wantErr: "DNSMASQ_MANAGER_API_TOKEN: ",
		},
		{
			name: "token with a trailing space",
			env: func(t *testing.T) []string {
				return append(without(minimal(t), "DNSMASQ_MANAGER_API_TOKEN"), "DNSMASQ_MANAGER_API_TOKEN="+token+" ")
			},
			wantErr: "DNSMASQ_MANAGER_API_TOKEN: ",
		},
		{
			name: "token with a leading tab",
			env: func(t *testing.T) []string {
				return append(without(minimal(t), "DNSMASQ_MANAGER_API_TOKEN"), "DNSMASQ_MANAGER_API_TOKEN=\t"+token)
			},
			wantErr: "DNSMASQ_MANAGER_API_TOKEN: ",
		},
		{
			name: "token with an inner space",
			env: func(t *testing.T) []string {
				return append(without(minimal(t), "DNSMASQ_MANAGER_API_TOKEN"), "DNSMASQ_MANAGER_API_TOKEN="+token[:16]+" "+token[16:])
			},
			wantErr: "DNSMASQ_MANAGER_API_TOKEN: ",
		},
		{
			name: "token with a control character",
			env: func(t *testing.T) []string {
				return append(without(minimal(t), "DNSMASQ_MANAGER_API_TOKEN"), "DNSMASQ_MANAGER_API_TOKEN="+token+"\x01")
			},
			wantErr: "DNSMASQ_MANAGER_API_TOKEN: ",
		},
		{
			name: "token with a no-break space",
			env: func(t *testing.T) []string {
				return append(without(minimal(t), "DNSMASQ_MANAGER_API_TOKEN"), "DNSMASQ_MANAGER_API_TOKEN="+token+"\u00a0")
			},
			wantErr: "DNSMASQ_MANAGER_API_TOKEN: ",
		},
		{
			name: "token file with a trailing space before the newline",
			env: func(t *testing.T) []string {
				env := without(minimal(t), "DNSMASQ_MANAGER_API_TOKEN")
				return append(env, "DNSMASQ_MANAGER_API_TOKEN_FILE="+writeFile(t, token+" \n", 0o600))
			},
			wantErr: "DNSMASQ_MANAGER_API_TOKEN_FILE: ",
		},
		{
			name: "token file with a leading space",
			env: func(t *testing.T) []string {
				env := without(minimal(t), "DNSMASQ_MANAGER_API_TOKEN")
				return append(env, "DNSMASQ_MANAGER_API_TOKEN_FILE="+writeFile(t, " "+token+"\n", 0o600))
			},
			wantErr: "DNSMASQ_MANAGER_API_TOKEN_FILE: ",
		},
		{
			name: "token file with two lines",
			env: func(t *testing.T) []string {
				env := without(minimal(t), "DNSMASQ_MANAGER_API_TOKEN")
				return append(env, "DNSMASQ_MANAGER_API_TOKEN_FILE="+writeFile(t, token+"\n"+token+"\n", 0o600))
			},
			wantErr: "DNSMASQ_MANAGER_API_TOKEN_FILE: ",
		},
		{
			name: "token file readable by others",
			env: func(t *testing.T) []string {
				env := without(minimal(t), "DNSMASQ_MANAGER_API_TOKEN")
				return append(env, "DNSMASQ_MANAGER_API_TOKEN_FILE="+writeFile(t, token, 0o644))
			},
			wantErr: "DNSMASQ_MANAGER_API_TOKEN_FILE: ",
		},
		{
			name: "missing token file",
			env: func(t *testing.T) []string {
				env := without(minimal(t), "DNSMASQ_MANAGER_API_TOKEN")
				return append(env, "DNSMASQ_MANAGER_API_TOKEN_FILE="+filepath.Join(t.TempDir(), "absent"))
			},
			wantErr: "DNSMASQ_MANAGER_API_TOKEN_FILE: ",
		},
		{
			name: "cert without key",
			env: func(t *testing.T) []string {
				return append(without(minimal(t), plain), "DNSMASQ_MANAGER_API_TLS_CERT=/etc/ssl/cert.pem")
			},
			wantErr: "DNSMASQ_MANAGER_API_TLS_KEY",
		},
		{
			name: "cert pair and plain HTTP",
			env: func(t *testing.T) []string {
				return minimal(t,
					"DNSMASQ_MANAGER_API_TLS_CERT=/etc/ssl/cert.pem",
					"DNSMASQ_MANAGER_API_TLS_KEY=/etc/ssl/key.pem")
			},
			wantErr: plain,
		},
		{
			name: "neither cert pair nor plain HTTP",
			env: func(t *testing.T) []string {
				return without(minimal(t), plain)
			},
			wantErr: plain,
		},
		{
			name: "host name address",
			env: func(t *testing.T) []string {
				return minimal(t, "DNSMASQ_MANAGER_API_ADDR=localhost:8443")
			},
			wantErr: "DNSMASQ_MANAGER_API_ADDR: ",
		},
		{
			name: "IPv4 wildcard without opt-in",
			env: func(t *testing.T) []string {
				return minimal(t, "DNSMASQ_MANAGER_API_ADDR=0.0.0.0:8443")
			},
			wantErr: "DNSMASQ_MANAGER_API_ADDR: ",
		},
		{
			name: "IPv6 wildcard without opt-in",
			env: func(t *testing.T) []string {
				return minimal(t, "DNSMASQ_MANAGER_WEB_ADDR=[::]:8443")
			},
			wantErr: "DNSMASQ_MANAGER_WEB_ADDR: ",
		},
		{
			name: "two listeners on one address",
			env: func(t *testing.T) []string {
				return minimal(t, "DNSMASQ_MANAGER_WEB_ADDR=127.0.0.1:8888")
			},
			wantErr: "DNSMASQ_MANAGER_WEB_ADDR: ",
		},
		{
			name: "IPv4-mapped wildcard without opt-in",
			env: func(t *testing.T) []string {
				return minimal(t, "DNSMASQ_MANAGER_WEB_ADDR=[::ffff:0.0.0.0]:8080")
			},
			wantErr: "DNSMASQ_MANAGER_WEB_ADDR: ",
		},
		{
			name: "IPv4-mapped address of another listener",
			env: func(t *testing.T) []string {
				return minimal(t, "DNSMASQ_MANAGER_WEB_ADDR=[::ffff:127.0.0.1]:8888")
			},
			wantErr: "DNSMASQ_MANAGER_WEB_ADDR: ",
		},
		{
			name: "IPv4-mapped allowed prefix",
			env: func(t *testing.T) []string {
				return minimal(t, "DNSMASQ_MANAGER_ALLOWED_CIDRS=::ffff:10.0.0.0/120")
			},
			wantErr: "DNSMASQ_MANAGER_ALLOWED_CIDRS: ",
		},
		{
			name: "IPv4-mapped webhook source",
			env: func(t *testing.T) []string {
				return minimal(t, "DNSMASQ_MANAGER_WEBHOOK_ALLOWED_SOURCES=::ffff:127.0.0.1")
			},
			wantErr: "DNSMASQ_MANAGER_WEBHOOK_ALLOWED_SOURCES: ",
		},
		{
			name: "IPv4-mapped webhook source prefix",
			env: func(t *testing.T) []string {
				return minimal(t, "DNSMASQ_MANAGER_WEBHOOK_ALLOWED_SOURCES=::ffff:10.0.0.0/120")
			},
			wantErr: "DNSMASQ_MANAGER_WEBHOOK_ALLOWED_SOURCES: ",
		},
		{
			name: "zone label of 64 characters",
			env: func(t *testing.T) []string {
				return append(without(minimal(t), "DNSMASQ_MANAGER_ZONES"), "DNSMASQ_MANAGER_ZONES="+strings.Repeat("a", 64)+".example.test")
			},
			wantErr: "DNSMASQ_MANAGER_ZONES: ",
		},
		{
			name: "zone of 254 characters",
			env: func(t *testing.T) []string {
				zone := strings.Repeat(strings.Repeat("a", 49)+".", 5) + "test"
				return append(without(minimal(t), "DNSMASQ_MANAGER_ZONES"), "DNSMASQ_MANAGER_ZONES="+zone)
			},
			wantErr: "DNSMASQ_MANAGER_ZONES: ",
		},
		{
			name: "one-label zone",
			env: func(t *testing.T) []string {
				return append(without(minimal(t), "DNSMASQ_MANAGER_ZONES"), "DNSMASQ_MANAGER_ZONES=test")
			},
			wantErr: "DNSMASQ_MANAGER_ZONES: ",
		},
		{
			name: "invalid zone label",
			env: func(t *testing.T) []string {
				return append(without(minimal(t), "DNSMASQ_MANAGER_ZONES"), "DNSMASQ_MANAGER_ZONES=home_lan.example.test")
			},
			wantErr: "DNSMASQ_MANAGER_ZONES: ",
		},
		{
			name: "prefix length out of range",
			env: func(t *testing.T) []string {
				return minimal(t, "DNSMASQ_MANAGER_ALLOWED_CIDRS=10.0.0.0/33")
			},
			wantErr: "DNSMASQ_MANAGER_ALLOWED_CIDRS: ",
		},
		{
			name: "unparsable webhook source",
			env: func(t *testing.T) []string {
				return minimal(t, "DNSMASQ_MANAGER_WEBHOOK_ALLOWED_SOURCES=nope")
			},
			wantErr: "DNSMASQ_MANAGER_WEBHOOK_ALLOWED_SOURCES: ",
		},
		{
			name: "boolean spelled yes",
			env: func(t *testing.T) []string {
				return append(without(minimal(t), plain), plain+"=yes")
			},
			wantErr: plain + ": ",
		},
		{
			name: "unknown log level",
			env: func(t *testing.T) []string {
				return minimal(t, "DNSMASQ_MANAGER_LOG_LEVEL=loud")
			},
			wantErr: "DNSMASQ_MANAGER_LOG_LEVEL: ",
		},
		{
			name: "hosts dir is a file",
			env: func(t *testing.T) []string {
				env := without(minimal(t), "DNSMASQ_MANAGER_HOSTS_DIR")
				return append(env, "DNSMASQ_MANAGER_HOSTS_DIR="+writeFile(t, "", 0o600))
			},
			wantErr: "DNSMASQ_MANAGER_HOSTS_DIR: ",
		},
		{
			name: "hosts dir missing",
			env: func(t *testing.T) []string {
				env := without(minimal(t), "DNSMASQ_MANAGER_HOSTS_DIR")
				return append(env, "DNSMASQ_MANAGER_HOSTS_DIR="+filepath.Join(t.TempDir(), "absent"))
			},
			wantErr: "DNSMASQ_MANAGER_HOSTS_DIR: ",
		},
		{
			name: "DNS probe host name",
			env: func(t *testing.T) []string {
				return minimal(t, "DNSMASQ_MANAGER_DNS_PROBE_ADDR=localhost:53")
			},
			wantErr: "DNSMASQ_MANAGER_DNS_PROBE_ADDR: ",
		},
		{
			name: "unknown variable",
			env: func(t *testing.T) []string {
				return minimal(t, "DNSMASQ_MANAGER_ZONE=home.example.test")
			},
			wantErr: "DNSMASQ_MANAGER_ZONE: ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load(tt.env(t))
			if err == nil {
				t.Fatalf("Load: err = nil, want one mentioning %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Load: err = %q, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadZones(t *testing.T) {
	env := append(without(minimal(t), "DNSMASQ_MANAGER_ZONES"),
		"DNSMASQ_MANAGER_ZONES=Home.Example.Test., lab.example.test")

	got, err := config.Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"home.example.test", "lab.example.test"}
	if !slices.Equal(got.Policy.Zones, want) {
		t.Errorf("Zones = %q, want %q", got.Policy.Zones, want)
	}
}

func TestLoadSettings(t *testing.T) {
	cert := "/etc/ssl/cert.pem"
	key := "/etc/ssl/key.pem"
	env := append(without(minimal(t), "DNSMASQ_MANAGER_API_PLAIN_HTTP"),
		"DNSMASQ_MANAGER_API_TLS_CERT="+cert,
		"DNSMASQ_MANAGER_API_TLS_KEY="+key,
		"DNSMASQ_MANAGER_API_ADDR=10.0.0.53:9443",
		"DNSMASQ_MANAGER_DNS_PROBE_ADDR=10.0.0.53:53",
		"DNSMASQ_MANAGER_ALLOWED_CIDRS=10.0.0.0/24,fd00:10::/64",
		"DNSMASQ_MANAGER_WEBHOOK_ALLOWED_SOURCES=10.0.0.10,fd00:10::10,10.0.0.128/25",
		"DNSMASQ_MANAGER_WEB_ADDR=127.0.0.1:8080",
		"DNSMASQ_MANAGER_LOG_LEVEL=debug",
	)

	got, err := config.Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.APITLSCert != cert || got.APITLSKey != key || got.APIPlainHTTP {
		t.Errorf("TLS = %q, %q, plain %v; want %q, %q, plain false", got.APITLSCert, got.APITLSKey, got.APIPlainHTTP, cert, key)
	}
	if want := netip.MustParseAddrPort("10.0.0.53:9443"); got.APIAddr != want {
		t.Errorf("APIAddr = %v, want %v", got.APIAddr, want)
	}
	if want := netip.MustParseAddrPort("10.0.0.53:53"); got.DNSProbeAddr != want {
		t.Errorf("DNSProbeAddr = %v, want %v", got.DNSProbeAddr, want)
	}
	wantPrefixes := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24"), netip.MustParsePrefix("fd00:10::/64")}
	if !slices.Equal(got.Policy.Prefixes, wantPrefixes) {
		t.Errorf("Prefixes = %v, want %v", got.Policy.Prefixes, wantPrefixes)
	}
	wantSources := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.10/32"),
		netip.MustParsePrefix("fd00:10::10/128"),
		netip.MustParsePrefix("10.0.0.128/25"),
	}
	if !slices.Equal(got.WebhookAllowedSources, wantSources) {
		t.Errorf("WebhookAllowedSources = %v, want %v", got.WebhookAllowedSources, wantSources)
	}
	if want := netip.MustParseAddrPort("127.0.0.1:8080"); got.WebAddr != want {
		t.Errorf("WebAddr = %v, want %v", got.WebAddr, want)
	}
	if got.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want debug", got.LogLevel)
	}
}

func TestLoadOff(t *testing.T) {
	env := minimal(t,
		"DNSMASQ_MANAGER_WEBHOOK_ADDR=off",
		"DNSMASQ_MANAGER_WEB_ADDR=off",
	)

	got, err := config.Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.WebhookEnabled || got.WebEnabled {
		t.Errorf("WebhookEnabled = %v, WebEnabled = %v; want both false", got.WebhookEnabled, got.WebEnabled)
	}
	if got.WebhookAddr.IsValid() || got.WebAddr.IsValid() {
		t.Errorf("WebhookAddr = %v, WebAddr = %v; want both zero", got.WebhookAddr, got.WebAddr)
	}
}

func TestLoadZeroPortsNeverClash(t *testing.T) {
	env := minimal(t,
		"DNSMASQ_MANAGER_API_ADDR=127.0.0.1:0",
		"DNSMASQ_MANAGER_WEBHOOK_ADDR=127.0.0.1:0",
		"DNSMASQ_MANAGER_WEB_ADDR=127.0.0.1:0",
	)

	got, err := config.Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	zero := netip.MustParseAddrPort("127.0.0.1:0")
	if got.APIAddr != zero || got.WebhookAddr != zero || got.WebAddr != zero {
		t.Errorf("addresses = %v, %v, %v; want all %v", got.APIAddr, got.WebhookAddr, got.WebAddr, zero)
	}
}

func TestLoadTokenFile(t *testing.T) {
	env := append(without(minimal(t), "DNSMASQ_MANAGER_API_TOKEN"),
		"DNSMASQ_MANAGER_API_TOKEN_FILE="+writeFile(t, token+"\n", 0o600))

	got, err := config.Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.APIToken != token {
		t.Errorf("APIToken = %q, want %q", got.APIToken, token)
	}
}

func TestLoadWildcardOptIn(t *testing.T) {
	env := minimal(t,
		"DNSMASQ_MANAGER_ALLOW_WILDCARD_BIND=true",
		"DNSMASQ_MANAGER_API_ADDR=0.0.0.0:8443",
		"DNSMASQ_MANAGER_WEB_ADDR=[::]:8080",
	)

	got, err := config.Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := netip.MustParseAddrPort("0.0.0.0:8443"); got.APIAddr != want {
		t.Errorf("APIAddr = %v, want %v", got.APIAddr, want)
	}
	if want := netip.MustParseAddrPort("[::]:8080"); got.WebAddr != want {
		t.Errorf("WebAddr = %v, want %v", got.WebAddr, want)
	}
}

func TestLoadTokenFileCRLF(t *testing.T) {
	env := append(without(minimal(t), "DNSMASQ_MANAGER_API_TOKEN"),
		"DNSMASQ_MANAGER_API_TOKEN_FILE="+writeFile(t, token+"\r\n", 0o600))

	got, err := config.Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.APIToken != token {
		t.Errorf("APIToken = %q, want %q", got.APIToken, token)
	}
}

func TestLoadUnmapsIPv4MappedAddresses(t *testing.T) {
	env := minimal(t,
		"DNSMASQ_MANAGER_API_ADDR=[::ffff:10.0.0.53]:9443",
		"DNSMASQ_MANAGER_WEBHOOK_ADDR=[::ffff:127.0.0.1]:8888",
		"DNSMASQ_MANAGER_WEB_ADDR=[::ffff:127.0.0.1]:8080",
		"DNSMASQ_MANAGER_DNS_PROBE_ADDR=[::ffff:127.0.0.1]:53",
	)

	got, err := config.Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, c := range []struct {
		name      string
		got, want netip.AddrPort
	}{
		{"APIAddr", got.APIAddr, netip.MustParseAddrPort("10.0.0.53:9443")},
		{"WebhookAddr", got.WebhookAddr, netip.MustParseAddrPort("127.0.0.1:8888")},
		{"WebAddr", got.WebAddr, netip.MustParseAddrPort("127.0.0.1:8080")},
		{"DNSProbeAddr", got.DNSProbeAddr, netip.MustParseAddrPort("127.0.0.1:53")},
	} {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestLoadIPv4MappedWildcardOptIn(t *testing.T) {
	env := minimal(t,
		"DNSMASQ_MANAGER_ALLOW_WILDCARD_BIND=true",
		"DNSMASQ_MANAGER_WEB_ADDR=[::ffff:0.0.0.0]:8080",
	)

	got, err := config.Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := netip.MustParseAddrPort("0.0.0.0:8080"); got.WebAddr != want {
		t.Errorf("WebAddr = %v, want %v", got.WebAddr, want)
	}
}

func TestLoadIgnoresOtherVariables(t *testing.T) {
	env := minimal(t, "PATH=/usr/bin:/bin", "HOME=/home/someone", "NOEQUALS")

	if _, err := config.Load(env); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestLoadToleratesInstallerVariables(t *testing.T) {
	env := minimal(t,
		"DNSMASQ_MANAGER_VERSION=v0.1.0",
		"DNSMASQ_MANAGER_BASE_URL=https://example.test/releases",
		"DNSMASQ_MANAGER_INSTALL_DIR=/usr/local/bin",
		"DNSMASQ_MANAGER_OS_RELEASE=/etc/os-release",
	)

	if _, err := config.Load(env); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestLoadStillRefusesNearInstallerNames(t *testing.T) {
	_, err := config.Load(minimal(t, "DNSMASQ_MANAGER_INSTALL_DIRS=/usr/local/bin"))
	if err == nil || !strings.Contains(err.Error(), "DNSMASQ_MANAGER_INSTALL_DIRS: unknown variable") {
		t.Fatalf("Load: err = %v, want DNSMASQ_MANAGER_INSTALL_DIRS reported as unknown", err)
	}
}

// installerVariables returns every DNSMASQ_MANAGER_* name install.sh, at the
// repository root, reads with a ${NAME...} expansion.
func installerVariables(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range regexp.MustCompile(`\$\{(DNSMASQ_MANAGER_[A-Z0-9_]+)`).FindAllStringSubmatch(string(b), -1) {
		if !slices.Contains(names, m[1]) {
			names = append(names, m[1])
		}
	}
	if len(names) == 0 {
		t.Fatal("install.sh reads no DNSMASQ_MANAGER_* variable; the pattern no longer matches it")
	}
	return names
}

func TestLoadToleratesEveryVariableInstallShReads(t *testing.T) {
	for _, name := range installerVariables(t) {
		t.Run(name, func(t *testing.T) {
			if _, err := config.Load(minimal(t, name+"=x")); err != nil {
				t.Errorf("Load with %s set: %v", name, err)
			}
		})
	}
}
