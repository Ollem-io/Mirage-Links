package application

import (
	"context"
	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
	"io"
	"strings"
	"testing"
	"time"
)

func TestHTTPFacingServiceMethods(t *testing.T) {
	m := newMem()
	f := &fake{mem: m, now: time.Now().UTC()}
	s := &Service{Repo: m, Clock: f, Hashes: f, Logs: fakeLogs{}}
	sp := domain.Space{ID: "s", Alias: "calm", ExpiresAt: f.now.Add(time.Hour), TokenHash: domain.Token("token").Hash()}
	if e := m.CreateSpace(context.Background(), sp); e != nil {
		t.Fatal(e)
	}
	l := domain.Link{ID: "l", SpaceID: sp.ID, Name: "api"}
	if e := m.CreateLink(context.Background(), l); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SpaceForToken(context.Background(), "token"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.GetSpace(context.Background(), "calm"); e != nil {
		t.Fatal(e)
	}
	r, e := s.FollowLogs(context.Background(), "calm", "token", "api")
	if e != nil {
		t.Fatal(e)
	}
	_, _ = io.ReadAll(r)
	_ = r.Close()
	if _, e := s.SpaceForToken(context.Background(), "wrong"); e == nil {
		t.Fatal("authorized wrong")
	}
}

type fakeLogs struct{}

func (fakeLogs) Tail(context.Context, domain.LinkID, int) ([]ports.LogEntry, error) { return nil, nil }
func (fakeLogs) Follow(context.Context, domain.LinkID) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("x")), nil
}
