package health

import (
	"context"
	"github.com/primeintellect/mirage/internal/domain"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func hc(t *testing.T, method, url string) domain.HealthCheck {
	t.Helper()
	h, e := domain.ParseHealthCheck(method + " " + url)
	if e != nil {
		t.Fatal(e)
	}
	return h
}
func TestCheckMethodsAndStatus(t *testing.T) {
	methods := []string{"GET", "HEAD", "POST"}
	for _, m := range methods {
		m := m
		t.Run(m, func(t *testing.T) {
			got := ""
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.Method; w.WriteHeader(204) }))
			defer srv.Close()
			if e := New(time.Second).Check(context.Background(), hc(t, m, srv.URL)); e != nil {
				t.Fatal(e)
			}
			if got != m {
				t.Fatal(got)
			}
		})
	}
}
func TestCheckRejectsBadStatusAndDirectNonLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	if New(time.Second).Check(context.Background(), hc(t, "GET", srv.URL)) == nil {
		t.Fatal("status accepted")
	}
	if New(time.Second).Check(context.Background(), domain.HealthCheck{Method: domain.HealthGET, URL: "http://example.com/"}) == nil {
		t.Fatal("public accepted")
	}
}
func TestRedirectCannotEscape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/", http.StatusFound)
	}))
	defer srv.Close()
	if New(time.Second).Check(context.Background(), hc(t, "GET", srv.URL)) == nil {
		t.Fatal("escape redirect accepted")
	}
}
func TestCheckUntil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	start := time.Now()
	if New(time.Second).CheckUntil(context.Background(), hc(t, "GET", srv.URL), 30*time.Millisecond) == nil {
		t.Fatal("expected grace")
	}
	if time.Since(start) < 20*time.Millisecond {
		t.Fatal("returned too early")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if New(time.Second).CheckUntil(ctx, hc(t, "GET", srv.URL), time.Second) == nil {
		t.Fatal("cancel accepted")
	}
}

func TestInjectedClientCannotPermitPublicRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "http://example.com/", 302) }))
	defer srv.Close()
	c := &Checker{Client: &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}}
	if c.Check(context.Background(), hc(t, "GET", srv.URL)) == nil {
		t.Fatal("custom redirect policy escaped")
	}
}

func TestConstructorAndMalformedAndSuccessUntil(t *testing.T) {
	if probeTimeout(time.Second) != time.Second {
		t.Fatal("explicit timeout")
	}
	if New(0).Client.Timeout != 2*time.Second {
		t.Fatal("default timeout")
	}
	// Constructed values are reparsed, so unsupported method and malformed URL cannot bypass domain validation.
	if New(time.Second).Check(context.Background(), domain.HealthCheck{Method: "PUT", URL: "http://127.0.0.1/"}) == nil {
		t.Fatal("method")
	}
	if New(time.Second).Check(context.Background(), domain.HealthCheck{Method: domain.HealthGET, URL: "%%%"}) == nil {
		t.Fatal("url")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	if e := New(time.Second).CheckUntil(context.Background(), hc(t, "GET", srv.URL), time.Second); e != nil {
		t.Fatal(e)
	}
}

func TestNilClientAndCanceledProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(time.Second) }))
	defer srv.Close()
	c := &Checker{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c.Check(ctx, hc(t, "GET", srv.URL)) == nil {
		t.Fatal("canceled request accepted")
	}
}
func TestLoopbackForms(t *testing.T) {
	for _, raw := range []string{"http://localhost/", "http://127.0.0.1/", "http://[::1]/", "http://127.0.0.2/", "http://0.0.0.0/"} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		want := raw != "http://0.0.0.0/"
		if loopback(u) != want {
			t.Fatal(raw)
		}
	}
}

func TestAdditionalClassifications(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(400) }))
	defer srv.Close()
	if New(time.Second).Check(context.Background(), hc(t, "GET", srv.URL)) == nil {
		t.Fatal("status")
	}
	if New(time.Second).Check(context.Background(), domain.HealthCheck{Method: domain.HealthGET, URL: "http://127.0.0.1:bad/"}) == nil {
		t.Fatal("bad authority")
	}
}

func TestPositiveTimeoutAndLoopbackRedirect(t *testing.T) {
	if New(17*time.Millisecond).Client.Timeout != 17*time.Millisecond {
		t.Fatal("positive timeout")
	}
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer final.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, final.URL, 302) }))
	defer redirect.Close()
	if err := New(time.Second).Check(context.Background(), hc(t, "GET", redirect.URL)); err != nil {
		t.Fatal(err)
	}
}

func TestInjectedTransportFailureAndGraceDefaultInterval(t *testing.T) {
	c := &Checker{Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })}}
	if c.Check(context.Background(), hc(t, "GET", "http://127.0.0.1:1/")) == nil {
		t.Fatal("transport error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c.CheckUntil(ctx, hc(t, "GET", "http://127.0.0.1:1/"), time.Second) == nil {
		t.Fatal("cancel")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNewDefaultAndInjectedNilClient(t *testing.T) {
	if probeTimeout(time.Second) != time.Second {
		t.Fatal("explicit timeout")
	}
	if New(0).Client.Timeout != 2*time.Second {
		t.Fatal("default timeout")
	}
	c := &Checker{}
	if c.Check(context.Background(), hc(t, "GET", "http://127.0.0.1:1/")) == nil {
		t.Fatal("expected connection failure")
	}
}
