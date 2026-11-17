// Command dnsmasq-manager will serve an HTTP API for LAN DNS records held in
// dnsmasq. For now it has one command, version, which prints its build
// version; the command tree is built in internal/cli.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/gsdevme/dnsmasq-manager/internal/cli"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the command line args against the root command and returns
// the process exit code: 0 on success, 1 on any error. It is the only place
// an error is printed, as "dnsmasq-manager: <error>" on stderr.
func run(args []string, stdout, stderr io.Writer) int {
	root := cli.NewRootCommand(stdout, stderr)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		_, _ = fmt.Fprintf(stderr, "dnsmasq-manager: %v\n", err)
		return 1
	}
	return 0
}
