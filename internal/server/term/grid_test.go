package term_test

import (
	"fmt"
	"image"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/server/term"
)

func TestGridRendersWrittenBytes(t *testing.T) {
	g := term.NewVT(10, 2)
	if _, err := io.WriteString(g, "hello\r\nworld"); err != nil {
		t.Fatalf("write: %v", err)
	}

	s := compose.NewSurface(10, 2)
	g.Draw(s, s.Bounds())

	got := compose.Text(s, s.Bounds())
	want := []string{"hello     ", "world     "}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d:\n  got  %q\n  want %q", i, got[i], want[i])
		}
	}
}

func TestGridReportsItsSize(t *testing.T) {
	g := term.NewVT(37, 9)
	cols, rows := g.Size()
	if cols != 37 || rows != 9 {
		t.Fatalf("Size() = %d,%d want 37,9", cols, rows)
	}
}

func TestGridHonoursResize(t *testing.T) {
	g := term.NewVT(10, 2)
	g.Resize(20, 4)
	cols, rows := g.Size()
	if cols != 20 || rows != 4 {
		t.Fatalf("Size() after resize = %d,%d want 20,4", cols, rows)
	}
}

// TestDrawScrollbackDoesNotRaceWrite pins Draw's scrollback branch
// against a concurrent Write: ScrollbackCellAt and CellAt both return
// *uv.Cell aliasing the emulator's live buffer, and Draw hands that
// pointer straight to dst.SetCell with no lock of its own held. No other
// test in this package scrolls a pane while output keeps arriving, so
// this is the only coverage that can make `go test -race` see this
// hazard at all; it caught a real, -race-confirmed race in review that
// no test in the server package's own concurrent-resize suite touched,
// because none of them scroll. The assertion is nominal (both goroutines
// simply run to their deadline without panicking); the point of this
// test is what `-race` says about it, not what it returns.
//
// The scroll offset is deliberately small, not the full scrollback
// length: Draw's scrollback branch reads from two different sources
// depending on whether a row's index has scrolled off (ScrollbackCellAt,
// a separate, already-settled buffer) or is still part of the live
// screen (CellAt, the same buffer Write mutates). Only the latter can
// race a concurrent Write, and only a *partial* scroll -- offset less
// than the viewport height -- puts any row of the drawn area on that
// live-screen side of the boundary. Confirmed by hand: reverting the
// writeResizeMu take in Draw's scrollback branch makes this test fail
// under -race; restoring it makes this test pass.
func TestDrawScrollbackDoesNotRaceWrite(t *testing.T) {
	g := term.NewVT(10, 5)

	// Push enough lines through to build real scrollback, then scroll
	// back only partway so the drawn viewport straddles the boundary
	// between scrollback and the still-live screen below it.
	for i := 0; i < 20; i++ {
		fmt.Fprintf(g, "line %d\r\n", i)
	}
	if g.ScrollbackLen() == 0 {
		t.Fatal("expected scrollback to be non-empty after writing 20 lines to a 5-row grid")
	}
	g.SetScrollOffset(2)

	s := compose.NewSurface(10, 5)
	deadline := time.Now().Add(200 * time.Millisecond)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		n := 0
		for time.Now().Before(deadline) {
			fmt.Fprintf(g, "more %d\r\n", n)
			n++
		}
	}()
	go func() {
		defer wg.Done()
		for time.Now().Before(deadline) {
			g.Draw(s, s.Bounds())
		}
	}()
	wg.Wait()
}

func TestGridDrawAt(t *testing.T) {
	g := term.NewVT(20, 5)
	for i := 0; i < 20; i++ {
		if i > 0 {
			fmt.Fprint(g, "\r\n")
		}
		fmt.Fprintf(g, "line %02d", i)
	}
	if g.ScrollOffset() != 0 {
		t.Fatalf("initial ScrollOffset = %d, want 0", g.ScrollOffset())
	}

	readRow := func(dst compose.Surface, y int) string {
		var b strings.Builder
		for x := 0; x < 20; x++ {
			c := dst.CellAt(x, y)
			if c != nil {
				b.WriteString(c.Content)
			}
		}
		return strings.TrimRight(b.String(), " ")
	}

	s0 := compose.NewSurface(20, 5)
	g.DrawAt(s0, s0.Bounds(), 0)
	if got := readRow(s0, 4); got != "line 19" {
		t.Errorf("DrawAt(0) bottom row = %q, want %q", got, "line 19")
	}

	s5 := compose.NewSurface(20, 5)
	g.DrawAt(s5, s5.Bounds(), 5)
	if got := readRow(s5, 4); got != "line 14" {
		t.Errorf("DrawAt(5) bottom row = %q, want %q", got, "line 14")
	}

	if g.ScrollOffset() != 0 {
		t.Errorf("ScrollOffset after DrawAt calls was mutated to %d, want 0", g.ScrollOffset())
	}
}

// TestGridDrawAtWrappedScrollback verifies that DrawAt renders lines in correct
// chronological order when the scrollback buffer has exceeded capacity and wrapped.
func TestGridDrawAtWrappedScrollback(t *testing.T) {
	g := term.NewVT(20, 5)
	defer g.Close()

	// Write 10,050 lines to fill and wrap the 10,000-line scrollback buffer.
	// Lines 0..4 fill the 5-row screen.
	// The next 10,045 lines (5..10049) scroll lines 0..10044 into scrollback.
	// With 10,000 scrollback capacity, lines 0..44 are evicted.
	// Line 45 is the oldest retained line; lines 10045..10049 are live on screen.
	totalLines := 10050
	for i := 0; i < totalLines; i++ {
		if i > 0 {
			fmt.Fprint(g, "\r\n")
		}
		fmt.Fprintf(g, "L%06d", i)
	}

	if sbLen := g.ScrollbackLen(); sbLen != 10000 {
		t.Fatalf("ScrollbackLen = %d, want 10000", sbLen)
	}

	readRow := func(dst compose.Surface, y int) string {
		var b strings.Builder
		for x := 0; x < 20; x++ {
			c := dst.CellAt(x, y)
			if c != nil {
				b.WriteString(c.Content)
			}
		}
		return strings.TrimRight(b.String(), " ")
	}

	// At offset 0: bottom row is the live line 10049.
	s0 := compose.NewSurface(20, 5)
	g.DrawAt(s0, s0.Bounds(), 0)
	if got := readRow(s0, 4); got != "L010049" {
		t.Errorf("DrawAt(0) bottom row = %q, want L010049", got)
	}

	// At offset 10000 (scrolled all the way to top of history):
	// Top row should be the oldest retained line (L000045).
	stop := compose.NewSurface(20, 5)
	g.DrawAt(stop, stop.Bounds(), 10000)
	if got := readRow(stop, 0); got != "L000045" {
		t.Errorf("DrawAt(10000) top row = %q, want L000045", got)
	}
	if got := readRow(stop, 1); got != "L000046" {
		t.Errorf("DrawAt(10000) second row = %q, want L000046", got)
	}

	// At offset 5000: midpoint in history
	// sbY = (10000 - 5000) + 0 = 5000th line in history -> line 45 + 5000 = 5045
	smid := compose.NewSurface(20, 5)
	g.DrawAt(smid, smid.Bounds(), 5000)
	if got := readRow(smid, 0); got != "L005045" {
		t.Errorf("DrawAt(5000) top row = %q, want L005045", got)
	}
}

// TestCloseUnblocksRead pins the one behaviour vtGrid.Close's bypass of
// (*vt.Emulator).Close rests on: a pending Read must still return io.EOF
// once Close runs, exactly as (*vt.Emulator).Close would have produced
// via CloseWithError(io.EOF). Nothing else exercises this through the
// real InputPipe path -- the server package's own
// TestCloseDoesNotHangOnWedgedResize uses a fake Grid -- so this is what
// would fail loudly if a future x/vt bump changed InputPipe's concrete
// type out from under the type assertion Close relies on, silently
// falling back to the racy path instead.
func TestCloseUnblocksRead(t *testing.T) {
	g := term.NewVT(10, 2)

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 16)
		_, err := g.Read(buf)
		done <- err
	}()

	// Give the goroutine a moment to actually park inside Read before
	// closing, so a passing test means Close unblocked it rather than
	// there having been nothing to unblock.
	time.Sleep(10 * time.Millisecond)

	if err := g.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case err := <-done:
		if err != io.EOF {
			t.Fatalf("Read after Close = %v, want io.EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read did not unblock within 2s of Close")
	}
}

// TestGridTracksCursorPosition guards the translation Defect 2's fix
// depends on: main.go composites the host cursor from
// Grid.CursorPosition() plus a pane's destination-rect origin, so this
// has to report cell coordinates relative to the grid's own origin, not
// the host screen's.
func TestGridTracksCursorPosition(t *testing.T) {
	g := term.NewVT(10, 4)

	if got := g.CursorPosition(); got != (image.Point{}) {
		t.Fatalf("CursorPosition() at start = %v, want (0,0)", got)
	}

	io.WriteString(g, "abc")
	if got, want := g.CursorPosition(), (image.Point{X: 3, Y: 0}); got != want {
		t.Fatalf("CursorPosition() after %q = %v, want %v", "abc", got, want)
	}

	io.WriteString(g, "\r\nxy")
	if got, want := g.CursorPosition(), (image.Point{X: 2, Y: 1}); got != want {
		t.Fatalf("CursorPosition() after CRLF+%q = %v, want %v", "xy", got, want)
	}
}

// TestGridTracksCursorVisibility guards the other half of Defect 2: a
// full-screen application that hides its cursor (DECTCEM, mode ?25) must
// be respected, and a freshly started shell — which has issued no such
// sequence — must default to visible so the very first frame still shows
// a cursor.
func TestGridTracksCursorVisibility(t *testing.T) {
	g := term.NewVT(10, 4)

	if !g.CursorVisible() {
		t.Fatal("CursorVisible() at start = false, want true (nothing has hidden it yet)")
	}

	io.WriteString(g, "\x1b[?25l") // DECTCEM off
	if g.CursorVisible() {
		t.Fatal("CursorVisible() after DECTCEM-off = true, want false")
	}

	io.WriteString(g, "\x1b[?25h") // DECTCEM on
	if !g.CursorVisible() {
		t.Fatal("CursorVisible() after DECTCEM-on = false, want true")
	}
}

func TestGridAppliesSGRWithoutPrintingIt(t *testing.T) {
	g := term.NewVT(10, 1)
	// Bold "hi" — the escape sequence must not appear as text.
	io.WriteString(g, "\x1b[1mhi\x1b[0m")

	s := compose.NewSurface(10, 1)
	g.Draw(s, s.Bounds())

	if got := compose.Text(s, s.Bounds())[0]; got != "hi        " {
		t.Fatalf("got %q, want %q", got, "hi        ")
	}
}

// SendKey encodes a decoded key event back into the bytes a child
// process expects. Forwarding KeyPressEvent.String() would send the
// literal text "ctrl+c" instead of \x03.
//
// SendKey writes to an io.Pipe and blocks until something reads, so the
// drain goroutine below is mandatory, not incidental.
func TestGridEncodesKeysForTheChild(t *testing.T) {
	cases := []struct {
		name string
		key  uv.KeyPressEvent
		want string
	}{
		{"printable", uv.KeyPressEvent{Code: 'a', Text: "a"}, "a"},
		{"printable m", uv.KeyPressEvent{Code: 'm', Text: "m"}, "m"},
		{"ctrl+c", uv.KeyPressEvent{Code: 'c', Mod: uv.ModCtrl}, "\x03"},
		{"ctrl+c with text and repeat", uv.KeyPressEvent{Code: 'c', Text: "c", Mod: uv.ModCtrl, IsRepeat: true}, "\x03"},
		{"enter", uv.KeyPressEvent{Code: uv.KeyEnter}, "\r"},
		{"up arrow", uv.KeyPressEvent{Code: uv.KeyUp}, "\x1b[A"},
		{"tab", uv.KeyPressEvent{Code: uv.KeyTab}, "\t"},
		{"backspace", uv.KeyPressEvent{Code: uv.KeyBackspace}, "\x7f"},

		// The regression this whole test exists to guard: x/vt's own
		// SendKey requires Mod == 0 in its default case, and Shift never
		// clears, so every shifted key — capitals, shifted digits,
		// shifted punctuation — used to vanish silently. vtGrid.SendKey
		// must route these through SendText instead.
		{"shift+m", uv.KeyPressEvent{Code: 'm', Text: "M", Mod: uv.ModShift}, "M"},
		{"shift+1", uv.KeyPressEvent{Code: '1', Text: "!", Mod: uv.ModShift}, "!"},

		// The trap in that fix: alt+l must still become "\x1bl", not the
		// bare "l" its Text carries. Alt changes the encoding; SendText
		// would lose the Meta prefix. This is the case that would go red
		// if the routing rule were ever "simplified" to always prefer
		// Text over SendKey whenever Text is non-empty — see the report
		// in .superpowers/keyfix-report.md for that failure captured
		// live.
		{"alt+l", uv.KeyPressEvent{Code: 'l', Text: "l", Mod: uv.ModAlt}, "\x1bl"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := term.NewVT(20, 3)

			out := make(chan string, 1)
			go func() {
				buf := make([]byte, 64)
				n, err := g.Read(buf)
				if n > 0 {
					out <- string(buf[:n])
					return
				}
				if err != nil {
					out <- ""
				}
			}()

			g.SendKey(uv.KeyEvent(tc.key))

			select {
			case got := <-out:
				if got != tc.want {
					t.Errorf("SendKey(%s) = %q, want %q", tc.name, got, tc.want)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("SendKey(%s) produced no bytes — is the drain running?", tc.name)
			}
		})
	}
}

// drainOne starts a reader and returns the first chunk SendKey produces,
// or "" if nothing arrives. SendKey blocks on an io.Pipe until something
// reads, so the reader must exist before the send.
func drainOne(t *testing.T, g term.Grid, send func()) string {
	t.Helper()
	out := make(chan string, 1)
	go func() {
		buf := make([]byte, 64)
		n, err := g.Read(buf)
		if n > 0 {
			out <- string(buf[:n])
			return
		}
		if err != nil {
			out <- ""
		}
	}()
	send()
	select {
	case got := <-out:
		return got
	case <-time.After(2 * time.Second):
		return ""
	}
}

// Every printable character must reach the child, with or without shift.
// This is an exhaustive sweep of a small finite space, not a sample: the
// defect that shipped in Plan 1 was that ModShift produced no bytes at
// all, and a seven-row table missed it.
func TestEveryPrintableKeyProducesBytes(t *testing.T) {
	for r := rune(0x20); r <= rune(0x7e); r++ {
		for _, mod := range []uv.KeyMod{0, uv.ModShift} {
			name := fmt.Sprintf("%q_mod%d", r, mod)
			t.Run(name, func(t *testing.T) {
				g := term.NewVT(20, 3)
				defer g.Close()
				k := uv.KeyPressEvent{Code: r, Text: string(r), Mod: mod}
				got := drainOne(t, g, func() { g.SendKey(uv.KeyEvent(k)) })
				if got == "" {
					t.Fatalf("no bytes produced for %q with mod %d", r, mod)
				}
				if got != string(r) {
					t.Errorf("got %q, want %q", got, string(r))
				}
			})
		}
	}
}

// For printable input the encode path must be the identity function.
// This needs no enumeration of cases and catches future encoding gaps
// that no hand-written table would anticipate.
func TestPrintableInputRoundTrips(t *testing.T) {
	const sample = "The Quick Brown Fox! @#$%^&*() 0123456789 {}[]|\\;:'\",.<>/?"
	for _, r := range sample {
		g := term.NewVT(20, 3)
		k := uv.KeyPressEvent{Code: r, Text: string(r)}
		got := drainOne(t, g, func() { g.SendKey(uv.KeyEvent(k)) })
		if got != string(r) {
			t.Errorf("round trip failed for %q: got %q", r, got)
		}
		g.Close()
	}
}

// The server resends a pane only when its generation has moved (#85), so
// a mutation that fails to advance it leaves every client stale.
func TestGenerationAdvancesOnWrite(t *testing.T) {
	g := term.NewVT(10, 3)
	defer g.Close()
	before := g.Generation()
	if _, err := g.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	if g.Generation() == before {
		t.Error("Write did not advance the generation")
	}
}

func TestGenerationAdvancesOnResizeOnlyWhenTheSizeChanges(t *testing.T) {
	g := term.NewVT(10, 3)
	defer g.Close()
	before := g.Generation()
	g.Resize(10, 3)
	if g.Generation() != before {
		t.Error("a same-size Resize advanced the generation")
	}
	g.Resize(8, 3)
	if g.Generation() == before {
		t.Error("Resize did not advance the generation")
	}
}

func TestGenerationAdvancesOnScrollOnlyWhenTheOffsetMoves(t *testing.T) {
	g := term.NewVT(10, 3)
	defer g.Close()
	for i := 0; i < 10; i++ {
		fmt.Fprintf(g, "%d\r\n", i)
	}
	if g.ScrollbackLen() < 2 {
		t.Fatalf("fixture produced %d scrollback lines, need >= 2", g.ScrollbackLen())
	}
	steps := []struct {
		offset int
		moves  bool
	}{
		{0, false}, // already at the bottom
		{2, true},
		{2, false},  // unchanged
		{-5, true},  // clamps to 0, which is a move from 2
		{-1, false}, // clamps to 0 again
	}
	for _, st := range steps {
		before := g.Generation()
		g.SetScrollOffset(st.offset)
		if moved := g.Generation() != before; moved != st.moves {
			t.Errorf("SetScrollOffset(%d): generation moved=%v, want %v", st.offset, moved, st.moves)
		}
	}
}
