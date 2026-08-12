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

// ErrorKind allows callers to make safe retry and HTTP mapping decisions
// without parsing an Admin API response.
type ErrorKind string

const (
	Timeout           ErrorKind = "timeout"
	Unavailable       ErrorKind = "unavailable"
	MalformedResponse ErrorKind = "malformed_response"
	AdminConflict     ErrorKind = "conflict"
	Rejected          ErrorKind = "rejected"
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
}

// Client is safe for concurrent use. Its mutex closes local GET/change races;
// a 409 from another admin client remains an explicit typed conflict.
type Client struct {
	base     *url.URL
	server   string
	http     *http.Client
	attempts int
	delay    time.Duration
	mu       sync.Mutex
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
	return &Client{base: u, server: server, http: client, attempts: attempts, delay: delay}, nil
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
	return c.write(ctx, http.MethodPost, c.routesPath(), encode(wanted))
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

// Reconcile upserts desired routes and deletes orphan Mirage routes. It makes no
// write for malformed/unrelated routes, preserving them exactly as received.
func (c *Client) Reconcile(ctx context.Context, desired []ports.Route) error {
	wanted := map[domain.LinkID]ports.Route{}
	for _, r := range desired {
		if err := validRoute(r); err != nil {
			return err
		}
		if _, dup := wanted[r.LinkID]; dup {
			return domain.NewConflict("duplicate desired Caddy route")
		}
		wanted[r.LinkID] = r
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	all, err := c.fetch(ctx)
	if err != nil {
		return err
	}
	// Delete reverse indices first so route indices cannot shift.
	for i := len(all) - 1; i >= 0; i-- {
		// The @id alone is the ownership boundary. A manually damaged owned
		// route is still ours to repair/remove; a non-Mirage route is untouchable.
		if !owned(all[i].ID) {
			continue
		}
		id := domain.LinkID(strings.TrimPrefix(all[i].ID, Namespace))
		desired, keep := wanted[id]
		if !keep {
			if err := c.write(ctx, http.MethodDelete, c.routesPath()+"/"+strconv.Itoa(i), nil); err != nil {
				return err
			}
			continue
		}
		have, valid := decode(all[i])
		if valid && equal(have, desired) {
			delete(wanted, id)
			continue
		}
		if err := c.write(ctx, http.MethodPut, c.routesPath()+"/"+strconv.Itoa(i), encode(desired)); err != nil {
			return err
		}
		delete(wanted, id)
	}
	for _, r := range wanted {
		if err := c.write(ctx, http.MethodPost, c.routesPath(), encode(r)); err != nil {
			return err
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
