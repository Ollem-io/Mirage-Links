// Package composition wires adapters at the executable boundary.
package composition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
	adminListen := ""
	if managed {
		adminListen, e = managedAdminListen(adminURL)
		if e != nil {
			store.Close()
			return nil, e
		}
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
		fallback := `{"@id":"mirage-fallback","handle":[{"handler":"static_response","status_code":404,"headers":{"Content-Type":["text/html; charset=utf-8"]},"body":"<!doctype html><html><body>Link invalid or expired</body></html>"}],"terminal":true}`
		cfg := fmt.Sprintf(`{"admin":{"listen":%q},"apps":{"http":{"servers":{"srv0":{"listen":[%q],"automatic_https":{"disable":true},"routes":[%s]}}}}}`, adminListen, o.PublicAddress, fallback)
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
	adminHash, e := readAdminHash(o.AdminTokenHashFile)
	if e != nil {
		if child != nil {
			_ = child.Stop()
		}
		_ = store.Close()
		return nil, e
	}
	svc := &application.Service{Repo: store, Clock: clock{}, IDs: ids{}, Aliases: aliases{}, Tokens: tokens{}, Hashes: hashes{}, Ports: process.NewAllocator(), Processes: sup, Health: health.New(2 * time.Second), Proxy: proxy, Logs: logStore, Audit: store, BaseHost: base, PublicPort: portNumber(o.PublicAddress), ExternalScheme: o.ExternalScheme, ExternalPort: o.ExternalPort, AdminTokenHash: adminHash}
	// Reconciliation is the readiness gate: no private listener (and hence no mutation) is admitted until state is repaired.
	if e = svc.Reconcile(ctx); e != nil {
		if child != nil {
			_ = child.Stop()
		}
		_ = store.Close()
		return nil, e
	}
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-cleanupCtx.Done():
				return
			case <-ticker.C:
				_ = svc.Reconcile(cleanupCtx)
			}
		}
	}()
	api := httpapi.New(svc, httpapi.Config{DashboardSSL: o.DashboardSSL})
	var servers *httpapi.Servers
	if managed {
		servers, e = StartPrivateHTTP(o.PrivateAddress, api)
	} else {
		servers, e = StartHTTP(ListenerConfig{PublicAddress: o.PublicAddress, PrivateAddress: o.PrivateAddress}, api, invalidLinkHandler())
	}
	if e != nil {
		cleanupCancel()
		<-cleanupDone
		_ = svc.Shutdown(context.Background())
		if child != nil {
			_ = child.Stop()
		}
		_ = store.Close()
		return nil, e
	}
	var once sync.Once
	var out error
	stop := func() error {
		once.Do(func() {
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			out = ShutdownHTTP(shutdown, servers)
			cleanupCancel()
			select {
			case <-cleanupDone:
			case <-shutdown.Done():
				if out == nil {
					out = shutdown.Err()
				}
			}
			if e := svc.Shutdown(shutdown); out == nil {
				out = e
			}
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

func managedAdminListen(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", domain.NewValidation("caddy.admin_url", "managed mode requires http://loopback:port with no path")
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || port == "" {
		return "", domain.NewValidation("caddy.admin_url", "managed mode requires an explicit port")
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return "", domain.NewValidation("caddy.admin_url", "managed mode requires a loopback host")
	}
	var n int
	if _, err = fmt.Sscan(port, &n); err != nil || n < 1 || n > 65535 {
		return "", domain.NewValidation("caddy.admin_url", "managed mode requires a valid port")
	}
	return net.JoinHostPort(host, port), nil
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

func invalidLinkHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, "<!doctype html><html><body>Link invalid or expired</body></html>")
	})
}

func readAdminHash(path string) (*domain.AdminTokenHash, error) {
	if path == "" {
		return nil, nil
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("admin token hash file: %w", err)
	}
	if !fi.Mode().IsRegular() || fi.Mode()&os.ModeSymlink != 0 || fi.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("admin token hash file must be a regular file not group/world writable")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	x := strings.TrimSpace(string(b))
	if len(x) != 64 {
		return nil, fmt.Errorf("admin token hash file must contain SHA-256 hex")
	}
	raw, err := hex.DecodeString(x)
	if err != nil {
		return nil, fmt.Errorf("admin token hash file must contain SHA-256 hex")
	}
	var h domain.AdminTokenHash
	copy(h[:], raw)
	_ = sha256.Size
	return &h, nil
}
