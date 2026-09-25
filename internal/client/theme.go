package client

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/protocol"
)

// Theme defines the resolved styling for TUI chrome elements.
type Theme struct {
	Working      uv.Style
	NeedsInput   uv.Style
	Done         uv.Style
	Failed       uv.Style
	Focus        uv.Style
	Dim          uv.Style
	Divider      uv.Style
	FocusDivider uv.Style
	NoColor      bool
}

// FormatBadge formats a fixed-slot badge: "[<focus> <id> <status>]".
// Missing focus is represented as " ", and missing (idle) status is " ",
// ensuring that the width of the badge never changes when focus moves
// or status changes for a given pane ID.
func FormatBadge(id int, isFocus bool, status protocol.PaneStatus) string {
	f, idStr, s := BadgeComponents(id, isFocus, status)
	return fmt.Sprintf("[%s %s %s]", f, idStr, s)
}

// BadgeComponents returns the component strings of a fixed-slot badge:
// focus string ("●" or " "), pane ID string, and status glyph string.
func BadgeComponents(id int, isFocus bool, status protocol.PaneStatus) (focusStr string, idStr string, statusStr string) {
	focusStr = " "
	if isFocus {
		focusStr = "●"
	}
	idStr = strconv.Itoa(id)
	statusStr = " "
	switch status {
	case protocol.StatusWorking:
		statusStr = "»"
	case protocol.StatusNeedsInput:
		statusStr = "!"
	case protocol.StatusDone:
		statusStr = "✓"
	case protocol.StatusFailed:
		statusStr = "✗"
	}
	return focusStr, idStr, statusStr
}

// StatusStyle returns the style for a given pane status.
func (t Theme) StatusStyle(status protocol.PaneStatus) uv.Style {
	if t.NoColor {
		switch status {
		case protocol.StatusWorking, protocol.StatusNeedsInput, protocol.StatusFailed:
			return uv.Style{Attrs: uv.AttrBold}
		default:
			return uv.Style{}
		}
	}
	switch status {
	case protocol.StatusWorking:
		return t.Working
	case protocol.StatusNeedsInput:
		return t.NeedsInput
	case protocol.StatusDone:
		return t.Done
	case protocol.StatusFailed:
		return t.Failed
	default:
		return uv.Style{}
	}
}

// DefaultTheme returns the default Theme with standard ANSI 16 colors.
func DefaultTheme() Theme {
	return NewTheme(config.ThemeConfig{}, nil)
}

// NewTheme constructs a Theme from user config and environment variables.
func NewTheme(cfg config.ThemeConfig, getenv func(string) string) Theme {
	if getenv == nil {
		getenv = os.Getenv
	}

	noColor := getenv("NO_COLOR") != "" || getenv("TERM") == "dumb"

	t := Theme{
		Working:      uv.Style{Fg: ansi.BasicColor(6)},                      // Cyan
		NeedsInput:   uv.Style{Fg: ansi.BasicColor(3), Attrs: uv.AttrBold},  // Yellow + Bold
		Done:         uv.Style{Fg: ansi.BasicColor(2)},                      // Green
		Failed:       uv.Style{Fg: ansi.BasicColor(1), Attrs: uv.AttrBold},  // Red + Bold
		Focus:        uv.Style{Fg: ansi.BasicColor(14), Attrs: uv.AttrBold}, // Bright Cyan + Bold
		Dim:          uv.Style{Attrs: uv.AttrFaint},                         // Dim
		Divider:      uv.Style{Attrs: uv.AttrFaint},                         // Dim
		FocusDivider: uv.Style{Fg: ansi.BasicColor(14), Attrs: uv.AttrBold}, // Bright Cyan + Bold
		NoColor:      noColor,
	}

	if noColor {
		t.Working.Fg = nil
		t.NeedsInput.Fg = nil
		t.Done.Fg = nil
		t.Failed.Fg = nil
		t.Focus.Fg = nil
		t.FocusDivider.Fg = nil
		return t
	}

	// Apply configuration overrides if present.
	if s, ok := parseStyleString(cfg.Working); ok {
		t.Working = s
	}
	if s, ok := parseStyleString(cfg.NeedsInput); ok {
		t.NeedsInput = s
	}
	if s, ok := parseStyleString(cfg.Done); ok {
		t.Done = s
	}
	if s, ok := parseStyleString(cfg.Failed); ok {
		t.Failed = s
	}
	if s, ok := parseStyleString(cfg.Focus); ok {
		t.Focus = s
	}
	if s, ok := parseStyleString(cfg.Dim); ok {
		t.Dim = s
	}
	if s, ok := parseStyleString(cfg.Divider); ok {
		t.Divider = s
	}
	if s, ok := parseStyleString(cfg.FocusDivider); ok {
		t.FocusDivider = s
	}

	return t
}

func parseStyleString(val string) (uv.Style, bool) {
	val = strings.TrimSpace(strings.ToLower(val))
	if val == "" {
		return uv.Style{}, false
	}

	var style uv.Style
	parts := strings.Fields(val)
	for _, p := range parts {
		switch p {
		case "bold":
			style.Attrs |= uv.AttrBold
		case "faint", "dim":
			style.Attrs |= uv.AttrFaint
		case "reverse", "invert":
			style.Attrs |= uv.AttrReverse
		case "black":
			style.Fg = ansi.BasicColor(0)
		case "red":
			style.Fg = ansi.BasicColor(1)
		case "green":
			style.Fg = ansi.BasicColor(2)
		case "yellow":
			style.Fg = ansi.BasicColor(3)
		case "blue":
			style.Fg = ansi.BasicColor(4)
		case "magenta":
			style.Fg = ansi.BasicColor(5)
		case "cyan":
			style.Fg = ansi.BasicColor(6)
		case "white":
			style.Fg = ansi.BasicColor(7)
		case "bright_black", "bright-black", "gray", "grey":
			style.Fg = ansi.BasicColor(8)
		case "bright_red", "bright-red":
			style.Fg = ansi.BasicColor(9)
		case "bright_green", "bright-green":
			style.Fg = ansi.BasicColor(10)
		case "bright_yellow", "bright-yellow":
			style.Fg = ansi.BasicColor(11)
		case "bright_blue", "bright-blue":
			style.Fg = ansi.BasicColor(12)
		case "bright_magenta", "bright-magenta":
			style.Fg = ansi.BasicColor(13)
		case "bright_cyan", "bright-cyan":
			style.Fg = ansi.BasicColor(14)
		case "bright_white", "bright-white":
			style.Fg = ansi.BasicColor(15)
		default:
			if idx, err := strconv.Atoi(p); err == nil && idx >= 0 && idx <= 255 {
				if idx < 16 {
					style.Fg = ansi.BasicColor(idx)
				} else {
					style.Fg = ansi.IndexedColor(idx)
				}
			}
		}
	}
	return style, true
}
