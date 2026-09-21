package client

import (
	"context"
	"image"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// newMotionClient returns a card-mode client whose panes are wider
// than the share they get, so a focus change actually re-deals the
// fan and there is geometry to animate.
//
// The obvious fixture -- two 25-cell panes in a 60-cell viewport,
// scroll mode -- does not work, and that is not a bug: both panes are
// fully visible, so changing focus moves no placement and correctly
// animates nothing. The wipe this replaces fired on every focus
// change regardless of whether anything had moved.
func newMotionClient(t *testing.T, cols, rows int) *Client {
	t.Helper()
	cli := NewClient(transport.NewInProcChannel(16), cols, rows, "C-b")
	focusTo(cli, 1)
	cli.HandleServerMsg(paneUpdate(1, 60, 10, "PANE-ONE"))
	cli.HandleServerMsg(paneUpdate(2, 60, 10, "PANE-TWO"))
	cli.HandleServerMsg(paneUpdate(3, 60, 10, "PANE-THREE"))
	return cli
}

// focusTo moves focus by replaying a layout snapshot, which is what
// the server sends and what arms an animation.
func focusTo(cli *Client, paneID int) {
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns:     threeColumns(),
		FocusPaneID: paneID,
		Layout:      protocol.LayoutCards,
	})
}

func rectOf(ps []protocol.PlacementData, paneID int) image.Rectangle {
	for _, p := range ps {
		if p.PaneID == paneID {
			return p.Dst
		}
	}
	return image.Rectangle{}
}

// The wipe this replaces blitted the new layout on one side of a
// moving seam and the old layout on the other. Nothing moved:
// content teleported in vertical bands, and in card mode -- where a
// focus change repositions every pane -- the screen showed two
// different geometries at once.
func TestMotionInterpolatesTowardTheTarget(t *testing.T) {
	from := []protocol.PlacementData{{PaneID: 1, Dst: image.Rect(0, 1, 10, 11), Src: image.Rect(0, 0, 10, 10)}}
	to := []protocol.PlacementData{{PaneID: 1, Dst: image.Rect(40, 1, 90, 11), Src: image.Rect(0, 0, 50, 10)}}

	if got := rectOf(interpolate(from, to, 0), 1); got != from[0].Dst {
		t.Errorf("at t=0 got %v, want the source rect %v", got, from[0].Dst)
	}
	if got := rectOf(interpolate(from, to, 1), 1); got != to[0].Dst {
		t.Errorf("at t=1 got %v, want the target rect %v", got, to[0].Dst)
	}

	mid := rectOf(interpolate(from, to, 0.5), 1)
	if !(mid.Min.X > from[0].Dst.Min.X && mid.Min.X < to[0].Dst.Min.X) {
		t.Errorf("midpoint X %d is not between %d and %d", mid.Min.X, from[0].Dst.Min.X, to[0].Dst.Min.X)
	}
	if !(mid.Dx() > from[0].Dst.Dx() && mid.Dx() < to[0].Dst.Dx()) {
		t.Errorf("midpoint width %d is not between %d and %d", mid.Dx(), from[0].Dst.Dx(), to[0].Dst.Dx())
	}
}

// A pane that only exists in the target grows from nothing rather
// than appearing at full size part-way through.
func TestMotionGrowsAnAddedPaneFromZero(t *testing.T) {
	from := []protocol.PlacementData{{PaneID: 1, Dst: image.Rect(0, 1, 40, 11), Src: image.Rect(0, 0, 40, 10)}}
	to := []protocol.PlacementData{
		{PaneID: 1, Dst: image.Rect(0, 1, 40, 11), Src: image.Rect(0, 0, 40, 10)},
		{PaneID: 2, Dst: image.Rect(40, 1, 80, 11), Src: image.Rect(0, 0, 40, 10)},
	}

	if got := rectOf(interpolate(from, to, 0), 2).Dx(); got != 0 {
		t.Errorf("added pane starts %d cells wide, want 0", got)
	}
	if got := rectOf(interpolate(from, to, 1), 2); got != to[1].Dst {
		t.Errorf("added pane ends at %v, want %v", got, to[1].Dst)
	}
	if got := rectOf(interpolate(from, to, 0.5), 2).Dx(); got <= 0 || got >= 40 {
		t.Errorf("added pane is %d cells at the midpoint, want strictly between 0 and 40", got)
	}
}

// The regression this whole family exists for, carried over from
// Plan 16: an animation that draws nothing at all shipped green once.
func TestMotionNeverShowsABlankFrame(t *testing.T) {
	const cols, rows = 90, 12
	cli := newMotionClient(t, cols, rows)

	focusTo(cli, 2)

	for step := 0; step <= motionFrames; step++ {
		scr := newFakeHostScreen(cols, rows)
		cli.Draw(scr, nil, nil)
		if blankAbove(scr, rows-1) {
			t.Fatalf("frame %d of the transition is blank above the status bar", step)
		}
	}
}

// It has to land on the layout an un-animated client would draw, or
// it is moving to the wrong place.
func TestMotionSettlesOnTheTargetLayout(t *testing.T) {
	const cols, rows = 90, 12

	animated := newMotionClient(t, cols, rows)
	focusTo(animated, 2)
	var last *fakeHostScreen
	for step := 0; step <= motionFrames; step++ {
		last = newFakeHostScreen(cols, rows)
		animated.Draw(last, nil, nil)
	}

	steady := NewClient(transport.NewInProcChannel(16), cols, rows, "C-b")
	focusTo(steady, 2)
	steady.HandleServerMsg(paneUpdate(1, 60, 10, "PANE-ONE"))
	steady.HandleServerMsg(paneUpdate(2, 60, 10, "PANE-TWO"))
	steady.HandleServerMsg(paneUpdate(3, 60, 10, "PANE-THREE"))
	want := newFakeHostScreen(cols, rows)
	steady.Draw(want, nil, nil)

	if got, exp := strings.Join(last.textAbove(rows-1), "\n"), strings.Join(want.textAbove(rows-1), "\n"); got != exp {
		t.Errorf("did not settle on the steady-state frame\n got:\n%s\nwant:\n%s", got, exp)
	}
}

// A snapshot that changes only a status glyph must not jitter the
// screen -- those arrive whenever a pane writes.
func TestNoMotionWhenPlacementsAreUnchanged(t *testing.T) {
	const cols, rows = 90, 12
	cli := newMotionClient(t, cols, rows)

	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns: threeColumns(), FocusPaneID: 1, Layout: protocol.LayoutCards,
		PaneStatuses: map[int]string{1: "»"},
	})

	cli.mu.Lock()
	running := cli.motion != nil
	cli.mu.Unlock()
	if running {
		t.Error("a status-only snapshot started an animation")
	}
}

// A resize invalidates the rects an animation is interpolating: they
// are in the old viewport's coordinates. Snap instead.
func TestResizeCancelsMotion(t *testing.T) {
	const cols, rows = 90, 12
	cli := newMotionClient(t, cols, rows)

	focusTo(cli, 2)
	cli.Draw(newFakeHostScreen(cols, rows), nil, nil)
	cli.mu.Lock()
	running := cli.motion != nil
	cli.mu.Unlock()
	if !running {
		t.Fatal("expected an animation to be running after a focus change")
	}

	cli.SendResize(context.Background(), cols+20, rows)

	cli.mu.Lock()
	running = cli.motion != nil
	cli.mu.Unlock()
	if running {
		t.Error("animation survived a resize; its rects are in the old viewport's coordinates")
	}

	scr := newFakeHostScreen(cols+20, rows)
	cli.Draw(scr, nil, nil)
	if blankAbove(scr, rows-1) {
		t.Error("snapped frame is blank")
	}
}

// The cursor belongs to a pane that is sliding, so its position is
// meaningless mid-flight. This is the one thing the wipe got right.
func TestCursorHiddenDuringMotion(t *testing.T) {
	const cols, rows = 90, 12
	cli := newMotionClient(t, cols, rows)

	focusTo(cli, 2)
	scr := newFakeHostScreen(cols, rows)
	scr.cursorShown = true
	cli.Draw(scr, nil, nil)

	if scr.cursorShown {
		t.Error("cursor left visible during motion")
	}
}

// A focus change that moves nothing must not animate. Two panes that
// both fit in the viewport keep identical placements when focus
// moves between them -- only the chrome changes, and chrome is
// instant. The wipe animated regardless, which is part of why it
// looked arbitrary.
func TestNoMotionWhenFocusMovesButGeometryDoesNot(t *testing.T) {
	const cols, rows = 60, 12
	cli := newTestClientWithTwoPanes(t, cols, rows) // scroll mode, both visible

	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns: twoColumns(), FocusPaneID: 2,
	})

	cli.mu.Lock()
	running := cli.motion != nil
	cli.mu.Unlock()
	if running {
		t.Error("animated a focus change that moved no placement")
	}
}

// A status or title snapshot must not restart an animation.
//
// The comparison used to be "does what is on screen differ from the
// new target", which is true on every frame of a running animation --
// so each of the status broadcasts a busy pane generates would reset
// the step counter and the motion would never settle. Compare the new
// target against the previous target instead.
func TestStatusSnapshotDoesNotRestartMotion(t *testing.T) {
	const cols, rows = 90, 12
	cli := newMotionClient(t, cols, rows)

	focusTo(cli, 2)
	cli.Draw(newFakeHostScreen(cols, rows), nil, nil)
	cli.Draw(newFakeHostScreen(cols, rows), nil, nil)

	cli.mu.Lock()
	stepBefore := cli.motion.step
	cli.mu.Unlock()
	if stepBefore == 0 {
		t.Fatal("expected the animation to have advanced")
	}

	// Same geometry, different glyphs -- exactly what
	// broadcastLayoutIfStatusChanged sends while a pane is working.
	for i := 0; i < 3; i++ {
		cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
			Columns: threeColumns(), FocusPaneID: 2, Layout: protocol.LayoutCards,
			PaneStatuses: map[int]string{2: "»"},
		})
	}

	cli.mu.Lock()
	stepAfter := 0
	if cli.motion != nil {
		stepAfter = cli.motion.step
	}
	cli.mu.Unlock()

	if stepAfter < stepBefore {
		t.Errorf("animation restarted: step went %d -> %d across status-only snapshots",
			stepBefore, stepAfter)
	}
}

// Interpolated rects can overlap part-way through -- a focused pane
// expanding leftward crosses the sliver that is shrinking out of its
// way. composeFrameLocked paints in slice order, so the set has to
// come back sorted with the highest Z last or a sliver paints over
// the pane the user is looking at.
func TestInterpolatedPlacementsArePaintedBackToFront(t *testing.T) {
	from := []protocol.PlacementData{
		{PaneID: 1, Dst: image.Rect(0, 1, 10, 11), Src: image.Rect(0, 0, 10, 10), Z: 0},
		{PaneID: 2, Dst: image.Rect(10, 1, 70, 11), Src: image.Rect(0, 0, 60, 10), Z: 1},
	}
	// Focus moves left: pane 1 becomes the wide one.
	to := []protocol.PlacementData{
		{PaneID: 1, Dst: image.Rect(0, 1, 60, 11), Src: image.Rect(0, 0, 60, 10), Z: 1},
		{PaneID: 2, Dst: image.Rect(60, 1, 70, 11), Src: image.Rect(0, 0, 10, 10), Z: 0},
	}

	for _, tt := range []float64{0, 0.25, 0.5, 0.75, 1} {
		got := interpolate(from, to, tt)
		for i := 1; i < len(got); i++ {
			if got[i-1].Z > got[i].Z {
				t.Fatalf("t=%v: Z goes %d then %d at index %d; painter's algorithm needs back to front",
					tt, got[i-1].Z, got[i].Z, i)
			}
		}
	}
}

// An outgoing pane must not be painted over the survivors just
// because it was appended last.
func TestCollapsingPaneDoesNotPaintOverTheRest(t *testing.T) {
	from := []protocol.PlacementData{
		{PaneID: 1, Dst: image.Rect(0, 1, 40, 11), Src: image.Rect(0, 0, 40, 10), Z: 1},
		{PaneID: 9, Dst: image.Rect(40, 1, 80, 11), Src: image.Rect(0, 0, 40, 10), Z: 0},
	}
	to := []protocol.PlacementData{
		{PaneID: 1, Dst: image.Rect(0, 1, 80, 11), Src: image.Rect(0, 0, 80, 10), Z: 1},
	}

	got := interpolate(from, to, 0.5)
	for i := 1; i < len(got); i++ {
		if got[i-1].Z > got[i].Z {
			t.Fatalf("Z goes %d then %d at index %d; the collapsing pane is painting last",
				got[i-1].Z, got[i].Z, i)
		}
	}
}
