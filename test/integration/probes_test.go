//go:build integration

package integration

import (
	"slices"
	"testing"
	"time"
)

// TestMockConfigAnswersLocalNames checks that the mock config answers its
// exact names and wildcards without consulting the unreachable upstreams.
func TestMockConfigAnswersLocalNames(t *testing.T) {
	s := startDnsmasq(t)
	s.eventually(t, "nas.home.example.test", "10.0.0.40")
	s.eventually(t, "anything.home.example.test", "10.0.0.17")
	s.eventually(t, "example.zip", "127.0.0.1")
}

// TestHostsDirRecordBeatsWildcardWithoutReload checks that a record added to
// the hosts directory overrides an address= wildcard without a SIGHUP.
func TestHostsDirRecordBeatsWildcardWithoutReload(t *testing.T) {
	s := startDnsmasq(t)
	s.eventually(t, "app.home.example.test", "10.0.0.17")
	s.writeHosts(t, "10.0.0.80 app.home.example.test\n")
	s.eventually(t, "app.home.example.test", "10.0.0.80")
}

// TestHostsDirUpdateDropsOldAddress checks when dnsmasq stops serving a
// record's old address after the hosts directory changes it. From 2.88 the
// new address replaces the old one without a SIGHUP. Before 2.88 the new
// address is added but the old one is kept alongside it until SIGHUP. On
// every version a SIGHUP afterwards leaves only the new address.
func TestHostsDirUpdateDropsOldAddress(t *testing.T) {
	s := startDnsmasq(t)
	s.writeHosts(t, "10.0.0.80 app.home.example.test\n")
	s.eventually(t, "app.home.example.test", "10.0.0.80")
	s.writeHosts(t, "10.0.0.81 app.home.example.test\n")
	if wantHostsDirFlush(t) {
		s.eventually(t, "app.home.example.test", "10.0.0.81")
	} else {
		s.eventually(t, "app.home.example.test", "10.0.0.80", "10.0.0.81")
	}
	s.reload(t)
	s.eventually(t, "app.home.example.test", "10.0.0.81")
}

// TestHostsDirRemovalDropsRecord checks when dnsmasq stops serving a record
// removed from the hosts directory. From 2.88 the name falls back to the
// wildcard without a SIGHUP. Before 2.88 the removed record is still served
// two seconds later and only goes on SIGHUP. On every version a SIGHUP
// afterwards leaves the wildcard answer.
func TestHostsDirRemovalDropsRecord(t *testing.T) {
	s := startDnsmasq(t)
	s.writeHosts(t, "10.0.0.80 app.home.example.test\n")
	s.eventually(t, "app.home.example.test", "10.0.0.80")
	s.writeHosts(t, "")
	if wantHostsDirFlush(t) {
		s.eventually(t, "app.home.example.test", "10.0.0.17")
	} else {
		time.Sleep(2 * time.Second)
		if got := s.lookup(t, "app.home.example.test"); !slices.Equal(got, []string{"10.0.0.80"}) {
			t.Fatalf("before reload = %v, want the stale record [10.0.0.80]", got)
		}
	}
	s.reload(t)
	s.eventually(t, "app.home.example.test", "10.0.0.17")
}
