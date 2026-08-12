// Package composition wires adapters at the executable boundary.
package composition

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/primeintellect/mirage/internal/adapters/inbound/cli"
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
	var proxy ports.Proxy = noProxy{}
	if o.CaddyAdmin != "" {
		p, e := caddy.New(caddy.Config{AdminURL: o.CaddyAdmin})
		if e != nil {
			store.Close()
			return nil, e
		}
		proxy = p
	}
	svc := &application.Service{Repo: store, Clock: clock{}, IDs: ids{}, Aliases: aliases{}, Tokens: tokens{}, Hashes: hashes{}, Ports: process.NewAllocator(), Processes: sup, Health: health.New(2 * time.Second), Proxy: proxy, Logs: logStore, Audit: store, BaseHost: base, PublicPort: portNumber(o.PublicAddress)}
	api := NewHTTPAPI(svc)
	servers, e := StartHTTP(ListenerConfig{PublicAddress: o.PublicAddress, PrivateAddress: o.PrivateAddress}, api, http.NotFoundHandler())
	if e != nil {
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
