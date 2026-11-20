package health

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Cached shares the outcome of a Checker between callers, so that requests
// anyone can send, such as GET /healthz and web page loads, cannot drive the
// hosts directory check and the dnsmasq probe at will.
//
// Concurrent callers share one in-flight check, and its Result is reused
// until ttl has passed since it finished. The check runs on its own
// goroutine with a context detached from the caller's cancellation, so a
// caller that gives up does not fail the check for the others; the
// Checker's Timeout still bounds the probe. A Cached is safe for concurrent
// use and satisfies the HealthChecker interfaces of the api and web
// packages.
type Cached struct {
	checker Checker
	ttl     time.Duration

	mu       sync.Mutex
	result   Result
	at       time.Time
	valid    bool
	inflight chan struct{}
}

// NewCached returns a Cached over c that reuses each Result for ttl.
func NewCached(c Checker, ttl time.Duration) *Cached {
	return &Cached{checker: c, ttl: ttl}
}

// Check returns the cached Result while it is fresh, and otherwise waits for
// a check, starting one unless another caller already has. When ctx ends
// first it returns a failing Result naming ctx's error, and the check carries
// on for the callers still waiting.
func (c *Cached) Check(ctx context.Context) Result {
	c.mu.Lock()
	if c.valid && time.Since(c.at) < c.ttl {
		r := c.result
		c.mu.Unlock()
		return r
	}
	done := c.inflight
	if done == nil {
		done = make(chan struct{})
		c.inflight = done
		go c.run(context.WithoutCancel(ctx), done)
	}
	c.mu.Unlock()

	select {
	case <-done:
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.result
	case <-ctx.Done():
		err := fmt.Errorf("health check abandoned: %w", ctx.Err())
		return Result{HostsDir: err, Dnsmasq: err}
	}
}

// run performs one check, stores its Result and closes done.
func (c *Cached) run(ctx context.Context, done chan struct{}) {
	r := c.checker.Check(ctx)
	c.mu.Lock()
	c.result, c.at, c.valid, c.inflight = r, time.Now(), true, nil
	c.mu.Unlock()
	close(done)
}
