package composition

import (
	"context"
	"net"
	"net/http"

	"github.com/primeintellect/mirage/internal/adapters/inbound/httpapi"
)

const (
	DefaultPublicAddress  = ":9955"
	DefaultPrivateAddress = "127.0.0.1:9956"
)

// ListenerConfig is the executable-boundary listener configuration. Management
// defaults to loopback; callers must explicitly opt into any other binding.
type ListenerConfig struct{ PublicAddress, PrivateAddress string }

func (c ListenerConfig) normalized() ListenerConfig {
	if c.PublicAddress == "" {
		c.PublicAddress = DefaultPublicAddress
	}
	if c.PrivateAddress == "" {
		c.PrivateAddress = DefaultPrivateAddress
	}
	return c
}

// StartHTTP binds isolated listener sockets before serving them. The API is
// supplied by later production dependency composition; public is the proxy-only
// handler and is never the private management mux.
// NewHTTPAPI constructs the private management adapter at production composition.
func NewHTTPAPI(service httpapi.Service) *httpapi.API { return httpapi.New(service, httpapi.Config{}) }

func StartHTTP(cfg ListenerConfig, api *httpapi.API, public http.Handler) (*httpapi.Servers, error) {
	cfg = cfg.normalized()
	privateListener, err := net.Listen("tcp", cfg.PrivateAddress)
	if err != nil {
		return nil, err
	}
	publicListener, err := net.Listen("tcp", cfg.PublicAddress)
	if err != nil {
		_ = privateListener.Close()
		return nil, err
	}
	servers := httpapi.NewServers(cfg.PrivateAddress, cfg.PublicAddress, api, public)
	servers.Serve(privateListener, publicListener)
	return servers, nil
}
func ShutdownHTTP(ctx context.Context, servers *httpapi.Servers) error { return servers.Shutdown(ctx) }

// StartPrivateHTTP binds only the private management listener when Caddy owns the public listener.
func StartPrivateHTTP(address string, api *httpapi.API) (*httpapi.Servers, error) {
	if address == "" {
		address = DefaultPrivateAddress
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	servers := httpapi.NewServers(address, "", api, nil)
	servers.Public = nil
	servers.Serve(listener, nil)
	return servers, nil
}
