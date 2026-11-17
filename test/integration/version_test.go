//go:build integration

package integration

import "testing"

// TestHostsDirFlushes checks how hostsDirFlushes parses `dnsmasq --version`
// lines and where it puts the 2.88 boundary. It needs neither dnsmasq nor dig.
func TestHostsDirFlushes(t *testing.T) {
	tests := []struct {
		line    string
		want    bool
		wantErr bool
	}{
		{line: "Dnsmasq version 2.85  Copyright (c) 2000-2021 Simon Kelley", want: false},
		{line: "Dnsmasq version 2.87  Copyright (c) 2000-2022 Simon Kelley", want: false},
		{line: "Dnsmasq version 2.88  Copyright (c) 2000-2022 Simon Kelley", want: true},
		{line: "Dnsmasq version 2.91  Copyright (c) 2000-2025 Simon Kelley", want: true},
		{line: "Dnsmasq version 2.92rc1  Copyright (c) 2000-2025 Simon Kelley", want: true},
		{line: "Dnsmasq version 2.100", want: true},
		{line: "Dnsmasq version 2.9", want: false},
		{line: "Dnsmasq version 3.0", want: true},
		{line: "Dnsmasq version 1.99", want: false},
		{line: "", wantErr: true},
		{line: "Dnsmasq version 2", wantErr: true},
		{line: "Dnsmasq version 2.rc1", wantErr: true},
		{line: "Dnsmasq version x.88", wantErr: true},
	}
	for _, tt := range tests {
		got, err := hostsDirFlushes(tt.line)
		if (err != nil) != tt.wantErr {
			t.Errorf("hostsDirFlushes(%q) error = %v, want error %v", tt.line, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("hostsDirFlushes(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}
