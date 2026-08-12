// Package composition wires adapters at the executable boundary.
package composition

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/primeintellect/mirage/internal/adapters/inbound/cli"
	"github.com/primeintellect/mirage/internal/adapters/inbound/httpapi"
	"github.com/primeintellect/mirage/internal/adapters/outbound/caddy"
	"github.com/primeintellect/mirage/internal/adapters/outbound/health"
	"github.com/primeintellect/mirage/internal/adapters/outbound/libsql"
	"github.com/primeintellect/mirage/internal/adapters/outbound/logs"
	"github.com/primeintellect/mirage/internal/adapters/outbound/process"
	"github.com/primeintellect/mirage/internal/application"
	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/buildinfo"
	"github.com/primeintellect/mirage/internal/domain"
)

func NewCommand(stdout, stderr io.Writer) cli.Command {
	return cli.NewWithStart(stdout, stderr, func() string { return buildinfo.Version }, Start)
}

type ids struct{}

func (ids) NewSpaceID() domain.SpaceID { return domain.NewSpaceID() }
func (ids) NewLinkID() domain.LinkID   { return domain.NewLinkID() }

type aliases struct{}

func (aliases) NewAlias() (domain.Alias, error) {
	return domain.ParseAlias("space-" + fmt.Sprintf("%x", time.Now().UnixNano())[8:])
}

type tokens struct{}

func (tokens) Generate() (domain.Token, error) { return domain.NewToken() }

type hashes struct{}

func (hashes) Hash(t domain.Token) domain.TokenHash           { return t.Hash() }
func (hashes) Verify(h domain.TokenHash, t domain.Token) bool { return h.Verify(t) }

type clock struct{}

func (clock) Now() time.Time { return time.Now().UTC() }

type noProxy struct{}

func (noProxy) Add(context.Context, ports.Route) error         { return nil }
func (noProxy) Remove(context.Context, domain.LinkID) error    { return nil }
func (noProxy) List(context.Context) ([]ports.Route, error)    { return nil, nil }
func (noProxy) Reconcile(context.Context, []ports.Route) error { return nil }

// Start composes durable storage, process/log adapters, API and isolated sockets.
// It returns an idempotent shutdown function for CLI signal handling/tests.
func Start(ctx context.Context, o cli.StartOptions) (func() error, error) {
	if o.PrivateAddress == "" {
		o.PrivateAddress = DefaultPrivateAddress
	}
	if o.PublicAddress == "" {
		o.PublicAddress = DefaultPublicAddress
	}
	if o.DataPath == "" {
		o.DataPath = filepath.Join(".", "mirage.db")
	}
	if o.BaseHost == "" {
		o.BaseHost = "localhost"
	}
	base, e := domain.ParseBaseHost(o.BaseHost)
	if e != nil {
		return nil, e
	}
	store, e := libsql.Open(o.DataPath)
	if e != nil {
		return nil, e
	}
	logStore := logs.NewStore()
	sup := process.NewSupervisor(logStore)
	managed := o.CaddyManaged
	if o.CaddyAdmin == "" && !managed {
		// The zero-value CLI options select the approved managed default.
		managed = true
	}
	adminURL := o.CaddyAdmin
	if adminURL == "" {
		adminURL = "http://127.0.0.1:2019"
	}
	p, e := caddy.New(caddy.Config{AdminURL: adminURL})
	if e != nil {
		store.Close()
		return nil, e
	}
	var proxy ports.Proxy = p
	var child *caddy.Child
	if managed {
		binary := o.CaddyBinary
		if binary == "" {
			binary = "caddy"
		}
		configPath := filepath.Join(filepath.Dir(o.DataPath), "mirage-caddy.json")
		cfg := fmt.Sprintf(`{"admin":{"listen":"127.0.0.1:2019"},"apps":{"http":{"servers":{"srv0":{"listen":[%q],"routes":[]}}}}}`, o.PublicAddress)
		if e = os.WriteFile(configPath, []byte(cfg), 0600); e != nil {
			store.Close()
			return nil, e
		}
		child, e = caddy.Start(ctx, caddy.ChildConfig{Managed: true, Binary: binary, Args: []string{"run", "--config", configPath}, ReadyTimeout: 5 * time.Second, Probe: func(probe context.Context) error { _, x := proxy.List(probe); return x }})
		if e != nil {
			store.Close()
			return nil, e
		}
	}
	svc := &application.Service{Repo: store, Clock: clock{}, IDs: ids{}, Aliases: aliases{}, Tokens: tokens{}, Hashes: hashes{}, Ports: process.NewAllocator(), Processes: sup, Health: health.New(2 * time.Second), Proxy: proxy, Logs: logStore, Audit: store, BaseHost: base, PublicPort: portNumber(o.PublicAddress)}
	// Initial cleanup/reconciliation completes before the management listener is bound.
	if e = svc.Cleanup(ctx); e != nil {
		if child != nil {
			_ = child.Stop()
		}
		store.Close()
		return nil, e
	}
	links, e := store.ReconciliationLinks(ctx, time.Now().UTC())
	if e != nil {
		if child != nil {
			_ = child.Stop()
		}
		store.Close()
		return nil, e
	}
	routes := make([]ports.Route, 0, len(links))
	for _, link := range links {
		if link.Status != domain.StatusActive || link.AllocatedPort == 0 {
			continue
		}
		sp, findErr := store.FindSpace(ctx, link.SpaceID)
		if findErr != nil {
			continue
		}
		route, routeErr := caddy.RouteFor(link.ID, base, link.Name, sp.Alias, link.AllocatedPort)
		if routeErr == nil {
			routes = append(routes, route)
		}
	}
	if e = proxy.Reconcile(ctx, routes); e != nil {
		if child != nil {
			_ = child.Stop()
		}
		store.Close()
		return nil, e
	}
	api := NewHTTPAPI(svc)
	var servers *httpapi.Servers
	if managed {
		servers, e = StartPrivateHTTP(o.PrivateAddress, api)
	} else {
		servers, e = StartHTTP(ListenerConfig{PublicAddress: o.PublicAddress, PrivateAddress: o.PrivateAddress}, api, http.NotFoundHandler())
	}
	if e != nil {
		if child != nil {
			_ = child.Stop()
		}
		store.Close()
		return nil, e
	}
	var once sync.Once
	var out error
	stop := func() error {
		once.Do(func() {
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out = ShutdownHTTP(shutdown, servers)
			if child != nil {
				if e := child.Stop(); out == nil {
					out = e
				}
			}
			if e := store.Close(); out == nil {
				out = e
			}
		})
		return out
	}
	return stop, nil
}
func portNumber(addr string) int {
	_, p, e := netSplit(addr)
	if e != nil {
		return 9955
	}
	var n int
	fmt.Sscan(p, &n)
	return n
}
func netSplit(a string) (string, string, error) { return net.SplitHostPort(a) }
