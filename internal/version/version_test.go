package version_test

import (
	"testing"

	"github.com/gsdevme/dnsmasq-manager/internal/version"
)

func TestStringDefaultsToDev(t *testing.T) {
	if got := version.String(); got != "dev" {
		t.Fatalf("String() = %q, want %q", got, "dev")
	}
}
