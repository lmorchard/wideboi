package client

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// A user in control mode who cannot see how to quit or how to get back
// out has no affordance at all: there is no unprefixed escape hatch any
// more, by design. Both verbs survive every plausible width.
func TestControlHelpAlwaysNamesQuitAndExit(t *testing.T) {
	for cols := 40; cols <= 200; cols++ {
		line := truncateRunes(controlHelp(cols-1), cols-1)
		for _, want := range []string{"q quit", "esc exit"} {
			if !strings.Contains(line, want) {
				t.Errorf("cols=%d: control help omits %q: %q", cols, want, line)
			}
		}
		if got := len([]rune(line)); got > cols-1 {
			t.Errorf("cols=%d: control help is %d cells, budget is %d: %q", cols, got, cols-1, line)
		}
	}
}

// The measurement the whole indicator decision rests on: inverting the
// bar costs nothing, so at 80 columns -- the commonest width and
// cmd/wideboi's own fallback -- every verb fits. If a verb is ever added
// that breaks this, the spec's argument needs revisiting, so fail loudly
// rather than silently dropping one.
func TestControlHelpFitsEveryVerbAt80Columns(t *testing.T) {
	at80 := controlHelp(79)
	for _, want := range append(append([]string{}, controlVerbs...), controlTail...) {
		if !strings.Contains(at80, want) {
			t.Errorf("80 columns: control help omits %q: %q", want, at80)
		}
	}
	if got := runeLen(at80); got > 79 {
		t.Errorf("control help is %d cells at 80 columns, budget is 79", got)
	}
}

// Normal mode's only affordance is the hint, so it has to name the
// prefix the user actually has -- not a hardcoded C-b.
func TestNormalStatusNamesTheConfiguredPrefix(t *testing.T) {
	c := &Client{cols: 100, rows: 30, focusPaneID: 1, prefixLabel: "C-a"}
	got, style := c.statusLineLocked(99)
	if !strings.Contains(got, "C-a for commands") {
		t.Errorf("normal status does not name the prefix: %q", got)
	}
	if !style.IsZero() {
		t.Errorf("normal status carries a style: %+v", style)
	}
}

// The inversion is the mode indicator. A partly-inverted row reads as a
// rendering glitch rather than a mode, so the line must fill the whole
// budget.
func TestControlStatusInvertsTheWholeRow(t *testing.T) {
	c := &Client{cols: 100, rows: 30, focusPaneID: 1, prefixLabel: "C-b", controlMode: true}
	got, style := c.statusLineLocked(99)
	if style.Attrs&uv.AttrReverse == 0 {
		t.Errorf("control status is not reverse video: %+v", style)
	}
	if n := runeLen(got); n != 99 {
		t.Errorf("control status is %d cells, want the full 99 so the whole row inverts: %q", n, got)
	}
	if !strings.Contains(got, "q quit") {
		t.Errorf("control status does not name the quit verb: %q", got)
	}
}

func TestSetControlModeSwitchesTheStatusLine(t *testing.T) {
	c := &Client{cols: 100, rows: 30, focusPaneID: 1, prefixLabel: "C-b"}
	normal, _ := c.statusLineLocked(99)
	c.SetControlMode(true)
	control, _ := c.statusLineLocked(99)
	if normal == control {
		t.Errorf("status line is identical in both modes: %q", normal)
	}
	c.SetControlMode(false)
	back, _ := c.statusLineLocked(99)
	if back != normal {
		t.Errorf("leaving control mode did not restore the status line:\n  got  %q\n  want %q", back, normal)
	}
}

// Truncation is by rune, not byte: the status glyphs are three bytes
// and one cell each, so a byte-indexed cut could both overcount the
// budget and leave a partial UTF-8 sequence on the wire.
func TestTruncateRunesCutsOnRuneBoundaries(t *testing.T) {
	const s = "focus: pane 1  [1 ✓]  [2 ✗]"
	for n := 0; n <= len([]rune(s)); n++ {
		got := truncateRunes(s, n)
		if len([]rune(got)) != n {
			t.Errorf("truncateRunes(%q, %d) = %q, %d runes", s, n, got, len([]rune(got)))
		}
		if !strings.HasPrefix(s, got) {
			t.Errorf("truncateRunes(%q, %d) = %q is not a prefix", s, n, got)
		}
	}
}
