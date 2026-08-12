package composition

import (
	"context"
	"github.com/primeintellect/mirage/internal/adapters/inbound/httpapi"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewHTTPAPI(t *testing.T) {
	if NewHTTPAPI(nil) == nil {
		t.Fatal("nil API")
	}
}
func TestListenerDefaults(t *testing.T) {
	c := ListenerConfig{}.normalized()
	if c.PublicAddress != DefaultPublicAddress || c.PrivateAddress != DefaultPrivateAddress {
		t.Fatal(c)
	}
}
func TestStartHTTPIsolates(t *testing.T) {
	a := httpapi.New(nil, httpapi.Config{})
	s, e := StartHTTP(ListenerConfig{PublicAddress: "127.0.0.1:0", PrivateAddress: "127.0.0.1:0"}, a, nil)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e = s.Shutdown(ctx); e != nil {
		t.Fatal(e)
	}
	_ = httptest.NewRecorder()
	_ = http.MethodGet
}

func TestStartPrivateHTTP(t *testing.T) {
	a := httpapi.New(nil, httpapi.Config{})
	srv, e := StartPrivateHTTP("127.0.0.1:0", a)
	if e != nil {
		t.Fatal(e)
	}
	if srv.Public != nil {
		t.Fatal("public listener must be caddy-owned")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e = srv.Shutdown(ctx); e != nil {
		t.Fatal(e)
	}
}
func TestPortNumber(t *testing.T) {
	if portNumber("127.0.0.1:1234") != 1234 || portNumber("bad") != 9955 {
		t.Fatal()
	}
}
