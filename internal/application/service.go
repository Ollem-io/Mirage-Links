// Package application implements Mirage use cases exclusively against ports.
package application

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/primeintellect/mirage/internal/application/compensation"
	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
)

// Service is the lifecycle coordinator.  Its mutex intentionally serializes
// mutations: a link has external side effects (a process and a proxy route),
// which cannot be made part of a database transaction.  Repository uniqueness
// remains the cross-instance authority.
type Service struct {
	Repo       ports.Repository
	Clock      ports.Clock
	IDs        ports.IDGenerator
	Aliases    ports.AliasGenerator
	Tokens     ports.TokenGenerator
	Hashes     ports.TokenHasher
	Ports      ports.PortAllocator
	Processes  ports.ProcessSupervisor
	Health     ports.HealthChecker
	Proxy      ports.Proxy
	Logs       ports.LogStream
	Audit      ports.Audit
	Scheduler  ports.Scheduler
	BaseHost   domain.BaseHost
	PublicPort int
	StopGrace  time.Duration
	mu         sync.Mutex
	scheduled  map[domain.LinkID]ports.CancelFunc
}

func (s *Service) now() time.Time {
	if s.Clock != nil {
		return s.Clock.Now().UTC()
	}
	return time.Now().UTC()
}
func (s *Service) stopGrace() time.Duration {
	if s.StopGrace > 0 {
		return s.StopGrace
	}
	return 5 * time.Second
}
func (s *Service) lock() {
	s.mu.Lock()
	if s.scheduled == nil {
		s.scheduled = map[domain.LinkID]ports.CancelFunc{}
	}
}
func (s *Service) unlock() { s.mu.Unlock() }

type CreateSpaceInput struct {
	TTL   time.Duration
	Alias string
}
type CreateSpaceResult struct {
	Space domain.Space
	Token domain.Token
}

func (s *Service) CreateSpace(ctx context.Context, in CreateSpaceInput) (CreateSpaceResult, error) {
	s.lock()
	defer s.unlock()
	ttl := in.TTL
	if ttl == 0 {
		ttl = domain.DefaultTTL
	}
	if ttl < domain.MinTTL || ttl > domain.MaxTTL {
		return CreateSpaceResult{}, domain.NewValidation("ttl", "out of allowed range")
	}
	var alias domain.Alias
	var err error
	if in.Alias != "" {
		alias, err = domain.ParseAlias(in.Alias)
	} else {
		if s.Aliases == nil {
			return CreateSpaceResult{}, fmt.Errorf("application: alias generator unavailable")
		}
		alias, err = s.Aliases.NewAlias()
	}
	if err != nil {
		return CreateSpaceResult{}, err
	}
	if s.IDs == nil || s.Tokens == nil || s.Hashes == nil {
		return CreateSpaceResult{}, fmt.Errorf("application: identity ports unavailable")
	}
	token, err := s.Tokens.Generate()
	if err != nil {
		return CreateSpaceResult{}, err
	}
	sp := domain.Space{ID: s.IDs.NewSpaceID(), Alias: alias, ExpiresAt: s.now().Add(ttl), TokenHash: s.Hashes.Hash(token)}
	if err = s.Repo.CreateSpace(ctx, sp); err != nil {
		return CreateSpaceResult{}, err
	}
	// Token is deliberately returned only here, never stored in Service.
	return CreateSpaceResult{Space: sp, Token: token}, nil
}
func (s *Service) ListSpaces(ctx context.Context) ([]domain.Space, error) {
	return s.Repo.ListSpaces(ctx)
}
func (s *Service) authenticate(ctx context.Context, alias domain.Alias, token domain.Token) (domain.Space, error) {
	sp, err := s.Repo.FindSpaceByAlias(ctx, alias)
	if err != nil {
		return domain.Space{}, err
	}
	if sp.Expired(s.now()) {
		return domain.Space{}, domain.NewNotFound("space not found")
	}
	if s.Hashes == nil || !s.Hashes.Verify(sp.TokenHash, token) {
		return domain.Space{}, domain.NewUnauthorized("invalid bearer token")
	}
	return sp, nil
}
func (s *Service) Authorize(ctx context.Context, alias string, token domain.Token) (domain.Space, error) {
	a, e := domain.ParseAlias(alias)
	if e != nil {
		return domain.Space{}, e
	}
	return s.authenticate(ctx, a, token)
}

type DeleteSpaceInput struct {
	Alias  string
	Token  domain.Token
	Force  bool
	Reason string
}

func (s *Service) DeleteSpace(ctx context.Context, in DeleteSpaceInput) error {
	s.lock()
	defer s.unlock()
	a, e := domain.ParseAlias(in.Alias)
	if e != nil {
		return e
	}
	sp, e := s.Repo.FindSpaceByAlias(ctx, a)
	if e != nil {
		return e
	}
	if in.Force {
		if strings.TrimSpace(in.Reason) == "" {
			return domain.NewValidation("reason", "must not be empty")
		}
		if s.Audit == nil {
			return fmt.Errorf("application: audit port unavailable")
		}
		if e = s.Audit.Record(ctx, ports.AuditEvent{At: s.now(), SpaceID: sp.ID, Action: "force_delete_space", Reason: in.Reason}); e != nil {
			return e
		}
	} else if s.Hashes == nil || !s.Hashes.Verify(sp.TokenHash, in.Token) {
		return domain.NewUnauthorized("invalid bearer token")
	}
	links, e := s.Repo.ListLinks(ctx, sp.ID)
	if e != nil {
		return e
	}
	for _, l := range links {
		if e = s.destroy(ctx, l, domain.StatusDeleted); e != nil {
			return e
		}
	}
	return s.Repo.DeleteSpace(ctx, sp.ID)
}

type CreateLinkInput struct {
	Alias       string
	Token       domain.Token
	Name        string
	Command     string
	Folder      string
	HealthCheck domain.HealthCheck
	Grace       time.Duration
	TTL         time.Duration
	Restarts    bool
}
type CreateLinkResult struct {
	Link       domain.Link
	URL        string
	RecentLogs []ports.LogEntry
}

func (s *Service) CreateLink(ctx context.Context, in CreateLinkInput) (CreateLinkResult, error) {
	s.lock()
	defer s.unlock()
	a, e := domain.ParseAlias(in.Alias)
	if e != nil {
		return CreateLinkResult{}, e
	}
	sp, e := s.authenticate(ctx, a, in.Token)
	if e != nil {
		return CreateLinkResult{}, e
	}
	if strings.TrimSpace(in.Command) == "" {
		return CreateLinkResult{}, domain.NewValidation("command", "must not be empty")
	}
	if strings.TrimSpace(in.Folder) == "" {
		return CreateLinkResult{}, domain.NewValidation("execution_folder", "must not be empty")
	}
	n, e := domain.ParseLinkName(in.Name)
	if e != nil {
		return CreateLinkResult{}, e
	}
	// Reparse at this boundary: application callers are not necessarily HTTP/CLI
	// adapters, so an invalid/non-loopback health endpoint cannot bypass domain validation.
	if in.HealthCheck.Method == "" || in.HealthCheck.URL == "" {
		return CreateLinkResult{}, domain.NewValidation("health_check", "must be METHOD URL")
	}
	if _, e = domain.ParseHealthCheck(string(in.HealthCheck.Method) + " " + strings.ReplaceAll(in.HealthCheck.URL, "{port}", "1")); e != nil {
		return CreateLinkResult{}, e
	}
	ttl := in.TTL
	if ttl == 0 {
		ttl = domain.DefaultTTL
	}
	if ttl < domain.MinTTL || ttl > domain.MaxTTL {
		return CreateLinkResult{}, domain.NewValidation("ttl", "out of allowed range")
	}
	grace := in.Grace
	if grace == 0 {
		grace = domain.DefaultGrace
	}
	if grace < domain.MinGrace || grace > domain.MaxGrace {
		return CreateLinkResult{}, domain.NewValidation("grace", "out of allowed range")
	}
	if s.IDs == nil || s.Ports == nil || s.Processes == nil || s.Health == nil || s.Proxy == nil {
		return CreateLinkResult{}, fmt.Errorf("application: lifecycle ports unavailable")
	}
	expiry := s.now().Add(ttl)
	if sp.ExpiresAt.Before(expiry) {
		expiry = sp.ExpiresAt
	}
	link := domain.Link{ID: s.IDs.NewLinkID(), SpaceID: sp.ID, Name: n, Status: domain.StatusCreating, Command: in.Command, Folder: in.Folder, HealthCheck: in.HealthCheck, Grace: grace, ExpiresAt: expiry, AutoRestart: in.Restarts}
	if e = s.Repo.CreateLink(ctx, link); e != nil {
		return CreateLinkResult{}, e
	}
	result, e := s.start(ctx, sp, link)
	if e != nil {
		logs := s.recent(ctx, link.ID)
		return CreateLinkResult{Link: link, RecentLogs: logs}, e
	}
	return result, nil
}
func (s *Service) recent(ctx context.Context, id domain.LinkID) []ports.LogEntry {
	if s.Logs == nil {
		return nil
	}
	x, _ := s.Logs.Tail(ctx, id, 100)
	return x
}
func replacePort(h domain.HealthCheck, p int) domain.HealthCheck {
	h.URL = strings.ReplaceAll(h.URL, "{port}", fmt.Sprint(p))
	return h
}
func (s *Service) start(ctx context.Context, sp domain.Space, l domain.Link) (CreateLinkResult, error) {
	// Exact expiry boundary is terminal, including an in-flight creation.
	if l.Expired(s.now()) {
		_ = s.Repo.DeleteLink(ctx, l.ID)
		return CreateLinkResult{Link: l}, domain.NewNotFound("link expired")
	}
	p, e := s.Ports.Allocate(ctx)
	if e != nil {
		return s.failStart(ctx, l, ports.Port{}, e)
	}
	l.AllocatedPort = p.Number
	l.Status = domain.StatusStarting
	if e = s.Repo.SaveLink(ctx, l); e != nil {
		return s.failStart(ctx, l, p, e)
	}
	id, e := s.Processes.Start(ctx, ports.StartRequest{LinkID: l.ID, Command: l.Command, Folder: l.Folder, Port: p, Environment: map[string]string{"PORT": fmt.Sprint(p.Number)}})
	if e != nil {
		return s.failStart(ctx, l, p, e)
	}
	l.ProcessIdentity = id.Value
	if e = s.Repo.SaveLink(ctx, l); e != nil {
		return s.failStart(ctx, l, p, e)
	}
	// A checker is responsible for probing up to its configured grace deadline;
	// the application never publishes before its success.
	l.HealthCheck = replacePort(l.HealthCheck, p.Number)
	if e = s.Health.CheckUntil(ctx, l.HealthCheck, minDuration(l.Grace, l.ExpiresAt.Sub(s.now()))); e != nil {
		return s.failStart(ctx, l, p, e)
	}
	if l.Expired(s.now()) {
		return s.failStart(ctx, l, p, domain.NewNotFound("link expired"))
	}
	l.Status = domain.StatusHealthy
	if e = s.Repo.SaveLink(ctx, l); e != nil {
		return s.failStart(ctx, l, p, e)
	}
	route := s.route(sp, l)
	if e = s.Proxy.Add(ctx, route); e != nil {
		return s.failStart(ctx, l, p, e)
	}
	l.Status = domain.StatusActive
	if e = s.Repo.SaveLink(ctx, l); e != nil {
		_ = s.Proxy.Remove(ctx, l.ID)
		return s.failStart(ctx, l, p, e)
	}
	publicPort := s.PublicPort
	if publicPort == 0 {
		publicPort = 9955
	}
	url, urlErr := domain.PublicURL(s.BaseHost, l.Name, sp.Alias, publicPort)
	if urlErr != nil {
		_ = s.Proxy.Remove(ctx, l.ID)
		return s.failStart(ctx, l, p, urlErr)
	}
	return CreateLinkResult{Link: l, URL: url}, nil
}
func (s *Service) route(sp domain.Space, l domain.Link) ports.Route {
	return ports.Route{LinkID: l.ID, Hostname: s.BaseHost.Host(l.Name, sp.Alias), Upstream: fmt.Sprintf("127.0.0.1:%d", l.AllocatedPort)}
}
func (s *Service) failStart(ctx context.Context, l domain.Link, p ports.Port, cause error) (CreateLinkResult, error) {
	compErr := compensation.Run(ctx, compensation.Steps{
		RemoveRoute: func(c context.Context) error { return s.Proxy.Remove(c, l.ID) },
		StopProcess: func(c context.Context) error {
			if l.ProcessIdentity == "" {
				return nil
			}
			return s.Processes.Stop(c, ports.ProcessIdentity{Value: l.ProcessIdentity}, s.stopGrace())
		},
		ReleasePort: func(c context.Context) error {
			if p.Number == 0 {
				return nil
			}
			return s.Ports.Release(c, p)
		},
	})
	l.Status = domain.StatusFailed
	if compErr == nil {
		l.ProcessIdentity = ""
		l.AllocatedPort = 0
	}
	saveErr := s.Repo.SaveLink(ctx, l)
	logs := s.recent(ctx, l.ID)
	if compErr != nil || saveErr != nil {
		return CreateLinkResult{Link: l, RecentLogs: logs}, domain.NewInternal(fmt.Sprintf("startup compensation failed (cause=%v, compensation=%v, persistence=%v)", cause, compErr, saveErr))
	}
	return CreateLinkResult{Link: l, RecentLogs: logs}, fmt.Errorf("link startup failed: %w", cause)
}
func (s *Service) ListLinks(ctx context.Context, alias string, token domain.Token) ([]domain.Link, error) {
	a, e := domain.ParseAlias(alias)
	if e != nil {
		return nil, e
	}
	sp, e := s.authenticate(ctx, a, token)
	if e != nil {
		return nil, e
	}
	return s.Repo.ListLinks(ctx, sp.ID)
}
func (s *Service) LogsFor(ctx context.Context, alias string, token domain.Token, name string, tail int) ([]ports.LogEntry, error) {
	a, e := domain.ParseAlias(alias)
	if e != nil {
		return nil, e
	}
	sp, e := s.authenticate(ctx, a, token)
	if e != nil {
		return nil, e
	}
	n, e := domain.ParseLinkName(name)
	if e != nil {
		return nil, e
	}
	l, e := s.Repo.FindLink(ctx, sp.ID, n)
	if e != nil {
		return nil, e
	}
	if s.Logs == nil {
		return nil, fmt.Errorf("application: logs port unavailable")
	}
	return s.Logs.Tail(ctx, l.ID, tail)
}

type LinkMutationInput struct {
	Alias string
	Token domain.Token
	Name  string
}

func (s *Service) DeleteLink(ctx context.Context, in LinkMutationInput) error {
	s.lock()
	defer s.unlock()
	sp, l, e := s.linkAuth(ctx, in)
	if e != nil {
		if domain.IsKind(e, domain.NotFound) && sp.ID != "" {
			n, parseErr := domain.ParseLinkName(in.Name)
			if parseErr == nil {
				deleted, queryErr := s.Repo.LinkDeleted(ctx, sp.ID, n)
				if queryErr != nil {
					return queryErr
				}
				if deleted {
					return nil
				}
			}
		}
		return e
	}
	return s.destroy(ctx, l, domain.StatusDeleted)
}
func (s *Service) linkAuth(ctx context.Context, in LinkMutationInput) (domain.Space, domain.Link, error) {
	a, e := domain.ParseAlias(in.Alias)
	if e != nil {
		return domain.Space{}, domain.Link{}, e
	}
	sp, e := s.authenticate(ctx, a, in.Token)
	if e != nil {
		return sp, domain.Link{}, e
	}
	n, e := domain.ParseLinkName(in.Name)
	if e != nil {
		return sp, domain.Link{}, e
	}
	l, e := s.Repo.FindLink(ctx, sp.ID, n)
	return sp, l, e
}
func (s *Service) compensationSteps(l domain.Link) compensation.Steps {
	return compensation.Steps{
		RemoveRoute: func(c context.Context) error { return s.Proxy.Remove(c, l.ID) },
		StopProcess: func(c context.Context) error {
			if l.ProcessIdentity == "" {
				return nil
			}
			return s.Processes.Stop(c, ports.ProcessIdentity{Value: l.ProcessIdentity}, s.stopGrace())
		},
		ReleasePort: func(c context.Context) error {
			if l.AllocatedPort == 0 {
				return nil
			}
			return s.Ports.Release(c, ports.Port{Number: l.AllocatedPort, Address: "127.0.0.1"})
		},
	}
}
func (s *Service) destroy(ctx context.Context, l domain.Link, terminal domain.LinkStatus) error {
	if c := s.scheduled[l.ID]; c != nil {
		c()
		delete(s.scheduled, l.ID)
	}
	if e := compensation.Run(ctx, s.compensationSteps(l)); e != nil {
		l.Status = domain.StatusFailed
		if saveErr := s.Repo.SaveLink(ctx, l); saveErr != nil {
			return domain.NewInternal(fmt.Sprintf("cleanup failed: %v; persist failed: %v", e, saveErr))
		}
		return domain.NewInternal(fmt.Sprintf("cleanup failed: %v", e))
	}
	l.Status = terminal
	l.ProcessIdentity = ""
	l.AllocatedPort = 0
	if e := s.Repo.SaveLink(ctx, l); e != nil && !domain.IsKind(e, domain.NotFound) {
		return e
	}
	return s.Repo.DeleteLink(ctx, l.ID)
}
func (s *Service) RestartLink(ctx context.Context, in LinkMutationInput) (CreateLinkResult, error) {
	s.lock()
	defer s.unlock()
	sp, l, e := s.linkAuth(ctx, in)
	if e != nil {
		return CreateLinkResult{}, e
	}
	if l.Expired(s.now()) {
		_ = s.destroy(ctx, l, domain.StatusExpired)
		return CreateLinkResult{}, domain.NewNotFound("link expired")
	}
	if e = s.destroyForRestart(ctx, l); e != nil {
		return CreateLinkResult{}, e
	}
	l.Status = domain.StatusCreating
	l.ProcessIdentity = ""
	l.AllocatedPort = 0
	if e = s.Repo.SaveLink(ctx, l); e != nil {
		return CreateLinkResult{}, e
	}
	return s.start(ctx, sp, l)
}
func (s *Service) destroyForRestart(ctx context.Context, l domain.Link) error {
	if e := compensation.Run(ctx, s.compensationSteps(l)); e != nil {
		l.Status = domain.StatusFailed
		if saveErr := s.Repo.SaveLink(ctx, l); saveErr != nil {
			return domain.NewInternal(fmt.Sprintf("restart cleanup failed: %v; persist failed: %v", e, saveErr))
		}
		return domain.NewInternal(fmt.Sprintf("restart cleanup failed: %v", e))
	}
	return nil
}

// ScheduleAutomaticRestart records deterministic exponential backoff. Callers
// invoke it after an unexpected exit or sustained failed health check.
func (s *Service) ScheduleAutomaticRestart(ctx context.Context, alias string, token domain.Token, name string) (time.Time, error) {
	s.lock()
	defer s.unlock()
	sp, l, e := s.linkAuth(ctx, LinkMutationInput{Alias: alias, Token: token, Name: name})
	if e != nil {
		return time.Time{}, e
	}
	if !l.AutoRestart {
		return time.Time{}, domain.NewConflict("automatic restart disabled")
	}
	if l.Expired(s.now()) {
		_ = s.destroy(ctx, l, domain.StatusExpired)
		return time.Time{}, domain.NewNotFound("link expired")
	}
	l.RestartCount++
	d := time.Second * time.Duration(1<<min(l.RestartCount-1, 6))
	if d > time.Minute {
		d = time.Minute
	}
	at := s.now().Add(d)
	if !l.ExpiresAt.After(at) {
		_ = s.destroy(ctx, l, domain.StatusExpired)
		return time.Time{}, domain.NewNotFound("link expired")
	}
	l.NextRestartAt = at
	l.Status = domain.StatusFailed
	if e = s.Repo.SaveLink(ctx, l); e != nil {
		return time.Time{}, e
	}
	if s.Scheduler != nil {
		if old := s.scheduled[l.ID]; old != nil {
			old()
		}
		cancel, err := s.Scheduler.Schedule(ctx, at, func(c context.Context) { s.runScheduledRestart(c, sp, l) })
		if err != nil {
			return time.Time{}, err
		}
		s.scheduled[l.ID] = cancel
	}
	return at, nil
}
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func (s *Service) runScheduledRestart(ctx context.Context, sp domain.Space, l domain.Link) {
	s.lock()
	defer s.unlock()
	cur, e := s.Repo.FindLink(ctx, l.SpaceID, l.Name)
	if e != nil || cur.Expired(s.now()) || cur.NextRestartAt.After(s.now()) {
		if e == nil && cur.Expired(s.now()) {
			_ = s.destroy(ctx, cur, domain.StatusExpired)
		}
		return
	}
	cur.Status = domain.StatusCreating
	cur.ProcessIdentity = ""
	cur.AllocatedPort = 0
	if e = s.Repo.SaveLink(ctx, cur); e != nil {
		cur.Status = domain.StatusFailed
		s.rescheduleFailed(ctx, sp, cur)
		return
	}
	if _, e = s.start(ctx, sp, cur); e != nil {
		latest, findErr := s.Repo.FindLink(ctx, cur.SpaceID, cur.Name)
		if findErr == nil {
			cur = latest
		}
		s.rescheduleFailed(ctx, sp, cur)
	}
}
func (s *Service) rescheduleFailed(ctx context.Context, sp domain.Space, l domain.Link) {
	if s.Scheduler == nil || !l.AutoRestart || l.Expired(s.now()) {
		return
	}
	l.RestartCount++
	d := time.Second * time.Duration(1<<min(l.RestartCount-1, 6))
	if d > time.Minute {
		d = time.Minute
	}
	at := s.now().Add(d)
	if !l.ExpiresAt.After(at) {
		l.Status = domain.StatusExpired
		_ = s.Repo.SaveLink(ctx, l)
		return
	}
	l.Status = domain.StatusFailed
	l.NextRestartAt = at
	if e := s.Repo.SaveLink(ctx, l); e != nil {
		return
	}
	cancel, e := s.Scheduler.Schedule(ctx, at, func(c context.Context) { s.runScheduledRestart(c, sp, l) })
	if e == nil {
		s.scheduled[l.ID] = cancel
	}
}

// Cleanup gives TTL priority over all other desired state.
func (s *Service) Cleanup(ctx context.Context) error {
	s.lock()
	defer s.unlock()
	now := s.now()
	links, e := s.Repo.ExpiredLinks(ctx, now)
	if e != nil {
		return e
	}
	for _, l := range links {
		if e = s.destroy(ctx, l, domain.StatusExpired); e != nil && !domain.IsKind(e, domain.NotFound) {
			return e
		}
	}
	spaces, e := s.Repo.ExpiredSpaces(ctx, now)
	if e != nil {
		return e
	}
	for _, sp := range spaces {
		ls, _ := s.Repo.ListLinks(ctx, sp.ID)
		for _, l := range ls {
			_ = s.destroy(ctx, l, domain.StatusExpired)
		}
		if e = s.Repo.DeleteSpace(ctx, sp.ID); e != nil {
			return e
		}
	}
	return nil
}
