package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
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
func spawnServer(args []string) (net.Conn, error) {
	syscall.ForkLock.RLock()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(fds[0])
		syscall.CloseOnExec(fds[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("socketpair: %w", err)
	}
	ours := os.NewFile(uintptr(fds[0]), "wideboi-owner")
	theirs := os.NewFile(uintptr(fds[1]), "wideboi-owner-server")
	defer ours.Close()   // FileConn dups it
	defer theirs.Close() // the child has its own copy once started

	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locating the wideboi binary: %w", err)
	}
	cmd := exec.Command(exe, serverArgs(args)...)
	cmd.ExtraFiles = []*os.File{theirs}
	// Its own session: the host terminal's SIGHUP and ^C belong to the
	// client, which decides what they mean for the session.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// Stdin, Stdout and Stderr are left nil, which is /dev/null. The
	// server logs to its file.
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting wideboi server: %w", err)
	}
	// Reap it if it exits while we are alive; if we die first, init does.
	go func() { _ = cmd.Wait() }()

	conn, err := net.FileConn(ours)
	if err != nil {
		return nil, fmt.Errorf("owner connection: %w", err)
	}
	return conn, nil
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
