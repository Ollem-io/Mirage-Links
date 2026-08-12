package process

import (
	"context"
	"fmt"
	"github.com/primeintellect/mirage/internal/application/ports"
	"net"
	"sync"
)

// Allocator reserves IPv4 loopback TCP ports until Release. Holding the
// listener makes allocation races and accidental public binding impossible.
type Allocator struct {
	mu           sync.Mutex
	listeners    map[int]net.Listener
	afterRelease func(ports.Port)
}

func NewAllocator() *Allocator { return &Allocator{listeners: make(map[int]net.Listener)} }
func (a *Allocator) Allocate(ctx context.Context) (ports.Port, error) {
	if err := ctx.Err(); err != nil {
		return ports.Port{}, err
	}
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return ports.Port{}, fmt.Errorf("reserve loopback port: %w", err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	a.mu.Lock()
	a.listeners[p] = l
	a.mu.Unlock()
	return ports.Port{Number: p, Address: "127.0.0.1"}, nil
}
func (a *Allocator) Release(ctx context.Context, p ports.Port) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	l, ok := a.listeners[p.Number]
	if ok {
		delete(a.listeners, p.Number)
	}
	a.mu.Unlock()
	if !ok {
		return nil
	}
	err := l.Close()
	if a.afterRelease != nil {
		a.afterRelease(p)
	}
	return err
}
