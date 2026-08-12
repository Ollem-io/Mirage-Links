package application

import (
	"context"
	"errors"
	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
	"testing"
	"time"
)

type reconcileProxy struct {
	routes  []ports.Route
	removed []domain.LinkID
	err     error
}

func (p *reconcileProxy) Add(context.Context, ports.Route) error { return nil }
func (p *reconcileProxy) Remove(_ context.Context, id domain.LinkID) error {
	p.removed = append(p.removed, id)
	return nil
}
func (p *reconcileProxy) List(context.Context) ([]ports.Route, error) { return p.routes, nil }
func (p *reconcileProxy) Reconcile(_ context.Context, r []ports.Route) error {
	p.routes = append([]ports.Route(nil), r...)
	return p.err
}

type reconcileProc struct {
	alive    bool
	stopped  []string
	aliveErr error
}

func (p *reconcileProc) Start(context.Context, ports.StartRequest) (ports.ProcessIdentity, error) {
	return ports.ProcessIdentity{}, errors.New("not used")
}
func (p *reconcileProc) Stop(_ context.Context, id ports.ProcessIdentity, _ time.Duration) error {
	p.stopped = append(p.stopped, id.Value)
	return nil
}
func (p *reconcileProc) Alive(context.Context, ports.ProcessIdentity) (bool, error) {
	return p.alive, p.aliveErr
}

type reconcilePort struct{}

func (reconcilePort) Allocate(context.Context) (ports.Port, error) {
	return ports.Port{}, errors.New("not used")
}
func (reconcilePort) Release(context.Context, ports.Port) error { return nil }

type reconcileHealth struct {
	err   error
	calls int
}

func (h *reconcileHealth) CheckUntil(context.Context, domain.HealthCheck, time.Duration) error {
	h.calls++
	return h.err
}

func reconcileService(t *testing.T, status domain.LinkStatus, alive bool, healthErr error) (*Service, *mem, *reconcileProxy, *reconcileProc) {
	t.Helper()
	m := newMem()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sp := domain.Space{ID: "s", Alias: "calm-fox", ExpiresAt: now.Add(time.Hour)}
	if err := m.CreateSpace(context.Background(), sp); err != nil {
		t.Fatal(err)
	}
	l := domain.Link{ID: "l", SpaceID: sp.ID, Name: "api", Status: status, ExpiresAt: now.Add(time.Hour), AllocatedPort: 3456, ProcessIdentity: "123:1", HealthCheck: domain.HealthCheck{Method: domain.HealthGET, URL: "http://127.0.0.1:{port}/"}}
	if err := m.CreateLink(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	p := &reconcileProxy{routes: []ports.Route{{LinkID: "orphan", Hostname: "old", Upstream: "127.0.0.1:1"}}}
	proc := &reconcileProc{alive: alive}
	h := &reconcileHealth{err: healthErr}
	return &Service{Repo: m, Clock: fixedClock{now}, Ports: reconcilePort{}, Processes: proc, Health: h, Proxy: p, BaseHost: "mirage.example.com"}, m, p, proc
}

type fixedClock struct{ n time.Time }

func (c fixedClock) Now() time.Time { return c.n }
func TestReconcileRestoresOnlyLiveHealthyActiveRoute(t *testing.T) {
	s, _, p, _ := reconcileService(t, domain.StatusActive, true, nil)
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(p.routes) != 1 || p.routes[0].Hostname != "api-calm-fox.mirage.example.com" {
		t.Fatalf("routes %#v", p.routes)
	}
}
func TestReconcileCleansDeadUnhealthyAndPartialRecords(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status domain.LinkStatus
		alive  bool
		health error
	}{{"dead", domain.StatusActive, false, nil}, {"unhealthy", domain.StatusActive, true, errors.New("down")}, {"partial", domain.StatusStarting, true, nil}} {
		t.Run(tc.name, func(t *testing.T) {
			s, m, p, proc := reconcileService(t, tc.status, tc.alive, tc.health)
			if err := s.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(p.routes) != 0 || len(proc.stopped) != 1 {
				t.Fatalf("routes=%v stopped=%v", p.routes, proc.stopped)
			}
			if len(m.links) != 0 {
				t.Fatal("stale link remains")
			}
		})
	}
}
func TestShutdownRemovesRoutesAndStopsRecordedProcesses(t *testing.T) {
	s, m, p, proc := reconcileService(t, domain.StatusActive, true, nil)
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(p.routes) != 0 || len(proc.stopped) != 1 || len(m.links) != 0 {
		t.Fatalf("routes=%v stops=%v links=%v", p.routes, proc.stopped, m.links)
	}
}
