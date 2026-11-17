// Package version holds the build version of dnsmasq-manager.
//
// The Makefile's build target overrides Version at link time with
// -ldflags "-X"; an unstamped build reports "dev".
package version

// Version is the build version, set at link time by the Makefile.
var Version = "dev"

// String returns the build version.
func String() string {
	return Version
}
