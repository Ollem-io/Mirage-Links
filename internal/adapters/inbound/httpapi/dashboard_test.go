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
	if w := dashboardRequest(h, "GET", "/dashboard", ""); w.Code != http.StatusOK {
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
		target := "api"
		if tc.path == "force" {
			target = f.space.Alias.String()
		}
		tc.call(a, w, r, f.space, "token", target)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "failed") || strings.Contains(w.Body.String(), "do not show") {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
}

func TestDashboardRejectsCrossSpaceMutationsWithoutSideEffects(t *testing.T) {
	f := fixture() // token authenticates to space "calm"
	a := New(f, Config{})
	a.SetReady(true)
	h := a.Handler()

	r := httptest.NewRequest(http.MethodDelete, "/dashboard/spaces/other", strings.NewReader("reason=attack"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Authorization", "Bearer token-for-calm")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-space delete status=%d body=%s", w.Code, w.Body.String())
	}
	if len(f.deleted) != 0 {
		t.Fatalf("cross-space delete reached service/audit: %#v", f.deleted)
	}

	// The authenticated space remains a valid target and its reason is carried
	// to the audited force-delete operation.
	r = httptest.NewRequest(http.MethodDelete, "/dashboard/spaces/calm", strings.NewReader("reason=operator+cleanup"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Authorization", "Bearer token-for-calm")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent || len(f.deleted) != 1 || f.deleted[0].Alias != "calm" || f.deleted[0].Reason != "operator cleanup" {
		t.Fatalf("own-space delete status=%d calls=%#v", w.Code, f.deleted)
	}
}

func TestDashboardAnonymousLoginSessionAndTrustedSSL(t *testing.T) {
	f := fixture()
	token, _ := domain.NewToken()
	f.space.TokenHash = token.Hash()
	a := New(f, Config{DashboardSSL: true})
	a.SetReady(true)
	h := a.Handler()
	w := dashboardRequest(h, "GET", "/dashboard", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `action="/dashboard/session"`) {
		t.Fatalf("landing %d %s", w.Code, w.Body.String())
	}
	r := httptest.NewRequest(http.MethodPost, "/dashboard/session", strings.NewReader("token="+token.Reveal()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/dashboard" {
		t.Fatalf("login %d", w.Code)
	}
	for _, c := range w.Result().Cookies() {
		if !c.Secure {
			t.Fatalf("cookie %s not Secure", c.Name)
		}
	}
	r = httptest.NewRequest(http.MethodPost, "/dashboard/session", strings.NewReader("token=not-a-token"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Unauthorized") || strings.Contains(w.Body.String(), "not-a-token") {
		t.Fatalf("invalid %d %s", w.Code, w.Body.String())
	}
}

func TestAdminDashboardSessionAndScopedControls(t *testing.T) {
	raw, _ := domain.NewAdminToken()
	h := raw.Hash()
	f := &adminFake{fake: fixture(), hash: &h}
	a := New(f, Config{})
	a.SetReady(true)
	srv := a.Handler()
	r := httptest.NewRequest("POST", "/dashboard/session", strings.NewReader("token="+raw.Reveal()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "mirage_dashboard_admin" {
			cookie = c
		}
	}
	if cookie == nil || cookie.MaxAge != 3600 || !cookie.HttpOnly {
		t.Fatal("cookie")
	}
	get := func(path string) *httptest.ResponseRecorder {
		q := httptest.NewRequest("GET", path, nil)
		q.AddCookie(cookie)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, q)
		return w
	}
	if w := get("/dashboard/admin"); w.Code != 200 || !strings.Contains(w.Body.String(), "Active spaces") || !strings.Contains(w.Body.String(), "hx-post='/dashboard/admin/spaces'") || !strings.Contains(w.Body.String(), "dashboard.js") {
		t.Fatal(w.Code)
	}
	if w := get("/dashboard/admin/spaces/calm"); w.Code != 200 || !strings.Contains(w.Body.String(), "Force delete space") {
		t.Fatal(w.Code)
	}
}
func TestDashboardSessionRejectsCrossOrigin(t *testing.T) {
	f := fixture()
	a := New(f, Config{})
	a.SetReady(true)
	r := httptest.NewRequest("POST", "/dashboard/session", strings.NewReader("token=mir_x"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://evil.test")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Unauthorized") {
		t.Fatal(w.Code)
	}
}

func TestAdminDashboardMutationRoutes(t *testing.T) {
	raw, _ := domain.NewAdminToken()
	hash := raw.Hash()
	f := &adminFake{fake: fixture(), hash: &hash}
	a := New(f, Config{})
	a.SetReady(true)
	h := a.Handler()
	login := httptest.NewRequest(http.MethodPost, "/dashboard/session", strings.NewReader("token="+raw.Reveal()))
	login.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	lw := httptest.NewRecorder()
	h.ServeHTTP(lw, login)
	var session, csrf *http.Cookie
	for _, c := range lw.Result().Cookies() {
		if c.Name == "mirage_dashboard_admin" {
			session = c
		}
		if c.Name == "mirage_dashboard_csrf" {
			csrf = c
		}
	}
	if session == nil || csrf == nil {
		t.Fatal("admin cookies absent")
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Host = "example.com"
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "http://example.com")
		r.Header.Set("X-Mirage-CSRF", csrf.Value)
		r.AddCookie(session)
		r.AddCookie(csrf)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "/dashboard/admin/spaces", "alias=new&ttl=1h"); w.Code != 200 || !strings.Contains(w.Body.String(), "shown once") {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	if w := call("GET", "/dashboard/admin/spaces/calm/links/api/logs", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "admin log") {
		t.Fatalf("logs %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/dashboard/admin/spaces/calm/links/api/restart", "/dashboard/admin/spaces/calm/links/api/delete"} {
		if w := call("POST", path, "reason=ticket"); w.Code != 303 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := call("POST", "/dashboard/admin/spaces/calm/delete", "reason=ticket"); w.Code != 303 {
		t.Fatalf("delete space %d %s", w.Code, w.Body.String())
	}
	if f.creates != 1 || f.deletes != 1 || f.changes != 2 {
		t.Fatalf("creates=%d deletes=%d changes=%d", f.creates, f.deletes, f.changes)
	}
	if w := call("POST", "/dashboard/admin/spaces", "ttl=bad"); w.Code != 400 {
		t.Fatalf("bad ttl=%d", w.Code)
	}
}

func TestAdminDashboardLogoutClearsCookies(t *testing.T) {
	a := New(fixture(), Config{})
	a.SetReady(true)
	r := httptest.NewRequest(http.MethodPost, "/dashboard/logout", nil)
	r.Header.Set("Authorization", "Bearer token")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("HX-Redirect") != "/dashboard" {
		t.Fatalf("status=%d hx=%q", w.Code, w.Header().Get("HX-Redirect"))
	}
	if len(w.Result().Cookies()) < 3 {
		t.Fatalf("cookies=%v", w.Result().Cookies())
	}
	for _, c := range w.Result().Cookies() {
		if c.MaxAge != -1 {
			t.Fatalf("cookie not cleared: %+v", c)
		}
	}
}

func TestAdminHTMXCreateAndLogoutRequireCSRF(t *testing.T) {
	raw, _ := domain.NewAdminToken()
	hash := raw.Hash()
	f := &adminFake{fake: fixture(), hash: &hash}
	a := New(f, Config{})
	a.SetReady(true)
	h := a.Handler()
	login := httptest.NewRequest(http.MethodPost, "/dashboard/session", strings.NewReader("token="+raw.Reveal()))
	login.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	lw := httptest.NewRecorder()
	h.ServeHTTP(lw, login)
	var session, csrf *http.Cookie
	for _, c := range lw.Result().Cookies() {
		if c.Name == "mirage_dashboard_admin" {
			session = c
		}
		if c.Name == "mirage_dashboard_csrf" {
			csrf = c
		}
	}
	request := func(path, nonce string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader("alias=created&ttl=1h"))
		r.Host = "example.com"
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("HX-Request", "true")
		r.Header.Set("Origin", "http://example.com")
		r.Header.Set("X-Mirage-CSRF", nonce)
		r.AddCookie(session)
		r.AddCookie(csrf)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("/dashboard/admin/spaces", ""); w.Code != http.StatusNotFound || f.creates != 0 {
		t.Fatalf("missing csrf status=%d creates=%d", w.Code, f.creates)
	}
	if w := request("/dashboard/admin/spaces", csrf.Value); w.Code != http.StatusOK || f.creates != 1 {
		t.Fatalf("create status=%d creates=%d body=%s", w.Code, f.creates, w.Body.String())
	}
	if w := request("/dashboard/logout", csrf.Value); w.Code != http.StatusSeeOther || w.Header().Get("HX-Redirect") != "/dashboard" {
		t.Fatalf("logout status=%d hx=%q", w.Code, w.Header().Get("HX-Redirect"))
	}
}
