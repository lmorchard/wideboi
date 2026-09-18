// Package hostterm owns the lifecycle of the terminal wideboi is running
// inside. Its one job is that the terminal is restored exactly once, on
// every exit path.
package hostterm

import (
	"sync"
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
