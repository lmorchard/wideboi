package main

import (
	"errors"
	"fmt"
	"net"

	"github.com/lmorchard/wideboi/internal/commands"
	"github.com/lmorchard/wideboi/internal/transport"
)

// handshakeServer checks that the server on conn speaks this build's
// protocol, and turns a refusal into something a user can act on.
func handshakeServer(conn net.Conn, socket string) error {
	return commands.HandshakeServer(conn, socket)
}

// describeHandshakeErr phrases a failed handshake with the server at
// socket. hint, if set, replaces the usual advice.
func describeHandshakeErr(socket string, err error, hint string) error {
	return commands.DescribeHandshakeErr(socket, err, hint)
}

// killHint is kill-session's advice: it cannot ask a server of another
// protocol to shut down, and must not kill it on its own authority.
func killHint(socket string, err error) string {
	var mm *transport.MismatchError
	if errors.As(err, &mm) && mm.PID != 0 {
		return fmt.Sprintf("Use a matching build to end it, or `kill %d`.", mm.PID)
	}
	return fmt.Sprintf("Use a matching build to end it, or kill the process holding %s.", socket)
}
