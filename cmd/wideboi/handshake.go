package main

import (
	"errors"
	"fmt"
	"net"

	"github.com/lmorchard/wideboi/internal/transport"
)

// handshakeServer checks that the server on conn speaks this build's
// protocol, and turns a refusal into something a user can act on. Long
// sessions outlive rebuilds, so a stale server on the socket is
// ordinary; before this it surfaced as "protobuf frame too large"
// (#174). conn is closed on failure.
func handshakeServer(conn net.Conn, socket string) error {
	if _, err := transport.Handshake(conn); err != nil {
		conn.Close()
		return describeHandshakeErr(socket, err, "")
	}
	return nil
}

// describeHandshakeErr phrases a failed handshake with the server at
// socket. hint, if set, replaces the usual advice.
func describeHandshakeErr(socket string, err error, hint string) error {
	var mm *transport.MismatchError
	if !errors.As(err, &mm) {
		return fmt.Errorf("handshake with the wideboi server at %s failed: %w", socket, err)
	}
	server := "the wideboi server at " + socket
	if mm.PID != 0 {
		server += fmt.Sprintf(" (pid %d)", mm.PID)
	}
	speaks := fmt.Sprintf("speaks protocol v%d", mm.Theirs)
	if mm.Theirs == 0 {
		speaks = "is from a build older than protocol versioning"
	}
	if hint == "" {
		hint = "Detach from it with a matching build, or start a separate session with -L <name>."
	}
	return &handshakeError{
		msg:   fmt.Sprintf("%s %s; this client speaks v%d. %s", server, speaks, mm.Ours, hint),
		cause: err,
	}
}

// handshakeError keeps the MismatchError reachable for errors.As while
// replacing its peer-neutral wording.
type handshakeError struct {
	msg   string
	cause error
}

func (e *handshakeError) Error() string { return e.msg }
func (e *handshakeError) Unwrap() error { return e.cause }

// killHint is kill-session's advice: it cannot ask a server of another
// protocol to shut down, and must not kill it on its own authority.
func killHint(socket string, err error) string {
	var mm *transport.MismatchError
	if errors.As(err, &mm) && mm.PID != 0 {
		return fmt.Sprintf("Use a matching build to end it, or `kill %d`.", mm.PID)
	}
	return fmt.Sprintf("Use a matching build to end it, or kill the process holding %s.", socket)
}
