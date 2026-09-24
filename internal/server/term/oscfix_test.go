package term_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lmorchard/wideboi/internal/server/term"
)

// screenText is the grid's visible text, one string per row, trailing
// blanks trimmed.
func screenText(g term.Grid) string {
	cols, rows := g.Size()
	var b strings.Builder
	for y := 0; y < rows; y++ {
		var row strings.Builder
		for x := 0; x < cols; x++ {
			if c := g.CellAt(x, y); c != nil && c.Content != "" {
				row.WriteString(c.Content)
			} else {
				row.WriteByte(' ')
			}
		}
		b.WriteString(strings.TrimRight(row.String(), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// U+2733 is E2 9C B3, and 0x9C is also C1 String Terminator. x/ansi's
// OSC state treats it as ST even mid-character, so Claude Code's
// "✳ Claude Code" title became the lone byte "\xe2" and the rest of the
// sequence was printed as text (#175). These drive real bytes through
// Grid.Write, the way a child process would.
func TestOSCTitleSurvivesA9CContinuationByte(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantTitle string
	}{
		{"BEL-terminated, the bug", "\x1b]0;✳ Claude Code\x07", "✳ Claude Code"},
		// Not just dingbats: 本 is E6 9C AC.
		{"CJK", "\x1b]2;日本語\x07", "日本語"},
		{"OSC 2, ESC-backslash terminated", "\x1b]2;✳ x\x1b\\", "✳ x"},
		{"no 0x9C: accented", "\x1b]0;héllo\x07", "héllo"},
		{"no 0x9C: braille spinner", "\x1b]0;⠂ working\x07", "⠂ working"},
		{"a later title in the same write wins", "\x1b]0;✳ a\x07\x1b]0;b\x07", "b"},
		{"a real 8-bit ST still terminates", "\x1b]0;abc\x9c", "abc"},
		{"CAN aborts the sequence", "\x1b]0;✳ gone\x18", ""},
		{"icon name is not the title", "\x1b]1;✳ icon\x07", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := term.NewVT(40, 3)
			defer g.Close()
			if _, err := g.Write([]byte(tc.input)); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if got := g.Title(); got != tc.wantTitle {
				t.Errorf("Title() = %q, want %q", got, tc.wantTitle)
			}
			if !utf8.ValidString(g.Title()) {
				t.Errorf("Title() %q is not valid UTF-8", g.Title())
			}
			if txt := strings.TrimSpace(screenText(g)); txt != "" {
				t.Errorf("the sequence leaked onto the screen: %q", txt)
			}
		})
	}
}

// A pty read can end anywhere, including mid-character inside the OSC.
func TestOSCTitleSurvivesEverySplitPoint(t *testing.T) {
	const input = "\x1b]0;✳ Claude Code\x07"
	for i := 0; i <= len(input); i++ {
		g := term.NewVT(40, 3)
		_, _ = g.Write([]byte(input[:i]))
		_, _ = g.Write([]byte(input[i:]))
		if got := g.Title(); got != "✳ Claude Code" {
			t.Errorf("split at %d: Title() = %q", i, got)
		}
		if txt := strings.TrimSpace(screenText(g)); txt != "" {
			t.Errorf("split at %d: leaked onto the screen: %q", i, txt)
		}
		g.Close()
	}
}

// Any other OSC carrying such a character must not spill text either;
// the character reaches the parser as U+FFFD.
func TestOSCHyperlinkWithA9CByteDoesNotLeak(t *testing.T) {
	g := term.NewVT(40, 3)
	defer g.Close()
	_, _ = g.Write([]byte("\x1b]8;;https://example.com/✳/page\x1b\\link\x1b]8;;\x1b\\"))
	if txt := strings.TrimSpace(screenText(g)); txt != "link" {
		t.Errorf("screen = %q, want just the link text", txt)
	}
}
