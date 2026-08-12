package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

// Listeners deliberately use different handlers: the public handler is never
// derived from the management mux, preventing accidental route exposure.
type Servers struct {
	Private, Public *http.Server
	api             *API
	wg              sync.WaitGroup
}

func NewServers(privateAddr, publicAddr string, api *API, public http.Handler) *Servers {
	if public == nil {
		public = http.NotFoundHandler()
	}
	return &Servers{Private: &http.Server{Addr: privateAddr, Handler: api.Handler()}, Public: &http.Server{Addr: publicAddr, Handler: public}, api: api}
}
func (s *Servers) Serve(privateListener, publicListener net.Listener) {
	s.api.SetReady(true)
	if privateListener != nil {
		s.wg.Add(1)
		go func() { defer s.wg.Done(); _ = s.Private.Serve(privateListener) }()
	}
	if publicListener != nil && s.Public != nil {
		s.wg.Add(1)
		go func() { defer s.wg.Done(); _ = s.Public.Serve(publicListener) }()
	}
}
func (s *Servers) Shutdown(ctx context.Context) error {
	if err := s.api.Drain(ctx); err != nil {
		return err
	}
	var wg sync.WaitGroup
	var first error
	var mu sync.Mutex
	for _, srv := range []*http.Server{s.Private, s.Public} {
		if srv == nil {
			continue
		}
		wg.Add(1)
		go func(x *http.Server) {
			defer wg.Done()
			if e := x.Shutdown(ctx); e != nil && !errors.Is(e, http.ErrServerClosed) {
				mu.Lock()
				if first == nil {
					first = e
				}
				mu.Unlock()
			}
		}(srv)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.wg.Wait()
	return first
}

// Close is a bounded convenience for command shutdown paths.
func (s *Servers) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.Shutdown(ctx)
}
