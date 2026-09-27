package main

import (
	"context"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// The ceiling bounds the whole hang-up, the send included. hangUp runs
// inside the signal guard's teardown, before the terminal is restored,
// so a send that could block forever would leave the terminal in the
// alt screen and the process deaf to the signal it caught.
func TestHangUpCeilingCoversTheSend(t *testing.T) {
	ours, theirs := net.Pipe()
	defer ours.Close()
	defer theirs.Close()
	// No pumps: nothing drains ClientSend, so once it is full a send
	// can only block.
	cc := transport.NewClientSocketConn(ours, 1)
	cc.SendClient(context.Background(), protocol.MsgResize{Cols: 1, Rows: 1})

	done := make(chan bool, 1)
	go func() { done <- hangUp(context.Background(), cc, protocol.MsgShutdown{}, 100*time.Millisecond) }()
	select {
	case ok := <-done:
		if ok {
			t.Error("hangUp reported an acknowledgement that never came")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hangUp blocked past its ceiling on a send that could not complete")
	}
}

// Only a hangup -- the terminal went away -- detaches, and only when
// the session is kept on owner loss. Every other signal is a deliberate
// stop, and a plain teardown (no signal) ends an owned session as ever.
func TestOwnerFarewellDetachesOnlyOnAKeptHangup(t *testing.T) {
	cases := []struct {
		sig        os.Signal
		keep       bool
		wantDetach bool
	}{
		{syscall.SIGHUP, true, true},
		{syscall.SIGHUP, false, false},
		{syscall.SIGTERM, true, false},
		{syscall.SIGINT, true, false},
		{syscall.SIGQUIT, true, false},
		{nil, true, false},
	}
	for _, c := range cases {
		msg, ceiling := ownerFarewell(c.sig, c.keep)
		_, detach := msg.(protocol.MsgDetach)
		if detach != c.wantDetach {
			t.Errorf("ownerFarewell(%v, keep=%v) = %T, want detach=%v", c.sig, c.keep, msg, c.wantDetach)
		}
		want := shutdownCeiling
		if c.wantDetach {
			want = detachCeiling
		}
		if ceiling != want {
			t.Errorf("ownerFarewell(%v, keep=%v) ceiling = %v, want %v", c.sig, c.keep, ceiling, want)
		}
	}
}
