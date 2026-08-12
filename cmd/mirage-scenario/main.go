// mirage-scenario is a deterministic lifecycle trace artifact. It deliberately
// uses a real embedded libSQL Store while every side-effecting lifecycle port is fake.
package main

import (
	"context"
	"fmt"
	"github.com/primeintellect/mirage/internal/adapters/outbound/libsql"
	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
	"os"
	"path/filepath"
	"time"
)

type trace struct{}

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
func (trace) Check(context.Context, domain.HealthCheck) error            { fmt.Println("health-ok"); return nil }
func (trace) Add(context.Context, ports.Route) error                     { fmt.Println("add-route"); return nil }
func (trace) Remove(context.Context, domain.LinkID) error                { fmt.Println("remove-route"); return nil }
func (trace) List(context.Context) ([]ports.Route, error)                { return nil, nil }
func (trace) Reconcile(context.Context, []ports.Route) error             { return nil }
func main() {
	p := filepath.Join(os.TempDir(), "mirage-mir06-scenario.db")
	_ = os.Remove(p)
	s, e := libsql.Open(p)
	if e != nil {
		panic(e)
	}
	defer s.Close()
	now := time.Now()
	sp := domain.Space{ID: "s", Alias: "calm-fox", ExpiresAt: now.Add(time.Hour), TokenHash: domain.TokenHash{1}}
	if e = s.CreateSpace(context.Background(), sp); e != nil {
		panic(e)
	}
	l := domain.Link{ID: "l", SpaceID: "s", Name: "api", Status: domain.StatusCreating, Command: "fixture", Folder: ".", HealthCheck: domain.HealthCheck{Method: domain.HealthGET, URL: "http://127.0.0.1:45678/"}, Grace: time.Second, ExpiresAt: now.Add(time.Hour)}
	_ = s.CreateLink(context.Background(), l)
	t := trace{}
	p0, _ := t.Allocate(context.Background())
	id, _ := t.Start(context.Background(), ports.StartRequest{})
	_ = id
	_ = t.Check(context.Background(), l.HealthCheck)
	_ = t.Add(context.Background(), ports.Route{})
	fmt.Println("active")
	_ = t.Remove(context.Background(), l.ID)
	_ = t.Stop(context.Background(), id, time.Second)
	_ = t.Release(context.Background(), p0)
	fmt.Println("deleted")
}
