//go:build integration

package integration

import "testing"

// TestCheckMinVersion checks how checkMinVersion parses `dnsmasq --version`
// lines and where it puts the 2.91 minimum. It needs neither dnsmasq nor dig.
func TestCheckMinVersion(t *testing.T) {
	tests := []struct {
		line    string
		wantErr bool
	}{
		{line: "Dnsmasq version 2.85  Copyright (c) 2000-2021 Simon Kelley", wantErr: true},
		{line: "Dnsmasq version 2.90  Copyright (c) 2000-2024 Simon Kelley", wantErr: true},
		{line: "Dnsmasq version 2.91  Copyright (c) 2000-2025 Simon Kelley"},
		{line: "Dnsmasq version 2.92rc1  Copyright (c) 2000-2025 Simon Kelley"},
		{line: "Dnsmasq version 2.90rc1", wantErr: true},
		{line: "Dnsmasq version 2.100"},
		{line: "Dnsmasq version 2.9", wantErr: true},
		{line: "Dnsmasq version 3.0"},
		{line: "Dnsmasq version 1.99", wantErr: true},
		{line: "", wantErr: true},
		{line: "Dnsmasq version 2", wantErr: true},
		{line: "Dnsmasq version 2.rc1", wantErr: true},
		{line: "Dnsmasq version x.91", wantErr: true},
	}
	for _, tt := range tests {
		err := checkMinVersion(tt.line)
		if (err != nil) != tt.wantErr {
			t.Errorf("checkMinVersion(%q) error = %v, want error %v", tt.line, err, tt.wantErr)
		}
	}
}
