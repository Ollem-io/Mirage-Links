package process

import (
	"context"
	"fmt"
	"net"
	"strconv"
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
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(p.Number))
	for {
		conn, err := (&net.Dialer{Timeout: bindPollInterval}).DialContext(ctx, "tcp4", address)
		if err == nil {
			_ = conn.Close()
			// A competing listener may win the release window. Require the
			// launched process to remain alive and the endpoint to remain bound
			// across a settling interval; a child whose bind failed exits here.
			timer := time.NewTimer(bindPollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			alive, aliveErr := s.Alive(ctx, id)
			if aliveErr != nil {
				return aliveErr
			}
			if alive {
				confirm, confirmErr := (&net.Dialer{Timeout: bindPollInterval}).DialContext(ctx, "tcp4", address)
				if confirmErr == nil {
					_ = confirm.Close()
					return nil
				}
			}
		}
		alive, aliveErr := s.Alive(ctx, id)
		if aliveErr != nil {
			return aliveErr
		}
		if !alive {
			return fmt.Errorf("child exited before binding %s", address)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("child did not bind %s within %s", address, grace)
		case <-tick.C:
		}
	}
}
