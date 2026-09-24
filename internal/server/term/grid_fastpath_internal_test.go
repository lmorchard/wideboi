package term

import (
	"fmt"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/client/compose"
)

// TestDrawFastPathDoesNotAcquireWriteResizeMu guards the fast path in Draw and
// DrawAt against regression: when no scrollback is in view (offset <= 0), live
// terminal cells must be drawn directly via SafeEmulator.Draw without locking
// vtGrid.writeResizeMu.
func TestDrawFastPathDoesNotAcquireWriteResizeMu(t *testing.T) {
	grid := NewVT(10, 5).(*vtGrid)
	for i := 0; i < 10; i++ {
		fmt.Fprintf(grid, "line %d\r\n", i)
	}

	// Hold writeResizeMu to simulate a concurrent Write or Resize in progress.
	grid.writeResizeMu.Lock()
	defer grid.writeResizeMu.Unlock()

	s := compose.NewSurface(10, 5)

	// DrawAt(..., 0) must proceed via the fast path without waiting on writeResizeMu.
	doneAt := make(chan struct{})
	go func() {
		grid.DrawAt(s, s.Bounds(), 0)
		close(doneAt)
	}()

	select {
	case <-doneAt:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("DrawAt(..., 0) blocked on writeResizeMu; fast path was not taken")
	}

	// Draw(...) with zero scroll offset must also proceed via the fast path.
	doneDraw := make(chan struct{})
	go func() {
		grid.Draw(s, s.Bounds())
		close(doneDraw)
	}()

	select {
	case <-doneDraw:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Draw(...) with zero offset blocked on writeResizeMu; fast path was not taken")
	}
}

// TestDrawScrollbackAcquiresWriteResizeMu verifies that positive scroll offsets
// do acquire writeResizeMu to protect live-buffer and scrollback pointer walks.
func TestDrawScrollbackAcquiresWriteResizeMu(t *testing.T) {
	grid := NewVT(10, 5).(*vtGrid)
	for i := 0; i < 10; i++ {
		fmt.Fprintf(grid, "line %d\r\n", i)
	}

	grid.writeResizeMu.Lock()

	s := compose.NewSurface(10, 5)
	done := make(chan struct{})
	go func() {
		grid.DrawAt(s, s.Bounds(), 1)
		close(done)
	}()

	select {
	case <-done:
		grid.writeResizeMu.Unlock()
		t.Fatal("DrawAt(..., 1) did not acquire writeResizeMu")
	case <-time.After(50 * time.Millisecond):
		// As expected, blocked waiting for writeResizeMu.
	}

	grid.writeResizeMu.Unlock()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("DrawAt(..., 1) failed to complete after writeResizeMu was unlocked")
	}
}
