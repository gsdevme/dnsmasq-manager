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

// TestHostsDirUpdateKeepsOldAddressUntilReload checks that changing a
// record's address in the hosts directory adds the new address without a
// SIGHUP but keeps serving the old one alongside it until dnsmasq reloads.
func TestHostsDirUpdateKeepsOldAddressUntilReload(t *testing.T) {
	s := startDnsmasq(t)
	s.writeHosts(t, "10.0.0.80 app.home.example.test\n")
	s.eventually(t, "app.home.example.test", "10.0.0.80")
	s.writeHosts(t, "10.0.0.81 app.home.example.test\n")
	s.eventually(t, "app.home.example.test", "10.0.0.80", "10.0.0.81")
	s.reload(t)
	s.eventually(t, "app.home.example.test", "10.0.0.81")
}

// TestHostsDirRemovalNeedsReload checks that removing a record from the hosts
// directory leaves it served until dnsmasq receives a SIGHUP.
func TestHostsDirRemovalNeedsReload(t *testing.T) {
	s := startDnsmasq(t)
	s.writeHosts(t, "10.0.0.80 app.home.example.test\n")
	s.eventually(t, "app.home.example.test", "10.0.0.80")
	s.writeHosts(t, "")
	time.Sleep(2 * time.Second)
	if got := s.lookup(t, "app.home.example.test"); !slices.Equal(got, []string{"10.0.0.80"}) {
		t.Fatalf("before reload = %v, want the stale record [10.0.0.80]", got)
	}
	s.reload(t)
	s.eventually(t, "app.home.example.test", "10.0.0.17")
}
