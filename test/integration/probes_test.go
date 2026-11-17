//go:build integration

package integration

import "testing"

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

// TestHostsDirUpdateDropsOldAddress checks that changing a record's address
// in the hosts directory replaces the old address without a SIGHUP, and that
// one second after a SIGHUP the answer is still only the new address.
func TestHostsDirUpdateDropsOldAddress(t *testing.T) {
	s := startDnsmasq(t)
	s.writeHosts(t, "10.0.0.80 app.home.example.test\n")
	s.eventually(t, "app.home.example.test", "10.0.0.80")
	s.writeHosts(t, "10.0.0.81 app.home.example.test\n")
	s.eventually(t, "app.home.example.test", "10.0.0.81")
	s.reloadKeeps(t, "app.home.example.test", "10.0.0.81")
}

// TestHostsDirRemovalDropsRecord checks that removing a record from the hosts
// directory makes the name fall back to the wildcard without a SIGHUP, and
// that one second after a SIGHUP the answer is still the wildcard.
func TestHostsDirRemovalDropsRecord(t *testing.T) {
	s := startDnsmasq(t)
	s.writeHosts(t, "10.0.0.80 app.home.example.test\n")
	s.eventually(t, "app.home.example.test", "10.0.0.80")
	s.writeHosts(t, "")
	s.eventually(t, "app.home.example.test", "10.0.0.17")
	s.reloadKeeps(t, "app.home.example.test", "10.0.0.17")
}
