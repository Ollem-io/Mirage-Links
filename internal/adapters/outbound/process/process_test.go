//go:build linux

package process

import (
	"context"
	"github.com/primeintellect/mirage/internal/adapters/outbound/logs"
	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func buildFixture(t *testing.T) string {
	t.Helper()
	if prebuilt := os.Getenv("MIRAGE_TEST_SERVICE"); prebuilt != "" {
		if _, err := os.Stat(prebuilt); err != nil {
			t.Fatalf("prebuilt fixture: %v", err)
		}
		return prebuilt
	}
	out := filepath.Join(t.TempDir(), "service")
	cmd := exec.Command("go", "build", "-o", out, "./testdata/service")
	cmd.Dir = "."
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v: %s", err, b)
	}
	return out
}

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

func TestNaturalExitForgetsIdentityAndAllocatedHandoff(t *testing.T) {
	s := NewSupervisor(nil)
	dir := t.TempDir()
	id, e := s.Start(context.Background(), ports.StartRequest{Folder: dir, Command: "exit 0"})
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(time.Second)
	for {
		alive, e := s.Alive(context.Background(), id)
		if e != nil {
			t.Fatal(e)
		}
		if !alive {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("natural exit ownership not removed")
		}
		time.Sleep(time.Millisecond)
	}
	a := NewAllocator()
	fixture := buildFixture(t)
	id, p, e := s.startAllocated(context.Background(), a, ports.StartRequest{Folder: dir, Command: fixture + " healthy"}, 2, time.Second)
	if e != nil || p.Number == 0 {
		t.Fatal(id, p, e)
	}
	if e = s.Stop(context.Background(), id, time.Second); e != nil {
		t.Fatal(e)
	}
}
func TestStartAllocatedCollisionRetry(t *testing.T) {
	fixture := buildFixture(t)
	a := NewAllocator()
	var collision net.Listener
	calls := 0
	a.afterRelease = func(p ports.Port) {
		calls++
		if calls == 1 {
			var err error
			collision, err = net.Listen("tcp4", net.JoinHostPort(p.Address, strconv.Itoa(p.Number)))
			if err != nil {
				t.Fatalf("force release-window collision: %v", err)
			}
		}
	}
	s := NewSupervisor(nil)
	id, p, err := s.startAllocated(context.Background(), a, ports.StartRequest{Folder: t.TempDir(), Command: fixture + " healthy"}, 2, 2*time.Second)
	if collision != nil {
		_ = collision.Close()
	}
	if err != nil || calls != 2 {
		t.Fatalf("id=%v port=%v calls=%d err=%v", id, p, calls, err)
	}
	if len(s.procs) != 1 {
		t.Fatalf("failed attempt leaked ownership: %d", len(s.procs))
	}
	if err := s.Stop(context.Background(), id, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestStartAllocatedNeverBindCleansEveryAttempt(t *testing.T) {
	fixture := buildFixture(t)
	s := NewSupervisor(nil)
	beforeG := runtime.NumGoroutine()
	_, p, err := s.startAllocated(context.Background(), NewAllocator(), ports.StartRequest{Folder: t.TempDir(), Command: fixture + " never-bind"}, 2, 40*time.Millisecond)
	if err == nil || p.Number != 0 {
		t.Fatalf("port=%v err=%v", p, err)
	}
	if len(s.procs) != 0 {
		t.Fatalf("ownership leak: %d", len(s.procs))
	}
	time.Sleep(30 * time.Millisecond)
	if runtime.NumGoroutine() > beforeG+4 {
		t.Fatalf("goroutine leak: before=%d after=%d", beforeG, runtime.NumGoroutine())
	}
}

func TestStopCancellationAndHandoffFailure(t *testing.T) {
	s := NewSupervisor(nil)
	dir := t.TempDir()
	id, e := s.Start(context.Background(), ports.StartRequest{Folder: dir, Command: "sleep 1"})
	if e != nil {
		t.Fatal(e)
	}
	ctx, c := context.WithCancel(context.Background())
	c()
	if e := s.Stop(ctx, id, time.Second); e == nil {
		t.Fatal("cancel stop")
	}
	if e := s.Stop(context.Background(), id, time.Second); e != nil {
		t.Fatal(e)
	}
	ctx, c = context.WithCancel(context.Background())
	c()
	if _, _, e := s.StartAllocated(ctx, NewAllocator(), ports.StartRequest{Folder: dir, Command: "true"}, 0); e == nil {
		t.Fatal("handoff cancel")
	}
}

func TestStopUnknownNegativeGraceAndAlreadyExited(t *testing.T) {
	s := NewSupervisor(nil)
	if e := s.Stop(context.Background(), ports.ProcessIdentity{Value: "unknown"}, time.Millisecond); e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	id, e := s.Start(context.Background(), ports.StartRequest{Folder: dir, Command: "true"})
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(20 * time.Millisecond)
	if e := s.Stop(context.Background(), id, -time.Second); e != nil {
		t.Fatal(e)
	}
}

func TestGracefulStopAndWaitHelpers(t *testing.T) {
	s := NewSupervisor(nil)
	id, err := s.Start(context.Background(), ports.StartRequest{Folder: t.TempDir(), Command: `trap 'exit 0' TERM; while :; do sleep 1; done`})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(context.Background(), id, time.Second); err != nil {
		t.Fatal(err)
	}
	if s.find(id) != nil {
		t.Fatal("not forgotten")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitGroup(ctx, syscall.Getpgrp(), time.Second) {
		t.Fatal("live group reported dead")
	}
}

func TestWaitBoundDeadAndCanceled(t *testing.T) {
	s := NewSupervisor(nil)
	id, err := s.Start(context.Background(), ports.StartRequest{Folder: t.TempDir(), Command: "exit 0"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.waitBound(context.Background(), id, ports.Port{Number: 1}, 100*time.Millisecond); err == nil {
		t.Fatal("dead accepted")
	}
	id, err = s.Start(context.Background(), ports.StartRequest{Folder: t.TempDir(), Command: "sleep 1"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.waitBound(ctx, id, ports.Port{Number: 1}, time.Second); err == nil {
		t.Fatal("cancel accepted")
	}
	_ = s.Stop(context.Background(), id, time.Second)
}

func TestStopInjectedBranches(t *testing.T) {
	oldAlive, oldWait, oldKill := groupAliveCall, waitGroupCall, killGroupCall
	defer func() { groupAliveCall, waitGroupCall, killGroupCall = oldAlive, oldWait, oldKill }()
	s := NewSupervisor(nil)
	id, err := s.Start(context.Background(), ports.StartRequest{Folder: t.TempDir(), Command: "sleep 10"})
	if err != nil {
		t.Fatal(err)
	}
	pid := idPID(id)
	groupAliveCall = func(int) bool { return false }
	if err := s.Stop(context.Background(), id, time.Second); err != nil {
		t.Fatal(err)
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)

	makeRun := func() ports.ProcessIdentity {
		id, e := s.Start(context.Background(), ports.StartRequest{Folder: t.TempDir(), Command: "sleep 10"})
		if e != nil {
			t.Fatal(e)
		}
		return id
	}
	groupAliveCall = func(int) bool { return true }
	killGroupCall = func(int, syscall.Signal) {}
	id = makeRun()
	waitGroupCall = func(context.Context, int, time.Duration) bool { return true }
	if err := s.Stop(context.Background(), id, time.Second); err != nil {
		t.Fatal(err)
	}
	_ = syscall.Kill(-idPID(id), syscall.SIGKILL)

	id = makeRun()
	waits := 0
	waitGroupCall = func(context.Context, int, time.Duration) bool { waits++; return waits == 2 }
	if err := s.Stop(context.Background(), id, time.Second); err != nil {
		t.Fatal(err)
	}
	_ = syscall.Kill(-idPID(id), syscall.SIGKILL)

	id = makeRun()
	ctx2, cancel2 := context.WithCancel(context.Background())
	waitGroupCall = func(context.Context, int, time.Duration) bool { cancel2(); return false }
	if err := s.Stop(ctx2, id, time.Second); err == nil {
		t.Fatal("cancel after term")
	}
	_ = syscall.Kill(-idPID(id), syscall.SIGKILL)

	id = makeRun()
	calls := 0
	waitGroupCall = func(context.Context, int, time.Duration) bool { calls++; return false }
	if err := s.Stop(context.Background(), id, time.Second); err == nil {
		t.Fatal("survival accepted")
	}
	_ = syscall.Kill(-idPID(id), syscall.SIGKILL)

	id = makeRun()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Stop(ctx, id, time.Second); err == nil {
		t.Fatal("cancel")
	}
	_ = syscall.Kill(-idPID(id), syscall.SIGKILL)
}
func idPID(id ports.ProcessIdentity) int {
	p, _ := strconv.Atoi(strings.Split(id.Value, ":")[0])
	return p
}

func TestStartAllocatedStartFailureAndMinimumAttempt(t *testing.T) {
	s := NewSupervisor(nil)
	_, _, err := s.startAllocated(context.Background(), NewAllocator(), ports.StartRequest{Folder: t.TempDir()}, 0, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "after 1") {
		t.Fatal(err)
	}
}

func TestAdditionalCanceledBranches(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := NewSupervisor(nil)
	if _, err := s.Start(ctx, ports.StartRequest{Folder: t.TempDir(), Command: "true"}); err == nil {
		t.Fatal("start cancel")
	}
	a := NewAllocator()
	p, err := a.Allocate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Release(ctx, p); err == nil {
		t.Fatal("release cancel")
	}
	if err := a.Release(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

func TestCompetitorListenerNeverAcceptedAsChildOwnership(t *testing.T) {
	fixture := buildFixture(t)
	for i := 0; i < 100; i++ {
		a := NewAllocator()
		var competitor net.Listener
		a.afterRelease = func(p ports.Port) {
			var err error
			competitor, err = net.Listen("tcp4", net.JoinHostPort(p.Address, strconv.Itoa(p.Number)))
			if err != nil {
				t.Fatalf("iteration %d steal: %v", i, err)
			}
		}
		s := NewSupervisor(nil)
		_, _, err := s.startAllocated(context.Background(), a, ports.StartRequest{Folder: t.TempDir(), Command: fixture + " never-bind"}, 1, 100*time.Millisecond)
		_ = competitor.Close()
		if err == nil || !strings.Contains(err.Error(), "not owned") {
			t.Fatalf("iteration %d competitor accepted: %v", i, err)
		}
		if len(s.procs) != 0 {
			t.Fatalf("iteration %d ownership leak", i)
		}
	}
}

func TestListenerOwnershipHelpers(t *testing.T) {
	if !procLoopback("0100007F") || !procLoopback("00000000000000000000000001000000") || procLoopback("00000000") {
		t.Fatal("loopback parser")
	}
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	owned, listening, err := ownsLoopbackListener(syscall.Getpgrp(), l.Addr().(*net.TCPAddr).Port)
	if err != nil || !owned || !listening {
		t.Fatalf("owned=%v listening=%v err=%v", owned, listening, err)
	}
	owned, listening, err = ownsLoopbackListener(syscall.Getpgrp()+999999, l.Addr().(*net.TCPAddr).Port)
	if err != nil || owned || !listening {
		t.Fatalf("competitor classification %v %v %v", owned, listening, err)
	}
	if _, err := identityPID(ports.ProcessIdentity{Value: "bad"}); err == nil {
		t.Fatal("bad identity")
	}
}

func TestRecoveredIdentityStopAndAlive(t *testing.T) {
	oldAlive, oldWait, oldKill := groupAliveCall, waitGroupCall, killGroupCall
	defer func() { groupAliveCall, waitGroupCall, killGroupCall = oldAlive, oldWait, oldKill }()
	s := NewSupervisor(nil)
	groupAliveCall = func(int) bool { return false }
	if err := s.Stop(context.Background(), ports.ProcessIdentity{Value: "321:1"}, time.Second); err != nil {
		t.Fatal(err)
	}
	alive, err := s.Alive(context.Background(), ports.ProcessIdentity{Value: "bad"})
	if err != nil || alive {
		t.Fatalf("%v %v", alive, err)
	}
	calls := 0
	groupAliveCall = func(int) bool { return true }
	waitGroupCall = func(context.Context, int, time.Duration) bool { calls++; return calls > 1 }
	killed := 0
	killGroupCall = func(int, syscall.Signal) { killed++ }
	if err := s.Stop(context.Background(), ports.ProcessIdentity{Value: "321:1"}, -1); err != nil {
		t.Fatal(err)
	}
	if killed != 2 || calls != 2 {
		t.Fatalf("kill=%d wait=%d", killed, calls)
	}
	groupAliveCall = func(int) bool { return true }
	alive, err = s.Alive(context.Background(), ports.ProcessIdentity{Value: "321:1"})
	if err != nil || !alive {
		t.Fatalf("%v %v", alive, err)
	}
}
