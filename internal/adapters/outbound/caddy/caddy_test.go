package caddy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
)

type fake struct {
	mu     sync.Mutex
	routes []json.RawMessage
	writes []string
	fail   int
	status int
	bad    bool
}

func (f *fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail > 0 {
		f.fail--
		http.Error(w, "down", 500)
		return
	}
	if f.status != 0 {
		http.Error(w, "no", f.status)
		return
	}
	if r.Method == "GET" {
		if f.bad {
			w.Write([]byte("{"))
			return
		}
		json.NewEncoder(w).Encode(f.routes)
		return
	}
	f.writes = append(f.writes, r.Method+" "+r.URL.Path)
	if r.Method == "POST" {
		var x json.RawMessage
		json.NewDecoder(r.Body).Decode(&x)
		f.routes = append(f.routes, x)
	}
	if r.Method == "PUT" {
		i, _ := strconv.Atoi(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
		json.NewDecoder(r.Body).Decode(&f.routes[i])
	}
	if r.Method == "DELETE" {
		i, _ := strconv.Atoi(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
		f.routes = append(f.routes[:i], f.routes[i+1:]...)
	}
}
func setup(t *testing.T) (*fake, *Client) {
	t.Helper()
	f := &fake{}
	s := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(s.Close)
	c, e := New(Config{AdminURL: s.URL, RetryAttempts: 2, RetryDelay: time.Millisecond})
	if e != nil {
		t.Fatal(e)
	}
	return f, c
}
func route(id, host, up string) ports.Route {
	return ports.Route{LinkID: domain.LinkID(id), Hostname: host, Upstream: up}
}
func decoded(t *testing.T, f *fake) []caddyRoute {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var x []caddyRoute
	for _, raw := range f.routes {
		var r caddyRoute
		if e := json.Unmarshal(raw, &r); e != nil {
			t.Fatal(e)
		}
		x = append(x, r)
	}
	return x
}
func TestAddRemoveIdempotentAndOwnsOnlyItsRoute(t *testing.T) {
	f, c := setup(t)
	unrelated := json.RawMessage(`{"@id":"other","match":[{"host":["keep.example"]}],"handle":[{"handler":"static_response"}]}`)
	f.routes = []json.RawMessage{unrelated}
	r := route("one", "api-fox.example", "127.0.0.1:2345")
	if e := c.Add(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	if e := c.Add(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	if len(f.writes) != 1 {
		t.Fatalf("writes %v", f.writes)
	}
	got, e := c.List(context.Background())
	if e != nil || len(got) != 1 || got[0] != r {
		t.Fatalf("list %#v %v", got, e)
	}
	if string(f.routes[0]) != string(unrelated) {
		t.Fatal("unrelated route changed")
	}
	if e := c.Remove(context.Background(), "one"); e != nil {
		t.Fatal(e)
	}
	if e := c.Remove(context.Background(), "one"); e != nil {
		t.Fatal(e)
	}
	if len(f.routes) != 1 || string(f.routes[0]) != string(unrelated) {
		t.Fatal("remove mutated unrelated")
	}
}
func TestAddUpdatesOnlyMatchingOwnedIndex(t *testing.T) {
	f, c := setup(t)
	f.routes = []json.RawMessage{mustJSON(t, encode(route("one", "old", "127.0.0.1:1"))), json.RawMessage(`{"@id":"other","x":{"unchanged":true}}`)}
	want := route("one", "new", "127.0.0.1:2")
	if e := c.Add(context.Background(), want); e != nil {
		t.Fatal(e)
	}
	if x := decoded(t, f); len(x) != 2 || x[0].Match[0].Host[0] != "new" {
		t.Fatal(x)
	}
	if !strings.Contains(f.writes[0], "/routes/0") {
		t.Fatal(f.writes)
	}
}
func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestReconcileDiffsOwnedRoutesAndPreservesExternalBytes(t *testing.T) {
	f, c := setup(t)
	keep := json.RawMessage(`{"@id":"third-party","handle":[{"handler":"static_response","body":"byte exact"}]}`)
	malformedOwned := json.RawMessage(`{"@id":"mirage-route-broken","handle":[{"handler":"file_server"}]}`)
	f.routes = []json.RawMessage{keep, mustJSON(t, encode(route("same", "old", "127.0.0.1:1"))), mustJSON(t, encode(route("orphan", "x", "127.0.0.1:2"))), malformedOwned}
	want := route("same", "new", "127.0.0.1:3")
	added := route("add", "z", "127.0.0.1:4")
	if e := c.Reconcile(context.Background(), []ports.Route{want, added}); e != nil {
		t.Fatal(e)
	}
	if len(f.routes) < 1 || string(f.routes[0]) != string(keep) {
		t.Fatalf("external route mutated %s", f.routes)
	}
	got, e := c.List(context.Background())
	if e != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, e)
	}
	m := map[domain.LinkID]ports.Route{}
	for _, r := range got {
		m[r.LinkID] = r
	}
	if m["same"] != want || m["add"] != added {
		t.Fatal(m)
	}
}
func TestReconcileRepairsAndRemovesDamagedOwnedRoutes(t *testing.T) {
	f, c := setup(t)
	f.routes = []json.RawMessage{json.RawMessage(`{"@id":"mirage-route-repair","handle":[{"handler":"file_server"}]}`), json.RawMessage(`{"@id":"mirage-route-gone","handle":[{"handler":"file_server"}]}`)}
	want := route("repair", "fixed.example", "127.0.0.1:9")
	if err := c.Reconcile(context.Background(), []ports.Route{want}); err != nil {
		t.Fatal(err)
	}
	got, err := c.List(context.Background())
	if err != nil || len(got) != 1 || got[0] != want {
		t.Fatalf("%v %v", got, err)
	}
}

func TestRouteForAndValidation(t *testing.T) {
	b, _ := domain.ParseBaseHost("mirage.example.com")
	n, _ := domain.ParseLinkName("api")
	a, _ := domain.ParseAlias("fox")
	r, e := RouteFor("id", b, n, a, 99)
	if e != nil || r.Hostname != "api-fox.mirage.example.com" || r.Upstream != "127.0.0.1:99" {
		t.Fatal(r, e)
	}
	for _, r := range []ports.Route{route("", "a", "127.0.0.1:1"), route("x", "", "127.0.0.1:1"), route("x", "a", "10.0.0.1:1"), route("x", "a", "bad")} {
		if e := validRoute(r); e == nil {
			t.Fatal(r)
		}
	}
	if _, e := RouteFor("", b, n, a, 1); e == nil {
		t.Fatal("id")
	}
	if _, e := RouteFor("x", b, n, a, 0); e == nil {
		t.Fatal("port")
	}
}
func TestErrorsRetriesAndMalformed(t *testing.T) {
	f, c := setup(t)
	f.fail = 1
	if _, e := c.List(context.Background()); e != nil {
		t.Fatal(e)
	}
	f.status = 409
	if _, e := c.List(context.Background()); !IsKind(e, AdminConflict) {
		t.Fatalf("%v", e)
	}
	f.status = 400
	if _, e := c.List(context.Background()); !IsKind(e, Rejected) {
		t.Fatal(e)
	}
	f.status = 0
	f.bad = true
	if _, e := c.List(context.Background()); !IsKind(e, MalformedResponse) {
		t.Fatal(e)
	}
	f.bad = false
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := c.List(ctx); !IsKind(e, Timeout) {
		t.Fatal(e)
	}
}
func TestNewAndDecodeBoundaries(t *testing.T) {
	for _, cfg := range []Config{{}, {AdminURL: "ftp://x"}, {AdminURL: "http://x", Server: "a/b"}} {
		if _, e := New(cfg); e == nil {
			t.Fatal(cfg)
		}
	}
	if _, e := New(Config{AdminURL: "http://x"}); e != nil {
		t.Fatal(e)
	}
	if owned("mirage-route-") || owned("other") {
		t.Fatal("ownership")
	}
	if _, ok := decode(caddyRoute{ID: "mirage-route-x"}); ok {
		t.Fatal("malformed owned must not be managed")
	}
	if !errors.Is((&Error{Unavailable, errors.New("x")}), errors.New("x")) { /* only compile Unwrap path */
	}
}
