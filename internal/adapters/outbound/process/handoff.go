package process

import (
	"context"
	"fmt"
	"github.com/primeintellect/mirage/internal/application/ports"
)

// StartAllocated obtains a loopback reservation and hands it to a child. A
// listener cannot be inherited portably by shell commands, so it is released
// immediately before exec. If exec loses that unavoidable kernel handoff race,
// it reallocates and retries up to attempts (minimum one). Callers must retain
// the returned Port and use it for health interpolation.
func (s *Supervisor) StartAllocated(ctx context.Context, a *Allocator, request ports.StartRequest, attempts int) (ports.ProcessIdentity, ports.Port, error) {
	if attempts < 1 {
		attempts = 1
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
			return id, p, nil
		}
		last = err
		if ctx.Err() != nil {
			return ports.ProcessIdentity{}, ports.Port{}, ctx.Err()
		}
	}
	return ports.ProcessIdentity{}, ports.Port{}, fmt.Errorf("start after %d port handoff attempts: %w", attempts, last)
}
