package composition

import (
	"bytes"
	"context"
	"github.com/primeintellect/mirage/internal/adapters/inbound/cli"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/primeintellect/mirage/internal/application/ports"
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

func TestManagedAdminListen(t *testing.T) {
	for _, good := range []string{"http://127.0.0.1:2020", "http://localhost:2021", "http://[::1]:2022"} {
		if got, err := managedAdminListen(good); err != nil || got == "" {
			t.Fatalf("%s: %q %v", good, got, err)
		}
	}
	for _, bad := range []string{"https://127.0.0.1:1", "http://127.0.0.1:1/path", "http://0.0.0.0:1", "http://example.com:1", "http://127.0.0.1", "ftp://127.0.0.1:1"} {
		if _, err := managedAdminListen(bad); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestManagedConfiguredAdminSuccessfulChild(t *testing.T) {
	d := t.TempDir()
	adminListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	admin := adminListener.Addr().String()
	adminListener.Close()
	publicListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	public := publicListener.Addr().String()
	publicListener.Close()
	fake := filepath.Join(d, "fake-caddy")
	script := `#!/bin/sh
python3 - "$3" <<'PY'
import http.server,json,sys
cfg=json.load(open(sys.argv[1])); host,port=cfg['admin']['listen'].rsplit(':',1)
class H(http.server.BaseHTTPRequestHandler):
 def log_message(self,*a): pass
 def do_GET(self):
  b=b'[]'; self.send_response(200); self.send_header('Content-Length',str(len(b))); self.end_headers(); self.wfile.write(b)
 def do_DELETE(self): self.send_response(200); self.end_headers()
 def do_POST(self): self.send_response(200); self.end_headers()
 def do_PUT(self): self.send_response(200); self.end_headers()
http.server.HTTPServer((host.strip('[]'),int(port)),H).serve_forever()
PY
`
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	stop, err := Start(context.Background(), cli.StartOptions{PublicAddress: public, PrivateAddress: "127.0.0.1:0", DataPath: filepath.Join(d, "db"), BaseHost: "example.com", CaddyManaged: true, CaddyBinary: fake, CaddyAdmin: "http://" + admin})
	if err != nil {
		t.Fatal(err)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(d, "mirage-caddy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), admin) {
		t.Fatalf("configured admin absent: %s", raw)
	}
}

func TestCompositionSmallAdapters(t *testing.T) {
	ctx := context.Background()
	if (ids{}).NewSpaceID() == "" || (ids{}).NewLinkID() == "" {
		t.Fatal()
	}
	if a, err := (aliases{}).NewAlias(); err != nil || a == "" {
		t.Fatal(a, err)
	}
	tok, err := (tokens{}).Generate()
	if err != nil {
		t.Fatal(err)
	}
	h := (hashes{}).Hash(tok)
	if !(hashes{}).Verify(h, tok) {
		t.Fatal()
	}
	n := noProxy{}
	if n.Add(ctx, ports.Route{}) != nil || n.Remove(ctx, "x") != nil || n.Reconcile(ctx, nil) != nil {
		t.Fatal()
	}
	if x, e := n.List(ctx); e != nil || x != nil {
		t.Fatal(x, e)
	}
}

func TestMoreStartAndListenerFailures(t *testing.T) {
	d := t.TempDir()
	if _, err := Start(context.Background(), cli.StartOptions{DataPath: filepath.Join(d, "missing", "db"), BaseHost: "example.com", CaddyAdmin: "http://127.0.0.1:1"}); err == nil {
		t.Fatal("open")
	}
	if _, err := Start(context.Background(), cli.StartOptions{DataPath: filepath.Join(d, "db2"), BaseHost: "example.com", CaddyAdmin: "ftp://bad"}); err == nil {
		t.Fatal("admin")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	api := NewHTTPAPI(nil)
	if _, err := StartHTTP(ListenerConfig{PrivateAddress: l.Addr().String(), PublicAddress: "127.0.0.1:0"}, api, nil); err == nil {
		t.Fatal("private bind")
	}
	l2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	if _, err := StartHTTP(ListenerConfig{PrivateAddress: "127.0.0.1:0", PublicAddress: l2.Addr().String()}, api, nil); err == nil {
		t.Fatal("public bind")
	}
	if _, err := StartPrivateHTTP(l.Addr().String(), api); err == nil {
		t.Fatal("private only bind")
	}
}
