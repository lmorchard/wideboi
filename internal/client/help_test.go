package client

import "strings"

import "testing"

// The quit verb is the one a user cannot do without, and it used to be
// the first casualty: the full help is 91 cells, the "focus: pane N"
// prefix is 13, and the budget at 80 columns is 79 -- so a single
// byte-index truncation cut the line at "alt+u/d scr" and never told an
// 80-column user that alt+q exists. 80 is the commonest terminal width
// and cmd/wideboi's own fallback when the host reports no size.
func TestHelpKeepsQuitAtEveryPlausibleWidth(t *testing.T) {
	const prefix = 13 // len("focus: pane 1")
	for cols := 40; cols <= 200; cols++ {
		line := truncateRunes(strings.Repeat("x", prefix)+helpFor(cols-1, prefix), cols-1)
		if !strings.Contains(line, "alt+q quit") {
			t.Errorf("cols=%d: status line does not name the quit key: %q", cols, line)
		}
		if got := len([]rune(line)); got > cols-1 {
			t.Errorf("cols=%d: status line is %d cells, budget is %d: %q", cols, got, cols-1, line)
		}
	}
}

// Verbs come back as the terminal widens rather than being dropped for
// good; at 80 the set is fixed by what fits.
func TestHelpDropsVerbsOnlyAsNeeded(t *testing.T) {
	const prefix = 13
	at80 := helpFor(79, prefix)
	for _, want := range []string{"alt+h/l focus", "alt+n new", "alt+w width", "alt+x kill", "alt+q quit"} {
		if !strings.Contains(at80, want) {
			t.Errorf("80 columns: help omits %q: %q", want, at80)
		}
	}
	if strings.Contains(at80, "alt+u/d scroll") {
		t.Errorf("80 columns: help claims to fit the scroll verb: %q", at80)
	}
	at200 := helpFor(199, prefix)
	for _, want := range append(helpSegments, helpQuit) {
		if !strings.Contains(at200, strings.TrimSpace(want)) {
			t.Errorf("200 columns: help omits %q: %q", want, at200)
		}
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
