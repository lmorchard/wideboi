package server

import (
	"context"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// Every way a session ends names itself: a server that closed without
// saying why is the gap that made an abrupt exit undiagnosable.
func TestShutdownRecordsReason(t *testing.T) {
	asker := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	s := newBareServer(asker)

	asker.ClientSend <- protocol.MsgShutdown{}
	runLoopUntilReturn(t, s, asker)

	if reason, _ := s.CloseReason(); reason != ReasonShutdownRequest {
		t.Errorf("CloseReason = %q, want %q", reason, ReasonShutdownRequest)
	}
}

func TestOwnerEOFRecordsReason(t *testing.T) {
	owner := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	s := newBareServer(owner)
	s.SetOwner(owner)

	close(owner.ClientSend)
	runLoopUntilReturn(t, s, owner)

	if reason, _ := s.CloseReason(); reason != ReasonOwnerLeft {
		t.Errorf("CloseReason = %q, want %q", reason, ReasonOwnerLeft)
	}
}

func TestDirectCloseIsUnspecified(t *testing.T) {
	s := newBareServer()
	_ = s.Close()
	if reason, _ := s.CloseReason(); reason != ReasonUnspecified {
		t.Errorf("CloseReason = %q, want %q", reason, ReasonUnspecified)
	}
}

// Close is idempotent and later triggers are echoes of the first, so
// the first reason is the one that explains the exit.
func TestFirstCloseReasonWins(t *testing.T) {
	s := newBareServer()
	_ = s.CloseFor(ReasonSignal, "signal", "terminated")
	_ = s.CloseFor(ReasonOwnerLeft)
	reason, attrs := s.CloseReason()
	if reason != ReasonSignal {
		t.Errorf("CloseReason = %q, want %q", reason, ReasonSignal)
	}
	if len(attrs) != 2 || attrs[1] != "terminated" {
		t.Errorf("attrs = %v, want [signal terminated]", attrs)
	}
}

// A session that ends because its last pane exited says which pane, and
// how its process ended.
func TestLastPaneExitRecordsStatus(t *testing.T) {
	s, tp := newControlFixture(t)
	ctx := context.Background()
	id := splitPane(t, ctx, s, tp, protocol.MsgSplitRequest{Command: "exit 7"})

	select {
	case <-s.stopCh:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not close after its last pane exited")
	}
	waitFor(t, 2*time.Second, "a close reason", func() bool {
		r, _ := s.CloseReason()
		return r != ReasonUnspecified
	})
	reason, attrs := s.CloseReason()
	if reason != ReasonLastPaneExited {
		t.Fatalf("CloseReason = %q, want %q", reason, ReasonLastPaneExited)
	}
	got := map[any]any{}
	for i := 0; i+1 < len(attrs); i += 2 {
		got[attrs[i]] = attrs[i+1]
	}
	if got["paneID"] != id || got["status"] != "exit status 7" {
		t.Errorf("attrs = %v, want paneID=%d status=\"exit status 7\"", attrs, id)
	}
}
