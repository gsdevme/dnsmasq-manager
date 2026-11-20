package health_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsdevme/dnsmasq-manager/internal/health"
)

// countingDNS starts a fake dnsmasq that answers every query after delay and
// returns its address and the number of queries it has received.
func countingDNS(t *testing.T, delay time.Duration) (string, *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	addr, _ := fakeDNS(t, func(q []byte) []byte {
		n.Add(1)
		time.Sleep(delay)
		return answer(q, 0)
	})
	return addr, &n
}

func TestCachedSharesOneCheck(t *testing.T) {
	addr, queries := countingDNS(t, 50*time.Millisecond)
	c := health.NewCached(health.Checker{HostsDir: t.TempDir(), ProbeAddr: addr}, time.Minute)

	const callers = 20
	start := make(chan struct{})
	results := make([]health.Result, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() {
			<-start
			results[i] = c.Check(context.Background())
		})
	}
	close(start)
	wg.Wait()

	for i, r := range results {
		if !r.OK() {
			t.Errorf("caller %d: Result = %+v, want OK", i, r)
		}
	}
	if !c.Check(context.Background()).OK() {
		t.Error("cached Result is not OK")
	}
	if got := queries.Load(); got != 1 {
		t.Errorf("dnsmasq got %d queries, want 1", got)
	}
}

func TestCachedChecksAgainAfterTTL(t *testing.T) {
	addr, queries := countingDNS(t, 0)
	c := health.NewCached(health.Checker{HostsDir: t.TempDir(), ProbeAddr: addr}, 50*time.Millisecond)

	c.Check(context.Background())
	c.Check(context.Background())
	if got := queries.Load(); got != 1 {
		t.Fatalf("dnsmasq got %d queries within the ttl, want 1", got)
	}
	time.Sleep(100 * time.Millisecond)
	if r := c.Check(context.Background()); !r.OK() {
		t.Errorf("Result = %+v, want OK", r)
	}
	if got := queries.Load(); got != 2 {
		t.Errorf("dnsmasq got %d queries after the ttl, want 2", got)
	}
}

func TestCachedCancelledCallerDoesNotPoison(t *testing.T) {
	addr, queries := countingDNS(t, 100*time.Millisecond)
	c := health.NewCached(health.Checker{HostsDir: t.TempDir(), ProbeAddr: addr}, time.Minute)

	short, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if r := c.Check(short); r.OK() {
		t.Errorf("Result for a caller that gave up = %+v, want failing", r)
	}

	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	c.Check(cancelled)

	if r := c.Check(context.Background()); !r.OK() {
		t.Errorf("Result after a caller gave up = %+v, want OK", r)
	}
	if got := queries.Load(); got != 1 {
		t.Errorf("dnsmasq got %d queries, want 1", got)
	}
}

func TestCachedBoundedByCheckerTimeout(t *testing.T) {
	addr, _ := fakeDNS(t, func([]byte) []byte { return nil })
	c := health.NewCached(health.Checker{HostsDir: t.TempDir(), ProbeAddr: addr, Timeout: 50 * time.Millisecond}, time.Minute)

	start := time.Now()
	r := c.Check(context.Background())
	if r.Dnsmasq == nil {
		t.Errorf("Dnsmasq = nil, want a timeout")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Check took %v, want it bounded by the 50ms Timeout", elapsed)
	}
}
