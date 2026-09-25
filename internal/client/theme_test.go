package client_test

import (
	"testing"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/lmorchard/wideboi/internal/client"
	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/protocol"
)

func TestFormatBadgeWidthInvariance(t *testing.T) {
	statuses := []protocol.PaneStatus{
		protocol.StatusIdle,
		protocol.StatusWorking,
		protocol.StatusNeedsInput,
		protocol.StatusDone,
		protocol.StatusFailed,
	}

	for _, id := range []int{1, 5, 42, 100} {
		var expectedLen int
		for i, status := range statuses {
			for _, isFocus := range []bool{false, true} {
				badge := client.FormatBadge(id, isFocus, status)
				w := utf8.RuneCountInString(badge)
				if i == 0 && !isFocus {
					expectedLen = w
				} else if w != expectedLen {
					t.Errorf("badge %q for id=%d, focus=%v, status=%v has rune length %d, want %d",
						badge, id, isFocus, status, w, expectedLen)
				}
			}
		}
	}
}

func TestFormatBadgeExactStrings(t *testing.T) {
	cases := []struct {
		id      int
		isFocus bool
		status  protocol.PaneStatus
		want    string
	}{
		{1, false, protocol.StatusIdle, "[  1  ]"},
		{1, true, protocol.StatusIdle, "[● 1  ]"},
		{1, false, protocol.StatusWorking, "[  1 »]"},
		{1, true, protocol.StatusWorking, "[● 1 »]"},
		{1, false, protocol.StatusNeedsInput, "[  1 !]"},
		{1, true, protocol.StatusNeedsInput, "[● 1 !]"},
		{1, false, protocol.StatusDone, "[  1 ✓]"},
		{1, true, protocol.StatusDone, "[● 1 ✓]"},
		{1, false, protocol.StatusFailed, "[  1 ✗]"},
		{1, true, protocol.StatusFailed, "[● 1 ✗]"},
	}

	for _, tc := range cases {
		got := client.FormatBadge(tc.id, tc.isFocus, tc.status)
		if got != tc.want {
			t.Errorf("FormatBadge(%d, %v, %v) = %q, want %q", tc.id, tc.isFocus, tc.status, got, tc.want)
		}
	}
}

func TestThemeDefaults(t *testing.T) {
	th := client.NewTheme(config.ThemeConfig{}, func(k string) string { return "" })
	if th.NoColor {
		t.Fatal("expected NoColor to be false by default")
	}

	// Working should use ANSI cyan (6)
	wStyle := th.StatusStyle(protocol.StatusWorking)
	if wStyle.Fg != ansi.BasicColor(6) {
		t.Errorf("Working Fg = %v, want ansi.BasicColor(6)", wStyle.Fg)
	}

	// Done should use ANSI green (2)
	dStyle := th.StatusStyle(protocol.StatusDone)
	if dStyle.Fg != ansi.BasicColor(2) {
		t.Errorf("Done Fg = %v, want ansi.BasicColor(2)", dStyle.Fg)
	}

	// NeedsInput should use ANSI yellow (3)
	nStyle := th.StatusStyle(protocol.StatusNeedsInput)
	if nStyle.Fg != ansi.BasicColor(3) {
		t.Errorf("NeedsInput Fg = %v, want ansi.BasicColor(3)", nStyle.Fg)
	}

	// Failed should use ANSI red (1)
	fStyle := th.StatusStyle(protocol.StatusFailed)
	if fStyle.Fg != ansi.BasicColor(1) {
		t.Errorf("Failed Fg = %v, want ansi.BasicColor(1)", fStyle.Fg)
	}
}

func TestThemeNoColor(t *testing.T) {
	th := client.NewTheme(config.ThemeConfig{}, func(k string) string {
		if k == "NO_COLOR" {
			return "1"
		}
		return ""
	})
	if !th.NoColor {
		t.Fatal("expected NoColor to be true when NO_COLOR is set")
	}

	wStyle := th.StatusStyle(protocol.StatusWorking)
	if wStyle.Fg != nil {
		t.Errorf("Working Fg under NO_COLOR = %v, want nil", wStyle.Fg)
	}
}

func TestThemeCustomConfig(t *testing.T) {
	cfg := config.ThemeConfig{
		Working: "magenta",
		Done:    "blue",
	}
	th := client.NewTheme(cfg, func(k string) string { return "" })

	wStyle := th.StatusStyle(protocol.StatusWorking)
	if wStyle.Fg != ansi.BasicColor(5) { // magenta = 5
		t.Errorf("custom Working Fg = %v, want ansi.BasicColor(5)", wStyle.Fg)
	}

	dStyle := th.StatusStyle(protocol.StatusDone)
	if dStyle.Fg != ansi.BasicColor(4) { // blue = 4
		t.Errorf("custom Done Fg = %v, want ansi.BasicColor(4)", dStyle.Fg)
	}
}

func TestThemeHeaderDefaults(t *testing.T) {
	th := client.DefaultTheme()
	if th.HeaderFocus.Bg != ansi.IndexedColor(236) {
		t.Errorf("HeaderFocus.Bg = %v, want ansi.IndexedColor(236)", th.HeaderFocus.Bg)
	}
	if th.HeaderFocus.Attrs&uv.AttrReverse != 0 {
		t.Errorf("HeaderFocus should not have AttrReverse")
	}
}

func TestThemeParseBg(t *testing.T) {
	cfg := config.ThemeConfig{
		HeaderFocus: "bg:236 bold",
		Header:      "bg:black faint",
	}
	th := client.NewTheme(cfg, func(k string) string { return "" })
	if th.HeaderFocus.Bg != ansi.IndexedColor(236) {
		t.Errorf("HeaderFocus.Bg = %v, want ansi.IndexedColor(236)", th.HeaderFocus.Bg)
	}
	if th.HeaderFocus.Attrs&uv.AttrBold == 0 {
		t.Errorf("HeaderFocus should have AttrBold")
	}
	if th.Header.Bg != ansi.BasicColor(0) {
		t.Errorf("Header.Bg = %v, want ansi.BasicColor(0)", th.Header.Bg)
	}
	if th.Header.Attrs&uv.AttrFaint == 0 {
		t.Errorf("Header should have AttrFaint")
	}
}
