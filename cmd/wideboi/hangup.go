package main

import (
	"context"
	"time"

	"github.com/lmorchard/wideboi/internal/server"
	"github.com/lmorchard/wideboi/internal/transport"
)

// shutdownCeiling bounds the wait for a server to reap its panes and
// hang up: the pane grace, the kill residual, and the margin -- the
// same budget the in-process binary used to sleep through.
const shutdownCeiling = server.CloseGrace + server.CloseResidual + signalExitMargin

// detachCeiling bounds the wait for the server to acknowledge a detach,
// which involves no reaping.
const detachCeiling = 2 * time.Second

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
