package server

import (
	"context"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/protocol"
	
)

// A click names the pane it landed on, so the server must be able to
// focus by ID rather than only step left and right.

// A stale click -- the pane closed between the draw and the press --
// must not move focus anywhere.

// A forwarded event reaches the named pane's queue, decoded back into
// the concrete uv type vt's encoder switches on. The drain into the
// grid runs on Start's key-writer goroutine, which needs a real pty;
// smoke.py covers that end of the chain.
func TestMouseMessageQueuesForNamedPane(t *testing.T) {
	s, _ := serverWithStatuses(t, map[int]protocol.PaneStatus{1: protocol.StatusIdle, 2: protocol.StatusIdle})
	p := s.panes[2]
	p.input = make(chan uv.Event, 1)

	s.handleClientMsg(context.Background(), protocol.MsgMouse{
		PaneID: 2, Kind: protocol.MouseRelease, X: 4, Y: 5, Button: int(uv.MouseLeft),
	})

	select {
	case ev := <-p.input:
		rel, ok := ev.(uv.MouseReleaseEvent)
		if !ok {
			t.Fatalf("queued %T, want uv.MouseReleaseEvent", ev)
		}
		if rel.X != 4 || rel.Y != 5 {
			t.Errorf("queued at (%d,%d), want (4,5)", rel.X, rel.Y)
		}
	default:
		t.Fatal("nothing queued for pane 2")
	}
}
