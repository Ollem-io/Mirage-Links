package cli

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandMatrix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/spaces" && r.Method == "POST":
			io.WriteString(w, `{"space":{"alias":"calm","expires_at":"soon"},"token":"mir_x"}`)
		case strings.HasPrefix(r.URL.Path, "/api/v1/spaces/"):
			io.WriteString(w, `{"space":{"alias":"calm","expires_at":"soon"}}`)
		case r.URL.Path == "/api/v1/spaces":
			io.WriteString(w, `{"spaces":[]}`)
		case r.URL.Path == "/api/v1/links" && r.Method == "POST":
			io.WriteString(w, `{"link":{"name":"api","url":"http://x","status":"active"}}`)
		case r.URL.Path == "/api/v1/links":
			io.WriteString(w, `{"links":[]}`)
		case strings.HasSuffix(r.URL.Path, "/logs"):
			io.WriteString(w, `{"logs":[]}`)
		default:
			io.WriteString(w, `{}`)
		}
	}))
	defer srv.Close()
	cases := [][]string{
		{"space", "create", "--ttl", "1h", "--alias", "calm"}, {"space", "create", "--json"},
		{"space", "list"}, {"space", "list", "calm"}, {"space", "delete", "calm", "--token", "tok"}, {"space", "delete", "calm", "--force", "audit reason"},
		{"link", "list", "--token", "tok"}, {"link", "list", "--token", "tok", "--json"},
		{"link", "create", "--token", "tok", "--name", "api", "--command", "echo", "--execution-folder", ".", "--health-check", "GET http://127.0.0.1:{port}", "--grace", "2s", "--ttl", "1h", "--restarts"},
		{"link", "logs", "api", "--token", "tok", "--tail", "5"}, {"link", "restart", "api", "--token", "tok"}, {"link", "delete", "api", "--token", "tok", "--json"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var o, e bytes.Buffer
			c := New(&o, &e, func() string { return "v" })
			full := append([]string{"--server", srv.URL}, args...)
			if x := c.Execute(full); x != 0 {
				t.Fatalf("exit %d stderr=%s", x, e.String())
			}
		})
	}
}
func TestResolutionAndValidationBranches(t *testing.T) {
	t.Setenv("MIRAGE_TOKEN", "env")
	var o, e bytes.Buffer
	c := New(&o, &e, func() string { return "v" })
	if x, _ := c.token(""); x != "env" {
		t.Fatal(x)
	}
	c.forcedToken = "global"
	if x, _ := c.token(""); x != "global" {
		t.Fatal(x)
	}
	if x, _ := c.token("local"); x != "local" {
		t.Fatal(x)
	}
	c.getwd = func() (string, error) { return "", context.Canceled }
	c.forcedToken = ""
	t.Setenv("MIRAGE_TOKEN", "")
	if _, x := c.token(""); x == nil {
		t.Fatal("want error")
	}
	for _, a := range []string{"bad", ":0", "x:99999", "x:no"} {
		if validBind(a) {
			t.Fatal(a)
		}
	}
	for _, a := range []string{"127.0.0.1:1", ":9955"} {
		if !validBind(a) {
			t.Fatal(a)
		}
	}
	if address("9955") != ":9955" || address("x:1") != "x:1" {
		t.Fatal()
	}
}
func TestConfigBranches(t *testing.T) {
	d := t.TempDir()
	good := filepath.Join(d, "c.yaml")
	os.WriteFile(good, []byte("base_host: example.com\npublic_address: ':1234'\nprivate_address: '127.0.0.1:1235'\ndata_path: db\ncaddy:\n  admin_url: http://127.0.0.1:2019\n  binary: caddy\n  managed: true\n"), 0600)
	c, e := loadConfig(good, os.Getenv, os.Getwd)
	if e != nil || !c.Caddy.Managed || c.BaseHost != "example.com" {
		t.Fatalf("%+v %v", c, e)
	}
	bad := filepath.Join(d, "bad")
	os.WriteFile(bad, []byte("base_host: BAD HOST\n"), 0600)
	if _, e = loadConfig(bad, os.Getenv, os.Getwd); e == nil {
		t.Fatal()
	}
	os.WriteFile(bad, []byte("private_address: nope\n"), 0600)
	if _, e = loadConfig(bad, os.Getenv, os.Getwd); e == nil {
		t.Fatal()
	}
}
func TestFailureMatrix(t *testing.T) {
	cases := [][]string{{"space"}, {"space", "wat"}, {"space", "delete"}, {"space", "create", "--ttl"}, {"link"}, {"link", "wat"}, {"link", "logs"}, {"link", "restart"}, {"link", "create", "--wat", "x"}, {"link", "list", "--token"}, {"start"}}
	for _, a := range cases {
		var o, e bytes.Buffer
		c := New(&o, &e, func() string { return "v" })
		if c.Execute(a) == 0 {
			t.Fatalf("%v", a)
		}
	}
}
func TestTransportAndMalformed(t *testing.T) {
	for _, body := range []string{"not-json", "[]"} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
		var o, e bytes.Buffer
		c := New(&o, &e, func() string { return "v" })
		if c.Execute([]string{"--server", s.URL, "space", "list"}) == 0 {
			t.Fatal(body)
		}
		s.Close()
	}
	var o, e bytes.Buffer
	c := New(&o, &e, func() string { return "v" })
	c.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, context.Canceled })}
	if c.Execute([]string{"space", "list"}) == 0 {
		t.Fatal()
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStartMatrix(t *testing.T) {
	cases := []struct {
		args                []string
		failStart, failStop bool
		want                int
	}{{[]string{"start", "--public", "1234", "--private", "1235"}, false, false, 0}, {[]string{"start", "--wat", "x"}, false, false, 2}, {[]string{"start", "--public"}, false, false, 2}, {[]string{"start"}, true, false, 1}, {[]string{"start"}, false, true, 1}}
	for _, tc := range cases {
		var o, e bytes.Buffer
		c := NewWithStart(&o, &e, func() string { return "v" }, func(ctx context.Context, opt StartOptions) (func() error, error) {
			if tc.failStart {
				return nil, context.Canceled
			}
			return func() error {
				if tc.failStop {
					return context.Canceled
				}
				return nil
			}, nil
		})
		c.waitSignal = func() {}
		if x := c.Execute(tc.args); x != tc.want {
			t.Fatalf("%v got %d: %s", tc.args, x, e.String())
		}
	}
}
func TestPrintRowsShapes(t *testing.T) {
	var o, e bytes.Buffer
	c := New(&o, &e, func() string { return "v" })
	c.printRows(map[string]any{"space": map[string]any{"alias": "a", "expires_at": "x"}}, "spaces", []string{"alias", "expires_at"})
	c.printRows(map[string]any{"spaces": []any{map[string]any{"alias": "a", "expires_at": "x"}, "bad"}}, "spaces", []string{"alias", "expires_at"})
	c.printRows(map[string]any{}, "spaces", nil)
	if !strings.Contains(o.String(), "Alias: a") {
		t.Fatal(o.String())
	}
}
func TestMoreFailures(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500); io.WriteString(w, "opaque secret") }))
	defer s.Close()
	var o, e bytes.Buffer
	c := New(&o, &e, func() string { return "v" })
	if c.Execute([]string{"--server", s.URL, "space", "list"}) != 1 || strings.Contains(e.String(), "secret") {
		t.Fatal(e.String())
	}
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, ".mirage_token"), nil, 0600)
	c.getwd = func() (string, error) { return d, nil }
	if _, x := c.token(""); x == nil {
		t.Fatal()
	}
}

func TestFollowEOFAndErrors(t *testing.T) {
	for _, status := range []int{200, 401} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			if status != 200 {
				io.WriteString(w, `{"code":"unauthorized","message":"no"}`)
			} else {
				io.WriteString(w, "line\n")
			}
		}))
		var o, e bytes.Buffer
		c := New(&o, &e, func() string { return "v" })
		x := c.Execute([]string{"--server", s.URL, "link", "logs", "api", "--token", "tok", "--follow"})
		if status == 200 && x != 0 {
			t.Fatal(x, e.String())
		}
		if status != 200 && x == 0 {
			t.Fatal()
		}
		s.Close()
	}
}
func TestCobraTree(t *testing.T) {
	var o, e bytes.Buffer
	c := New(&o, &e, func() string { return "v" })
	root := c.cobraTree()
	root.SetOut(&o)
	if x := root.Help(); x != nil {
		t.Fatal(x)
	}
	if !strings.Contains(o.String(), "Manage spaces") {
		t.Fatal(o.String())
	}
}

func TestUsageEdgeMatrix(t *testing.T) {
	cases := [][]string{{"--server"}, {"--config"}, {"--token"}, {"space", "list", "a", "b"}, {"space", "delete", "a", "--force"}, {"space", "delete", "a", "--wat"}, {"link", "list", "--token", "t", "extra"}, {"link", "create", "--restarts", "oops"}, {"link", "logs", "api", "--tail"}, {"link", "logs", "api", "--wat"}, {"link", "delete", "api", "--token", "t", "extra"}}
	for _, a := range cases {
		var o, e bytes.Buffer
		c := New(&o, &e, func() string { return "v" })
		if c.Execute(a) == 0 {
			t.Fatalf("%v", a)
		}
	}
}
func TestEmptyAndBadRequestBodies(t *testing.T) {
	for _, empty := range []bool{true, false} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !empty {
				io.WriteString(w, `{"spaces":[]}`)
			}
		}))
		var o, e bytes.Buffer
		c := New(&o, &e, func() string { return "v" })
		x := c.Execute([]string{"--server", s.URL, "--json", "space", "list"})
		if x != 0 {
			t.Fatal(x, e.String())
		}
		s.Close()
	}
}

func TestConfigResolutionDefaults(t *testing.T) {
	d := t.TempDir()
	t.Setenv("HOME", d)
	c, e := loadConfig("", os.Getenv, os.Getwd)
	if e != nil || c.BaseHost != "" {
		t.Fatal(c, e)
	}
	t.Setenv("MIRAGE_CONFIG", filepath.Join(d, "missing"))
	if _, e = loadConfig("", os.Getenv, os.Getwd); e != nil {
		t.Fatal(e)
	}
	if _, e = loadConfig(d, os.Getenv, os.Getwd); e == nil {
		t.Fatal()
	}
}
func TestServerWithoutScheme(t *testing.T) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	s := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"spaces":[]}`) })}
	go s.Serve(l)
	defer s.Close()
	var o, er bytes.Buffer
	c := New(&o, &er, func() string { return "v" })
	if x := c.Execute([]string{"--server", l.Addr().String(), "space", "list"}); x != 0 {
		t.Fatal(x, er.String())
	}
}
