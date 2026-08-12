// mirage-scenario runs the real application coordinator and embedded libSQL
// repository with deterministic fake side-effect ports.
package main

import (
	"context"
	"fmt"
	"github.com/primeintellect/mirage/internal/adapters/outbound/libsql"
	"github.com/primeintellect/mirage/internal/application"
	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
	"io"
	"os"
	"path/filepath"
	"time"
)

type trace struct{ now time.Time }

func (trace) NewSpaceID() domain.SpaceID                     { return "s" }
func (trace) NewLinkID() domain.LinkID                       { return "l" }
func (trace) NewAlias() (domain.Alias, error)                { return "calm-fox", nil }
func (trace) Generate() (domain.Token, error)                { return "token", nil }
func (trace) Hash(t domain.Token) domain.TokenHash           { return t.Hash() }
func (trace) Verify(h domain.TokenHash, t domain.Token) bool { return h.Verify(t) }
func (t trace) Now() time.Time                               { return t.now }
func (trace) Allocate(context.Context) (ports.Port, error) {
	fmt.Println("reserve-port")
	return ports.Port{Number: 45678, Address: "127.0.0.1"}, nil
}
func (trace) Release(context.Context, ports.Port) error { fmt.Println("release-port"); return nil }
func (trace) Start(context.Context, ports.StartRequest) (ports.ProcessIdentity, error) {
	fmt.Println("start-process")
	return ports.ProcessIdentity{Value: "fixture"}, nil
}
func (trace) Stop(context.Context, ports.ProcessIdentity, time.Duration) error {
	fmt.Println("stop-process")
	return nil
}
func (trace) Alive(context.Context, ports.ProcessIdentity) (bool, error) { return true, nil }
func (trace) CheckUntil(context.Context, domain.HealthCheck, time.Duration) error {
	fmt.Println("health-ok")
	return nil
}
func (trace) Add(context.Context, ports.Route) error                             { fmt.Println("add-route"); return nil }
func (trace) Remove(context.Context, domain.LinkID) error                        { fmt.Println("remove-route"); return nil }
func (trace) List(context.Context) ([]ports.Route, error)                        { return nil, nil }
func (trace) Reconcile(context.Context, []ports.Route) error                     { return nil }
func (trace) Tail(context.Context, domain.LinkID, int) ([]ports.LogEntry, error) { return nil, nil }
func (trace) Follow(context.Context, domain.LinkID) (io.ReadCloser, error)       { return nil, nil }
func main() {
	ctx := context.Background()
	p := filepath.Join(os.TempDir(), "mirage-mir06-scenario.db")
	_ = os.Remove(p)
	repo, e := libsql.Open(p)
	if e != nil {
		panic(e)
	}
	defer repo.Close()
	t := trace{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	svc := &application.Service{Repo: repo, Clock: t, IDs: t, Aliases: t, Tokens: t, Hashes: t, Ports: t, Processes: t, Health: t, Proxy: t, Logs: t, BaseHost: "mirage.example.com"}
	sp, e := svc.CreateSpace(ctx, application.CreateSpaceInput{})
	if e != nil {
		panic(e)
	}
	r, e := svc.CreateLink(ctx, application.CreateLinkInput{Alias: "calm-fox", Token: sp.Token, Name: "api", Command: "fixture", Folder: ".", HealthCheck: domain.HealthCheck{Method: domain.HealthGET, URL: "http://127.0.0.1:{port}/"}})
	if e != nil {
		panic(e)
	}
	fmt.Println(r.Link.Status)
	if e = svc.DeleteLink(ctx, application.LinkMutationInput{Alias: "calm-fox", Token: sp.Token, Name: "api"}); e != nil {
		panic(e)
	}
	fmt.Println("deleted")
}
