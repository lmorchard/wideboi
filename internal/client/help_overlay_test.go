package client

import (
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/keys"
)

// The overlay documents the table, so it must document all of it. A
// binding missing from here is a binding a user cannot discover.
func TestHelpLinesNameEveryBinding(t *testing.T) {
	joined := strings.Join(helpLines("C-b", true), "\n")
	for _, b := range keys.Bindings {
		if !strings.Contains(joined, b.Long) {
			t.Errorf("overlay omits the description of %q: %q", b.Key, b.Long)
		}
		if !strings.Contains(joined, b.Key) {
			t.Errorf("overlay omits the key %q", b.Key)
		}
	}
}

// Detach is hidden in-process for the same reason it is hidden in the
// bar: offering it would be advertising data loss.
func TestHelpLinesHideDetachInProcess(t *testing.T) {
	joined := strings.Join(helpLines("C-b", false), "\n")
	if strings.Contains(joined, "detach") {
		t.Errorf("in-process overlay mentions detach:\n%s", joined)
	}
}

// The hint has to name the prefix the user actually configured. A
// hardcoded C-b is wrong for anyone who set WIDEBOI_PREFIX, and they are
// exactly the people most likely to open help.
func TestHelpLinesNameTheConfiguredPrefix(t *testing.T) {
	joined := strings.Join(helpLines("C-a", true), "\n")
	if !strings.Contains(joined, "C-a") {
		t.Errorf("overlay does not name the configured prefix:\n%s", joined)
	}
	if strings.Contains(joined, "C-b") {
		t.Errorf("overlay hardcodes C-b:\n%s", joined)
	}
}

// make verify-exit runs wideboi at 4x2, 1x1 and 0x0. The last two are
// exactly where an unguarded centred-box origin goes negative, and a
// panic there would take out the teardown contract ptycheck.py asserts.
func TestHelpOverlaySurvivesTinyViewports(t *testing.T) {
	for _, size := range []struct{ cols, rows int }{
		{0, 0}, {1, 1}, {2, 1}, {4, 2}, {10, 3}, {-1, -1}, {80, 24},
	} {
		t.Run("", func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%dx%d panicked: %v", size.cols, size.rows, r)
				}
			}()
			w, h := max(size.cols, 0), max(size.rows, 0)
			buf := compose.NewSurface(max(w, 1), max(h, 1))
			drawHelpOverlay(buf, size.cols, size.rows, "C-b", true)
		})
	}
}

// At a usable size the box must actually land inside the viewport, with
// the last column left alone -- ultraviolet brackets a final-column
// write with autowrap toggles, which splits text across escapes on the
// wire.
//
// 10x3 is included alongside the ordinary 80x24: it is the one size in
// TestHelpOverlaySurvivesTinyViewports that does not hit either early
// return (cols<=0/rows<=0, or boxW<4/boxH<3) and so actually paints
// content, which makes it the size where the last-column rule could
// break. It is correct today -- verified by hand -- so this pins that
// rather than proving it for the first time.
func TestHelpOverlayStaysInsideTheViewport(t *testing.T) {
	for _, size := range []struct{ cols, rows int }{{80, 24}, {10, 3}} {
		t.Run("", func(t *testing.T) {
			cols, rows := size.cols, size.rows
			buf := compose.NewSurface(cols, rows)
			drawHelpOverlay(buf, cols, rows, "C-b", true)

			painted := false
			for y := 0; y < rows; y++ {
				for x := 0; x < cols; x++ {
					c := buf.CellAt(x, y)
					if c == nil || c.Content == "" || c.Content == " " {
						continue
					}
					painted = true
					if x >= cols-1 {
						t.Errorf("overlay wrote the last column at row %d", y)
					}
				}
			}
			if !painted {
				t.Errorf("overlay drew nothing at %dx%d", cols, rows)
			}
		})
	}
}

// SetHelpVisible is a plain setter; the gating behaviour it feeds is
// tested separately by TestLayerLockedPrecedence.
func TestSetHelpVisibleRoundTrips(t *testing.T) {
	c := &Client{cols: 80, rows: 24, prefixLabel: "C-b"}
	if c.helpVisible {
		t.Fatal("help should start hidden")
	}
	c.SetHelpVisible(true)
	if !c.helpVisible {
		t.Error("SetHelpVisible(true) did not take")
	}
	c.SetHelpVisible(false)
	if c.helpVisible {
		t.Error("SetHelpVisible(false) did not take")
	}
}

// The decision Draw acts on, asserted directly rather than through a
// terminal: help outranks a wipe, a wipe outranks the ordinary panes,
// and either alone lands where it should.
func TestLayerLockedPrecedence(t *testing.T) {
	fA := compose.NewSurface(80, 24)
	fB := compose.NewSurface(80, 24)
	activeWipe := NewWipeTransition(fA, fB, 80, 24, WipeLeftToRight, 8)

	cases := []struct {
		name        string
		helpVisible bool
		wipe        *WipeTransition
		want        drawLayer
	}{
		{"neither", false, nil, layerPanes},
		{"wipe only", false, activeWipe, layerWipe},
		{"help only", true, nil, layerHelp},
		{"help wins over an active wipe", true, activeWipe, layerHelp},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{helpVisible: tc.helpVisible, activeWipe: tc.wipe}
			if got := c.layerLocked(); got != tc.want {
				t.Errorf("layerLocked() = %v, want %v", got, tc.want)
			}
		})
	}
}
