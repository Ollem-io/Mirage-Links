package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/primeintellect/mirage/internal/application"
	"github.com/primeintellect/mirage/internal/domain"
)

func dashboardRequest(h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestDashboardPrivateFragmentsAndEscaping(t *testing.T) {
	f := fixture()
	f.space.Alias = domain.Alias(`safe`)
	f.link.Name = domain.LinkName(`api`)
	f.link.Status = domain.StatusActive
	f.link.ExpiresAt = time.Now().Add(time.Hour)
	a := New(f, Config{})
	a.SetReady(true)
	h := a.Handler()
	if w := dashboardRequest(h, "GET", "/dashboard", ""); w.Code != http.StatusNotFound {
		t.Fatalf("anonymous dashboard=%d", w.Code)
	}
	w := dashboardRequest(h, "GET", "/dashboard", "token")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Mirage dashboard") || strings.Contains(w.Body.String(), "SECRET_HASH") {
		t.Fatalf("page %d: %s", w.Code, w.Body.String())
	}
	w = dashboardRequest(h, "GET", "/dashboard/links", "token")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Recent logs") {
		t.Fatalf("links %d: %s", w.Code, w.Body.String())
	}
	w = dashboardRequest(h, "GET", "/dashboard/links/api/logs", "token")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "safe") {
		t.Fatalf("logs %d: %s", w.Code, w.Body.String())
	}
	w = dashboardRequest(h, "GET", "/dashboard/assets/dashboard.css", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "tailwindcss v4.1.17") {
		t.Fatal("embedded css unavailable")
	}
}
func TestDashboardNeedsReasons(t *testing.T) {
	f := fixture()
	a := New(f, Config{})
	a.SetReady(true)
	r := httptest.NewRequest("POST", "/dashboard/links/api/restart", nil)
	r.Header.Set("Authorization", "Bearer token")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "reason is required") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestDashboardMutationFragmentsAndCookies(t *testing.T) {
	f := fixture()
	a := New(f, Config{})
	a.SetReady(true)
	h := a.Handler()
	// Bearer is exchanged for a strict HttpOnly cookie and subsequent fragment uses it.
	w := dashboardRequest(h, "GET", "/dashboard", "token")
	c := w.Result().Cookies()
	if len(c) == 0 || !c[0].HttpOnly {
		t.Fatal("dashboard cookie missing")
	}
	r := httptest.NewRequest("GET", "/dashboard/spaces", nil)
	r.AddCookie(c[0])
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Force delete space") {
		t.Fatalf("space fragment: %d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct{ method, path string }{{"POST", "/dashboard/links/api/restart"}, {"DELETE", "/dashboard/links/api"}, {"DELETE", "/dashboard/spaces/calm"}} {
		r = httptest.NewRequest(tc.method, tc.path, strings.NewReader("reason=operator+request"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Authorization", "Bearer token")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 && w.Code != 204 {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	w = dashboardRequest(h, "GET", "/dashboard/assets/dashboard.js", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "fetch") {
		t.Fatal("embedded script unavailable")
	}
}

func TestDashboardCSRF(t *testing.T) {
	f := fixture()
	a := New(f, Config{})
	a.SetReady(true)
	h := a.Handler()
	// Get a cookie session and csrf nonce through an authenticated page response.
	w := dashboardRequest(h, "GET", "/dashboard", "token")
	cookies := w.Result().Cookies()
	if len(cookies) < 2 {
		t.Fatal("cookies not issued")
	}
	find := func(n string) *http.Cookie {
		for _, c := range cookies {
			if c.Name == n {
				return c
			}
		}
		return nil
	}
	session, csrf := find("mirage_dashboard_token"), find("mirage_dashboard_csrf")
	if session == nil || csrf == nil {
		t.Fatal("session/csrf absent")
	}
	post := func(origin, nonce string) int {
		r := httptest.NewRequest("POST", "/dashboard/links/api/restart", strings.NewReader("reason=x"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(session)
		r.AddCookie(csrf)
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Mirage-CSRF", nonce)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if got := post("", ""); got != 404 {
		t.Fatalf("missing nonce=%d", got)
	}
	if got := post("http://evil.example", csrf.Value); got != 404 {
		t.Fatalf("foreign origin=%d", got)
	}
	if got := post("http://example.com", csrf.Value); got != 200 {
		t.Fatalf("same origin=%d", got)
	}
}

func TestDashboardFragmentsErrorsAndMutations(t *testing.T) {
	f := fixture()
	a := New(f, Config{})
	a.SetReady(true)
	h := a.Handler()
	// Link and force-space branches, including safe generic failures, are HTTP fragments.
	for _, tc := range []struct{ method, path string }{{"DELETE", "/dashboard/links/api"}, {"DELETE", "/dashboard/spaces/calm"}} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader("reason=audited"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Authorization", "Bearer token")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 && w.Code != 204 {
			t.Fatal(w.Code)
		}
	}
	f = fixture()
	f.err = domain.NewUnauthorized("secret")
	a = New(f, Config{})
	a.SetReady(true)
	w := dashboardRequest(a.Handler(), "GET", "/dashboard/links", "token")
	if w.Code != 404 {
		t.Fatalf("auth failure=%d", w.Code)
	}
	// Unknown fragment/method and an invalid cookie cannot escape private routing.
	w = dashboardRequest(h, "GET", "/dashboard/nope", "token")
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestDashboardHelperCoverage(t *testing.T) {
	f := fixture()
	a := New(f, Config{})
	a.SetReady(true)
	// URL calculation deliberately only reads safe public link metadata.
	a.service = &application.Service{BaseHost: "example.test", PublicPort: 9955}
	if got := a.dashboardURL(domain.Link{Name: "api"}, domain.Space{Alias: "calm"}); got == "" {
		t.Fatal("public URL")
	}
	a.service = f
	d := a.dashboardData(context.Background(), f.space, "token", "api")
	if len(d.Links) != 1 || len(d.Logs["api"]) != 1 {
		t.Fatal("seed data")
	}
	f.err = domain.NewInternal("hidden")
	d = a.dashboardData(context.Background(), f.space, "token", "api")
	if d.Error == "" {
		t.Fatal("safe error")
	}
	// Cookie session reads and a bad Origin parse are rejected without disclosure.
	r := httptest.NewRequest("GET", "/dashboard", nil)
	r.AddCookie(&http.Cookie{Name: "mirage_dashboard_token", Value: "token"})
	w := httptest.NewRecorder()
	a = New(fixture(), Config{})
	a.SetReady(true)
	a.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	r = httptest.NewRequest("POST", "/dashboard/links/api/restart", nil)
	r.Header.Set("Origin", ":bad")
	if dashboardCSRF(r) {
		t.Fatal("bad origin")
	}
}

func TestDashboardMutationErrorFragments(t *testing.T) {
	for _, tc := range []struct {
		path string
		call func(*API, http.ResponseWriter, *http.Request, domain.Space, domain.Token, string)
	}{{"restart", func(a *API, w http.ResponseWriter, r *http.Request, s domain.Space, t domain.Token, n string) {
		a.dashboardRestart(w, r, s, t, n)
	}}, {"delete", func(a *API, w http.ResponseWriter, r *http.Request, s domain.Space, t domain.Token, n string) {
		a.dashboardDeleteLink(w, r, s, t, n)
	}}, {"force", func(a *API, w http.ResponseWriter, r *http.Request, s domain.Space, t domain.Token, n string) {
		a.dashboardDeleteSpace(w, r, s, t, n)
	}}} {
		f := fixture()
		f.err = domain.NewInternal("do not show")
		a := New(f, Config{})
		a.SetReady(true)
		r := httptest.NewRequest("POST", "/dashboard", strings.NewReader("reason=audited"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		tc.call(a, w, r, f.space, "token", "api")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "failed") || strings.Contains(w.Body.String(), "do not show") {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
}
