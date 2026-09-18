package term_test

import (
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
		{"ctrl+c", uv.KeyPressEvent{Code: 'c', Mod: uv.ModCtrl}, "\x03"},
		{"enter", uv.KeyPressEvent{Code: uv.KeyEnter}, "\r"},
		{"up arrow", uv.KeyPressEvent{Code: uv.KeyUp}, "\x1b[A"},
		{"tab", uv.KeyPressEvent{Code: uv.KeyTab}, "\t"},
		{"backspace", uv.KeyPressEvent{Code: uv.KeyBackspace}, "\x7f"},
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
