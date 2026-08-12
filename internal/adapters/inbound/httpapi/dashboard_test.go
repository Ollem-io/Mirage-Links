package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Mirage dashboard") {
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
