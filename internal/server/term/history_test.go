package term_test

import (
	"testing"

	"github.com/lmorchard/wideboi/internal/server/term"
)

func TestHistoryRowsIncludesScreenAndScrollback(t *testing.T) {
	g := term.NewVT(12, 3)
	t.Cleanup(func() { _ = g.Close() })
	_, _ = g.Write([]byte("old 本\r\nother\r\nlive\r\nlast"))
	length, rows := g.HistoryRows()
	if length != 1 || len(rows) != 4 {
		t.Fatalf("history length=%d rows=%q", length, rows)
	}
	for i, want := range []string{"old 本", "other", "live", "last"} {
		if rows[i] != want {
			t.Errorf("row %d = %q, want %q", i, rows[i], want)
		}
	}
}

func TestHistoryRowsKeepsSoftWrapPhysical(t *testing.T) {
	g := term.NewVT(4, 2)
	t.Cleanup(func() { _ = g.Close() })
	_, _ = g.Write([]byte("abcdef"))
	_, rows := g.HistoryRows()
	if rows[0] != "abcd" || rows[1] != "ef" {
		t.Fatalf("soft-wrapped rows = %q", rows)
	}
}
