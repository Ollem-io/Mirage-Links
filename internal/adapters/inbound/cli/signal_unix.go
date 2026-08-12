//go:build unix

package cli

import (
	"os"
	"os/signal"
	"syscall"
)

func signalNotify(ch chan os.Signal) { signal.Notify(ch, os.Interrupt, syscall.SIGTERM) }
