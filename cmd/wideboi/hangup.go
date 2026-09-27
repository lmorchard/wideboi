package main

import (
	"context"
	"os"
	"syscall"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server"
	"github.com/lmorchard/wideboi/internal/transport"
)

// shutdownCeiling bounds the wait for a server to hang up its panes and
// then its clients: the pane grace and the margin.
const shutdownCeiling = server.CloseGrace + signalExitMargin

// detachCeiling bounds the wait for the server to acknowledge a detach,
// which involves no reaping.
const detachCeiling = 2 * time.Second

// ownerFarewell is what an owning client tells its server when sig ends
// it, and how long to wait for the acknowledgement. A hangup means the
// terminal went away -- a dropped ssh connection, a closed window -- and
// with keep set that leaves the session running, detached. Every other
// signal is a deliberate stop and ends it, as does a teardown with no
// signal at all.
func ownerFarewell(sig os.Signal, keep bool) (msg transport.ClientMessage, ceiling time.Duration) {
	if keep && sig == syscall.SIGHUP {
		return protocol.MsgDetach{}, detachCeiling
	}
	return protocol.MsgShutdown{}, shutdownCeiling
}

// hangUp sends msg -- MsgDetach or MsgShutdown -- and waits for the
// server to close the connection, which is its acknowledgement. It
// reports whether that happened within ceiling. A connection that ended
// in a protocol failure is not an acknowledgement, even though it
// closed.
//
// The ceiling covers the send as well as the wait. This runs inside
// the signal guard's teardown, before the terminal is restored, so a
// send stuck behind a stalled write pump must not be able to hold it.
//
// Whatever arrives in the meantime is drained and dropped: nothing is
// drawn after a hang-up, and an undrained channel would stall the
// server's write pump behind us.
func hangUp(ctx context.Context, conn *transport.ClientSocketConn, msg transport.ClientMessage, ceiling time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, ceiling)
	defer cancel()
	if !conn.SendClient(ctx, msg) {
		return false
	}
	for {
		select {
		case _, ok := <-conn.ServerSendChan():
			if !ok {
				return conn.Err() == nil
			}
		case <-ctx.Done():
			return false
		}
	}
}
