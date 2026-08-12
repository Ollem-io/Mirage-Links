// Package health probes validated loopback HTTP services.
package health

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/primeintellect/mirage/internal/domain"
)

// Checker rejects redirects unless every destination remains loopback. Timeout
// bounds each probe; callers implement grace/retry policy with CheckUntil.
type Checker struct{ Client *http.Client }

func New(timeout time.Duration) *Checker {
	c := &http.Client{Timeout: probeTimeout(timeout)}
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !loopback(req.URL) {
			return fmt.Errorf("health redirect leaves loopback")
		}
		return nil
	}
	return &Checker{Client: c}
}
func probeTimeout(d time.Duration) time.Duration {
	if d <= 0 {
		return 2 * time.Second
	}
	return d
}
func loopback(u *url.URL) bool {
	h := u.Hostname()
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
func (c *Checker) Check(ctx context.Context, h domain.HealthCheck) error { // Reparse protects callers constructing HealthCheck directly.
	parsed, err := domain.ParseHealthCheck(string(h.Method) + " " + h.URL)
	if err != nil {
		return err
	}
	u, err := url.Parse(parsed.URL)
	if err != nil || !loopback(u) {
		return fmt.Errorf("invalid loopback health target")
	}
	client := c.Client
	if client == nil {
		client = New(2 * time.Second).Client
	}
	// Always override injected client redirect behavior: an injected transport
	// may be useful in tests, but it may not weaken the loopback boundary.
	copyClient := *client
	copyClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !loopback(req.URL) {
			return fmt.Errorf("health redirect leaves loopback")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, string(parsed.Method), parsed.URL, nil)
	if err != nil {
		return fmt.Errorf("health request: %w", err)
	}
	resp, err := copyClient.Do(req)
	if err != nil {
		return fmt.Errorf("health probe: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("health probe: status %d", resp.StatusCode)
	}
	return nil
}

// CheckUntil probes until healthy, context cancellation, or grace elapsed.
func (c *Checker) CheckUntil(ctx context.Context, h domain.HealthCheck, grace time.Duration) error {
	interval := 100 * time.Millisecond
	deadline := time.NewTimer(grace)
	defer deadline.Stop()
	for {
		if err := c.Check(ctx, h); err == nil {
			return nil
		}
		t := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-deadline.C:
			t.Stop()
			return fmt.Errorf("health grace expired")
		case <-t.C:
		}
	}
}
