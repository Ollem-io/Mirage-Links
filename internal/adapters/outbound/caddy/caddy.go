// Package caddy implements Mirage's deliberately narrow Caddy Admin API ownership.
//
// The adapter reads the configured HTTP server's route list, but writes only
// routes bearing a Mirage @id.  In particular it never replaces the server,
// its route list, or another controller's route.
package caddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
)

const Namespace = "mirage-route-"
const defaultServer = "srv0"

// FallbackID is the fixed final route seeded for a managed public listener.
// It is intentionally not an owned link route.
const FallbackID = "mirage-fallback"

// ErrorKind allows callers to make safe retry and HTTP mapping decisions
// without parsing an Admin API response.
type ErrorKind string

const (
	Timeout               ErrorKind = "timeout"
	Unavailable           ErrorKind = "unavailable"
	MalformedResponse     ErrorKind = "malformed_response"
	AdminConflict         ErrorKind = "conflict"
	Rejected              ErrorKind = "rejected"
	RollbackIncomplete    ErrorKind = "rollback_incomplete"
	RollbackIndeterminate ErrorKind = "rollback_indeterminate"
)

type Error struct {
	Kind ErrorKind
	Err  error
}

func (e *Error) Error() string { return "caddy admin " + string(e.Kind) + ": " + e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }
func IsKind(err error, kind ErrorKind) bool {
	var target *Error
	return errors.As(err, &target) && target.Kind == kind
}

// Config selects the pre-existing Caddy HTTP server whose routes Mirage may
// manage. AdminURL must be an HTTP(S) URL. RetryAttempts includes the first
// request; zero selects one attempt.
type Config struct {
	AdminURL      string
	Server        string
	HTTPClient    *http.Client
	Timeout       time.Duration
	RetryAttempts int
	RetryDelay    time.Duration
	// CompensationTimeout bounds rollback and final-state verification after a
	// partially applied reconciliation. Zero selects two seconds.
	CompensationTimeout time.Duration
}

// Client is safe for concurrent use. Its mutex closes local GET/change races;
// a 409 from another admin client remains an explicit typed conflict.
type Client struct {
	base                *url.URL
	server              string
	http                *http.Client
	attempts            int
	delay               time.Duration
	compensationTimeout time.Duration
	mu                  sync.Mutex
}

func New(cfg Config) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(cfg.AdminURL, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, domain.NewValidation("caddy.admin_url", "must be an absolute HTTP URL")
	}
	server := cfg.Server
	if server == "" {
		server = defaultServer
	}
	if strings.Contains(server, "/") {
		return nil, domain.NewValidation("caddy.server", "must not contain a slash")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	if cfg.Timeout > 0 {
		clone := *client
		clone.Timeout = cfg.Timeout
		client = &clone
	}
	attempts := cfg.RetryAttempts
	if attempts <= 0 {
		attempts = 1
	}
	delay := cfg.RetryDelay
	if delay <= 0 {
		delay = 10 * time.Millisecond
	}
	compensationTimeout := cfg.CompensationTimeout
	if compensationTimeout <= 0 {
		compensationTimeout = 2 * time.Second
	}
	return &Client{base: u, server: server, http: client, attempts: attempts, delay: delay, compensationTimeout: compensationTimeout}, nil
}

// RouteFor translates only validated domain values into a public hostname and
// loopback reverse-proxy upstream. It cannot generate a management route.
func RouteFor(id domain.LinkID, base domain.BaseHost, name domain.LinkName, alias domain.Alias, port int) (ports.Route, error) {
	if id == "" {
		return ports.Route{}, domain.NewValidation("link_id", "required")
	}
	if port < 1 || port > 65535 {
		return ports.Route{}, domain.NewValidation("upstream_port", "must be 1 to 65535")
	}
	return ports.Route{LinkID: id, Hostname: base.Host(name, alias), Upstream: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}, nil
}

func routeID(id domain.LinkID) string { return Namespace + string(id) }
func owned(id string) bool            { return strings.HasPrefix(id, Namespace) && len(id) > len(Namespace) }

type caddyRoute struct {
	ID    string `json:"@id"`
	Match []struct {
		Host []string `json:"host"`
	} `json:"match"`
	Handle []struct {
		Handler   string `json:"handler"`
		Upstreams []struct {
			Dial string `json:"dial"`
		} `json:"upstreams"`
	} `json:"handle"`
	Terminal bool `json:"terminal"`
}

func encode(r ports.Route) caddyRoute {
	var x caddyRoute
	x.ID = routeID(r.LinkID)
	x.Match = []struct {
		Host []string `json:"host"`
	}{{Host: []string{r.Hostname}}}
	x.Handle = []struct {
		Handler   string `json:"handler"`
		Upstreams []struct {
			Dial string `json:"dial"`
		} `json:"upstreams"`
	}{{Handler: "reverse_proxy", Upstreams: []struct {
		Dial string `json:"dial"`
	}{{Dial: r.Upstream}}}}
	x.Terminal = true
	return x
}
func decode(x caddyRoute) (ports.Route, bool) {
	if !owned(x.ID) || len(x.Match) != 1 || len(x.Match[0].Host) != 1 || len(x.Handle) != 1 || x.Handle[0].Handler != "reverse_proxy" || len(x.Handle[0].Upstreams) != 1 || x.Handle[0].Upstreams[0].Dial == "" {
		return ports.Route{}, false
	}
	return ports.Route{LinkID: domain.LinkID(strings.TrimPrefix(x.ID, Namespace)), Hostname: x.Match[0].Host[0], Upstream: x.Handle[0].Upstreams[0].Dial}, true
}
func equal(a, b ports.Route) bool {
	return a.LinkID == b.LinkID && a.Hostname == b.Hostname && a.Upstream == b.Upstream
}
func (c *Client) routesPath() string {
	return "/config/apps/http/servers/" + url.PathEscape(c.server) + "/routes"
}

func fallbackIndex(routes []caddyRoute) int {
	for i, route := range routes {
		if route.ID == FallbackID {
			return i
		}
	}
	return len(routes)
}

// Add upserts one owned route. A matching existing route makes no Admin write.
func (c *Client) Add(ctx context.Context, wanted ports.Route) error {
	if err := validRoute(wanted); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	all, err := c.fetch(ctx)
	if err != nil {
		return err
	}
	for i, x := range all {
		if x.ID == routeID(wanted.LinkID) {
			have, ok := decode(x)
			if ok && equal(have, wanted) {
				return nil
			}
			return c.write(ctx, http.MethodPut, c.routesPath()+"/"+strconv.Itoa(i), encode(wanted))
		}
	}
	// A fixed fallback must remain last: insert a new Mirage route immediately
	// before it, without rewriting unrelated route objects.
	return c.write(ctx, http.MethodPost, c.routesPath()+"/"+strconv.Itoa(fallbackIndex(all)), encode(wanted))
}

// Remove deletes exactly one owned route. Unknown or already removed routes are harmless.
func (c *Client) Remove(ctx context.Context, id domain.LinkID) error {
	if id == "" {
		return domain.NewValidation("link_id", "required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	all, err := c.fetch(ctx)
	if err != nil {
		return err
	}
	for i, x := range all {
		if x.ID == routeID(id) {
			return c.write(ctx, http.MethodDelete, c.routesPath()+"/"+strconv.Itoa(i), nil)
		}
	}
	return nil
}

// List returns only well-formed Mirage-owned routes; external config is never exposed as managed state.
func (c *Client) List(ctx context.Context) ([]ports.Route, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	all, err := c.fetch(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ports.Route, 0)
	for _, x := range all {
		if r, ok := decode(x); ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// Reconcile makes Mirage's owned routes exactly desired. It first validates the
// complete desired set and snapshots the route array. Changes have compensating
// inverse operations; on any Admin failure it restores every prior owned route
// at its original array position. It never writes a whole server/config object.
func (c *Client) Reconcile(ctx context.Context, desired []ports.Route) error {
	wanted := map[domain.LinkID]ports.Route{}
	for _, r := range desired {
		if err := validRoute(r); err != nil {
			return err
		}
		if _, duplicate := wanted[r.LinkID]; duplicate {
			return domain.NewConflict("duplicate desired Caddy route")
		}
		wanted[r.LinkID] = r
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	original, err := c.fetchRaw(ctx)
	if err != nil {
		return err
	}
	all := make([]caddyRoute, len(original))
	for i := range original {
		if err := json.Unmarshal(original[i], &all[i]); err != nil {
			return &Error{MalformedResponse, err}
		}
	}

	type change struct{ undo func(context.Context) error }
	undos := make([]change, 0)
	rollback := func(cause error) error {
		// Compensation must survive the caller deadline which commonly caused the
		// failed mutation. It is independently bounded so Reconcile cannot hang.
		compCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.compensationTimeout)
		defer cancel()
		var compensationErr error
		for i := len(undos) - 1; i >= 0; i-- {
			if err := undos[i].undo(compCtx); err != nil && compensationErr == nil {
				compensationErr = err
			}
		}
		current, verifyErr := c.fetchRaw(compCtx)
		if verifyErr != nil {
			return &Error{RollbackIndeterminate, fmt.Errorf("mutation failed: %w; compensation: %v; verification: %v", cause, compensationErr, verifyErr)}
		}
		wantJSON, _ := json.Marshal(original)
		gotJSON, _ := json.Marshal(current)
		if !bytes.Equal(wantJSON, gotJSON) {
			return &Error{RollbackIncomplete, fmt.Errorf("mutation failed: %w; compensation: %v; final route state differs from snapshot", cause, compensationErr)}
		}
		return cause
	}
	// A mutation that is canceled while in flight has an unknowable server-side
	// outcome and can race its own compensation. Execute each mutation on a
	// bounded detached context, while checking the caller immediately before and
	// after it. Thus cancellation prevents a not-yet-started mutation, but an
	// admitted mutation reaches a known terminal outcome before rollback starts.
	mutate := func(method, path string, body any) error {
		if err := ctx.Err(); err != nil {
			return &Error{Timeout, err}
		}
		mutationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.compensationTimeout)
		defer cancel()
		err := c.write(mutationCtx, method, path, body)
		if callerErr := ctx.Err(); callerErr != nil {
			return &Error{Timeout, callerErr}
		}
		return err
	}

	// First repair existing desired records. PUT preserves both route index and
	// every non-owned route byte for byte.
	for i, existing := range all {
		if !owned(existing.ID) {
			continue
		}
		id := domain.LinkID(strings.TrimPrefix(existing.ID, Namespace))
		want, keep := wanted[id]
		if !keep {
			continue
		}
		have, valid := decode(existing)
		if valid && equal(have, want) {
			delete(wanted, id)
			continue
		}
		index, before := i, original[i]
		undos = append(undos, change{func(ctx context.Context) error {
			return c.write(ctx, http.MethodPut, c.routesPath()+"/"+strconv.Itoa(index), before)
		}})
		if err := mutate(http.MethodPut, c.routesPath()+"/"+strconv.Itoa(index), encode(want)); err != nil {
			return rollback(err)
		}
		delete(wanted, id)
	}
	// Then append missing records. Undo finds its namespaced identity rather
	// than relying on a position that another Admin client could have shifted.
	for _, want := range wanted {
		added := want
		undos = append(undos, change{func(ctx context.Context) error {
			current, err := c.fetch(ctx)
			if err != nil {
				return err
			}
			for i, r := range current {
				if r.ID == routeID(added.LinkID) {
					return c.write(ctx, http.MethodDelete, c.routesPath()+"/"+strconv.Itoa(i), nil)
				}
			}
			return nil
		}})
		index := fallbackIndex(all)
		if err := mutate(http.MethodPost, c.routesPath()+"/"+strconv.Itoa(index), encode(added)); err != nil {
			return rollback(err)
		}
		all = append(all, caddyRoute{})
		copy(all[index+1:], all[index:])
		all[index] = encode(added)
	}
	// Finally remove orphans from highest to lowest index. The inverse POST to
	// an array index inserts the exact saved object back at that index.
	for i := len(original) - 1; i >= 0; i-- {
		existing := all[i]
		before := original[i]
		if !owned(existing.ID) {
			continue
		}
		id := domain.LinkID(strings.TrimPrefix(existing.ID, Namespace))
		if _, keep := wanted[id]; keep {
			continue
		} // wanted is empty for retained entries
		// An entry retained earlier was deleted from wanted; distinguish it from
		// an orphan by looking at the original desired IDs.
		retained := false
		for _, d := range desired {
			if d.LinkID == id {
				retained = true
				break
			}
		}
		if retained {
			continue
		}
		index := i
		undos = append(undos, change{func(ctx context.Context) error {
			current, err := c.fetchRaw(ctx)
			if err != nil {
				return err
			}
			for _, raw := range current {
				if bytes.Equal(raw, before) {
					return nil // failed/ambiguous DELETE did not remove it
				}
			}
			return c.write(ctx, http.MethodPost, c.routesPath()+"/"+strconv.Itoa(index), before)
		}})
		if err := mutate(http.MethodDelete, c.routesPath()+"/"+strconv.Itoa(index), nil); err != nil {
			return rollback(err)
		}
	}
	return nil
}
func validRoute(r ports.Route) error {
	if r.LinkID == "" {
		return domain.NewValidation("link_id", "required")
	}
	if strings.TrimSpace(r.Hostname) == "" {
		return domain.NewValidation("hostname", "required")
	}
	host, port, err := net.SplitHostPort(r.Upstream)
	if err != nil || port == "" || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return domain.NewValidation("upstream", "must be a loopback host:port")
	}
	return nil
}
func (c *Client) fetch(ctx context.Context) ([]caddyRoute, error) {
	var out []caddyRoute
	if err := c.request(ctx, http.MethodGet, c.routesPath(), nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		return []caddyRoute{}, nil
	}
	return out, nil
}

// fetchRaw preserves the exact per-route JSON representation used to prove a
// failed reconciliation restored both owned and unrelated routes exactly.
func (c *Client) fetchRaw(ctx context.Context) ([]json.RawMessage, error) {
	var out []json.RawMessage
	if err := c.request(ctx, http.MethodGet, c.routesPath(), nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		return []json.RawMessage{}, nil
	}
	return out, nil
}
func (c *Client) write(ctx context.Context, method, path string, body any) error {
	return c.request(ctx, method, path, body, nil)
}
func (c *Client) request(ctx context.Context, method, path string, body any, out any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return &Error{MalformedResponse, err}
		}
	}
	for n := 0; n < c.attempts; n++ {
		if n > 0 {
			select {
			case <-ctx.Done():
				return &Error{Timeout, ctx.Err()}
			case <-time.After(c.delay):
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, c.base.String()+path, bytes.NewReader(data))
		if err != nil {
			return &Error{MalformedResponse, err}
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return &Error{Timeout, ctx.Err()}
			}
			if n+1 < c.attempts {
				continue
			}
			return &Error{Unavailable, err}
		}
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		if readErr != nil {
			return &Error{MalformedResponse, readErr}
		}
		if res.StatusCode >= 200 && res.StatusCode < 300 {
			if out != nil && len(raw) > 0 {
				if err := json.Unmarshal(raw, out); err != nil {
					return &Error{MalformedResponse, err}
				}
			}
			return nil
		}
		e := fmt.Errorf("status %d: %s", res.StatusCode, strings.TrimSpace(string(raw)))
		if res.StatusCode == http.StatusConflict {
			return &Error{AdminConflict, e}
		}
		if res.StatusCode >= 500 && n+1 < c.attempts {
			continue
		}
		return &Error{Rejected, e}
	}
	return &Error{Unavailable, errors.New("retry attempts exhausted")}
}

var _ ports.Proxy = (*Client)(nil)
