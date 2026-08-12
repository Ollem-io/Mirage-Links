package httpapi

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"html/template"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/primeintellect/mirage/internal/application"
	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
)

//go:embed dashboard.html
var dashboardTemplate string

//go:embed dashboard.css
var dashboardCSS []byte

//go:embed dashboard.js
var dashboardJS []byte

var dashboardTmpl = template.Must(template.New("dashboard").Funcs(template.FuncMap{
	"badge": func(s domain.LinkStatus) string {
		switch s {
		case domain.StatusActive, domain.StatusHealthy:
			return "badge-success"
		case domain.StatusCreating, domain.StatusStarting, domain.StatusStopping:
			return "badge-warning"
		case domain.StatusFailed, domain.StatusExpired, domain.StatusDeleted:
			return "badge-error"
		default:
			return "badge-info"
		}
	},
	"until": func(t time.Time) string {
		d := time.Until(t).Round(time.Second)
		if d < 0 {
			return "expired"
		}
		return d.String()
	},
	"stamp": func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
}).Parse(dashboardTemplate))

type dashboardData struct {
	Title, Body, Home, Notice, Reveal string
	Logout                            bool
	Space                             domain.Space
	Spaces                            []domain.Space
	Links                             []dashboardLink
	Logs                              map[string][]ports.LogEntry
	Error                             string
	Now                               time.Time
}
type dashboardLink struct {
	Name, URL string
	Status    domain.LinkStatus
	ExpiresAt time.Time
	Restarts  bool
}

func (a *API) dashboardToken(r *http.Request) (domain.Token, bool) {
	if t, e := bearer(r); e == nil {
		return t, true
	}
	c, e := r.Cookie("mirage_dashboard_token")
	if e != nil || strings.TrimSpace(c.Value) == "" {
		return "", false
	}
	return domain.Token(c.Value), true
}
func (a *API) dashboardAuth(w http.ResponseWriter, r *http.Request) (domain.Space, domain.Token, bool) {
	t, ok := a.dashboardToken(r)
	if !ok {
		return domain.Space{}, "", false
	}
	s, e := a.service.SpaceForToken(r.Context(), t)
	if e != nil {
		return domain.Space{}, "", false
	}
	// Bootstrap a cookie session only from the full page. Fragment requests with
	// a bearer token must not rotate the CSRF nonce (parallel initial loads race).
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") && r.URL.Path == "/dashboard" {
		a.setDashboardCookies(w, r, t)
	}
	return s, t, true
}

// Cookie authentication is protected from cross-site form submission. Bearer
// authentication is an API-client token strategy and does not use cookie CSRF.
func dashboardCSRF(r *http.Request) bool {
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return true
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	origin := r.Header.Get("Origin")
	if origin != "" {
		u, e := url.Parse(origin)
		if e != nil || u.Host != r.Host {
			return false
		}
	}
	c, e := r.Cookie("mirage_dashboard_csrf")
	return e == nil && c.Value != "" && r.Header.Get("X-Mirage-CSRF") == c.Value
}
func dashboardForbidden(w http.ResponseWriter) {
	writeErr(w, http.StatusNotFound, "not_found", "route not found", nil)
}
func (a *API) setDashboardCookies(w http.ResponseWriter, r *http.Request, t domain.Token) {
	secure := r.TLS != nil || a.dashboardSSL
	http.SetCookie(w, &http.Cookie{Name: "mirage_dashboard_token", Value: t.Reveal(), Path: "/dashboard", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secure, MaxAge: 3600})
	csrf := make([]byte, 24)
	if _, err := rand.Read(csrf); err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "mirage_dashboard_csrf", Value: base64.RawURLEncoding.EncodeToString(csrf), Path: "/dashboard", SameSite: http.SameSiteStrictMode, Secure: secure})
}

func (a *API) setAdminDashboardCookies(w http.ResponseWriter, r *http.Request, t domain.AdminToken) {
	secure := r.TLS != nil || a.dashboardSSL
	http.SetCookie(w, &http.Cookie{Name: "mirage_dashboard_admin", Value: t.Reveal(), Path: "/dashboard", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secure, MaxAge: 3600})
	csrf := make([]byte, 24)
	if _, err := rand.Read(csrf); err == nil {
		http.SetCookie(w, &http.Cookie{Name: "mirage_dashboard_csrf", Value: base64.RawURLEncoding.EncodeToString(csrf), Path: "/dashboard", SameSite: http.SameSiteStrictMode, Secure: secure, MaxAge: 3600})
	}
}
func (a *API) dashboardAdmin(r *http.Request) (adminService, domain.AdminToken, bool) {
	c, e := r.Cookie("mirage_dashboard_admin")
	if e != nil {
		return nil, "", false
	}
	t, e := domain.ParseAdminToken(c.Value)
	if e != nil {
		return nil, "", false
	}
	ad, ok := a.service.(adminService)
	if !ok || !ad.AdminConfigured() || ad.AuthorizeAdmin(r.Context(), t) != nil {
		return nil, "", false
	}
	return ad, t, true
}
func dashboardLogout(w http.ResponseWriter, r *http.Request) {
	for _, n := range []string{"mirage_dashboard_token", "mirage_dashboard_admin", "mirage_dashboard_csrf"} {
		http.SetCookie(w, &http.Cookie{Name: n, Value: "", Path: "/dashboard", MaxAge: -1, HttpOnly: n != "mirage_dashboard_csrf", SameSite: http.SameSiteStrictMode})
	}
	dashboardRedirect(w, r, "/dashboard")
}

// HTMX/fetch clients must see HX-Redirect on a 2xx response: fetch follows a
// 303 before JavaScript can inspect its headers. Non-HX forms retain 303.
func dashboardRedirect(w http.ResponseWriter, r *http.Request, target string) {
	w.Header().Set("HX-Redirect", target)
	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func dashboardLogin(w http.ResponseWriter, invalid bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	d := dashboardData{Title: "Mirage dashboard", Body: "login-body", Home: "/dashboard"}
	if invalid {
		d.Error = "Unauthorized. Please try again."
	}
	if e := dashboardTmpl.ExecuteTemplate(w, "login", d); e != nil {
		http.Error(w, "", http.StatusInternalServerError)
	}
}

func (a *API) dashboardSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		dashboardForbidden(w)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, e := url.Parse(origin)
		if e != nil || u.Host != r.Host {
			dashboardLogin(w, true)
			return
		}
	}
	media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/x-www-form-urlencoded" {
		dashboardLogin(w, true)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(a.maxBody))
	if err := r.ParseForm(); err != nil {
		dashboardLogin(w, true)
		return
	}
	raw := r.PostForm.Get("token")
	if t, err := domain.ParseAdminToken(raw); err == nil {
		if ad, ok := a.service.(adminService); ok && ad.AdminConfigured() && ad.AuthorizeAdmin(r.Context(), t) == nil {
			a.setAdminDashboardCookies(w, r, t)
			http.Redirect(w, r, "/dashboard/admin", http.StatusSeeOther)
			return
		}
	}
	t, err := domain.ParseToken(raw)
	if err != nil {
		dashboardLogin(w, true)
		return
	}
	if _, err = a.service.SpaceForToken(r.Context(), t); err != nil {
		dashboardLogin(w, true)
		return
	}
	a.setDashboardCookies(w, r, t)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func dashboardNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

func (a *API) dashboard(w http.ResponseWriter, r *http.Request) {
	// Assets are the sole cacheable dashboard responses.
	if r.URL.Path == "/dashboard/assets/dashboard.css" {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = w.Write(dashboardCSS)
		return
	}
	if r.URL.Path == "/dashboard/assets/dashboard.js" {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = w.Write(dashboardJS)
		return
	}
	dashboardNoStore(w)
	if r.URL.Path == "/dashboard/logout" && r.Method == http.MethodPost {
		if !dashboardCSRF(r) {
			dashboardForbidden(w)
			return
		}
		dashboardLogout(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/dashboard/admin") {
		a.dashboardAdminRoute(w, r)
		return
	}
	if r.URL.Path == "/dashboard/session" {
		a.dashboardSession(w, r)
		return
	}
	if r.URL.Path == "/dashboard" && r.Method == http.MethodGet {
		t, present := a.dashboardToken(r)
		if !present {
			dashboardLogin(w, false)
			return
		}
		if _, err := a.service.SpaceForToken(r.Context(), t); err != nil {
			// A stale/invalid cookie is anonymous, not a private route disclosure.
			dashboardLogin(w, false)
			return
		}
	}
	s, t, ok := a.dashboardAuth(w, r)
	if !ok || !dashboardCSRF(r) {
		dashboardForbidden(w)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/dashboard")
	if path == "" || path == "/" {
		if r.Method != "GET" {
			method(w)
			return
		}
		a.renderDashboard(w, r, "page", s, t, "")
		return
	}
	if path == "/spaces" && r.Method == "GET" {
		a.renderDashboard(w, r, "spaces", s, t, "")
		return
	}
	if path == "/links" && r.Method == "GET" {
		a.renderDashboard(w, r, "links", s, t, "")
		return
	}
	if strings.HasPrefix(path, "/links/") {
		bits := strings.Split(strings.Trim(path, "/"), "/")
		if len(bits) == 3 && bits[2] == "logs" && r.Method == "GET" {
			a.renderDashboard(w, r, "logs", s, t, bits[1])
			return
		}
		if len(bits) == 3 && bits[2] == "restart" && r.Method == "POST" {
			a.dashboardRestart(w, r, s, t, bits[1])
			return
		}
		if len(bits) == 2 && r.Method == "DELETE" {
			a.dashboardDeleteLink(w, r, s, t, bits[1])
			return
		}
	}
	if strings.HasPrefix(path, "/spaces/") && r.Method == "DELETE" {
		a.dashboardDeleteSpace(w, r, s, t, strings.TrimPrefix(path, "/spaces/"))
		return
	}
	dashboardForbidden(w)
}
func (a *API) dashboardData(ctx context.Context, s domain.Space, t domain.Token, logName string) dashboardData {
	d := dashboardData{Space: s, Logs: map[string][]ports.LogEntry{}, Now: time.Now().UTC()}
	links, e := a.service.ListLinks(ctx, s.Alias.String(), t)
	if e != nil {
		d.Error = "Unable to load links."
		return d
	}
	for _, l := range links {
		d.Links = append(d.Links, dashboardLink{Name: l.Name.String(), URL: a.dashboardURL(l, s), Status: l.Status, ExpiresAt: l.ExpiresAt, Restarts: l.AutoRestart})
	}
	if logName != "" {
		x, e := a.service.LogsFor(ctx, s.Alias.String(), t, logName, 100)
		if e != nil {
			d.Error = "Unable to load recent logs."
		} else {
			d.Logs[logName] = x
		}
	}
	return d
}
func (a *API) dashboardURL(l domain.Link, s domain.Space) string {
	if svc, ok := a.service.(*application.Service); ok {
		if u, e := svc.AdvertisedURL(l.Name, s.Alias); e == nil {
			return u
		}
	}
	return ""
}
func (a *API) renderDashboard(w http.ResponseWriter, r *http.Request, part string, s domain.Space, t domain.Token, logName string) {
	d := a.dashboardData(r.Context(), s, t, logName)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if part == "page" {
		d.Title, d.Body, d.Home, d.Logout = "Mirage dashboard", "space-page", "/dashboard", true
	}
	if e := dashboardTmpl.ExecuteTemplate(w, part, d); e != nil { /* never disclose template/internal error */
		http.Error(w, "", 500)
	}
}
func dashboardReason(r *http.Request) string {
	// net/http ParseForm intentionally ignores URL-encoded DELETE bodies, while
	// dashboard delete controls send their audited reason in the request body.
	// Parse both encodings emitted by browser/headless form clients, bounded by
	// the same request-size order as the API.
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.Method == http.MethodDelete && mediaType == "application/x-www-form-urlencoded" {
		b, _ := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
		if len(b) <= maxBody {
			values, _ := url.ParseQuery(string(b))
			return strings.TrimSpace(values.Get("reason"))
		}
		return ""
	}
	if mediaType == "multipart/form-data" {
		_ = r.ParseMultipartForm(maxBody)
	} else {
		_ = r.ParseForm()
	}
	return strings.TrimSpace(r.FormValue("reason"))
}
func dashboardError(w http.ResponseWriter, r *http.Request, s domain.Space, t domain.Token, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = dashboardTmpl.ExecuteTemplate(w, "error", dashboardData{Space: s, Error: msg})
}
func (a *API) dashboardRestart(w http.ResponseWriter, r *http.Request, s domain.Space, t domain.Token, name string) {
	reason := dashboardReason(r)
	if reason == "" {
		dashboardError(w, r, s, t, "A reason is required to restart a link.")
		return
	}
	a.mutation(w, r, func() {
		_, e := a.service.RestartLink(r.Context(), application.LinkMutationInput{Alias: s.Alias.String(), Token: t, Name: name, Reason: reason})
		if e != nil {
			dashboardError(w, r, s, t, "Restart failed.")
			return
		}
		a.renderDashboard(w, r, "links", s, t, "")
	})
}
func (a *API) dashboardDeleteLink(w http.ResponseWriter, r *http.Request, s domain.Space, t domain.Token, name string) {
	reason := dashboardReason(r)
	if reason == "" {
		dashboardError(w, r, s, t, "A reason is required to delete a link.")
		return
	}
	a.mutation(w, r, func() {
		e := a.service.DeleteLink(r.Context(), application.LinkMutationInput{Alias: s.Alias.String(), Token: t, Name: name, Reason: reason})
		if e != nil {
			dashboardError(w, r, s, t, "Delete failed.")
			return
		}
		a.renderDashboard(w, r, "links", s, t, "")
	})
}
func (a *API) dashboardDeleteSpace(w http.ResponseWriter, r *http.Request, s domain.Space, t domain.Token, alias string) {
	// The URL is attacker-controlled. Force deletion intentionally bypasses token
	// validation in the application service, so bind the target to the space that
	// dashboard authentication resolved before parsing a reason or admitting a
	// mutation (and therefore before any delete/audit side effect).
	if alias != s.Alias.String() {
		dashboardForbidden(w)
		return
	}
	reason := dashboardReason(r)
	if reason == "" {
		dashboardError(w, r, s, t, "A reason is required to force delete a space.")
		return
	}
	a.mutation(w, r, func() {
		e := a.service.DeleteSpace(r.Context(), application.DeleteSpaceInput{Alias: alias, Force: true, Reason: reason})
		if e != nil {
			dashboardError(w, r, s, t, "Force delete failed.")
			return
		}
		w.Header().Set("HX-Redirect", "/")
		w.WriteHeader(http.StatusNoContent)
	})
}

// keep net/url in this file's dependency graph as a guard against ever treating
// a dashboard path component as a URL without escaping it.
var _ = url.PathEscape

func (a *API) dashboardAdminRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		a.mutation(w, r, func() { a.dashboardAdminRouteInner(w, r) })
		return
	}
	a.dashboardAdminRouteInner(w, r)
}
func (a *API) dashboardAdminRouteInner(w http.ResponseWriter, r *http.Request) {
	ad, t, ok := a.dashboardAdmin(r)
	if !ok || !dashboardCSRF(r) {
		dashboardForbidden(w)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/dashboard/admin")
	if path == "" || path == "/" {
		if r.Method != http.MethodGet {
			dashboardForbidden(w)
			return
		}
		a.renderAdmin(w, r, ad, t, "")
		return
	}
	if path == "/spaces" && r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, int64(a.maxBody))
		if e := r.ParseForm(); e != nil {
			dashboardForbidden(w)
			return
		}
		ttl := time.Duration(0)
		var e error
		if v := r.FormValue("ttl"); v != "" {
			ttl, e = time.ParseDuration(v)
			if e != nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusBadRequest)
				a.dashboardAdminError(w, "Invalid TTL.")
				return
			}
		}
		x, e := ad.AdminCreateSpace(r.Context(), t, application.CreateSpaceInput{TTL: ttl, Alias: r.FormValue("alias")})
		if e != nil {
			a.dashboardAdminError(w, "Create failed.")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = dashboardTmpl.ExecuteTemplate(w, "admin-create", dashboardData{Space: x.Space, Reveal: x.Token.Reveal()})
		return
	}
	const pre = "/spaces/"
	if !strings.HasPrefix(path, pre) {
		dashboardForbidden(w)
		return
	}
	bits := strings.Split(strings.TrimPrefix(path, pre), "/")
	alias := bits[0]
	if len(bits) == 2 && bits[1] == "delete" && r.Method == http.MethodPost {
		reason := dashboardReason(r)
		if reason == "" {
			a.dashboardAdminError(w, "A reason is required to force delete a space.")
			return
		}
		if e := ad.AdminDeleteSpace(r.Context(), t, application.DeleteSpaceInput{Alias: alias, Force: true, Reason: reason}); e != nil {
			a.dashboardAdminError(w, "Force delete failed.")
			return
		}
		dashboardRedirect(w, r, "/dashboard/admin")
		return
	}
	if len(bits) >= 3 && bits[1] == "links" {
		name := bits[2]
		if len(bits) == 4 && bits[3] == "logs" && r.Method == http.MethodGet {
			entries, e := ad.AdminLogsFor(r.Context(), t, alias, name, 100)
			if e != nil {
				dashboardForbidden(w)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = dashboardTmpl.ExecuteTemplate(w, "admin-logs", dashboardData{Logs: map[string][]ports.LogEntry{name: entries}})
			return
		}
		if len(bits) == 4 && (bits[3] == "restart" || bits[3] == "delete") && r.Method == http.MethodPost {
			reason := dashboardReason(r)
			if reason == "" {
				a.dashboardAdminError(w, "A reason is required for this action.")
				return
			}
			var e error
			if bits[3] == "restart" {
				_, e = ad.AdminRestartLink(r.Context(), t, alias, name, reason)
			} else {
				e = ad.AdminDeleteLink(r.Context(), t, alias, name, reason)
			}
			if e != nil {
				a.dashboardAdminError(w, "Operation failed.")
				return
			}
			dashboardRedirect(w, r, "/dashboard/admin/spaces/"+url.PathEscape(alias))
			return
		}
	}
	if r.Method == http.MethodGet {
		sp, e := ad.AdminGetSpace(r.Context(), t, alias)
		if e != nil {
			dashboardForbidden(w)
			return
		}
		links, e := ad.AdminListLinks(r.Context(), t, alias)
		if e != nil {
			dashboardForbidden(w)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		d := dashboardData{Title: "Mirage administration", Body: "admin-space-body", Home: "/dashboard/admin", Logout: true, Space: sp}
		for _, l := range links {
			d.Links = append(d.Links, dashboardLink{Name: l.Name.String(), Status: l.Status, ExpiresAt: l.ExpiresAt, Restarts: l.AutoRestart})
		}
		if e := dashboardTmpl.ExecuteTemplate(w, "admin-space", d); e != nil {
			http.Error(w, "", 500)
		}
		return
	}
	dashboardForbidden(w)
}
func (a *API) dashboardAdminError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = dashboardTmpl.ExecuteTemplate(w, "error", dashboardData{Error: message})
}
func (a *API) renderAdmin(w http.ResponseWriter, r *http.Request, ad adminService, t domain.AdminToken, reveal string) {
	xs, e := ad.AdminListSpaces(r.Context(), t)
	if e != nil {
		dashboardForbidden(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	d := dashboardData{Title: "Mirage administration", Body: "admin-body", Home: "/dashboard/admin", Logout: true, Spaces: xs, Reveal: reveal}
	if e := dashboardTmpl.ExecuteTemplate(w, "admin", d); e != nil {
		http.Error(w, "", 500)
	}
}
