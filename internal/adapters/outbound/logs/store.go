// Package logs provides bounded, sanitized per-link process logs.
package logs

import (
	"context"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
)

// Capacity is the maximum retained byte size for one link.
const Capacity = 10 << 20

var bearer = regexp.MustCompile(`(?i)\bBearer[[:space:]]+[^[:space:]]+`)
var mirToken = regexp.MustCompile(`\bmir_[A-Za-z0-9_-]+`)

type record struct {
	entry ports.LogEntry
	size  int
}
type follower struct {
	pipe  *io.PipeWriter
	done  chan struct{}
	queue chan []byte
}
type linkLog struct {
	records   []record
	bytes     int
	closed    bool
	followers map[*follower]struct{}
}

// Store is a concurrency-safe 10 MiB ring for each link. Secrets are literal
// values to redact in addition to bearer credentials and Mirage tokens.
type Store struct {
	mu      sync.Mutex
	links   map[domain.LinkID]*linkLog
	secrets []string
	now     func() time.Time
}

func NewStore(secrets ...string) *Store {
	return &Store{links: make(map[domain.LinkID]*linkLog), secrets: append([]string(nil), secrets...), now: func() time.Time { return time.Now().UTC() }}
}
func (s *Store) get(id domain.LinkID) *linkLog {
	l := s.links[id]
	if l == nil {
		l = &linkLog{followers: make(map[*follower]struct{})}
		s.links[id] = l
	}
	return l
}
func (s *Store) redact(text string) string {
	text = bearer.ReplaceAllString(text, "Bearer [redacted]")
	text = mirToken.ReplaceAllString(text, "[redacted]")
	for _, secret := range s.secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	return text
}
func encodedSize(e ports.LogEntry) int { b, _ := json.Marshal(e); return len(b) + 1 }

// Append stores a complete timestamped record and broadcasts it to followers.
func (s *Store) Append(ctx context.Context, id domain.LinkID, entry ports.LogEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if entry.At.IsZero() {
		entry.At = s.now()
	}
	entry.At = entry.At.UTC()
	entry.Text = s.redact(entry.Text)
	if entry.Stream != "stdout" && entry.Stream != "stderr" {
		entry.Stream = "stdout"
	}
	r := record{entry, encodedSize(entry)}
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.get(id)
	if l.closed {
		return io.ErrClosedPipe
	}
	// A record larger than the whole ring cannot be retained without violating
	// the complete-record guarantee.
	if r.size <= Capacity {
		for l.bytes+r.size > Capacity && len(l.records) > 0 {
			l.bytes -= l.records[0].size
			l.records = l.records[1:]
		}
		l.records = append(l.records, r)
		l.bytes += r.size
	}
	b, _ := json.Marshal(entry)
	b = append(b, '\n')
	// A follower has one bounded writer worker. Slow clients lose new records
	// rather than blocking capture or creating unbounded goroutines/memory.
	for f := range l.followers {
		select {
		case f.queue <- b:
		default:
		}
	}
	return nil
}
func (s *Store) Tail(ctx context.Context, id domain.LinkID, n int) ([]ports.LogEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if n < 0 {
		n = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.get(id)
	if n > len(l.records) {
		n = len(l.records)
	}
	out := make([]ports.LogEntry, n)
	start := len(l.records) - n
	for i := range out {
		out[i] = l.records[start+i].entry
	}
	return out, nil
}

// Follow returns newline-delimited JSON records. It closes when Close is called
// for this link or when the supplied context is canceled.
func (s *Store) Follow(ctx context.Context, id domain.LinkID) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, w := io.Pipe()
	f := &follower{pipe: w, done: make(chan struct{}), queue: make(chan []byte, 64)}
	s.mu.Lock()
	l := s.get(id)
	if l.closed {
		s.mu.Unlock()
		_ = w.Close()
		return r, nil
	}
	l.followers[f] = struct{}{}
	s.mu.Unlock()
	go s.runFollower(id, f)
	go func() {
		select {
		case <-ctx.Done():
			s.remove(id, f, ctx.Err())
		case <-f.done:
		}
	}()
	return r, nil
}
func (s *Store) runFollower(id domain.LinkID, f *follower) {
	for {
		select {
		case b := <-f.queue:
			if _, err := f.pipe.Write(b); err != nil {
				s.remove(id, f, err)
				return
			}
		case <-f.done:
			return
		}
	}
}

func (s *Store) remove(id domain.LinkID, f *follower, err error) {
	s.mu.Lock()
	if l := s.links[id]; l != nil {
		if _, ok := l.followers[f]; ok {
			delete(l.followers, f)
			close(f.done)
			_ = f.pipe.CloseWithError(err)
		}
	}
	s.mu.Unlock()
}

// Close marks a process log complete and releases every follower.
func (s *Store) Close(id domain.LinkID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.get(id)
	if l.closed {
		return nil
	}
	l.closed = true
	for f := range l.followers {
		delete(l.followers, f)
		close(f.done)
		_ = f.pipe.Close()
	}
	return nil
}

// Bytes reports retained bytes; it is intentionally useful to adapter tests and diagnostics.
func (s *Store) Bytes(id domain.LinkID) int { s.mu.Lock(); defer s.mu.Unlock(); return s.get(id).bytes }
