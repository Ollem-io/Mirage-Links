package caddy

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"
)

// ChildConfig controls connection to an existing Caddy or a child Mirage
// launches. External mode never starts or terminates a process.
type ChildConfig struct {
	Managed      bool
	Binary       string
	Args         []string
	ReadyTimeout time.Duration
	Probe        func(context.Context) error
}

// Child owns exactly the command started by Start. It cannot terminate an
// externally managed Caddy instance.
type Child struct {
	managed bool
	cmd     *exec.Cmd
	done    chan error
	once    sync.Once
}

func Start(ctx context.Context, cfg ChildConfig) (*Child, error) {
	c := &Child{managed: cfg.Managed}
	if !cfg.Managed {
		return c, nil
	}
	if cfg.Binary == "" {
		return nil, errors.New("caddy binary is required in managed mode")
	}
	cmd := exec.Command(cfg.Binary, cfg.Args...)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c.cmd = cmd
	c.done = make(chan error, 1)
	go func() { c.done <- cmd.Wait() }()
	if cfg.Probe == nil {
		return c, nil
	}
	timeout := cfg.ReadyTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ready, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := cfg.Probe(ready); err == nil {
			return c, nil
		}
		select {
		case <-ready.Done():
			_ = c.Stop()
			return nil, ready.Err()
		case <-tick.C:
		}
	}
}

// Stop terminates only a managed child and is safe to call repeatedly.
func (c *Child) Stop() error {
	if c == nil || !c.managed || c.cmd == nil {
		return nil
	}
	var err error
	c.once.Do(func() {
		if c.cmd.Process != nil {
			err = c.cmd.Process.Kill()
		}
		if c.done != nil {
			<-c.done
		}
	})
	return err
}
