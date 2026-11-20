package health

import (
	"strings"
	"testing"
)

func TestProbeFileSharesTheHostsTempPrefix(t *testing.T) {
	const hostsTempPrefix = ".dnsmasq-manager.hosts."
	if !strings.HasPrefix(probeFilePattern, hostsTempPrefix) {
		t.Errorf("probeFilePattern = %q, want it to start with %q so start-up removes a leftover", probeFilePattern, hostsTempPrefix)
	}
}
