package application

import (
	"context"
	"errors"
	"fmt"
	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
	"io"
	"sync"
	"testing"
	"time"
)

type mem struct {
	spaces     map[domain.Alias]domain.Space
	links      map[domain.LinkID]domain.Link
	tombstones map[string]struct{}
	mu         sync.Mutex
}

func newMem() *mem {
	return &mem{spaces: map[domain.Alias]domain.Space{}, links: map[domain.LinkID]domain.Link{}, tombstones: map[string]struct{}{}}
}
func (m *mem) CreateSpace(_ context.Context, x domain.Space) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.spaces[x.Alias]; ok {
		return domain.NewConflict("dup")
	}
	m.spaces[x.Alias] = x
	return nil
}
func (m *mem) FindSpace(_ context.Context, id domain.SpaceID) (domain.Space, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.spaces {
		if x.ID == id {
			return x, nil
		}
	}
	return domain.Space{}, domain.NewNotFound("space")
}
func (m *mem) FindSpaceByAlias(_ context.Context, a domain.Alias) (domain.Space, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	x, ok := m.spaces[a]
	if !ok {
		return x, domain.NewNotFound("space")
	}
	return x, nil
}
func (m *mem) ListSpaces(context.Context) ([]domain.Space, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := []domain.Space{}
	for _, x := range m.spaces {
		r = append(r, x)
	}
	return r, nil
}
func (m *mem) ActiveSpaces(_ context.Context, n time.Time) ([]domain.Space, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := []domain.Space{}
	for _, x := range m.spaces {
		if !x.Expired(n) {
			r = append(r, x)
		}
	}
	return r, nil
}
func (m *mem) ExpiredSpaces(_ context.Context, n time.Time) ([]domain.Space, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := []domain.Space{}
	for _, x := range m.spaces {
		if x.Expired(n) {
			r = append(r, x)
		}
	}
	return r, nil
}
func (m *mem) DeleteSpace(_ context.Context, id domain.SpaceID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for a, x := range m.spaces {
		if x.ID == id {
			delete(m.spaces, a)
		}
	}
	return nil
}
func (m *mem) CreateLink(_ context.Context, x domain.Link) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range m.links {
		if v.SpaceID == x.SpaceID && v.Name == x.Name {
			return domain.NewConflict("dup")
		}
	}
	m.links[x.ID] = x
	return nil
}
func (m *mem) FindLink(_ context.Context, s domain.SpaceID, n domain.LinkName) (domain.Link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.links {
		if x.SpaceID == s && x.Name == n {
			return x, nil
		}
	}
	return domain.Link{}, domain.NewNotFound("link")
}
func (m *mem) LinkDeleted(_ context.Context, sid domain.SpaceID, n domain.LinkName) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.tombstones[string(sid)+"/"+string(n)]
	return ok, nil
}
func (m *mem) ListLinks(_ context.Context, s domain.SpaceID) ([]domain.Link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := []domain.Link{}
	for _, x := range m.links {
		if x.SpaceID == s {
			r = append(r, x)
		}
	}
	return r, nil
}
func (m *mem) SaveLink(_ context.Context, x domain.Link) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.links[x.ID]; !ok {
		return domain.NewNotFound("link")
	}
	m.links[x.ID] = x
	return nil
}
func (m *mem) ExpiredLinks(_ context.Context, n time.Time) ([]domain.Link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := []domain.Link{}
	for _, x := range m.links {
		if x.Expired(n) {
			r = append(r, x)
		}
	}
	return r, nil
}
func (m *mem) ReconciliationLinks(_ context.Context, now time.Time) ([]domain.Link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := []domain.Link{}
	for _, x := range m.links {
		if !x.Expired(now) && !x.Status.Terminal() {
			r = append(r, x)
		}
	}
	return r, nil
}
func (m *mem) DeleteLink(_ context.Context, id domain.LinkID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.links[id]; !ok {
		return domain.NewNotFound("link")
	}
	x := m.links[id]
	m.tombstones[string(x.SpaceID)+"/"+string(x.Name)] = struct{}{}
	delete(m.links, id)
	return nil
}

type fake struct {
	mem    *mem
	now    time.Time
	events []string
	fail   string
	next   int
	audit  []ports.AuditEvent
}

func (f *fake) Now() time.Time                                 { return f.now }
func (f *fake) NewSpaceID() domain.SpaceID                     { return "s" }
func (f *fake) NewLinkID() domain.LinkID                       { f.next++; return domain.LinkID(fmt.Sprintf("l%d", f.next)) }
func (f *fake) NewAlias() (domain.Alias, error)                { return "calm-fox", nil }
func (f *fake) Generate() (domain.Token, error)                { return domain.Token("token"), nil }
func (f *fake) Hash(t domain.Token) domain.TokenHash           { return t.Hash() }
func (f *fake) Verify(h domain.TokenHash, t domain.Token) bool { return h.Verify(t) }
func (f *fake) Allocate(context.Context) (ports.Port, error) {
	f.events = append(f.events, "reserve")
	if f.fail == "port" {
		return ports.Port{}, errors.New("x")
	}
	return ports.Port{Number: 3456, Address: "127.0.0.1"}, nil
}
func (f *fake) Release(context.Context, ports.Port) error {
	f.events = append(f.events, "release")
	if f.fail == "release" {
		return errors.New("release")
	}
	return nil
}
func (f *fake) Start(context.Context, ports.StartRequest) (ports.ProcessIdentity, error) {
	f.events = append(f.events, "start")
	if f.fail == "start" {
		return ports.ProcessIdentity{}, errors.New("x")
	}
	return ports.ProcessIdentity{Value: "p"}, nil
}
func (f *fake) Stop(context.Context, ports.ProcessIdentity, time.Duration) error {
	f.events = append(f.events, "stop")
	if f.fail == "stop" {
		return errors.New("stop")
	}
	return nil
}
func (f *fake) Alive(context.Context, ports.ProcessIdentity) (bool, error) { return true, nil }
func (f *fake) CheckUntil(_ context.Context, _ domain.HealthCheck, grace time.Duration) error {
	f.events = append(f.events, "health")
	if f.fail == "health" {
		return errors.New("x")
	}
	return nil
}
func (f *fake) Add(context.Context, ports.Route) error {
	f.events = append(f.events, "add")
	if f.fail == "add" {
		return errors.New("x")
	}
	return nil
}
func (f *fake) Remove(context.Context, domain.LinkID) error {
	f.events = append(f.events, "remove")
	if f.fail == "remove" {
		return errors.New("remove")
	}
	return nil
}
func (f *fake) List(context.Context) ([]ports.Route, error)    { return nil, nil }
func (f *fake) Reconcile(context.Context, []ports.Route) error { return nil }
func (f *fake) Tail(context.Context, domain.LinkID, int) ([]ports.LogEntry, error) {
	return []ports.LogEntry{{Text: "redacted"}}, nil
}
func (f *fake) Follow(context.Context, domain.LinkID) (io.ReadCloser, error) { return nil, nil }
func (f *fake) Record(_ context.Context, e ports.AuditEvent) error {
	f.audit = append(f.audit, e)
	return nil
}

// scheduler deliberately executes only when test asks; this keeps backoff deterministic.
type sched struct {
	at time.Time
	fn func(context.Context)
}

func (s *sched) Schedule(_ context.Context, a time.Time, f func(context.Context)) (ports.CancelFunc, error) {
	s.at = a
	s.fn = f
	return func() {}, nil
}
func setup(t *testing.T) (*Service, *fake, domain.Token) {
	t.Helper()
	m := newMem()
	f := &fake{mem: m, now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := &Service{Repo: m, Clock: f, IDs: f, Aliases: f, Tokens: f, Hashes: f, Ports: f, Processes: f, Health: f, Proxy: f, Logs: f, Audit: f, BaseHost: "mirage.example.com", PublicPort: 9955}
	r, e := s.CreateSpace(context.Background(), CreateSpaceInput{})
	if e != nil {
		t.Fatal(e)
	}
	return s, f, r.Token
}
func input(tok domain.Token) CreateLinkInput {
	return CreateLinkInput{Restarts: true, Alias: "calm-fox", Token: tok, Name: "api", Command: "x {port}", Folder: ".", HealthCheck: domain.HealthCheck{Method: domain.HealthGET, URL: "http://127.0.0.1:{port}/"}}
}
func TestLifecycleOrderingAndCompensation(t *testing.T) {
	for _, fail := range []string{"", "port", "start", "health", "add"} {
		t.Run(fail, func(t *testing.T) {
			s, f, tok := setup(t)
			f.fail = fail
			r, e := s.CreateLink(context.Background(), input(tok))
			if fail == "" {
				if e != nil || r.Link.Status != domain.StatusActive {
					t.Fatalf("%v %#v", e, r)
				}
				if fmt.Sprint(f.events) != fmt.Sprint([]string{"reserve", "start", "health", "add"}) {
					t.Fatal(f.events)
				}
				if e = s.DeleteLink(context.Background(), LinkMutationInput{Alias: "calm-fox", Token: tok, Name: "api"}); e != nil {
					t.Fatal(e)
				}
				if fmt.Sprint(f.events[len(f.events)-3:]) != fmt.Sprint([]string{"remove", "stop", "release"}) {
					t.Fatal(f.events)
				}
			} else {
				if e == nil {
					t.Fatal("expected")
				}
				if fail == "health" || fail == "add" {
					if fmt.Sprint(f.events[len(f.events)-3:]) != fmt.Sprint([]string{"remove", "stop", "release"}) {
						t.Fatal(f.events)
					}
				}
			}
		})
	}
}
func TestAuthForceTTLRestart(t *testing.T) {
	s, f, tok := setup(t)
	in := input(tok)
	if _, e := s.CreateLink(context.Background(), input("wrong")); !domain.IsKind(e, domain.Unauthorized) {
		t.Fatal(e)
	}
	r, e := s.CreateLink(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	old := r.Link.ExpiresAt
	if _, e = s.RestartLink(context.Background(), LinkMutationInput{Alias: "calm-fox", Token: tok, Name: "api"}); e != nil {
		t.Fatal(e)
	}
	l, _ := s.Repo.FindLink(context.Background(), "s", "api")
	if !l.ExpiresAt.Equal(old) {
		t.Fatal("ttl extended")
	}
	if e = s.DeleteSpace(context.Background(), DeleteSpaceInput{Alias: "calm-fox", Force: true}); !domain.IsKind(e, domain.Validation) {
		t.Fatal(e)
	}
	if e = s.DeleteSpace(context.Background(), DeleteSpaceInput{Alias: "calm-fox", Force: true, Reason: "ops"}); e != nil || len(f.audit) != 1 {
		t.Fatal(e)
	}
}
func TestBackoffAndExpiry(t *testing.T) {
	s, f, tok := setup(t)
	sc := &sched{}
	s.Scheduler = sc
	if _, e := s.CreateLink(context.Background(), input(tok)); e != nil {
		t.Fatal(e)
	}
	at, e := s.ScheduleAutomaticRestart(context.Background(), "calm-fox", tok, "api")
	if e != nil || !at.Equal(f.now.Add(time.Second)) {
		t.Fatal(e, at)
	}
	f.now = at
	sc.fn(context.Background())
	l, e := s.Repo.FindLink(context.Background(), "s", "api")
	if e != nil || l.Status != domain.StatusActive {
		t.Fatal(e, l)
	}
	f.now = l.ExpiresAt
	if e = s.Cleanup(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Repo.FindLink(context.Background(), "s", "api"); !domain.IsKind(e, domain.NotFound) {
		t.Fatal(e)
	}
}
