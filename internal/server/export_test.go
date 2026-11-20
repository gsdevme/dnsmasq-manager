package server

import (
	"fmt"
	"time"
)

// SetReadHeaderTimeout replaces the read-header timeout given to servers
// built by later calls to Bind, and restores the previous value when the
// returned function is called.
func SetReadHeaderTimeout(d time.Duration) (restore func()) {
	previous := readHeaderTimeout
	readHeaderTimeout = d
	return func() { readHeaderTimeout = previous }
}

// CloseListener closes the named listener's socket without telling s, so
// that its Serve sees an accept failure.
func CloseListener(s *Server, name string) error {
	for _, b := range s.bound {
		if b.name == name {
			return b.listener.Close()
		}
	}
	return fmt.Errorf("no listener %q", name)
}
