package caddy

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestExternalChildNeverStartsOrTerminates(t *testing.T) {
	c, e := Start(context.Background(), ChildConfig{})
	if e != nil || c.managed || c.cmd != nil {
		t.Fatal(c, e)
	}
	if e := c.Stop(); e != nil {
		t.Fatal(e)
	}
}
func TestManagedChildReadinessAndStop(t *testing.T) {
	c, e := Start(context.Background(), ChildConfig{Managed: true, Binary: "sh", Args: []string{"-c", "sleep 30"}, ReadyTimeout: time.Second, Probe: func(context.Context) error { return nil }})
	if e != nil {
		t.Fatal(e)
	}
	p := c.cmd.Process
	if e := c.Stop(); e != nil {
		t.Fatal(e)
	}
	if e := c.Stop(); e != nil {
		t.Fatal(e)
	}
	if e := p.Signal(os.Signal(os.Interrupt)); e == nil {
		t.Fatal("child remained alive")
	}
}
func TestManagedFailureAndReadinessTimeout(t *testing.T) {
	if _, e := Start(context.Background(), ChildConfig{Managed: true}); e == nil {
		t.Fatal("missing")
	}
	if _, e := Start(context.Background(), ChildConfig{Managed: true, Binary: "sh", Args: []string{"-c", "sleep 30"}, ReadyTimeout: 10 * time.Millisecond, Probe: func(context.Context) error { return os.ErrNotExist }}); e == nil {
		t.Fatal("timeout")
	}
}
