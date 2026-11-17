package main

import (
	"bytes"
	"testing"
)

func TestRunPrintsVersion(t *testing.T) {
	var out bytes.Buffer
	if code := run(&out); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if got, want := out.String(), "dnsmasq-manager dev\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
