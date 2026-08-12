package composition

import (
	"bytes"
	"context"
	"github.com/primeintellect/mirage/internal/adapters/inbound/cli"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/primeintellect/mirage/internal/buildinfo"
)

func TestNewCommandUsesBuildVersion(t *testing.T) {
	original := buildinfo.Version
	buildinfo.Version = "test-build"
	t.Cleanup(func() { buildinfo.Version = original })

	var stdout, stderr bytes.Buffer
	exit := NewCommand(&stdout, &stderr).Execute([]string{"version"})
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if got := stdout.String(); got != "mirage test-build\n" {
		t.Fatalf("stdout = %q", got)
	}
	if strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestStartExternalLifecycle(t *testing.T) {
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	}))
	defer admin.Close()
	d := t.TempDir()
	stop, e := Start(context.Background(), cli.StartOptions{PublicAddress: "127.0.0.1:0", PrivateAddress: "127.0.0.1:0", DataPath: filepath.Join(d, "db"), BaseHost: "example.com", CaddyAdmin: admin.URL, CaddyManaged: false})
	if e != nil {
		t.Fatal(e)
	}
	if e = stop(); e != nil {
		t.Fatal(e)
	}
	if e = stop(); e != nil {
		t.Fatal(e)
	}
}
func TestStartValidationFailures(t *testing.T) {
	if _, e := Start(context.Background(), cli.StartOptions{BaseHost: "BAD HOST"}); e == nil {
		t.Fatal()
	}
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("[]")) }))
	defer admin.Close()
	d := t.TempDir()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	if _, e = Start(context.Background(), cli.StartOptions{PrivateAddress: l.Addr().String(), PublicAddress: "127.0.0.1:0", DataPath: filepath.Join(d, "db"), CaddyAdmin: admin.URL}); e == nil {
		t.Fatal()
	}
}

func TestStartManagedFailure(t *testing.T) {
	d := t.TempDir()
	if _, e := Start(context.Background(), cli.StartOptions{PublicAddress: "127.0.0.1:0", PrivateAddress: "127.0.0.1:0", DataPath: filepath.Join(d, "db"), BaseHost: "example.com", CaddyManaged: true, CaddyBinary: filepath.Join(d, "missing")}); e == nil {
		t.Fatal()
	}
}
