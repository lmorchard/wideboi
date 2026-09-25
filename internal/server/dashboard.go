package server

import (
	"fmt"
	"strings"
	"sync"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/protocol"
)

// PaneInfo captures the metadata and state of an active terminal pane
// for display in the dashboard overview.
type PaneInfo struct {
	ID      int
	Status  protocol.PaneStatus
	Title   string
	CWD     string
	Width   int
	Height  int
	Focused bool
}

// Dashboard manages the in-app overview table of active terminal panes,
// tracking row selection and rendering formatted ANSI output.
type Dashboard struct {
	mu       sync.Mutex
	selected int
	panes    []PaneInfo
}

// NewDashboard creates an initialized status dashboard.
func NewDashboard() *Dashboard {
	return &Dashboard{}
}

// SelectedPaneID returns the ID of the currently selected pane, or 0 if none.
func (d *Dashboard) SelectedPaneID() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.panes) == 0 || d.selected < 0 || d.selected >= len(d.panes) {
		return 0
	}
	return d.panes[d.selected].ID
}

// Render formats the dashboard table into ANSI escape sequences and text
// ready to be written to a VT emulator grid.
func (d *Dashboard) Render(panes []PaneInfo, cols, rows int) []byte {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.panes = make([]PaneInfo, len(panes))
	copy(d.panes, panes)

	if len(d.panes) == 0 {
		d.selected = 0
	} else {
		if d.selected >= len(d.panes) {
			d.selected = len(d.panes) - 1
		}
		if d.selected < 0 {
			d.selected = 0
		}
	}

	var sb strings.Builder
	// Hide cursor, enable SGR mouse tracking, clear screen, move home
	sb.WriteString("\x1b[?25l\x1b[?1000h\x1b[?1006h\x1b[2J\x1b[H")

	// Table header
	header := fmt.Sprintf("  %-9s %-12s %-24s %s", "PANE ID", "STATUS", "TITLE", "CWD")
	if cols > 0 && len(header) < cols {
		header += strings.Repeat(" ", cols-len(header))
	} else if cols > 0 && len(header) > cols {
		header = header[:cols]
	}
	sb.WriteString("\x1b[7m" + header + "\x1b[0m\r\n")

	if len(d.panes) == 0 {
		msg := "   (no active terminal panes)"
		if cols > 0 && len(msg) > cols {
			msg = msg[:cols]
		}
		sb.WriteString("\r\n" + msg + "\r\n")
	} else {
		for i, p := range d.panes {
			marker := "  "
			rowStart := ""
			rowEnd := ""
			if i == d.selected {
				marker = "> "
				rowStart = "\x1b[1;36;7m"
				rowEnd = "\x1b[0m"
			}

			statusStr := formatStatus(p.Status)
			title := p.Title
			if title == "" {
				title = "-"
			}
			if len(title) > 24 {
				title = title[:21] + "..."
			}

			cwd := p.CWD
			if cwd == "" {
				cwd = "-"
			}

			idStr := fmt.Sprintf("[%d]", p.ID)
			if p.Focused {
				idStr += "*"
			}
			line := fmt.Sprintf("%s%-9s %-12s %-24s %s", marker, idStr, statusStr, title, cwd)
			if cols > 0 && len(line) > cols {
				line = line[:cols]
			}
			sb.WriteString(rowStart + line + rowEnd + "\r\n")
		}
	}

	// Instructions footer
	footer := "  [j/k/↑/↓] Select   [Enter/Click] Jump   [C-b x] Close"
	if cols > 0 && len(footer) > cols {
		footer = footer[:cols]
	}
	sb.WriteString("\r\n\x1b[2m" + footer + "\x1b[0m\r\n")

	return []byte(sb.String())
}

func formatStatus(st protocol.PaneStatus) string {
	switch st {
	case protocol.StatusWorking:
		return "▲ working"
	case protocol.StatusNeedsInput:
		return "! input"
	case protocol.StatusDone:
		return "✔ done"
	case protocol.StatusFailed:
		return "✖ failed"
	default:
		return "● idle"
	}
}

// HandleKey processes a keyboard event on the dashboard pane.
// It returns (targetPaneID, handled). If targetPaneID > 0, the client should jump to that pane.
func (d *Dashboard) HandleKey(ev uv.KeyEvent) (int, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.panes) == 0 {
		return 0, false
	}

	switch ev := ev.(type) {
	case uv.KeyPressEvent:
		if ev.MatchString("j", "down") {
			if d.selected < len(d.panes)-1 {
				d.selected++
			}
			return 0, true
		}
		if ev.MatchString("k", "up") {
			if d.selected > 0 {
				d.selected--
			}
			return 0, true
		}
		if ev.MatchString("enter") || ev.Code == uv.KeyEnter || ev.Code == 13 || ev.Code == 10 {
			if d.selected >= 0 && d.selected < len(d.panes) {
				return d.panes[d.selected].ID, true
			}
		}
	}
	return 0, false
}

// HandleMouse processes a mouse event on the dashboard pane.
// It returns (targetPaneID, handled). If targetPaneID > 0, the client should jump to that pane.
func (d *Dashboard) HandleMouse(ev uv.MouseEvent) (int, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.panes) == 0 {
		return 0, false
	}

	switch ev := ev.(type) {
	case uv.MouseClickEvent:
		if ev.Button == uv.MouseLeft {
			// Header is row 0.
			idx := ev.Y - 1
			if idx >= 0 && idx < len(d.panes) {
				d.selected = idx
				return d.panes[d.selected].ID, true
			}
		}
	case uv.MouseWheelEvent:
		if ev.Button == uv.MouseWheelUp {
			if d.selected > 0 {
				d.selected--
				return 0, true
			}
		} else if ev.Button == uv.MouseWheelDown {
			if d.selected < len(d.panes)-1 {
				d.selected++
				return 0, true
			}
		}
	}
	return 0, false
}
