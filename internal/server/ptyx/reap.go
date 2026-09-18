package ptyx

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Descendants returns every process descended from pid, deepest first.
//
// This exists because neither a process-group kill nor a kill of the root
// pid reaches a process that deliberately left the group, via nohup,
// setsid, or an interactive shell's job control.
func Descendants(pid int) ([]int, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("ptyx: descendants: invalid pid %d", pid)
	}

	out, err := exec.Command("ps", "-axo", "pid=,ppid=").Output()
	if err != nil {
		return nil, err
	}

	children := map[int][]int{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		p, err1 := strconv.Atoi(fields[0])
		pp, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		children[pp] = append(children[pp], p)
	}

	// seen guards against a self- or cross-referencing ppid row (e.g. pid
	// 1's ppid is 0 on Linux) sending walk into infinite recursion.
	seen := map[int]bool{pid: true}
	var walk func(int) []int
	walk = func(root int) []int {
		var out []int
		for _, c := range children[root] {
			if seen[c] {
				continue
			}
			seen[c] = true
			out = append(out, walk(c)...)
			out = append(out, c)
		}
		return out
	}
	return walk(pid), nil // deepest first
}

// Kill stops the pane's entire process tree. It snapshots the tree's
// descendants once, while the root is still alive, then sends SIGTERM by
// the three routes below and waits up to grace.
//
//  1. kill on each snapshotted descendant, deepest first
//  2. killpg on the pane's group  — the shell and its foreground job
//  3. kill on the root pid        — the shell itself
//
// Escalation to SIGKILL then splits by target:
//
//   - The snapshot is signalled unconditionally. A root that exits
//     promptly says nothing about a descendant that ignored SIGTERM by
//     trapping it away or installing its own handler, and the snapshot,
//     taken before anything was signalled, is the only way to still
//     reach such a descendant once the root is gone, because a dead
//     root's escaped children reparent to init/launchd and a fresh ps
//     walk from the root pid can no longer find them.
//   - The group and root pid are signalled only if the root is confirmed
//     still alive after grace. Once the root has been reaped, its pid —
//     and, since Setsid made them equal, its pgid — may already have
//     been recycled by the OS; signalling a recycled pid's group is
//     exactly the blast radius an idempotent Kill must not have.
//
// SIGKILL against an already-dead snapshot pid is a harmless ESRCH.
//
// Kill is idempotent: once the tree has already been reaped, a later call
// closes the master and returns immediately without signalling anything.
//
// The master is closed before any SIGKILL is sent, not after: on macOS a
// PTY session leader that is SIGKILLed while another process still holds
// the master open can wedge indefinitely in kernel exit teardown (visible
// as "E" state in ps) and never reach Wait. This is conditional on
// nothing else draining the master concurrently — true in this package's
// own tests, but not necessarily true of the real multiplexer, where a
// reader goroutine is expected to pump the pty continuously; closing
// first is a no-op either way if the root already exited on its own
// during grace.
//
// Kill returns an error if the root or any snapshotted descendant is
// still alive after the SIGKILL pass.
func (p *Pane) Kill(grace time.Duration) error {
	select {
	case <-p.done:
		_ = p.Master.Close()
		return nil
	default:
	}

	descendants, _ := Descendants(p.Cmd.Process.Pid)

	p.signalDescendants(descendants, syscall.SIGTERM)
	p.signalRoot(syscall.SIGTERM)

	rootExited := p.waitForExit(grace)

	_ = p.Master.Close()

	// Unconditional: escapees live in the snapshot, and this is what
	// preserves the anti-leak guarantee even when the root exits
	// promptly on its own.
	p.signalDescendants(descendants, syscall.SIGKILL)

	if !rootExited {
		// Gated: the root is confirmed still alive, so its pid and pgid
		// are still its own. Signalling them after the root is already
		// gone would risk hitting whatever the OS has since done with
		// that recycled pid.
		p.signalRoot(syscall.SIGKILL)
		rootExited = p.waitForExit(time.Second)
	}

	if !rootExited || anyAlive(descendants, 500*time.Millisecond) {
		return fmt.Errorf("ptyx: kill: process tree for pid %d survived SIGKILL", p.Cmd.Process.Pid)
	}
	return nil
}

// signalDescendants signals each pid in a Descendants snapshot.
func (p *Pane) signalDescendants(descendants []int, sig syscall.Signal) {
	// Deepest-first, so a parent cannot respawn a child we already killed.
	for _, pid := range descendants {
		_ = syscall.Kill(pid, sig)
	}
}

// signalRoot signals the pane's own process group and root pid. Kill
// calls this only when the root has not been confirmed exited, to avoid
// signalling a pid — and, via PGID, a process group — that the OS may
// have already recycled. See Kill's gating rationale.
func (p *Pane) signalRoot(sig syscall.Signal) {
	if p.PGID > 0 {
		_ = syscall.Kill(-p.PGID, sig)
	}
	_ = syscall.Kill(p.Cmd.Process.Pid, sig)
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

// anyAlive reports whether any pid is still alive after polling for up to
// within. It exists because SIGKILL delivery is not synchronous — a
// process can take a moment to actually leave the process table.
func anyAlive(pids []int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		alive := false
		for _, pid := range pids {
			if processAlive(pid) {
				alive = true
				break
			}
		}
		if !alive {
			return false
		}
		if time.Now().After(deadline) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// processAlive reports whether pid still exists, probed via signal 0.
// EPERM means the process exists but is owned by someone else; treat
// that as alive, since we cannot confirm it is gone.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, syscall.Signal(0))
	if err == nil {
		return true
	}
	return err != syscall.ESRCH
}
