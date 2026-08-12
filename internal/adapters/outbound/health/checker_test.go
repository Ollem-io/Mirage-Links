package health

import (
	"context"
	"github.com/primeintellect/mirage/internal/domain"
	"net/http"
	"net/http/httptest"
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
	if New(time.Second).CheckUntil(context.Background(), hc(t, "GET", srv.URL), 30*time.Millisecond, 5*time.Millisecond) == nil {
		t.Fatal("expected grace")
	}
	if time.Since(start) < 20*time.Millisecond {
		t.Fatal("returned too early")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if New(time.Second).CheckUntil(ctx, hc(t, "GET", srv.URL), time.Second, time.Millisecond) == nil {
		t.Fatal("cancel accepted")
	}
}
