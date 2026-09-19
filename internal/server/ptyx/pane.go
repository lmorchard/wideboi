// Package ptyx spawns and tears down PTY-backed child processes.
//
// A pane's child is given its own session and process group. That is what
// makes signal delivery and teardown addressable per pane rather than
// per multiplexer.
package ptyx

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// Pane is one PTY and the process tree rooted at its child.
type Pane struct {
	Master *os.File
	Cmd    *exec.Cmd
	PGID   int

	// done closes when the child has been reaped. Exactly one goroutine
	// ever calls Wait, because a second call fails.
	done chan struct{}
}

// Done returns a channel closed when the pane's child process exits.
func (p *Pane) Done() <-chan struct{} { return p.done }

// PID returns the root process PID of the spawned child.
func (p *Pane) PID() int {
	if p.Cmd != nil && p.Cmd.Process != nil {
		return p.Cmd.Process.Pid
	}
	return 0
}

// Spawn starts argv on a new PTY sized cols x rows, with dir as its
// working directory.
func Spawn(argv []string, cols, rows int, dir string) (*Pane, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("ptyx: empty argv")
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
	}

	master, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
	if err != nil {
		return nil, fmt.Errorf("ptyx: start %s: %w", argv[0], err)
	}

	// Setsid makes the child a session and group leader, so its pgid is
	// its own pid.
	p := &Pane{
		Master: master,
		Cmd:    cmd,
		PGID:   cmd.Process.Pid,
		done:   make(chan struct{}),
	}

	// Reap in exactly one place. Calling Wait twice returns an error, so
	// Kill must observe exit through this channel rather than waiting
	// again itself.
	go func() {
		_ = cmd.Wait()
		close(p.done)
	}()

	return p, nil
}

// Resize reports a new logical size to the child.
func (p *Pane) Resize(cols, rows int) error {
	return pty.Setsize(p.Master, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
}

// Close releases the PTY master. It does not stop the child; see Kill.
func (p *Pane) Close() error {
	return p.Master.Close()
}
