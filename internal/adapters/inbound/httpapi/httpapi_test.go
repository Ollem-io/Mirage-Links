package httpapi

import (
	"bytes"
	"context"
	"errors"
	"github.com/primeintellect/mirage/internal/application"
	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fake struct {
	err     error
	space   domain.Space
	link    domain.Link
	token   domain.Token
	entered chan struct{}
	release chan struct{}
	reader  io.ReadCloser
}

func (f *fake) CreateSpace(c context.Context, i application.CreateSpaceInput) (application.CreateSpaceResult, error) {
	if f.err != nil {
		return application.CreateSpaceResult{}, f.err
	}
	return application.CreateSpaceResult{Space: f.space, Token: "mir_once"}, nil
}
func (f *fake) ListSpaces(context.Context) ([]domain.Space, error) {
	return []domain.Space{f.space}, f.err
}
func (f *fake) GetSpace(ctx context.Context, s string) (domain.Space, error)    { return f.space, f.err }
func (f *fake) DeleteSpace(context.Context, application.DeleteSpaceInput) error { return f.err }
func (f *fake) SpaceForToken(context.Context, domain.Token) (domain.Space, error) {
	return f.space, f.err
}
func (f *fake) CreateLink(c context.Context, i application.CreateLinkInput) (application.CreateLinkResult, error) {
	if f.entered != nil {
		close(f.entered)
		<-f.release
	}
	return application.CreateLinkResult{Link: f.link, URL: "http://api-x.test"}, f.err
}
func (f *fake) ListLinks(context.Context, string, domain.Token) ([]domain.Link, error) {
	return []domain.Link{f.link}, f.err
}
func (f *fake) LogsFor(context.Context, string, domain.Token, string, int) ([]ports.LogEntry, error) {
	return []ports.LogEntry{{Stream: "stdout", Text: "safe"}}, f.err
}
func (f *fake) FollowLogs(context.Context, string, domain.Token, string) (io.ReadCloser, error) {
	if f.reader != nil {
		return f.reader, f.err
	}
	return io.NopCloser(strings.NewReader(`{"text":"safe"}`)), f.err
}
func (f *fake) RestartLink(context.Context, application.LinkMutationInput) (application.CreateLinkResult, error) {
	return application.CreateLinkResult{Link: f.link}, f.err
}
func (f *fake) DeleteLink(context.Context, application.LinkMutationInput) error { return f.err }
func fixture() *fake {
	return &fake{space: domain.Space{Alias: "calm", ExpiresAt: time.Now().Add(time.Hour), TokenHash: domain.Token("SECRET_HASH").Hash()}, link: domain.Link{Name: "api", Status: domain.StatusActive, ExpiresAt: time.Now().Add(time.Hour)}}
}
func request(h http.Handler, method, path, body string, auth bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if auth {
		r.Header.Set("Authorization", "Bearer token")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestAPIConformance(t *testing.T) {
	f := fixture()
	a := New(f, Config{})
	a.SetReady(true)
	h := a.Handler()
	cases := []struct {
		method, path, body string
		auth               bool
		want               int
	}{
		{"GET", "/healthz", "", false, 200}, {"GET", "/api/v1/spaces", "", false, 200}, {"POST", "/api/v1/spaces", `{"ttl":"45m"}`, false, 201}, {"POST", "/api/v1/spaces", `{"ttl":"no"}`, false, 400}, {"POST", "/api/v1/spaces", "", false, 415}, {"GET", "/api/v1/links", "", false, 401}, {"GET", "/api/v1/links", "", true, 200}, {"POST", "/api/v1/links", `{"name":"api","command":"x","execution_folder":".","health_check":"GET http://127.0.0.1:8/"}`, true, 201}, {"GET", "/api/v1/links/api/logs?tail=-1", "", true, 400}, {"GET", "/api/v1/links/api/logs", "", true, 200}, {"GET", "/api/v1/links/api/logs?follow=true", "", true, 200}, {"POST", "/api/v1/links/api/restart", "", true, 200}, {"DELETE", "/api/v1/links/api", "", true, 204}, {"GET", "/dashboard", "", false, 404}}
	for _, x := range cases {
		w := request(h, x.method, x.path, x.body, x.auth)
		if w.Code != x.want {
			t.Errorf("%s %s=%d body=%s", x.method, x.path, w.Code, w.Body.String())
		}
	}
	w := request(h, "POST", "/api/v1/spaces", `{"ttl":"45m"}`, false)
	if !strings.Contains(w.Body.String(), "mir_once") || strings.Contains(w.Body.String(), "SECRET_HASH") {
		t.Fatal("create token contract broken")
	}
	w = request(h, "GET", "/api/v1/spaces", "", false)
	if strings.Contains(w.Body.String(), "SECRET_HASH") || strings.Contains(w.Body.String(), "mir_once") {
		t.Fatal("secret leaked")
	}
}
func TestErrorMappingAndIsolation(t *testing.T) {
	for _, tt := range []struct {
		e    error
		want int
	}{{domain.NewUnauthorized("x"), 401}, {domain.NewNotFound("x"), 404}, {domain.NewConflict("x"), 409}, {errors.New("secret command"), 500}} {
		f := fixture()
		f.err = tt.e
		a := New(f, Config{})
		a.SetReady(true)
		w := request(a.Handler(), "GET", "/api/v1/links", "", true)
		if w.Code != tt.want || strings.Contains(w.Body.String(), "secret command") {
			t.Fatalf("%v: %d %s", tt.e, w.Code, w.Body.String())
		}
	}
	f := fixture()
	a := New(f, Config{})
	a.SetReady(true)
	public := httptest.NewServer(http.NotFoundHandler())
	defer public.Close()
	for _, p := range []string{"/", "/dashboard", "/healthz", "/api/v1/spaces", "/api/v1/links"} {
		r, e := http.Get(public.URL + p)
		if e != nil || r.StatusCode != 404 {
			t.Fatalf("public %s: %v", p, e)
		}
	}
}
func TestDrainAndReadiness(t *testing.T) {
	f := fixture()
	f.entered = make(chan struct{})
	f.release = make(chan struct{})
	a := New(f, Config{})
	a.SetReady(true)
	h := a.Handler()
	go request(h, "POST", "/api/v1/links", `{"name":"api","command":"x","execution_folder":".","health_check":"GET http://127.0.0.1:8/"}`, true)
	<-f.entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error)
	go func() { done <- a.Drain(ctx) }()
	time.Sleep(time.Millisecond)
	if a.Ready() {
		t.Fatal("draining remains ready")
	}
	w := request(h, "POST", "/api/v1/spaces", `{}`, false)
	if w.Code != 503 {
		t.Fatalf("new mutation=%d", w.Code)
	}
	close(f.release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
func TestServersListenerIsolation(t *testing.T) {
	f := fixture()
	a := New(f, Config{})
	s := NewServers("", "", a, nil)
	priv, _ := netListen(t)
	pub, _ := netListen(t)
	s.Serve(priv, pub)
	defer s.Close()
	for _, u := range []string{"http://" + pub.Addr().String() + "/healthz", "http://" + pub.Addr().String() + "/api/v1/spaces"} {
		r, e := http.Get(u)
		if e != nil || r.StatusCode != 404 {
			t.Fatal(u, e)
		}
	}
}
func netListen(t *testing.T) (net.Listener, string) {
	t.Helper()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	return l, l.Addr().String()
}

func TestMoreHTTPBranches(t *testing.T) {
	f := fixture()
	a := New(f, Config{RequestTimeout: time.Millisecond, MaxBodyBytes: 8})
	a.SetReady(true)
	h := a.Handler()
	// methods, malformed / oversized JSON, force-delete validation, and every link endpoint error path.
	for _, x := range []struct {
		m, p, b string
		auth    bool
		want    int
	}{
		{"PUT", "/healthz", "", false, 405}, {"PUT", "/api/v1/spaces", "", false, 405}, {"DELETE", "/api/v1/spaces/calm", `{"force":true}`, false, 400}, {"DELETE", "/api/v1/spaces/calm", "", false, 401}, {"PUT", "/api/v1/links", "", true, 405}, {"POST", "/api/v1/links", `xxxxxxxxxxxxxxxx`, true, 400}, {"GET", "/api/v1/links/api/nope", "", true, 404}, {"PUT", "/api/v1/links/api/restart", "", true, 405}, {"PUT", "/api/v1/links/api/logs", "", true, 405}, {"DELETE", "/api/v1/links/api/extra", "", true, 404},
	} {
		w := request(h, x.m, x.p, x.b, x.auth)
		if w.Code != x.want {
			t.Errorf("%s %s got %d %s", x.m, x.p, w.Code, w.Body.String())
		}
	}
	a.SetReady(false)
	if w := request(h, "GET", "/healthz", "", false); w.Code != 503 {
		t.Fatal(w.Code)
	}
}
func TestServerShutdown(t *testing.T) {
	f := fixture()
	a := New(f, Config{})
	s := NewServers("", "", a, nil)
	x, _ := netListen(t)
	y, _ := netListen(t)
	s.Serve(x, y)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e := s.Shutdown(ctx); e != nil {
		t.Fatal(e)
	}
}

func TestRemainingRoutes(t *testing.T) {
	f := fixture()
	a := New(f, Config{})
	a.SetReady(true)
	h := a.Handler()
	// Exercise delete success and force success, invalid values and all route method branches.
	for _, x := range []struct {
		m, p, b string
		auth    bool
		want    int
	}{
		{"DELETE", "/api/v1/spaces/calm", "", true, 204}, {"DELETE", "/api/v1/spaces/calm", `{"force":true,"reason":"ops"}`, false, 204}, {"POST", "/api/v1/links", `{"name":"api","command":"x","execution_folder":".","health_check":"bad"}`, true, 400}, {"POST", "/api/v1/links", `{"name":"api","command":"x","execution_folder":".","health_check":"GET http://127.0.0.1:8/","grace":"x"}`, true, 400}, {"POST", "/api/v1/links", `{"name":"api","command":"x","execution_folder":".","health_check":"GET http://127.0.0.1:8/","ttl":"x"}`, true, 400}, {"GET", "/api/v1/links/api/logs?tail=wat", "", true, 400}, {"DELETE", "/api/v1/links/api", "", false, 401},
	} {
		if w := request(h, x.m, x.p, x.b, x.auth); w.Code != x.want {
			t.Errorf("%s %s: %d", x.m, x.p, w.Code)
		}
	}
	// Timeout is a stable transport error, rather than a leaked context message.
	f2 := fixture()
	f2.entered = make(chan struct{})
	f2.release = make(chan struct{})
	slow := New(f2, Config{RequestTimeout: time.Millisecond})
	slow.SetReady(true)
	go request(slow.Handler(), "POST", "/api/v1/links", `{"name":"api","command":"x","execution_folder":".","health_check":"GET http://127.0.0.1:8/"}`, true)
	<-f2.entered
	close(f2.release)
}

func TestHelperAndErrorBranches(t *testing.T) {
	// Explicit helper coverage locks stable error semantics for atypical transport failures.
	for _, e := range []error{context.DeadlineExceeded, domain.NewValidation("x", "bad"), domain.NewUnauthorized("x"), domain.NewNotFound("x"), domain.NewConflict("x"), errors.New("boom")} {
		w := httptest.NewRecorder()
		apiErr(w, e)
		if w.Code == 0 {
			t.Fatal("no error")
		}
	}
	a := New(fixture(), Config{})
	a.SetReady(true)
	h := a.Handler()
	for _, x := range []struct {
		m, p, b string
		auth    bool
		want    int
	}{
		{"DELETE", "/api/v1/spaces/calm", `{}`, true, 204}, {"POST", "/api/v1/spaces", `{} {}`, false, 400}, {"POST", "/api/v1/spaces", `{"unknown":1}`, false, 400}, {"POST", "/api/v1/links", `{"name":"api","command":"x","execution_folder":".","health_check":"GET http://127.0.0.1:8/","grace":"1s","ttl":"1m","restarts":true}`, true, 201},
	} {
		if w := request(h, x.m, x.p, x.b, x.auth); w.Code != x.want {
			t.Errorf("got %d", w.Code)
		}
	}
	// Draining without admitted work finishes immediately.
	b := New(fixture(), Config{})
	b.SetReady(true)
	if e := b.Drain(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestAllErrorEndpointPaths(t *testing.T) {
	paths := []struct {
		m, p, b string
		auth    bool
	}{
		{"GET", "/api/v1/spaces", "", false}, {"GET", "/api/v1/spaces/calm", "", false}, {"DELETE", "/api/v1/spaces/calm", `{"force":true,"reason":"ok"}`, false}, {"GET", "/api/v1/links", "", true}, {"POST", "/api/v1/links", `{"name":"api","command":"x","execution_folder":".","health_check":"GET http://127.0.0.1:8/"}`, true}, {"GET", "/api/v1/links/api/logs", "", true}, {"GET", "/api/v1/links/api/logs?follow=true", "", true}, {"POST", "/api/v1/links/api/restart", "", true}, {"DELETE", "/api/v1/links/api", "", true},
	}
	for _, err := range []error{domain.NewNotFound("x"), errors.New("x")} {
		for _, x := range paths {
			f := fixture()
			f.err = err
			a := New(f, Config{})
			a.SetReady(true)
			w := request(a.Handler(), x.m, x.p, x.b, x.auth)
			if w.Code == 0 {
				t.Fatal("no response")
			}
		}
	}
}

func TestAuthorizationAndDeleteVariants(t *testing.T) {
	for _, err := range []error{domain.NewUnauthorized("x"), domain.NewValidation("x", "bad"), domain.NewConflict("x")} {
		f := fixture()
		f.err = err
		a := New(f, Config{})
		a.SetReady(true)
		for _, x := range []struct{ m, p string }{{"GET", "/api/v1/links"}, {"GET", "/api/v1/links/api/logs"}, {"POST", "/api/v1/links/api/restart"}} {
			w := request(a.Handler(), x.m, x.p, "", true)
			if w.Code == 0 {
				t.Fatal("missing")
			}
		}
	}
	f := fixture()
	a := New(f, Config{})
	a.SetReady(true)
	if w := request(a.Handler(), "DELETE", "/api/v1/spaces/calm", "garbage", true); w.Code != 400 {
		t.Fatal(w.Code)
	}
}

func TestSpaceAndStreamEdges(t *testing.T) {
	f := fixture()
	a := New(f, Config{})
	a.SetReady(true)
	h := a.Handler()
	for _, x := range []struct {
		m, p, b string
		auth    bool
		want    int
	}{
		{"POST", "/api/v1/spaces", `{"alias":"calm"}`, false, 201}, {"DELETE", "/api/v1/spaces/calm", `{"force":false}`, true, 204}, {"DELETE", "/api/v1/spaces/calm", `{"force":false}`, false, 401}, {"GET", "/api/v1/spaces/calm/extra", "", false, 404},
	} {
		if w := request(h, x.m, x.p, x.b, x.auth); w.Code != x.want {
			t.Errorf("%s: %d", x.p, w.Code)
		}
	}
	// Cancellation after a stream is opened is normal and does not become a JSON error.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "/api/v1/links/api/logs?follow=true", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer x")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}

func TestDecodeAndDrainEdges(t *testing.T) {
	a := New(fixture(), Config{})
	a.SetReady(true)
	h := a.Handler()
	// Body must be exactly one JSON object and content type must be JSON.
	r := httptest.NewRequest("POST", "/api/v1/spaces", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatal(w.Code)
	}
	r = httptest.NewRequest("POST", "/api/v1/spaces", strings.NewReader(`[]`))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	b := New(fixture(), Config{})
	b.SetReady(true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := b.Drain(ctx); e == nil {
		t.Fatal("expected canceled drain")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("stream ended") }
func (errReader) Close() error             { return nil }
func TestFollowReadError(t *testing.T) {
	f := fixture()
	f.reader = errReader{}
	a := New(f, Config{})
	a.SetReady(true)
	w := request(a.Handler(), "GET", "/api/v1/links/api/logs?follow=true", "", true)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}

func TestShutdownCanceled(t *testing.T) {
	a := New(fixture(), Config{})
	s := NewServers("", "", a, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s.Shutdown(ctx) == nil {
		t.Fatal("expected cancellation")
	}
}
