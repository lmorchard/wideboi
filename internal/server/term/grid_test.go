package term_test

import (
	"fmt"
	"image"
	"io"
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
