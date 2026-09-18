package ptyx

import (
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

	var walk func(int) []int
	walk = func(root int) []int {
		var out []int
		for _, c := range children[root] {
			out = append(out, walk(c)...)
			out = append(out, c)
		}
		return out
	}
	return walk(pid), nil // deepest first
}

// Kill stops the pane's entire process tree. It sends SIGTERM by the
// three routes below, waits up to grace for the root to exit, then
// escalates to SIGKILL on anything left.
//
//  1. killpg on the pane's group  — the shell and its foreground job
//  2. kill on the root pid        — the shell itself
//  3. a ps tree walk, deepest first — anything that left the group
//
// Kill is safe to call more than once.
func (p *Pane) Kill(grace time.Duration) error {
	p.signalTree(syscall.SIGTERM)

	if p.waitForExit(grace) {
		_ = p.Master.Close()
		return nil
	}

	p.signalTree(syscall.SIGKILL)
	p.waitForExit(time.Second)
	_ = p.Master.Close()
	return nil
}

func (p *Pane) signalTree(sig syscall.Signal) {
	// Deepest-first, so a parent cannot respawn a child we already killed.
	if kids, err := Descendants(p.Cmd.Process.Pid); err == nil {
		for _, pid := range kids {
			_ = syscall.Kill(pid, sig)
		}
	}
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
