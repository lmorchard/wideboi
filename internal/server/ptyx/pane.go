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
	"time"

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

	rawMaster, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
	if err != nil {
		return nil, fmt.Errorf("ptyx: start %s: %w", argv[0], err)
	}

	// creack/pty opens the master PTY in blocking mode, which causes Go's
	// os.File to initialize without netpoller support (SetDeadline returns
	// "file type does not support deadline"). To enable bounded writes,
	// dup the descriptor, set it non-blocking, and wrap it in a fresh
	// os.File so Go registers it with the runtime poller. Closing rawMaster
	// immediately avoids fd aliasing and GC finalizer races.
	newFd, err := syscall.Dup(int(rawMaster.Fd()))
	if err != nil {
		_ = rawMaster.Close()
		return nil, fmt.Errorf("ptyx: dup master: %w", err)
	}
	syscall.CloseOnExec(newFd)
	_ = rawMaster.Close()

	if err := syscall.SetNonblock(newFd, true); err != nil {
		_ = syscall.Close(newFd)
		return nil, fmt.Errorf("ptyx: set nonblock: %w", err)
	}
	master := os.NewFile(uintptr(newFd), rawMaster.Name())

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
//
// Goes through setsize rather than pty.Setsize directly; see setsize's
// doc comment in ioctl.go for why.
func (p *Pane) Resize(cols, rows int) error {
	return setsize(p.Master, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
}

// WriteBounded writes b to the child's PTY master with an optional timeout.
// If timeout > 0, it arms a write deadline on the poller-registered master
// so that a child that has stopped reading stdin cannot block the write
// indefinitely. When the deadline expires, it returns an error matching
// os.ErrDeadlineExceeded.
func (p *Pane) WriteBounded(b []byte, timeout time.Duration) (int, error) {
	if timeout > 0 {
		_ = p.Master.SetWriteDeadline(time.Now().Add(timeout))
		defer func() { _ = p.Master.SetWriteDeadline(time.Time{}) }()
	}
	return p.Master.Write(b)
}

// Close releases the PTY master. It does not stop the child; see Kill.
func (p *Pane) Close() error {
	return p.Master.Close()
}
