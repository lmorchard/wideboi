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
//
// The re-raise can be discarded, and then the process has to exit by
// itself. POSIX requires a shell to set SIGINT and SIGQUIT to SIG_IGN
// for an asynchronous list, so anything started as `cmd &` inherits
// them ignored -- and Go's runtime deliberately respects an inherited
// SIG_IGN for SIGHUP and SIGINT (sigInstallGoHandler), so signal.Reset
// restores SIG_IGN rather than SIG_DFL. signal.Notify still catches the
// signal, so shutdown runs correctly; the re-raise then goes nowhere.
// Before this was handled, `wideboi &` followed by SIGINT tore the
// session down, restored the terminal, and then lived forever, immune
// to every signal it had armed.
func (g *Guard) Arm(sigs ...os.Signal) {
	// Sampled before Notify, which installs a handler and would make
	// every signal report as not-ignored from here on.
	inherited := make(map[os.Signal]bool, len(sigs))
	for _, s := range sigs {
		inherited[s] = signal.Ignored(s)
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, sigs...)

	go func() {
		s := <-ch
		_ = g.Stop()

		signal.Stop(ch)
		signal.Reset(s)

		sig, ok := s.(syscall.Signal)
		if !ok {
			return
		}
		if inherited[s] {
			// Reset put the inherited SIG_IGN back, so a re-raise would
			// be discarded and this process would never exit. Exit with
			// the status a shell would have reported anyway.
			//
			// Decided here rather than after a failed re-raise: Kill can
			// return before the signal is delivered, so "did Kill
			// return?" is a race, and it loses often enough to break
			// TestGuardArmRestoresAndReRaises.
			os.Exit(128 + int(sig))
		}
		_ = syscall.Kill(os.Getpid(), sig)
	}()
}
