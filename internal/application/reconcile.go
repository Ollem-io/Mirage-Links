package application

import (
	"context"
	"fmt"
	"time"

	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
)

// Reconcile repairs persisted desired state after startup or during the minute
// cleanup pass.  It deliberately publishes only an active link whose owned
// process is still alive and whose loopback health check succeeds.  Every
// other persisted process record is treated as stale and is cleaned up before
// the proxy's owned route set is reconciled.  Proxy.Reconcile is namespace
// scoped by contract, so unrelated Caddy routes cannot be changed here.
func (s *Service) Reconcile(ctx context.Context) error {
	s.lock()
	defer s.unlock()
	if s.Repo == nil || s.Proxy == nil || s.Processes == nil || s.Health == nil {
		return fmt.Errorf("application: reconciliation ports unavailable")
	}
	if err := s.cleanupLocked(ctx); err != nil {
		return err
	}
	now := s.now()
	links, err := s.Repo.ReconciliationLinks(ctx, now)
	if err != nil {
		return err
	}
	desired := make([]ports.Route, 0, len(links))
	for _, link := range links {
		// An interrupted create/restart, failed record, no identity/port, or a
		// dead child must never be made public merely because it is in SQLite.
		if link.Status != domain.StatusActive || link.ProcessIdentity == "" || link.AllocatedPort == 0 {
			if err := s.destroy(ctx, link, domain.StatusFailed); err != nil && !domain.IsKind(err, domain.NotFound) {
				return err
			}
			continue
		}
		alive, err := s.Processes.Alive(ctx, ports.ProcessIdentity{Value: link.ProcessIdentity})
		if err != nil || !alive {
			if err := s.destroy(ctx, link, domain.StatusFailed); err != nil && !domain.IsKind(err, domain.NotFound) {
				return err
			}
			continue
		}
		// Reconciliation health is bounded; it is not the creation grace
		// period. A failed probe removes the route and terminates the stale
		// process rather than exposing an unhealthy upstream.
		probe := replacePort(link.HealthCheck, link.AllocatedPort)
		if err := s.Health.CheckUntil(ctx, probe, time.Second); err != nil {
			if err := s.destroy(ctx, link, domain.StatusFailed); err != nil && !domain.IsKind(err, domain.NotFound) {
				return err
			}
			continue
		}
		space, err := s.Repo.FindSpace(ctx, link.SpaceID)
		if err != nil || space.Expired(now) {
			if err := s.destroy(ctx, link, domain.StatusExpired); err != nil && !domain.IsKind(err, domain.NotFound) {
				return err
			}
			continue
		}
		desired = append(desired, s.route(space, link))
	}
	return s.Proxy.Reconcile(ctx, desired)
}

// Shutdown removes every owned route before stopping every owned process. It
// is idempotent and is called before managed Caddy/storage are closed.
func (s *Service) Shutdown(ctx context.Context) error {
	s.lock()
	defer s.unlock()
	links, err := s.Repo.ReconciliationLinks(ctx, s.now())
	if err != nil {
		return err
	}
	var first error
	for _, link := range links {
		if err := s.destroy(ctx, link, domain.StatusDeleted); err != nil && !domain.IsKind(err, domain.NotFound) && first == nil {
			first = err
		}
	}
	// Reconcile an empty owned set catches routes whose records were already
	// archived and guarantees graceful shutdown leaves no Mirage route.
	if err := s.Proxy.Reconcile(ctx, nil); err != nil && first == nil {
		first = err
	}
	return first
}

func (s *Service) cleanupLocked(ctx context.Context) error {
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
		ls, listErr := s.Repo.ListLinks(ctx, sp.ID)
		if listErr != nil {
			return listErr
		}
		for _, l := range ls {
			if e = s.destroy(ctx, l, domain.StatusExpired); e != nil && !domain.IsKind(e, domain.NotFound) {
				return e
			}
		}
		if e = s.Repo.DeleteSpace(ctx, sp.ID); e != nil {
			return e
		}
	}
	return nil
}
