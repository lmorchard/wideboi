package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/lmorchard/wideboi/internal/logger"
)

// spawnServer starts `wideboi server` in the background as the session
// this process will own, and returns this side of the private
// connection to it.
//
// The connection is a socketpair inherited as fd 3, not a dial of the
// listening socket: nothing can race the spawner to it, nothing can
// forge it, and it works before the server has bound anything.
//
// Both ends are made close-on-exec under ForkLock before anything else
// can fork -- darwin has no SOCK_CLOEXEC. If the server inherited *our*
// end too, it would hold its own owner connection open, and the EOF
// that tells it its owner died would never come. ExtraFiles clears the
// flag on fd 3 in the child, which is the one copy it should have.
//
// exited delivers the server's exit code once it has been reaped (-1 if
// a signal killed it). An owner whose server quits before saying
// anything reads it to tell "another server already had the session"
// from a real failure.
func spawnServer(socket string, args []string) (conn net.Conn, exited <-chan int, err error) {
	syscall.ForkLock.RLock()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(fds[0])
		syscall.CloseOnExec(fds[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, nil, fmt.Errorf("socketpair: %w", err)
	}
	ours := os.NewFile(uintptr(fds[0]), "wideboi-owner")
	theirs := os.NewFile(uintptr(fds[1]), "wideboi-owner-server")
	defer ours.Close()   // FileConn dups it
	defer theirs.Close() // the child has its own copy once started

	exe, err := os.Executable()
	if err != nil {
		return nil, nil, fmt.Errorf("locating the wideboi binary: %w", err)
	}
	cmd := exec.Command(exe, serverArgs(args)...)
	cmd.ExtraFiles = []*os.File{theirs}
	// Its own session: the host terminal's SIGHUP and ^C belong to the
	// client, which decides what they mean for the session.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// Direct child stderr to the server log so startup panics and fatal errors
	// are captured rather than dropped into /dev/null.
	if socket != "" {
		logPath := logger.Path(socket, "server")
		_ = os.MkdirAll(filepath.Dir(logPath), 0700)
		if logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600); err == nil {
			defer logFile.Close()
			cmd.Stderr = logFile
		}
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("starting wideboi server: %w", err)
	}
	// Reap it if it exits while we are alive; if we die first, init does.
	code := make(chan int, 1)
	go func() {
		_ = cmd.Wait()
		c := -1
		if cmd.ProcessState != nil {
			c = cmd.ProcessState.ExitCode()
		}
		code <- c
	}()

	conn, err = net.FileConn(ours)
	if err != nil {
		return nil, nil, fmt.Errorf("owner connection: %w", err)
	}
	return conn, code, nil
}

// serverArgs is the argument list for the server spawnServer starts:
// the user's own flags, so it resolves exactly the config this process
// did (environment and cwd are inherited), with our --owner-fd first.
//
// First, because the flag parser stops at the first non-flag argument,
// so anything stray before it would hide it. And any --owner-fd the user
// passed is dropped, because the parser keeps the last value it sees.
func serverArgs(user []string) []string {
	out := []string{"server", "--owner-fd", "3"}
	for i := 0; i < len(user); i++ {
		switch a := user[i]; {
		case a == "-owner-fd" || a == "--owner-fd":
			i++ // and its value
		case strings.HasPrefix(a, "-owner-fd=") || strings.HasPrefix(a, "--owner-fd="):
		default:
			out = append(out, a)
		}
	}
	return out
}
