package client

import (
	"context"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// focusTo moves focus by replaying a layout snapshot, which is what the
// server sends and what arms a wipe.
func focusTo(cli *Client, paneID int) {
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns:     twoColumns(),
		FocusPaneID: paneID,
	})
}

// A focus change used to blank the screen for eight frames: Draw built
// its two wipe frames with compose.NewSurface and never drew into
// either, so WipeTransition interpolated blank against blank. Measured
// on a real pty, that was one erase followed by ~128ms of silence.
//
// wipe_test.go missed it because it writes "AAA..."/"BBB..." into its
// own frames before constructing the transition. The only production
// call site did not, so the mechanism was proven and the wiring was
// not. Assert against Draw itself.
func TestFocusChangeWipeNeverShowsABlankFrame(t *testing.T) {
	const cols, rows = 60, 12
	cli := newTestClientWithTwoPanes(t, cols, rows)

	focusTo(cli, 2)

	for step := 0; step <= wipeSteps; step++ {
		scr := newFakeHostScreen(cols, rows)
		cli.Draw(scr, nil, nil)
		if blankAbove(scr, rows-1) {
			t.Fatalf("frame %d of the transition is blank above the status bar", step)
		}
	}
}

// The transition has to land on the same screen an un-animated client
// would have drawn, or it is a wipe to the wrong place.
func TestFocusChangeWipeEndsOnTheSteadyStateFrame(t *testing.T) {
	const cols, rows = 60, 12

	// One client animates through the transition.
	animated := newTestClientWithTwoPanes(t, cols, rows)
	focusTo(animated, 2)
	var last *fakeHostScreen
	for step := 0; step <= wipeSteps; step++ {
		last = newFakeHostScreen(cols, rows)
		animated.Draw(last, nil, nil)
	}

	// Another reaches the same state without ever arming a wipe,
	// because it was created already focused on pane 2.
	steady := NewClient(transport.NewInProcChannel(16), cols, rows, "C-b")
	steady.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns:     twoColumns(),
		FocusPaneID: 2,
	})
	steady.HandleServerMsg(paneUpdate(1, 25, 10, "PANE-ONE"))
	steady.HandleServerMsg(paneUpdate(2, 25, 10, "PANE-TWO"))
	want := newFakeHostScreen(cols, rows)
	steady.Draw(want, nil, nil)

	got := strings.Join(last.textAbove(rows-1), "\n")
	expected := strings.Join(want.textAbove(rows-1), "\n")
	if got != expected {
		t.Errorf("transition did not settle on the steady-state frame\n got:\n%s\nwant:\n%s", got, expected)
	}
}

// A resize between the focus change and the next draw invalidates the
// retained geometry: compositing the old placements at the new size
// would animate a layout that never existed. Snap instead.
func TestResizeDuringPendingWipeSnapsInsteadOfAnimating(t *testing.T) {
	const cols, rows = 60, 12
	cli := newTestClientWithTwoPanes(t, cols, rows)

	focusTo(cli, 2)
	cli.SendResize(context.Background(), cols+20, rows)

	scr := newFakeHostScreen(cols+20, rows)
	cli.Draw(scr, nil, nil)

	if got := cli.layerLocked(); got != layerPanes {
		t.Errorf("layer after a resize during a pending wipe = %v, want layerPanes (no wipe)", got)
	}
	if blankAbove(scr, rows-1) {
		t.Error("snapped frame is blank")
	}
}

// The pending-wipe guard only covers a resize that lands before the
// transition is realized. A resize after that leaves activeWipe holding
// frames composed for the old viewport, and blitting them paints the
// previous layout for the rest of the transition. Drop the wipe and
// snap instead.
func TestResizeDuringActiveWipeDropsIt(t *testing.T) {
	const cols, rows = 60, 12
	cli := newTestClientWithTwoPanes(t, cols, rows)

	focusTo(cli, 2)

	// First Draw realizes the pending wipe into an active one.
	cli.Draw(newFakeHostScreen(cols, rows), nil, nil)
	if got := cli.layerLocked(); got != layerWipe {
		t.Fatalf("expected an active wipe after the first draw, got layer %v", got)
	}

	cli.SendResize(context.Background(), cols+20, rows)

	scr := newFakeHostScreen(cols+20, rows)
	cli.Draw(scr, nil, nil)

	if got := cli.layerLocked(); got != layerPanes {
		t.Errorf("layer after a resize mid-transition = %v, want layerPanes (wipe dropped)", got)
	}
	// And the snapped frame must use the new width, not the old one.
	got := strings.Join(scr.textAbove(rows-1), "\n")
	if !strings.Contains(got, "PANE-TWO") {
		t.Errorf("snapped frame after resize is missing pane content:\n%s", got)
	}
}
