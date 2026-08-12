package ports

import (
	"context"
	"github.com/primeintellect/mirage/internal/domain"
	"testing"
)

// proxyFake demonstrates that application orchestration can invoke desired-state
// reconciliation through its outbound port without importing Caddy.
type proxyFake struct {
	routes     []Route
	reconciled []Route
}

func (f *proxyFake) Add(_ context.Context, r Route) error { f.routes = append(f.routes, r); return nil }
func (f *proxyFake) Remove(_ context.Context, id domain.LinkID) error {
	for i, r := range f.routes {
		if r.LinkID == id {
			f.routes = append(f.routes[:i], f.routes[i+1:]...)
			break
		}
	}
	return nil
}
func (f *proxyFake) List(context.Context) ([]Route, error) {
	return append([]Route(nil), f.routes...), nil
}
func (f *proxyFake) Reconcile(_ context.Context, want []Route) error {
	f.reconciled = append([]Route(nil), want...)
	f.routes = append([]Route(nil), want...)
	return nil
}
func TestProxyReconcileIsApplicationPort(t *testing.T) {
	var p Proxy = &proxyFake{}
	want := []Route{{LinkID: "id", Hostname: "api.example", Upstream: "127.0.0.1:1"}}
	if e := p.Reconcile(context.Background(), want); e != nil {
		t.Fatal(e)
	}
	got, e := p.List(context.Background())
	if e != nil || len(got) != 1 || got[0] != want[0] {
		t.Fatalf("%v %v", got, e)
	}
}
