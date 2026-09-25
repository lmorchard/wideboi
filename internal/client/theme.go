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
	HeaderFocus  uv.Style
	Header       uv.Style
	ControlHints uv.Style
	ControlKey   uv.Style
	ControlDesc  uv.Style
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
		HeaderFocus:  uv.Style{Bg: ansi.IndexedColor(236)},                  // Charcoal gray background
		Header:       uv.Style{},                                            // Default
		ControlHints: uv.Style{Bg: ansi.IndexedColor(236)},                  // Charcoal gray background
		ControlKey:   uv.Style{Fg: ansi.BasicColor(14), Bg: ansi.IndexedColor(236), Attrs: uv.AttrBold},
		ControlDesc:  uv.Style{Bg: ansi.IndexedColor(236), Attrs: uv.AttrFaint},
		NoColor:      noColor,
	}

	if noColor {
		t.Working.Fg = nil
		t.NeedsInput.Fg = nil
		t.Done.Fg = nil
		t.Failed.Fg = nil
		t.Focus.Fg = nil
		t.FocusDivider.Fg = nil
		t.HeaderFocus.Bg = nil
		t.HeaderFocus.Attrs |= uv.AttrBold
		t.ControlHints.Bg = nil
		t.ControlKey.Fg = nil
		t.ControlKey.Bg = nil
		t.ControlDesc.Bg = nil
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
	if s, ok := parseStyleString(cfg.HeaderFocus); ok {
		t.HeaderFocus = s
	}
	if s, ok := parseStyleString(cfg.Header); ok {
		t.Header = s
	}
	if s, ok := parseStyleString(cfg.ControlHints); ok {
		t.ControlHints = s
	}
	if s, ok := parseStyleString(cfg.ControlKey); ok {
		t.ControlKey = s
	}
	if s, ok := parseStyleString(cfg.ControlDesc); ok {
		t.ControlDesc = s
	}

	return t
}

func parseColor(val string) (ansi.Color, bool) {
	switch val {
	case "black":
		return ansi.BasicColor(0), true
	case "red":
		return ansi.BasicColor(1), true
	case "green":
		return ansi.BasicColor(2), true
	case "yellow":
		return ansi.BasicColor(3), true
	case "blue":
		return ansi.BasicColor(4), true
	case "magenta":
		return ansi.BasicColor(5), true
	case "cyan":
		return ansi.BasicColor(6), true
	case "white":
		return ansi.BasicColor(7), true
	case "bright_black", "bright-black", "gray", "grey":
		return ansi.BasicColor(8), true
	case "bright_red", "bright-red":
		return ansi.BasicColor(9), true
	case "bright_green", "bright-green":
		return ansi.BasicColor(10), true
	case "bright_yellow", "bright-yellow":
		return ansi.BasicColor(11), true
	case "bright_blue", "bright-blue":
		return ansi.BasicColor(12), true
	case "bright_magenta", "bright-magenta":
		return ansi.BasicColor(13), true
	case "bright_cyan", "bright-cyan":
		return ansi.BasicColor(14), true
	case "bright_white", "bright-white":
		return ansi.BasicColor(15), true
	default:
		if idx, err := strconv.Atoi(val); err == nil && idx >= 0 && idx <= 255 {
			if idx < 16 {
				return ansi.BasicColor(idx), true
			}
			return ansi.IndexedColor(idx), true
		}
		return nil, false
	}
}

func parseStyleString(val string) (uv.Style, bool) {
	val = strings.TrimSpace(strings.ToLower(val))
	if val == "" {
		return uv.Style{}, false
	}

	var style uv.Style
	parts := strings.Fields(val)
	for _, p := range parts {
		switch {
		case p == "bold":
			style.Attrs |= uv.AttrBold
		case p == "faint" || p == "dim":
			style.Attrs |= uv.AttrFaint
		case p == "reverse" || p == "invert":
			style.Attrs |= uv.AttrReverse
		case strings.HasPrefix(p, "bg:"):
			if c, ok := parseColor(strings.TrimPrefix(p, "bg:")); ok {
				style.Bg = c
			}
		default:
			if c, ok := parseColor(p); ok {
				style.Fg = c
			}
		}
	}
	return style, true
}
