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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// Pane is one PTY and the process tree rooted at its child.
type Pane struct {
	Master *os.File
	Cmd    *exec.Cmd

	// writeMu serializes calls to WriteBounded so concurrent callers cannot
	// clobber each other's deadlines or interleave writes to Master.
	writeMu sync.Mutex

	// done closes when the child has been reaped. Exactly one goroutine
	// ever calls Wait, because a second call fails.
	done chan struct{}

	// exitCode and exitDesc are written by the reaper before it closes
	// done; the close publishes them. See ExitCode and ExitDescription.
	exitCode int
	exitDesc string
}

// Done returns a channel closed when the pane's child process exits.
func (p *Pane) Done() <-chan struct{} { return p.done }

// ExitCode reports the child's exit status once it has been reaped: its
// exit code, or 128+signal if a signal killed it, as a shell reports it.
// reaped is false while the child is still running.
func (p *Pane) ExitCode() (code int, reaped bool) {
	select {
	case <-p.done:
		return p.exitCode, true
	default:
		return 0, false
	}
}

// ExitDescription reports the reaped status in words, as
// os.ProcessState puts it ("exit status 1", "signal: killed"), for logs.
// reaped is false while the child is still running.
func (p *Pane) ExitDescription() (desc string, reaped bool) {
	select {
	case <-p.done:
		return p.exitDesc, true
	default:
		return "", false
	}
}

// PID returns the root process PID of the spawned child.
func (p *Pane) PID() int {
	if p.Cmd != nil && p.Cmd.Process != nil {
		return p.Cmd.Process.Pid
	}
	return 0
}

// Adopt takes ownership of an existing child process and its PTY master fd.
func Adopt(pid int, fd int, name string, alreadyExited bool, exitCode int) (*Pane, error) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("ptyx: find process %d: %w", pid, err)
	}

	// Restore CloseOnExec so future child spawns don't leak this PTY fd
	syscall.CloseOnExec(fd)

	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("ptyx: set nonblock: %w", err)
	}
	master := os.NewFile(uintptr(fd), name)

	p := &Pane{
		Master: master,
		Cmd:    &exec.Cmd{Process: proc},
		done:   make(chan struct{}),
	}

	if alreadyExited {
		p.exitCode = exitCode
		p.exitDesc = fmt.Sprintf("exit code %d (before upgrade)", exitCode)
		close(p.done)
		return p, nil
	}

	go func() {
		state, err := proc.Wait()
		if err == nil {
			p.exitCode = exitStatus(state)
		} else {
			p.exitCode = -1
		}
		p.exitDesc = describeState(state, err)
		close(p.done)
	}()

	return p, nil
}

func cleanEnv(base []string, removeKeys ...string) []string {
	var out []string
	for _, entry := range base {
		drop := false
		for _, key := range removeKeys {
			if strings.HasPrefix(entry, key+"=") {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, entry)
		}
	}
	return out
}

// Spawn starts argv on a new PTY sized cols x rows, with dir as its
// working directory. Any extraEnv entries are appended to the child
// environment, with any inherited WIDEBOI_SOCK and WIDEBOI_SESSION stripped first.
func Spawn(argv []string, cols, rows int, dir string, extraEnv ...string) (*Pane, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("ptyx: empty argv")
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	baseEnv := cleanEnv(os.Environ(), "WIDEBOI_SOCK", "WIDEBOI_SESSION")
	cmd.Env = append(baseEnv, "TERM=xterm-256color")
	cmd.Env = append(cmd.Env, extraEnv...)
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

	p := &Pane{
		Master: master,
		Cmd:    cmd,
		done:   make(chan struct{}),
	}

	// Reap in exactly one place. Calling Wait twice returns an error, so
	// Kill must observe exit through this channel rather than waiting
	// again itself.
	go func() {
		err := cmd.Wait()
		p.exitCode = exitStatus(cmd.ProcessState)
		p.exitDesc = describeState(cmd.ProcessState, err)
		close(p.done)
	}()

	return p, nil
}

// describeState is a reaped status as os.ProcessState words it, for
// logs. A non-zero exit is an error from Wait but still has a state, so
// the state wins when there is one.
func describeState(ps *os.ProcessState, err error) string {
	if ps != nil {
		return ps.String()
	}
	if err != nil {
		return "wait failed: " + err.Error()
	}
	return "unknown"
}

// exitStatus renders a reaped child's status the way a shell's $? does.
func exitStatus(ps *os.ProcessState) int {
	if ps == nil {
		return -1
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
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
//
// Writes are serialized under writeMu so concurrent callers cannot clobber
// each other's deadlines or interleave bytes on Master.
func (p *Pane) WriteBounded(b []byte, timeout time.Duration) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if timeout > 0 {
		_ = p.Master.SetWriteDeadline(time.Now().Add(timeout))
		defer func() { _ = p.Master.SetWriteDeadline(time.Time{}) }()
	}
	return p.Master.Write(b)
}

// Close releases the PTY master. Hangup is the teardown path.
func (p *Pane) Close() error {
	return p.Master.Close()
}
