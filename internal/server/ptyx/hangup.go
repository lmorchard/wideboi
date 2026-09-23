package ptyx

import "time"

// Hangup ends the pane the way closing a terminal window does, and the
// way tmux does. It closes the pty master, so the kernel hangs up the
// terminal and sends SIGHUP to the shell and its foreground job. It
// then waits up to grace for the shell to exit.
//
// It signals nothing itself and walks no process table. A process that
// opted out of the hangup (nohup, disown, trap "" HUP, setsid) keeps
// running by design, as it would under tmux or after closing a real
// terminal. An interactive shell exits even if it ignores SIGHUP,
// because its next read from the hung-up terminal fails.
//
// Hangup is idempotent. A second call finds the master already closed
// and the root already reaped, and returns at once.
func (p *Pane) Hangup(grace time.Duration) {
	_ = p.Master.Close()
	p.waitForExit(grace)
}

// waitForExit observes the single reaper goroutine started in Spawn. It
// never calls Wait itself, so it is safe to call repeatedly.
func (p *Pane) waitForExit(within time.Duration) bool {
	select {
	case <-p.done:
		return true
	case <-time.After(within):
		return false
	}
}
