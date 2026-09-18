// Package hostterm owns the lifecycle of the terminal wideboi is running
// inside. Its one job is that the terminal is restored exactly once, on
// every exit path.
package hostterm

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// Guard runs a shutdown function at most once, from whichever exit path
// reaches it first: an ordinary return, a panic, or a signal.
type Guard struct {
	once sync.Once
	stop func() error
	err  error
}

// NewGuard returns a Guard that will call stop at most once.
func NewGuard(stop func() error) *Guard {
	return &Guard{stop: stop}
}

// Stop runs the shutdown function if it has not run already. The first
// caller receives the shutdown function's error; later callers get nil.
func (g *Guard) Stop() error {
	var ran bool
	g.once.Do(func() {
		ran = true
		g.err = g.stop()
	})
	if ran {
		return g.err
	}
	return nil
}

// Arm installs a handler for the given signals. On receipt it runs the
// shutdown function, then re-raises the signal with the default handler
// so the parent process observes the conventional 128+signo status.
//
// Go delivers signals on an ordinary goroutine, so this handler may
// allocate, take locks, and run arbitrary code.
func (g *Guard) Arm(sigs ...os.Signal) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, sigs...)

	go func() {
		s := <-ch
		_ = g.Stop()

		signal.Stop(ch)
		signal.Reset(s)
		if sig, ok := s.(syscall.Signal); ok {
			_ = syscall.Kill(os.Getpid(), sig)
		}
	}()
}
