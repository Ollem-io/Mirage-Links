//go:build linux

// Package process starts trusted local commands in owned Unix process groups.
package process

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
)

type sink interface {
	Append(context.Context, domain.LinkID, ports.LogEntry) error
}
type closer interface{ Close(domain.LinkID) error }
type running struct {
	cmd  *exec.Cmd
	done chan struct{}
	once sync.Once
}

// Supervisor owns each started process group. It uses /bin/sh deliberately:
// commands are trusted local inputs and the approved contract requires shell execution.
type Supervisor struct {
	mu    sync.Mutex
	procs map[string]*running
	logs  sink
	next  uint64
}

func NewSupervisor(logSink sink) *Supervisor {
	return &Supervisor{procs: make(map[string]*running), logs: logSink}
}
func InterpolatePort(value string, p ports.Port) string {
	return strings.ReplaceAll(value, "{port}", strconv.Itoa(p.Number))
}

// InterpolateHealthCheck applies the same literal substitution to a health URL
// while preserving its already validated method. The application supplies this
// value to its HealthChecker after starting the child.
func InterpolateHealthCheck(check domain.HealthCheck, p ports.Port) domain.HealthCheck {
	check.URL = InterpolatePort(check.URL, p)
	return check
}
func (s *Supervisor) Start(ctx context.Context, r ports.StartRequest) (ports.ProcessIdentity, error) {
	if err := ctx.Err(); err != nil {
		return ports.ProcessIdentity{}, err
	}
	if r.Command == "" {
		return ports.ProcessIdentity{}, fmt.Errorf("start process: empty command")
	}
	if r.Folder == "" {
		return ports.ProcessIdentity{}, fmt.Errorf("start process: empty execution folder")
	}
	info, err := os.Stat(r.Folder)
	if err != nil || !info.IsDir() {
		return ports.ProcessIdentity{}, fmt.Errorf("start process: invalid execution folder: %w", err)
	}
	cmd := exec.Command("/bin/sh", "-c", InterpolatePort(r.Command, r.Port))
	cmd.Dir = r.Folder
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append([]string{}, os.Environ()...)
	cmd.Env = append(cmd.Env, "PORT="+strconv.Itoa(r.Port.Number))
	for k, v := range r.Environment {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ports.ProcessIdentity{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return ports.ProcessIdentity{}, err
	}
	if err = cmd.Start(); err != nil {
		return ports.ProcessIdentity{}, fmt.Errorf("start process: %w", err)
	}
	run := &running{cmd: cmd, done: make(chan struct{})}
	s.mu.Lock()
	s.next++
	// A generation makes identities unambiguous even if the OS quickly reuses a PID.
	id := strconv.Itoa(cmd.Process.Pid) + ":" + strconv.FormatUint(s.next, 10)
	s.procs[id] = run
	s.mu.Unlock()
	go s.capture(r.LinkID, "stdout", stdout)
	go s.capture(r.LinkID, "stderr", stderr)
	go func() {
		_ = cmd.Wait()
		run.once.Do(func() { close(run.done) })
		// Delete only if this exact run still owns the identity. This prevents a
		// natural exit from erasing a later process after PID reuse.
		s.mu.Lock()
		if s.procs[id] == run {
			delete(s.procs, id)
		}
		s.mu.Unlock()
		if c, ok := s.logs.(closer); ok {
			_ = c.Close(r.LinkID)
		}
	}()
	return ports.ProcessIdentity{Value: id}, nil
}
func (s *Supervisor) capture(id domain.LinkID, stream string, reader io.Reader) {
	scan := bufio.NewScanner(reader)
	buf := make([]byte, 64*1024)
	scan.Buffer(buf, 1024*1024)
	for scan.Scan() {
		if s.logs != nil {
			_ = s.logs.Append(context.Background(), id, ports.LogEntry{At: time.Now().UTC(), Stream: stream, Text: scan.Text()})
		}
	}
}
func (s *Supervisor) find(id ports.ProcessIdentity) *running {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.procs[id.Value]
}

var (
	groupAliveCall = groupAlive
	waitGroupCall  = waitGroup
	killGroupCall  = func(pgid int, sig syscall.Signal) { _ = syscall.Kill(-pgid, sig) }
)

func (s *Supervisor) Stop(ctx context.Context, id ports.ProcessIdentity, grace time.Duration) error {
	r := s.find(id)
	// After a Mirage crash the new supervisor has no in-memory run record, but
	// SQLite retains the process-group leader identity.  Recover that group so
	// stale recorded children do not survive a restart. IDs are generated only
	// from a positive PID plus generation by Start.
	if r == nil {
		pidPart := strings.Split(id.Value, ":")[0]
		pgid, err := strconv.Atoi(pidPart)
		if err != nil || pgid <= 1 {
			return nil
		}
		if !groupAliveCall(pgid) {
			return nil
		}
		if grace < 0 {
			grace = 0
		}
		killGroupCall(pgid, syscall.SIGTERM)
		if waitGroupCall(ctx, pgid, grace) {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		killGroupCall(pgid, syscall.SIGKILL)
		if waitGroupCall(ctx, pgid, time.Second) {
			return nil
		}
		return fmt.Errorf("recovered process group %d survived SIGKILL", pgid)
	}
	if grace < 0 {
		grace = 0
	}
	pgid := r.cmd.Process.Pid
	if !groupAliveCall(pgid) {
		s.forget(id)
		return nil
	}
	killGroupCall(pgid, syscall.SIGTERM)
	if waitGroupCall(ctx, pgid, grace) {
		s.forget(id)
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	killGroupCall(pgid, syscall.SIGKILL)
	if !waitGroupCall(ctx, pgid, time.Second) {
		if err := ctx.Err(); err != nil {
			return err
		}
		return fmt.Errorf("process group %d survived SIGKILL", pgid)
	}
	s.forget(id)
	return nil
}

func groupAlive(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
func waitGroup(ctx context.Context, pgid int, d time.Duration) bool {
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if !groupAlive(pgid) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return !groupAlive(pgid)
		case <-tick.C:
		}
	}
}
func (s *Supervisor) forget(id ports.ProcessIdentity) {
	s.mu.Lock()
	delete(s.procs, id.Value)
	s.mu.Unlock()
}
func (s *Supervisor) Alive(ctx context.Context, id ports.ProcessIdentity) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r := s.find(id)
	if r == nil {
		pidPart := strings.Split(id.Value, ":")[0]
		pgid, err := strconv.Atoi(pidPart)
		return err == nil && pgid > 1 && groupAliveCall(pgid), nil
	}
	select {
	case <-r.done:
		return false, nil
	default:
	}
	return groupAlive(r.cmd.Process.Pid), nil
}
