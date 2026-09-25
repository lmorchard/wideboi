package client

import (
	"fmt"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/lmorchard/wideboi/internal/keys"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// A user in control mode who cannot see how to quit or how to get back
// out has no affordance at all: there is no unprefixed escape hatch any
// more, by design. Both verbs survive every plausible width.
func TestControlHelpAlwaysNamesQuitAndExit(t *testing.T) {
	for cols := 40; cols <= 200; cols++ {
		line := truncateRunes(controlHelp(cols-1, true), cols-1)
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

// The whole menu must fit at 80 columns, which is the commonest width
// and cmd/wideboi's own fallback when the host reports no size.
//
// This assertion replaces one that pinned a smaller table. Adding
// scroll-down pushed the descriptive form to 81 cells against a 79-cell
// budget, so h/j/k/l collapse into one "hjkl move" entry and the help
// overlay does the teaching. If a verb is ever added that breaks this
// again, the overlay is already there and the bar should shed the entry
// rather than grow.
func TestControlHelpFitsEveryEntryAt80Columns(t *testing.T) {
	for _, detachable := range []bool{true, false} {
		at80 := controlHelp(79, detachable)
		droppable, essential := keys.BarItems(detachable)
		for _, want := range append(append([]string{}, droppable...), essential...) {
			if !strings.Contains(at80, want) {
				t.Errorf("detachable=%v: control help omits %q: %q", detachable, want, at80)
			}
		}
		if got := runeLen(at80); got > 79 {
			t.Errorf("detachable=%v: control help is %d cells at 80 columns, budget is 79: %q",
				detachable, got, at80)
		}
	}
}

// Detach is the one entry whose presence depends on how the client
// reached its server. An in-process wideboi owns its panes, so offering
// the verb would be advertising data loss.
func TestControlHelpOffersDetachOnlyWhenDetachable(t *testing.T) {
	if got := controlHelp(79, false); strings.Contains(got, "d detach") {
		t.Errorf("in-process control help offers detach: %q", got)
	}
	if got := controlHelp(79, true); !strings.Contains(got, "d detach") {
		t.Errorf("attached control help omits detach: %q", got)
	}
}

// The bar is built from the same table the router routes from, so a
// binding can never be advertised without existing or exist without
// being advertised.
func TestControlHelpNamesEveryBarGroup(t *testing.T) {
	at200 := controlHelp(199, true)
	for _, b := range keys.Bindings {
		if !strings.Contains(at200, b.BarGroup) {
			t.Errorf("control help omits %q (binding %q): %q", b.BarGroup, b.Key, at200)
		}
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

// The hints row carries the charcoal background across the whole budget.
func TestControlHintsRowHasCharcoalBackground(t *testing.T) {
	c := &Client{cols: 100, rows: 30, focusPaneID: 1, prefixLabel: "C-b", controlMode: true, theme: DefaultTheme()}
	got, style := c.controlHintsLineLocked(99)
	if style.Bg != ansi.IndexedColor(236) {
		t.Errorf("control hints background = %v, want ansi.IndexedColor(236)", style.Bg)
	}
	if style.Attrs&uv.AttrReverse != 0 {
		t.Errorf("control hints should not have AttrReverse: %+v", style)
	}
	if n := runeLen(got); n != 99 {
		t.Errorf("control hints is %d cells, want the full 99 so the whole row has background: %q", n, got)
	}
	if !strings.Contains(got, "q quit") {
		t.Errorf("control hints does not name the quit verb: %q", got)
	}
}

func TestControlModeDisplaysHintsAboveStatusBar(t *testing.T) {
	ch := transport.NewInProcChannel(64)
	c := NewClient(ch, 100, 24, "C-b")
	c.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns: []protocol.ColumnData{
			{PaneID: 1, Width: 40, Height: 22},
			{PaneID: 2, Width: 40, Height: 22},
		},
	})

	// Before control mode: bottom row 23 has normal status bar.
	scrBefore := newFakeHostScreen(100, 24)
	c.Draw(scrBefore)
	bottomBefore := scrBefore.text()[23]
	if !strings.Contains(bottomBefore, "C-b for commands") {
		t.Fatalf("before control mode: row 23 missing prefix hint: %q", bottomBefore)
	}

	// Enter control mode.
	c.SetControlMode(true)
	scrControl := newFakeHostScreen(100, 24)
	c.Draw(scrControl)

	// Bottom row 23 must still show the normal status bar!
	bottomControl := scrControl.text()[23]
	if !strings.Contains(bottomControl, "C-b for commands") {
		t.Errorf("during control mode: row 23 missing prefix hint (should still be visible): %q", bottomControl)
	}
	if !strings.Contains(bottomControl, "[● 1  ]") {
		t.Errorf("during control mode: row 23 missing pane badge: %q", bottomControl)
	}

	// Row 22 (above status bar) must show the control mode hints with charcoal background.
	hintsRow := scrControl.text()[22]
	if !strings.Contains(hintsRow, "q quit") {
		t.Errorf("hints row 22 missing 'q quit': %q", hintsRow)
	}
	if !strings.Contains(hintsRow, "esc exit") {
		t.Errorf("hints row 22 missing 'esc exit': %q", hintsRow)
	}
	// Check charcoal background on row 22.
	cell := scrControl.CellAt(0, 22)
	if cell == nil {
		t.Fatal("hints row 22 cell (0, 22) is nil")
	}
	if cell.Style.Bg != ansi.IndexedColor(236) {
		t.Errorf("hints row 22 cell (0, 22) Bg = %v, want ansi.IndexedColor(236)", cell.Style.Bg)
	}
	// Check keycap styling (accent/bold) for 'q'.
	foundQ := false
	for x := 0; x < 99; x++ {
		cAt := scrControl.CellAt(x, 22)
		if cAt != nil && cAt.Content == "q" {
			foundQ = true
			if cAt.Style.Fg != ansi.BasicColor(14) {
				t.Errorf("keycap 'q' Fg = %v, want ansi.BasicColor(14)", cAt.Style.Fg)
			}
			if cAt.Style.Attrs&uv.AttrBold == 0 {
				t.Errorf("keycap 'q' should have AttrBold")
			}
			break
		}
	}
	if !foundQ {
		t.Error("keycap 'q' not found on row 22")
	}

	// Exit control mode.
	c.SetControlMode(false)
	scrAfter := newFakeHostScreen(100, 24)
	c.Draw(scrAfter)
	hintsAfter := scrAfter.text()[22]
	if strings.Contains(hintsAfter, "q quit") {
		t.Errorf("after leaving control mode: row 22 should not contain 'q quit': %q", hintsAfter)
	}
	bottomAfter := scrAfter.text()[23]
	if !strings.Contains(bottomAfter, "C-b for commands") {
		t.Errorf("after leaving control mode: row 23 missing prefix hint: %q", bottomAfter)
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

// The control hints line is what a user actually reads, so assert through it
// as well as through controlHelp: a Client that never passed its own
// detachable flag down would pass the test above and still show the
// wrong menu.
func TestControlStatusOffersDetachOnlyWhenDetachable(t *testing.T) {
	local := &Client{cols: 100, rows: 30, focusPaneID: 1, prefixLabel: "C-b", controlMode: true}
	got, _ := local.controlHintsLineLocked(99)
	if strings.Contains(got, "d detach") {
		t.Errorf("in-process hints line offers %q: %q", "d detach", got)
	}

	attached := &Client{cols: 100, rows: 30, focusPaneID: 1, prefixLabel: "C-b", controlMode: true}
	attached.SetDetachable(true)
	got, _ = attached.controlHintsLineLocked(99)
	if !strings.Contains(got, "d detach") {
		t.Errorf("attached hints line omits %q: %q", "d detach", got)
	}
}

func TestCustomBindingsInControlHelpAndHelpLines(t *testing.T) {
	custom, err := keys.BuildBindings(map[string][]string{
		keys.ActionNameKillPane: {"k"},
		keys.ActionNameScrollUp: {"e"},
	})
	if err != nil {
		t.Fatalf("BuildBindings: %v", err)
	}

	c := NewClient(nil, 80, 24, "C-b")
	c.SetBindings(custom)
	c.SetDetachable(true)
	c.SetControlMode(true)

	status, _ := c.controlHintsLineLocked(79)
	if !strings.Contains(status, "k kill") {
		t.Errorf("hints line should contain 'k kill', got: %q", status)
	}
	if !strings.Contains(status, "hjel move") {
		t.Errorf("status line should contain 'hjel move', got: %q", status)
	}

	lines := helpLines("C-b", true, custom)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "k     kill the focused pane") {
		t.Errorf("help lines should contain 'k     kill the focused pane', got: %q", joined)
	}
}

// Ten digit rows would swamp the overlay; they share one line.
func TestHelpGroupsCollapseToOneLine(t *testing.T) {
	lines := helpLines("C-b", true)
	digits := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "0-9 ") {
			digits++
		}
		for n := 1; n <= 9; n++ {
			if strings.HasPrefix(l, fmt.Sprintf("%d ", n)) {
				t.Errorf("digit %d has its own overlay line: %q", n, l)
			}
		}
	}
	if digits != 1 {
		t.Errorf("found %d \"0-9\" lines, want 1:\n%s", digits, strings.Join(lines, "\n"))
	}
}

// Without a HelpKey, a group's label is its members' keys, so it stays
// right when a user remaps one of them.
func TestHelpGroupLabelIsItsKeys(t *testing.T) {
	table := []keys.Binding{
		{Key: "e", Long: "one", HelpGroup: "do the pair"},
		{Key: "u", Long: "two", HelpGroup: "do the pair"},
		{Key: "x", Long: "kill it"},
	}
	lines := helpLines("C-b", true, table)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "e/u   do the pair") {
		t.Errorf("want a joined \"e/u\" group line, got:\n%s", joined)
	}
	if strings.Count(joined, "do the pair") != 1 {
		t.Errorf("group printed more than once:\n%s", joined)
	}
	if !strings.Contains(joined, "x     kill it") {
		t.Errorf("ungrouped row lost:\n%s", joined)
	}
}

// The real table, remapped: a grouped line names the key the user chose.
func TestRemappedPairKeepsItsGroupLabel(t *testing.T) {
	custom, err := keys.BuildBindings(map[string][]string{keys.ActionNameFocusLeft: {"e"}})
	if err != nil {
		t.Fatalf("BuildBindings: %v", err)
	}
	joined := strings.Join(helpLines("C-b", true, custom), "\n")
	if !strings.Contains(joined, "e/l   focus the column left / right") {
		t.Errorf("remapped focus pair not labelled e/l:\n%s", joined)
	}
}

func TestHelpShowsOnlyPrimaryAndOmitsUnbound(t *testing.T) {
	custom, err := keys.BuildBindings(map[string][]string{
		keys.ActionNameNewColumn: {"n", "enter"},
		keys.ActionNameDetach:    {},
	})
	if err != nil {
		t.Fatalf("BuildBindings: %v", err)
	}
	text := strings.Join(helpLines("C-b", true, custom), "\n")
	if !strings.Contains(text, "n     open a new column") {
		t.Errorf("help lacks the primary-key line for new_column:\n%s", text)
	}
	for _, absent := range []string{"enter", "detach"} {
		if strings.Contains(text, absent) {
			t.Errorf("help mentions %q:\n%s", absent, text)
		}
	}
}

// A pair's shared line names both directions, so once one is unbound the
// survivor must fall back to its own description.
func TestUnbindingHalfAPairDropsTheSharedLine(t *testing.T) {
	custom, err := keys.BuildBindings(map[string][]string{keys.ActionNameFocusRight: {}})
	if err != nil {
		t.Fatalf("BuildBindings: %v", err)
	}
	text := strings.Join(helpLines("C-b", true, custom), "\n")
	if !strings.Contains(text, "h     focus the column to the left") {
		t.Errorf("help lacks focus_left's own line:\n%s", text)
	}
	if strings.Contains(text, "focus the column left / right") {
		t.Errorf("help still advertises the unbound direction:\n%s", text)
	}
}
