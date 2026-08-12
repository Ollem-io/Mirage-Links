//go:build linux

package process

import (
	"context"
	"github.com/primeintellect/mirage/internal/adapters/outbound/logs"
	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAllocatorReservesLoopbackAndRelease(t *testing.T) {
	a := NewAllocator()
	p, e := a.Allocate(context.Background())
	if e != nil || p.Address != "127.0.0.1" || p.Number == 0 {
		t.Fatal(p, e)
	}
	if l, e := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p.Number)); e == nil {
		l.Close()
		t.Fatal("not reserved")
	}
	if e := a.Release(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	l, e := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p.Number))
	if e != nil {
		t.Fatal(e)
	}
	l.Close()
	if e := a.Release(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	ctx, c := context.WithCancel(context.Background())
	c()
	if _, e := a.Allocate(ctx); e == nil {
		t.Fatal("canceled")
	}
}
func TestInterpolation(t *testing.T) {
	p := ports.Port{Number: 1234}
	if x := InterpolatePort("a {port} {port}", p); x != "a 1234 1234" {
		t.Fatal(x)
	}
	h := InterpolateHealthCheck(domain.HealthCheck{Method: domain.HealthGET, URL: "http://127.0.0.1:{port}/"}, p)
	if h.URL != "http://127.0.0.1:1234/" {
		t.Fatal(h.URL)
	}
}
func TestStartEnvironmentFolderLogsAndStop(t *testing.T) {
	dir := t.TempDir()
	store := logs.NewStore("shhh")
	s := NewSupervisor(store)
	p := ports.Port{Number: 23456}
	id, e := s.Start(context.Background(), ports.StartRequest{LinkID: "l", Folder: dir, Port: p, Environment: map[string]string{"CUSTOM": "shhh"}, Command: `printf '%s|%s|%s\n' "$PORT" "$PWD" "$CUSTOM"; printf 'err mir_secret\n' >&2; trap 'exit 0' TERM; while :; do sleep 1; done`})
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(80 * time.Millisecond)
	alive, e := s.Alive(context.Background(), id)
	if e != nil || !alive {
		t.Fatal(alive, e)
	}
	if e := s.Stop(context.Background(), id, time.Second); e != nil {
		t.Fatal(e)
	}
	time.Sleep(30 * time.Millisecond)
	alive, _ = s.Alive(context.Background(), id)
	if alive {
		t.Fatal("still alive")
	}
	entries, _ := store.Tail(context.Background(), "l", 10)
	joined := ""
	for _, x := range entries {
		joined += x.Stream + ":" + x.Text + "\n"
	}
	if !strings.Contains(joined, "23456|"+dir+"|[redacted]") || !strings.Contains(joined, "stderr:err [redacted]") {
		t.Fatalf("%q", joined)
	}
}
func TestForceKillGroupAndInputFailures(t *testing.T) {
	s := NewSupervisor(nil)
	if _, e := s.Start(context.Background(), ports.StartRequest{}); e == nil {
		t.Fatal("empty accepted")
	}
	if _, e := s.Start(context.Background(), ports.StartRequest{Command: "true", Folder: filepath.Join(t.TempDir(), "none")}); e == nil {
		t.Fatal("bad folder")
	}
	dir := t.TempDir()
	id, e := s.Start(context.Background(), ports.StartRequest{LinkID: domain.LinkID("l"), Folder: dir, Command: `trap '' TERM; while :; do sleep 1; done`})
	if e != nil {
		t.Fatal(e)
	}
	if e := s.Stop(context.Background(), id, 20*time.Millisecond); e != nil {
		t.Fatal(e)
	}
	if ok, _ := s.Alive(context.Background(), id); ok {
		t.Fatal("force kill failed")
	}
	if e := s.Stop(context.Background(), id, time.Millisecond); e != nil {
		t.Fatal(e)
	}
	ctx, c := context.WithCancel(context.Background())
	c()
	if _, e := s.Alive(ctx, id); e == nil {
		t.Fatal("cancel")
	}
	_ = os.RemoveAll(dir)
}
