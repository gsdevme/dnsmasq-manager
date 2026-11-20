// Command dnsmasq-manager serves an HTTP API, an external-dns webhook provider
// and a read-only web page for LAN DNS records held in dnsmasq. Its serve
// command runs until SIGINT or SIGTERM; the command tree is built in
// internal/cli.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/gsdevme/dnsmasq-manager/internal/cli"
)

// main runs the command line with a context cancelled by SIGINT or SIGTERM,
// then exits with the code run returns.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run executes the command line args against the root command with ctx,
// whose cancellation stops serve, and returns the process exit code: 0 on
// success, 1 on any error. It is the only place an error is printed, as
// "dnsmasq-manager: <error>" on stderr.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	root := cli.NewRootCommand(stdout, stderr)
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "dnsmasq-manager: %v\n", err)
		return 1
	}
	return 0
}
