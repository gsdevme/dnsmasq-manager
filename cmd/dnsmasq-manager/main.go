// Command dnsmasq-manager will serve an HTTP API for LAN DNS records held in
// dnsmasq. For now it prints its build version and exits.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/gsdevme/dnsmasq-manager/internal/version"
)

func main() {
	os.Exit(run(os.Stdout))
}

// run writes the program name and build version to w and returns the
// process exit code: 0 on success, 1 if the write fails.
func run(w io.Writer) int {
	if _, err := fmt.Fprintf(w, "dnsmasq-manager %s\n", version.String()); err != nil {
		return 1
	}
	return 0
}
