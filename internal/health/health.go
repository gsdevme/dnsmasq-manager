// Package health runs the two checks behind GET /healthz: that the hosts
// directory exists and accepts new files, and that dnsmasq answers a DNS query
// on its probe address.
package health

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// defaultTimeout bounds the dnsmasq probe when Checker.Timeout is zero.
const defaultTimeout = time.Second

// probeFilePattern names the file the writability check creates. The leading
// dot matters: dnsmasq ignores dotfiles in its hosts directory, so the check
// never triggers an inotify reread. It shares the ".dnsmasq-manager.hosts."
// prefix of the hostsfile package's temporary files, so a probe file left by
// a crash is removed by the start-up cleanup like any other leftover.
const probeFilePattern = ".dnsmasq-manager.hosts.health-*"

// Checker holds what the health checks need. The zero Timeout means one
// second. A Checker is safe for concurrent use.
type Checker struct {
	// HostsDir is the dnsmasq hosts directory the service writes to.
	HostsDir string
	// ProbeAddr is the host:port dnsmasq listens on for DNS over UDP.
	ProbeAddr string
	// Timeout bounds the dnsmasq probe; zero means one second.
	Timeout time.Duration
}

// Result is the outcome of one Check. A nil field means that check passed;
// otherwise it holds the reason it failed.
type Result struct {
	HostsDir error
	Dnsmasq  error
}

// OK reports whether every check passed.
func (r Result) OK() bool {
	return r.HostsDir == nil && r.Dnsmasq == nil
}

// Check runs both checks and returns their outcome. The probe stops at the
// earlier of the Checker's Timeout and the end of ctx. The hosts directory
// check creates and removes one dot-prefixed file, so it leaves nothing behind.
func (c Checker) Check(ctx context.Context) Result {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return Result{
		HostsDir: checkHostsDir(c.HostsDir),
		Dnsmasq:  probe(ctx, c.ProbeAddr, timeout),
	}
}

// checkHostsDir reports whether dir is a directory in which the process can
// create a file, by creating one and removing it again. Creating a real file
// covers permission bits, ACLs and read-only mounts alike on Linux and macOS.
func checkHostsDir(dir string) error {
	if dir == "" {
		return errors.New("hosts directory not set")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	f, err := os.CreateTemp(dir, probeFilePattern)
	if err != nil {
		return fmt.Errorf("cannot create files in %s: %w", dir, err)
	}
	name := f.Name()
	closeErr := f.Close()
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("cannot remove %s: %w", name, err)
	}
	if closeErr != nil {
		return fmt.Errorf("cannot close %s: %w", name, closeErr)
	}
	return nil
}
