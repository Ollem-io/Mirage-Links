package caddy

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
)

func freeAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}
func waitAdmin(t *testing.T, admin string) func(context.Context) error {
	t.Helper()
	return func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+admin+"/config/", nil)
		if err != nil {
			return err
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		res.Body.Close()
		if res.StatusCode/100 != 2 {
			return &Error{Rejected, io.EOF}
		}
		return nil
	}
}

// TestRealCaddyAdminHarness is intentionally invoked by `mise run caddy-integration`.
// It launches the pinned Caddy executable on ephemeral loopback ports and proves
// Admin API route mutation produces actual proxy traffic without touching a
// third-party route. This remains hermetic: no DNS, privileged ports, or host
// Caddy is used.
func TestRealCaddyAdminHarness(t *testing.T) {
	binary, err := exec.LookPath("caddy")
	if err != nil {
		t.Skip("run with mise run caddy-integration")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("upstream ok")) }))
	defer upstream.Close()
	admin, public := freeAddress(t), freeAddress(t)
	external := map[string]any{"@id": "third-party", "match": []any{map[string]any{"host": []string{"external.test"}}}, "handle": []any{map[string]any{"handler": "static_response", "body": "external"}}, "terminal": true}
	config := map[string]any{"admin": map[string]any{"listen": admin}, "apps": map[string]any{"http": map[string]any{"servers": map[string]any{"srv0": map[string]any{"listen": []string{public}, "automatic_https": map[string]any{"disable": true}, "routes": []any{external}}}}}}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "caddy.json")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	child, err := Start(ctx, ChildConfig{Managed: true, Binary: binary, Args: []string{"run", "--config", file}, ReadyTimeout: 4 * time.Second, Probe: waitAdmin(t, admin)})
	if err != nil {
		t.Fatal(err)
	}
	defer child.Stop()
	client, err := New(Config{AdminURL: "http://" + admin, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	r := ports.Route{LinkID: domain.LinkID("real"), Hostname: "proxy.test", Upstream: upstream.Listener.Addr().String()}
	if err := client.Add(ctx, r); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+public+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "proxy.test"
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "upstream ok" {
		t.Fatalf("proxy body=%q status=%d", body, res.StatusCode)
	}
	// Snapshot the unrelated route via Admin API; remove must preserve it exactly.
	var before []json.RawMessage
	get := func(out any) {
		res, err := http.Get("http://" + admin + "/config/apps/http/servers/srv0/routes")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	get(&before)
	if err := client.Remove(ctx, "real"); err != nil {
		t.Fatal(err)
	}
	var after []json.RawMessage
	get(&after)
	if len(after) != 1 || string(after[0]) != string(before[0]) {
		t.Fatalf("external route changed: before=%s after=%s", before, after)
	}
	// External mode has no process ownership, even when connected to this real API.
	externalChild, err := Start(ctx, ChildConfig{})
	if err != nil || externalChild.cmd != nil {
		t.Fatal(externalChild, err)
	}
	if err := externalChild.Stop(); err != nil {
		t.Fatal(err)
	}
}
