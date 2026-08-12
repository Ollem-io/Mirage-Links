package process

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/primeintellect/mirage/internal/application/ports"
)

const (
	defaultBindGrace = 750 * time.Millisecond
	bindPollInterval = 20 * time.Millisecond
)

// StartAllocated reserves a loopback port, starts the child, and does not
// report success until the child has actually accepted a TCP connection on
// that port. The reservation-to-exec window cannot be atomic for the approved
// shell-command contract, so a collision or a child that never binds is
// detected within a bounded grace, the entire failed process group is stopped,
// and a fresh reservation is tried.
func (s *Supervisor) StartAllocated(ctx context.Context, a *Allocator, request ports.StartRequest, attempts int) (ports.ProcessIdentity, ports.Port, error) {
	return s.startAllocated(ctx, a, request, attempts, defaultBindGrace)
}

func (s *Supervisor) startAllocated(ctx context.Context, a *Allocator, request ports.StartRequest, attempts int, grace time.Duration) (ports.ProcessIdentity, ports.Port, error) {
	if attempts < 1 {
		attempts = 1
	}
	if grace <= 0 {
		grace = defaultBindGrace
	}
	var last error
	for i := 0; i < attempts; i++ {
		p, err := a.Allocate(ctx)
		if err != nil {
			return ports.ProcessIdentity{}, ports.Port{}, err
		}
		request.Port = p
		if err = a.Release(ctx, p); err != nil {
			return ports.ProcessIdentity{}, ports.Port{}, err
		}
		id, err := s.Start(ctx, request)
		if err == nil {
			err = s.waitBound(ctx, id, p, grace)
		}
		if err == nil {
			return id, p, nil
		}
		last = err
		// Never overlap attempts: release descendants, pipes, and ownership
		// before obtaining another candidate port.
		_ = s.Stop(context.Background(), id, 100*time.Millisecond)
		if ctx.Err() != nil {
			return ports.ProcessIdentity{}, ports.Port{}, ctx.Err()
		}
	}
	return ports.ProcessIdentity{}, ports.Port{}, fmt.Errorf("start after %d port handoff attempts: %w", attempts, last)
}

func (s *Supervisor) waitBound(ctx context.Context, id ports.ProcessIdentity, p ports.Port, grace time.Duration) error {
	deadline := time.NewTimer(grace)
	defer deadline.Stop()
	tick := time.NewTicker(bindPollInterval)
	defer tick.Stop()
	pgid, err := identityPID(id)
	if err != nil {
		return err
	}
	var ownershipErr error
	for {
		owned, listening, err := ownsLoopbackListener(pgid, p.Number)
		if err != nil {
			ownershipErr = err
		} else if owned {
			return nil
		} else if listening {
			return fmt.Errorf("port %d listener is not owned by process group %d", p.Number, pgid)
		}
		alive, aliveErr := s.Alive(ctx, id)
		if aliveErr != nil {
			return aliveErr
		}
		if !alive {
			return fmt.Errorf("child exited before binding 127.0.0.1:%d", p.Number)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if ownershipErr != nil {
				return fmt.Errorf("verify listener ownership: %w", ownershipErr)
			}
			return fmt.Errorf("child did not bind 127.0.0.1:%d within %s", p.Number, grace)
		case <-tick.C:
		}
	}
}

func identityPID(id ports.ProcessIdentity) (int, error) {
	parts := strings.SplitN(id.Value, ":", 2)
	pid, err := strconv.Atoi(parts[0])
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid process identity %q", id.Value)
	}
	return pid, nil
}
