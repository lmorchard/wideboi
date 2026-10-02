package server

import (
	"fmt"
	"testing"

	"github.com/lmorchard/wideboi/internal/server/term"
)

func TestPaneCaptureText(t *testing.T) {
	g := term.NewVT(40, 10)
	defer g.Close()

	fmt.Fprintf(g, "pane line 1\r\npane line 2\r\n")

	p := &Pane{
		id:     1,
		grid:   g,
		cols:   40,
		rows:   10,
		closed: make(chan struct{}),
	}

	got := p.CaptureText(false, 0)
	want := "pane line 1\npane line 2\n"
	if got != want {
		t.Fatalf("p.CaptureText(false, 0) = %q, want %q", got, want)
	}

	close(p.closed)
	gotClosed := p.CaptureText(false, 0)
	if gotClosed != "" {
		t.Fatalf("p.CaptureText after close = %q, want empty", gotClosed)
	}
}

func TestPaneDumpText(t *testing.T) {
	g := term.NewVT(40, 10)
	defer g.Close()

	fmt.Fprintf(g, "pane line 1\r\npane line 2\r\n")

	p := &Pane{
		id:     1,
		grid:   g,
		cols:   40,
		rows:   10,
		closed: make(chan struct{}),
	}

	got, total := p.DumpText(false, 0, 1, 0, false)
	want := "pane line 1\n"
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	if got != want {
		t.Fatalf("p.DumpText(false, 0, 1, 0, false) = %q, want %q", got, want)
	}

	close(p.closed)
	gotClosed, totalClosed := p.DumpText(false, 0, 1, 0, false)
	if gotClosed != "" || totalClosed != 0 {
		t.Fatalf("p.DumpText after close = %q, %d, want empty, 0", gotClosed, totalClosed)
	}
}
