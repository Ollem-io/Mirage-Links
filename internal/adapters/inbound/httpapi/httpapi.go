// Package httpapi exposes the private, versioned Mirage management API.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/primeintellect/mirage/internal/application"
	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
)

const maxBody = 1 << 20

type Service interface {
	CreateSpace(context.Context, application.CreateSpaceInput) (application.CreateSpaceResult, error)
	ListSpaces(context.Context) ([]domain.Space, error)
	GetSpace(context.Context, string) (domain.Space, error)
	DeleteSpace(context.Context, application.DeleteSpaceInput) error
	SpaceForToken(context.Context, domain.Token) (domain.Space, error)
	CreateLink(context.Context, application.CreateLinkInput) (application.CreateLinkResult, error)
	ListLinks(context.Context, string, domain.Token) ([]domain.Link, error)
	LogsFor(context.Context, string, domain.Token, string, int) ([]ports.LogEntry, error)
	FollowLogs(context.Context, string, domain.Token, string) (io.ReadCloser, error)
	RestartLink(context.Context, application.LinkMutationInput) (application.CreateLinkResult, error)
	DeleteLink(context.Context, application.LinkMutationInput) error
}

type Config struct {
	RequestTimeout time.Duration
	MaxBodyBytes   int
	// DashboardSSL marks a trusted TLS-terminating deployment; forwarded headers
	// are deliberately not used as a security signal.
	DashboardSSL bool
}
type API struct {
	service         Service
	timeout         time.Duration
	maxBody         int
	dashboardSSL    bool
	ready           atomic.Bool
	gateMu          sync.Mutex
	draining        bool
	activeMutations int
	drained         chan struct{}
}

func New(service Service, cfg Config) *API {
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 30 * time.Second
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = maxBody
	}
	a := &API{service: service, timeout: cfg.RequestTimeout, maxBody: cfg.MaxBodyBytes, dashboardSSL: cfg.DashboardSSL}
	return a
}
func (a *API) SetReady(v bool) { a.ready.Store(v) }
func (a *API) Ready() bool {
	a.gateMu.Lock()
	draining := a.draining
	a.gateMu.Unlock()
	return a.ready.Load() && !draining
}

// Drain atomically closes mutation admission before observing in-flight work.
// Unlike a WaitGroup Add concurrent with Wait, this gate is safe when request
// admission races shutdown.
func (a *API) Drain(ctx context.Context) error {
	a.ready.Store(false)
	a.gateMu.Lock()
	a.draining = true
	if a.activeMutations == 0 {
		a.gateMu.Unlock()
		return nil
	}
	if a.drained == nil {
		a.drained = make(chan struct{})
	}
	done := a.drained
	a.gateMu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (a *API) Handler() http.Handler { return a.middleware(http.HandlerFunc(a.route)) }
func (a *API) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			ctx, cancel := context.WithTimeout(r.Context(), a.timeout)
			defer cancel()
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}
func (a *API) route(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" || r.URL.Path == "/dashboard" || strings.HasPrefix(r.URL.Path, "/dashboard/") {
		a.dashboard(w, r)
		return
	}
	if r.URL.Path == "/healthz" {
		if r.Method != "GET" {
			method(w)
			return
		}
		if !a.Ready() {
			writeErr(w, http.StatusServiceUnavailable, "not_ready", "server is not ready", nil)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
		writeErr(w, http.StatusNotFound, "not_found", "route not found", nil)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	seg := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case len(seg) == 1 && seg[0] == "spaces":
		a.spaces(w, r)
	case len(seg) == 2 && seg[0] == "spaces":
		a.space(w, r, seg[1])
	case len(seg) >= 4 && seg[0] == "admin" && seg[1] == "spaces" && seg[3] == "links":
		a.adminLinks(w, r, seg)
	case len(seg) == 1 && seg[0] == "links":
		a.links(w, r)
	case len(seg) >= 2 && seg[0] == "links":
		a.link(w, r, seg)
	default:
		writeErr(w, http.StatusNotFound, "not_found", "route not found", nil)
	}
}
func method(w http.ResponseWriter) {
	w.Header().Set("Allow", "GET")
	writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeErr(w http.ResponseWriter, status int, code, msg string, details any) {
	writeJSON(w, status, errorBody{code, msg, details})
}
func apiErr(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		writeErr(w, http.StatusGatewayTimeout, "timeout", "request timed out", nil)
	case domain.IsKind(err, domain.Validation):
		var e *domain.Error
		_ = errors.As(err, &e)
		writeErr(w, 400, "validation", "invalid request", map[string]string{"field": e.Field, "message": e.Message})
	case domain.IsKind(err, domain.Unauthorized):
		writeErr(w, 401, "unauthorized", "unauthorized", nil)
	case domain.IsKind(err, domain.NotFound):
		writeErr(w, 404, "not_found", "not found", nil)
	case domain.IsKind(err, domain.Conflict):
		writeErr(w, 409, "conflict", "conflict", nil)
	default:
		writeErr(w, 500, "internal", "internal server error", nil)
	}
}
func decode(w http.ResponseWriter, r *http.Request, max int, v any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeErr(w, 415, "unsupported_media_type", "Content-Type must be application/json", nil)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(max))
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		writeErr(w, 400, "validation", "invalid JSON", nil)
		return false
	}
	if d.Decode(&struct{}{}) != io.EOF {
		writeErr(w, 400, "validation", "invalid JSON", nil)
		return false
	}
	return true
}
func (a *API) mutation(w http.ResponseWriter, r *http.Request, fn func()) {
	a.gateMu.Lock()
	if a.draining {
		a.gateMu.Unlock()
		writeErr(w, 503, "draining", "server is draining", nil)
		return
	}
	a.activeMutations++
	a.gateMu.Unlock()
	defer func() {
		a.gateMu.Lock()
		a.activeMutations--
		if a.draining && a.activeMutations == 0 && a.drained != nil {
			close(a.drained)
			a.drained = nil
		}
		a.gateMu.Unlock()
	}()
	fn()
}

type spaceOut struct {
	Alias     string    `json:"alias"`
	ExpiresAt time.Time `json:"expires_at"`
}

func outSpace(s domain.Space) spaceOut { return spaceOut{s.Alias.String(), s.ExpiresAt} }

type linkOut struct {
	Name      string            `json:"name"`
	URL       string            `json:"url,omitempty"`
	Status    domain.LinkStatus `json:"status"`
	ExpiresAt time.Time         `json:"expires_at"`
	Restarts  bool              `json:"restarts"`
}

func outLink(l domain.Link, url string) linkOut {
	return linkOut{l.Name.String(), url, l.Status, l.ExpiresAt, l.AutoRestart}
}
func bearer(r *http.Request) (domain.Token, error) {
	v := r.Header.Get("Authorization")
	const p = "Bearer "
	if !strings.HasPrefix(v, p) || strings.TrimSpace(strings.TrimPrefix(v, p)) == "" {
		return "", domain.NewUnauthorized("missing bearer token")
	}
	return domain.Token(strings.TrimSpace(strings.TrimPrefix(v, p))), nil
}

type adminService interface {
	AdminConfigured() bool
	AdminListSpaces(context.Context, domain.AdminToken) ([]domain.Space, error)
	AdminCreateSpace(context.Context, domain.AdminToken, application.CreateSpaceInput) (application.CreateSpaceResult, error)
	AdminGetSpace(context.Context, domain.AdminToken, string) (domain.Space, error)
	AuthorizeAdmin(context.Context, domain.AdminToken) error
	AdminListLinks(context.Context, domain.AdminToken, string) ([]domain.Link, error)
	AdminLogsFor(context.Context, domain.AdminToken, string, string, int) ([]ports.LogEntry, error)
	AdminDeleteSpace(context.Context, domain.AdminToken, application.DeleteSpaceInput) error
	AdminRestartLink(context.Context, domain.AdminToken, string, string, string) (application.CreateLinkResult, error)
	AdminDeleteLink(context.Context, domain.AdminToken, string, string, string) error
}

func adminBearer(r *http.Request) (domain.AdminToken, error) {
	v := r.Header.Get("Authorization")
	const p = "Bearer "
	if !strings.HasPrefix(v, p) {
		return "", domain.NewUnauthorized("missing admin bearer token")
	}
	t, err := domain.ParseAdminToken(strings.TrimSpace(strings.TrimPrefix(v, p)))
	if err != nil {
		return "", domain.NewUnauthorized("invalid admin bearer token")
	}
	return t, nil
}
func (a *API) admin(r *http.Request) (adminService, domain.AdminToken, error) {
	x, ok := a.service.(adminService)
	// Older test/in-process Service implementations have no installation auth component.
	if !ok {
		return nil, "", nil
	}
	if !x.AdminConfigured() {
		return nil, "", domain.NewUnauthorized("installation administration is not configured")
	}
	t, e := adminBearer(r)
	if e != nil {
		return x, "", e
	}
	e = x.AuthorizeAdmin(r.Context(), t)
	return x, t, e
}
func (a *API) spaces(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		ad, tok, e := a.admin(r)
		if e != nil {
			apiErr(w, e)
			return
		}
		var x []domain.Space
		if ad != nil {
			x, e = ad.AdminListSpaces(r.Context(), tok)
		} else {
			x, e = a.service.ListSpaces(r.Context())
		}
		if e != nil {
			apiErr(w, e)
			return
		}
		o := make([]spaceOut, len(x))
		for i := range x {
			o[i] = outSpace(x[i])
		}
		writeJSON(w, 200, map[string]any{"spaces": o})
	case "POST":
		a.mutation(w, r, func() {
			var in struct {
				TTL   string `json:"ttl"`
				Alias string `json:"alias"`
			}
			if !decode(w, r, a.maxBody, &in) {
				return
			}
			var ttl time.Duration
			var e error
			if in.TTL != "" {
				ttl, e = time.ParseDuration(in.TTL)
				if e != nil {
					apiErr(w, domain.NewValidation("ttl", "invalid duration"))
					return
				}
			}
			ad, tok, e := a.admin(r)
			if e != nil {
				apiErr(w, e)
				return
			}
			var x application.CreateSpaceResult
			if ad != nil {
				x, e = ad.AdminCreateSpace(r.Context(), tok, application.CreateSpaceInput{TTL: ttl, Alias: in.Alias})
			} else {
				x, e = a.service.CreateSpace(r.Context(), application.CreateSpaceInput{TTL: ttl, Alias: in.Alias})
			}
			if e != nil {
				apiErr(w, e)
				return
			}
			writeJSON(w, 201, map[string]any{"space": outSpace(x.Space), "token": x.Token.Reveal()})
		})
	default:
		method(w)
	}
}
func (a *API) space(w http.ResponseWriter, r *http.Request, alias string) {
	switch r.Method {
	case "GET":
		ad, tok, e := a.admin(r)
		if e != nil {
			apiErr(w, e)
			return
		}
		var x domain.Space
		if ad != nil {
			x, e = ad.AdminGetSpace(r.Context(), tok, alias)
		} else {
			x, e = a.service.GetSpace(r.Context(), alias)
		}
		if e != nil {
			apiErr(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"space": outSpace(x)})
	case "DELETE":
		a.mutation(w, r, func() {
			var in struct {
				Force  bool   `json:"force"`
				Reason string `json:"reason"`
			}
			if r.ContentLength != 0 && !decode(w, r, a.maxBody, &in) {
				return
			}
			var tok domain.Token
			if !in.Force {
				var e error
				tok, e = bearer(r)
				if e != nil {
					apiErr(w, e)
					return
				}
			}
			var e error
			if in.Force {
				ad, at, ae := a.admin(r)
				if ae != nil {
					apiErr(w, ae)
					return
				}
				// Force deletion is always privileged. Unlike legacy global query and
				// create compatibility paths, it must never fall back to the ordinary
				// service when installation administration is unavailable.
				if ad == nil {
					apiErr(w, domain.NewUnauthorized("installation administration is not available"))
					return
				}
				e = ad.AdminDeleteSpace(r.Context(), at, application.DeleteSpaceInput{Alias: alias, Force: true, Reason: in.Reason})
			} else {
				e = a.service.DeleteSpace(r.Context(), application.DeleteSpaceInput{Alias: alias, Token: tok})
			}
			if e != nil {
				apiErr(w, e)
				return
			}
			w.WriteHeader(204)
		})
	default:
		method(w)
	}
}
func (a *API) authorized(r *http.Request) (domain.Space, domain.Token, error) {
	t, e := bearer(r)
	if e != nil {
		return domain.Space{}, "", e
	}
	s, e := a.service.SpaceForToken(r.Context(), t)
	return s, t, e
}
func (a *API) links(w http.ResponseWriter, r *http.Request) {
	sp, t, e := a.authorized(r)
	if e != nil {
		apiErr(w, e)
		return
	}
	switch r.Method {
	case "GET":
		x, e := a.service.ListLinks(r.Context(), sp.Alias.String(), t)
		if e != nil {
			apiErr(w, e)
			return
		}
		o := make([]linkOut, len(x))
		for i := range x {
			o[i] = outLink(x[i], "")
		}
		writeJSON(w, 200, map[string]any{"links": o})
	case "POST":
		a.mutation(w, r, func() {
			var in struct {
				Name            string `json:"name"`
				Command         string `json:"command"`
				ExecutionFolder string `json:"execution_folder"`
				HealthCheck     string `json:"health_check"`
				Grace           string `json:"grace"`
				TTL             string `json:"ttl"`
				Restarts        bool   `json:"restarts"`
			}
			if !decode(w, r, a.maxBody, &in) {
				return
			}
			h, e := domain.ParseHealthCheck(in.HealthCheck)
			if e != nil {
				apiErr(w, e)
				return
			}
			var grace, ttl time.Duration
			if in.Grace != "" {
				grace, e = time.ParseDuration(in.Grace)
				if e != nil {
					apiErr(w, domain.NewValidation("grace", "invalid duration"))
					return
				}
			}
			if in.TTL != "" {
				ttl, e = time.ParseDuration(in.TTL)
				if e != nil {
					apiErr(w, domain.NewValidation("ttl", "invalid duration"))
					return
				}
			}
			x, e := a.service.CreateLink(r.Context(), application.CreateLinkInput{Alias: sp.Alias.String(), Token: t, Name: in.Name, Command: in.Command, Folder: in.ExecutionFolder, HealthCheck: h, Grace: grace, TTL: ttl, Restarts: in.Restarts})
			if e != nil {
				apiErr(w, e)
				return
			}
			writeJSON(w, 201, map[string]any{"link": outLink(x.Link, x.URL), "recent_logs": x.RecentLogs})
		})
	default:
		method(w)
	}
}
func (a *API) link(w http.ResponseWriter, r *http.Request, seg []string) {
	sp, t, e := a.authorized(r)
	if e != nil {
		apiErr(w, e)
		return
	}
	name := seg[1]
	if len(seg) == 3 && seg[2] == "logs" {
		if r.Method != "GET" {
			method(w)
			return
		}
		tail := 100
		if v := r.URL.Query().Get("tail"); v != "" {
			tail, e = strconv.Atoi(v)
			if e != nil || tail < 0 {
				apiErr(w, domain.NewValidation("tail", "must be a non-negative integer"))
				return
			}
		}
		if r.URL.Query().Get("follow") == "true" {
			a.follow(w, r, sp, t, name)
			return
		}
		x, e := a.service.LogsFor(r.Context(), sp.Alias.String(), t, name, tail)
		if e != nil {
			apiErr(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"logs": x})
		return
	}
	if len(seg) == 3 && seg[2] == "restart" {
		if r.Method != "POST" {
			method(w)
			return
		}
		a.mutation(w, r, func() {
			x, e := a.service.RestartLink(r.Context(), application.LinkMutationInput{Alias: sp.Alias.String(), Token: t, Name: name})
			if e != nil {
				apiErr(w, e)
				return
			}
			writeJSON(w, 200, map[string]any{"link": outLink(x.Link, x.URL)})
		})
		return
	}
	if len(seg) != 2 {
		writeErr(w, 404, "not_found", "route not found", nil)
		return
	}
	if r.Method != "DELETE" {
		method(w)
		return
	}
	a.mutation(w, r, func() {
		e := a.service.DeleteLink(r.Context(), application.LinkMutationInput{Alias: sp.Alias.String(), Token: t, Name: name})
		if e != nil {
			apiErr(w, e)
			return
		}
		w.WriteHeader(204)
	})
}
func (a *API) follow(w http.ResponseWriter, r *http.Request, sp domain.Space, t domain.Token, name string) {
	stream, e := a.service.FollowLogs(r.Context(), sp.Alias.String(), t, name)
	if e != nil {
		apiErr(w, e)
		return
	}
	defer stream.Close()
	w.Header().Set("Content-Type", "application/x-ndjson")
	f, ok := w.(http.Flusher)
	if !ok {
		apiErr(w, fmt.Errorf("streaming unsupported"))
		return
	}
	buf := make([]byte, 32*1024)
	for {
		n, e := stream.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			f.Flush()
		}
		if e != nil {
			return
		}
		select {
		case <-r.Context().Done():
			return
		default:
		}
	}
}

func (a *API) adminLinks(w http.ResponseWriter, r *http.Request, seg []string) {
	if r.Method == http.MethodPost || r.Method == http.MethodDelete {
		a.mutation(w, r, func() { a.adminLinksInner(w, r, seg) })
		return
	}
	a.adminLinksInner(w, r, seg)
}
func (a *API) adminLinksInner(w http.ResponseWriter, r *http.Request, seg []string) {
	ad, t, e := a.admin(r)
	if e != nil || ad == nil {
		if e == nil {
			e = domain.NewUnauthorized("admin disabled")
		}
		apiErr(w, e)
		return
	}
	alias := seg[2]
	if len(seg) == 4 && r.Method == http.MethodGet {
		x, e := ad.AdminListLinks(r.Context(), t, alias)
		if e != nil {
			apiErr(w, e)
			return
		}
		o := make([]linkOut, len(x))
		for i := range x {
			o[i] = outLink(x[i], "")
		}
		writeJSON(w, 200, map[string]any{"links": o})
		return
	}
	if len(seg) == 6 && seg[5] == "logs" && r.Method == http.MethodGet {
		tail := 100
		if v := r.URL.Query().Get("tail"); v != "" {
			tail, e = strconv.Atoi(v)
			if e != nil || tail < 0 {
				apiErr(w, domain.NewValidation("tail", "must be a non-negative integer"))
				return
			}
		}
		x, e := ad.AdminLogsFor(r.Context(), t, alias, seg[4], tail)
		if e != nil {
			apiErr(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"logs": x})
		return
	}
	if len(seg) == 6 && (seg[5] == "restart" || seg[5] == "delete") {
		var x struct {
			Reason string `json:"reason"`
		}
		if !decode(w, r, a.maxBody, &x) {
			return
		}
		if seg[5] == "restart" && r.Method == http.MethodPost {
			z, e := ad.AdminRestartLink(r.Context(), t, alias, seg[4], x.Reason)
			if e != nil {
				apiErr(w, e)
				return
			}
			writeJSON(w, 200, map[string]any{"link": outLink(z.Link, z.URL)})
			return
		}
		if seg[5] == "delete" && r.Method == http.MethodDelete {
			e := ad.AdminDeleteLink(r.Context(), t, alias, seg[4], x.Reason)
			if e != nil {
				apiErr(w, e)
				return
			}
			w.WriteHeader(204)
			return
		}
	}
	method(w)
}
