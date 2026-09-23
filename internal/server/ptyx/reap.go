package ptyx

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Budgets for the work Kill does after its caller's grace window has
// expired. They are named, and summed into KillResidual, because a
// caller that has to wait out a Kill needs the bound rather than a copy
// of these numbers; see client.CloseResidual.
const (
	// killWait is how long Kill waits for the root to die after the
	// gated SIGKILL.
	killWait = time.Second
	// aliveWait is how long anyAlive polls before concluding a pid is
	// really still there. SIGKILL delivery is not synchronous.
	aliveWait = 500 * time.Millisecond
)

// KillResidual is the worst-case time Kill still needs once its grace
// window has expired: the SIGKILL pass's wait for the root, plus the
// confirmation poll over the descendant snapshot. Kill closes the pty
// master before that residual work begins, so a caller woken by the
// master closing must allow at least this long for Kill to finish.
const KillResidual = killWait + aliveWait

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

// TTYMates returns every process other than pid whose controlling tty is
// pid's own. Empty if pid has none.
//
// This exists because Descendants cannot see a process whose parent
// exited first: it is reparented to init/launchd and drops out of the
// root's tree, but it keeps its controlling tty (#88). For a pane root,
// that tty is the pane's pty. What neither finds is a process that
// called setsid itself, since that also drops its controlling tty.
func TTYMates(pid int) ([]int, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("ptyx: tty mates: invalid pid %d", pid)
	}

	out, err := exec.Command("ps", "-axo", "pid=,tty=").Output()
	if err != nil {
		return nil, err
	}

	ttys := map[int]string{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		p, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ttys[p] = fields[1]
	}

	// ps prints "??" on macOS and "?" on Linux for no controlling tty.
	own := ttys[pid]
	if own == "" || strings.Trim(own, "?") == "" {
		return nil, nil
	}
	var mates []int
	for p, tty := range ttys {
		if p != pid && tty == own {
			mates = append(mates, p)
		}
	}
	return mates, nil
}

// Kill stops the pane's entire process tree. It snapshots the tree's
// descendants once, while the root is still alive, then sends SIGTERM by
// the three routes below and waits up to grace. The snapshot also holds
// the root's TTYMates: escapees whose parent exited, which the tree no
// longer reaches. The master is still open at that point, so the pty is
// still this pane's alone and every process holding it is ours.
//
//  1. kill on each snapshotted descendant, deepest first, then each tty mate
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
// Kill has no mutual exclusion and must not be called concurrently for
// the same Pane; the done check, the descendant snapshot, and the
// escalation gate would all interleave. cmd/wideboi's closePanes runs
// one Kill per pane in parallel, which is fine — those are distinct
// Panes — and nothing calls Kill twice for one pane at the same time.
// Making it safe to is Plan 2's problem, along with pane lifecycle
// generally.
//
// Known, deliberately parked leak path: the p.done short-circuit above
// means that if a pane's root exits on its own before Kill is ever
// called, Kill signals nothing and reports success, so anything that
// escaped the pane's process group is never reaped. Closing that needs
// a descendant snapshot maintained while the root is alive, which this
// design does not keep. Recorded in the v1 spec under Teardown.
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
	mates, _ := TTYMates(p.Cmd.Process.Pid)
	descendants = appendNew(descendants, mates)

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
		rootExited = p.waitForExit(killWait)
	}

	if !rootExited || anyAlive(descendants, aliveWait) {
		return fmt.Errorf("ptyx: kill: process tree for pid %d survived SIGKILL", p.Cmd.Process.Pid)
	}
	return nil
}

// appendNew appends each pid in more that snapshot does not already hold.
// The tty mates go after the descendants: they were reparented away from
// the tree, so deepest-first ordering has nothing to say about them.
func appendNew(snapshot, more []int) []int {
	have := make(map[int]bool, len(snapshot))
	for _, pid := range snapshot {
		have[pid] = true
	}
	for _, pid := range more {
		if !have[pid] {
			snapshot = append(snapshot, pid)
		}
	}
	return snapshot
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
