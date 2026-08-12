package httpapi

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/primeintellect/mirage/internal/application"
	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
)

// TestDashboardRunningListenerArtifact is a black-box, headless HTTP/DOM
// journey. Unlike handler/httptest unit tests, it starts both production
// Servers on random loopback listeners and drives them through net/http.
type artifactService struct {
	*fake
	links    []domain.Link
	logs     []ports.LogEntry
	restarts []application.LinkMutationInput
	deletes  []application.LinkMutationInput
}

func (s *artifactService) ListLinks(context.Context, string, domain.Token) ([]domain.Link, error) {
	return append([]domain.Link(nil), s.links...), nil
}
func (s *artifactService) LogsFor(context.Context, string, domain.Token, string, int) ([]ports.LogEntry, error) {
	return append([]ports.LogEntry(nil), s.logs...), nil
}
func (s *artifactService) RestartLink(_ context.Context, in application.LinkMutationInput) (application.CreateLinkResult, error) {
	s.restarts = append(s.restarts, in)
	return application.CreateLinkResult{Link: s.link}, nil
}
func (s *artifactService) DeleteLink(_ context.Context, in application.LinkMutationInput) error {
	s.deletes = append(s.deletes, in)
	return nil
}
func (s *artifactService) FollowLogs(context.Context, string, domain.Token, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("unused")), nil
}

func TestDashboardRunningListenerArtifact(t *testing.T) {
	statuses := []domain.LinkStatus{domain.StatusCreating, domain.StatusStarting, domain.StatusHealthy, domain.StatusActive, domain.StatusStopping, domain.StatusDeleted, domain.StatusExpired, domain.StatusFailed}
	seed := fixture()
	seed.space.Alias = "calm"
	svc := &artifactService{fake: seed, logs: []ports.LogEntry{{At: time.Unix(1, 0).UTC(), Stream: "stdout", Text: `<script>token=mir_leak</script>&`}}}
	for i, status := range statuses {
		svc.links = append(svc.links, domain.Link{Name: domain.LinkName([]string{"creating", "starting", "healthy", "active", "stopping", "deleted", "expired", "failed"}[i]), Status: status, ExpiresAt: time.Now().Add(time.Hour)})
	}

	a := New(svc, Config{})
	servers := NewServers("", "", a, nil)
	private, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	public, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	servers.Serve(private, public)
	defer servers.Close()
	privateURL := "http://" + private.Addr().String()
	publicURL := "http://" + public.Addr().String()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 3 * time.Second}

	do := func(method, target, body string, headers map[string]string) (int, string, http.Header) {
		t.Helper()
		req, e := http.NewRequest(method, target, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		resp, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, e := io.ReadAll(resp.Body)
		if e != nil {
			t.Fatal(e)
		}
		return resp.StatusCode, string(b), resp.Header
	}
	assertNoSecrets := func(body string) {
		t.Helper()
		for _, secret := range []string{"SECRET_HASH", "token-for-calm", "mirage_dashboard_token="} {
			if strings.Contains(body, secret) {
				t.Fatalf("secret %q leaked in %s", secret, body)
			}
		}
	}

	// Public listener never serves management, dashboard, health, or assets.
	for _, path := range []string{"/", "/dashboard", "/dashboard/links", "/dashboard/assets/dashboard.js", "/api/v1/spaces", "/healthz"} {
		status, body, _ := do(http.MethodGet, publicURL+path, "", nil)
		if status != http.StatusNotFound {
			t.Fatalf("public %s=%d %s", path, status, body)
		}
	}
	status, _, _ := do(http.MethodGet, privateURL+"/dashboard", "", nil)
	if status != http.StatusNotFound {
		t.Fatalf("anonymous dashboard=%d", status)
	}

	status, page, _ := do(http.MethodGet, privateURL+"/dashboard", "", map[string]string{"Authorization": "Bearer token-for-calm"})
	if status != http.StatusOK || !strings.Contains(page, "Mirage dashboard") || !strings.Contains(page, `hx-get="/dashboard/spaces"`) {
		t.Fatalf("full page=%d %s", status, page)
	}
	assertNoSecrets(page)
	var csrf string
	dashboardURL, _ := url.Parse(privateURL + "/dashboard")
	for _, c := range jar.Cookies(dashboardURL) {
		if c.Name == "mirage_dashboard_csrf" {
			csrf = c.Value
		}
	}
	if csrf == "" {
		t.Fatal("CSRF cookie not issued")
	}

	status, spaces, _ := do(http.MethodGet, privateURL+"/dashboard/spaces", "", nil)
	if status != http.StatusOK || !strings.Contains(spaces, "Force delete space") {
		t.Fatalf("spaces fragment=%d %s", status, spaces)
	}
	status, links, _ := do(http.MethodGet, privateURL+"/dashboard/links", "", nil)
	if status != http.StatusOK {
		t.Fatalf("links fragment=%d %s", status, links)
	}
	for _, lifecycle := range statuses {
		if !strings.Contains(links, string(lifecycle)) {
			t.Fatalf("missing lifecycle %s", lifecycle)
		}
	}
	assertNoSecrets(links)
	status, logs, _ := do(http.MethodGet, privateURL+"/dashboard/links/active/logs", "", nil)
	if status != http.StatusOK || strings.Contains(logs, "<script>") || !strings.Contains(logs, "&lt;script&gt;") || !strings.Contains(logs, "mir_leak") {
		t.Fatalf("escaped logs=%d %s", status, logs)
	}

	// Cookie mutation requires both nonce and same-origin Origin.
	status, _, _ = do(http.MethodPost, privateURL+"/dashboard/links/active/restart", "reason=artifact", nil)
	if status != http.StatusNotFound || len(svc.restarts) != 0 {
		t.Fatal("CSRF-less mutation admitted")
	}
	mutationHeaders := map[string]string{"Origin": privateURL, "X-Mirage-CSRF": csrf, "HX-Request": "true"}
	status, reason, _ := do(http.MethodPost, privateURL+"/dashboard/links/active/restart", "", mutationHeaders)
	if status != http.StatusOK || !strings.Contains(reason, "reason is required") || len(svc.restarts) != 0 {
		t.Fatal("missing mutation reason not enforced")
	}
	status, _, _ = do(http.MethodPost, privateURL+"/dashboard/links/active/restart", "reason=operator+restart", mutationHeaders)
	if status != http.StatusOK || len(svc.restarts) != 1 || svc.restarts[0].Alias != "calm" || svc.restarts[0].Reason != "operator restart" {
		t.Fatalf("restart audit %#v", svc.restarts)
	}
	status, _, _ = do(http.MethodDelete, privateURL+"/dashboard/links/failed", "reason=operator+delete", mutationHeaders)
	if status != http.StatusOK || len(svc.deletes) != 1 || svc.deletes[0].Alias != "calm" || svc.deletes[0].Reason != "operator delete" {
		t.Fatalf("delete audit %#v", svc.deletes)
	}

	status, _, _ = do(http.MethodDelete, privateURL+"/dashboard/spaces/other", "reason=attack", mutationHeaders)
	if status != http.StatusNotFound || len(seed.deleted) != 0 {
		t.Fatalf("cross-space status=%d deletes=%#v", status, seed.deleted)
	}
	status, _, hdr := do(http.MethodDelete, privateURL+"/dashboard/spaces/calm", "reason=operator+cleanup", mutationHeaders)
	if status != http.StatusNoContent || hdr.Get("HX-Redirect") != "/" || len(seed.deleted) != 1 || seed.deleted[0].Reason != "operator cleanup" {
		t.Fatalf("own delete=%d %#v", status, seed.deleted)
	}
}
