//go:build unix

package cli

import (
	"os"
	"os/signal"
	"syscall"
)

func signalNotify(ch chan os.Signal) { signal.Notify(ch, os.Interrupt, syscall.SIGTERM) }
func signalStop(ch chan os.Signal)   { signal.Stop(ch) }

func waitForSignal() { ch := make(chan os.Signal, 1); signalNotify(ch); defer signalStop(ch); <-ch }
