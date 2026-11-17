package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gsdevme/dnsmasq-manager/internal/cli"
)

// execute runs the root command with args and returns what it wrote to
// stdout and stderr, and the error Execute returned.
func execute(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := cli.NewRootCommand(&out, &errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestRootCommand(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantOut     string
		outContains []string
		wantErr     string
	}{
		{name: "version subcommand", args: []string{"version"}, wantOut: "dnsmasq-manager dev\n"},
		{name: "version flag", args: []string{"--version"}, wantOut: "dnsmasq-manager dev\n"},
		{name: "no args prints help", args: nil, outContains: []string{"Usage:", "version"}},
		{name: "unknown command", args: []string{"bogus"}, wantErr: `unknown command "bogus" for "dnsmasq-manager"`},
		{name: "version takes no args", args: []string{"version", "extra"}, wantErr: `unknown command "extra" for "dnsmasq-manager version"`},
		{name: "no -v shorthand", args: []string{"-v"}, wantErr: "unknown shorthand flag: 'v' in -v"},
		{name: "no completion command", args: []string{"completion", "bash"}, wantErr: `unknown command "completion" for "dnsmasq-manager"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := execute(t, tt.args...)

			if stderr != "" {
				t.Errorf("stderr = %q, want empty", stderr)
			}

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("err = nil, want %q", tt.wantErr)
				}
				if got := err.Error(); got != tt.wantErr {
					t.Errorf("err = %q, want %q", got, tt.wantErr)
				}
				if stdout != "" {
					t.Errorf("stdout = %q, want empty", stdout)
				}
				return
			}

			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if tt.wantOut != "" && stdout != tt.wantOut {
				t.Errorf("stdout = %q, want %q", stdout, tt.wantOut)
			}
			for _, sub := range tt.outContains {
				if !strings.Contains(stdout, sub) {
					t.Errorf("stdout = %q, want it to contain %q", stdout, sub)
				}
			}
		})
	}
}
