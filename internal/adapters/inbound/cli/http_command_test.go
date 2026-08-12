package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPCommandsAndJSON(t *testing.T) {
	var got struct{ path, auth string }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.String()
		got.auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v1/spaces":
			io.WriteString(w, `{"space":{"alias":"calm","expires_at":"tomorrow"},"token":"mir_secret"}`)
		case r.URL.Path == "/api/v1/links":
			io.WriteString(w, `{"links":[{"name":"api","status":"active"}]}`)
		default:
			io.WriteString(w, `{"logs":[]}`)
		}
	}))
	defer srv.Close()
	var out, err bytes.Buffer
	c := New(&out, &err, func() string { return "x" })
	if x := c.Execute([]string{"--server", srv.URL, "space", "create", "--ttl", "1h"}); x != 0 {
		t.Fatal(x, err.String())
	}
	if !strings.Contains(out.String(), "Token: mir_secret") {
		t.Fatal(out.String())
	}
	out.Reset()
	err.Reset()
	if x := c.Execute([]string{"--server", srv.URL, "--token", "top", "--json", "link", "list"}); x != 0 {
		t.Fatal(x, err.String())
	}
	if got.auth != "Bearer top" || !strings.Contains(out.String(), `"links"`) {
		t.Fatalf("%q %q", got.auth, out.String())
	}
}
func TestTokenFileExactAndWarning(t *testing.T) {
	d := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(d)
	os.WriteFile(".mirage_token", []byte(" file-token \n"), 0644)
	var out, er bytes.Buffer
	c := New(&out, &er, func() string { return "x" })
	x, e := c.token("")
	if e != nil || x != "file-token" || !strings.Contains(er.String(), "group/world-readable") {
		t.Fatal(x, e, er.String())
	}
	os.Remove(".mirage_token")
	os.Mkdir("child", 0755)
	os.WriteFile(filepath.Join(d, ".mirage_token"), []byte("parent"), 0600)
	os.Chdir("child")
	c.getwd = os.Getwd
	if _, e = c.token(""); e == nil {
		t.Fatal("searched parent")
	}
}
func TestAPIError(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, `{"code":"unauthorized","message":"no"}`)
	}))
	defer s.Close()
	var o, e bytes.Buffer
	c := New(&o, &e, func() string { return "x" })
	if c.Execute([]string{"--server", s.URL, "space", "list"}) != 1 || !strings.Contains(e.String(), "unauthorized") {
		t.Fatal(e.String())
	}
}

func TestEqualsFlagsAndMalformedSuccessShape(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"spaces":"not-an-array"}`)
	}))
	defer s.Close()
	var out, er bytes.Buffer
	c := New(&out, &er, func() string { return "x" })
	if got := c.Execute([]string{"--server=" + s.URL, "--json", "space", "list"}); got != 0 {
		t.Fatalf("%d %s", got, er.String())
	}
	out.Reset()
	er.Reset()
	if got := c.Execute([]string{"--server=" + s.URL, "--token=ok", "link", "list"}); got != 0 {
		t.Fatalf("%d %s", got, er.String())
	}
}
