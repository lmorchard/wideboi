package server

import (
	"fmt"
	"sort"
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
	mu           sync.Mutex
	selected     int
	scrollOffset int
	panes        []PaneInfo
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

// statusPriority assigns an urgency score to a PaneStatus for ordering
// panes in the dashboard overview. Higher urgency appears first.
func statusPriority(st protocol.PaneStatus) int {
	switch st {
	case protocol.StatusNeedsInput:
		return 5
	case protocol.StatusFailed:
		return 4
	case protocol.StatusDone:
		return 3
	case protocol.StatusWorking:
		return 2
	case protocol.StatusIdle:
		return 1
	default:
		return 0
	}
}

// padOrTruncate pads s with spaces to target width, or truncates by runes to fit.
func padOrTruncate(s string, target int) string {
	if target <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) < target {
		return s + strings.Repeat(" ", target-len(runes))
	}
	if len(runes) > target {
		return string(runes[:target])
	}
	return s
}

// Render formats the dashboard table into ANSI escape sequences and text
// ready to be written to a VT emulator grid.
func (d *Dashboard) Render(panes []PaneInfo, cols, rows int) []byte {
	d.mu.Lock()
	defer d.mu.Unlock()

	prevSelectedID := 0
	if len(d.panes) > 0 && d.selected >= 0 && d.selected < len(d.panes) {
		prevSelectedID = d.panes[d.selected].ID
	}

	d.panes = make([]PaneInfo, len(panes))
	copy(d.panes, panes)

	// Sort panes by urgency (highest priority first), breaking ties by ID.
	sort.SliceStable(d.panes, func(i, j int) bool {
		pi := statusPriority(d.panes[i].Status)
		pj := statusPriority(d.panes[j].Status)
		if pi != pj {
			return pi > pj
		}
		return d.panes[i].ID < d.panes[j].ID
	})

	// Vertical viewport sizing: 1 banner row + 1 header row + 2 footer rows = 4 chrome rows.
	maxVisible := rows - 4
	if maxVisible < 1 {
		maxVisible = 1
	}

	if len(d.panes) == 0 {
		d.selected = 0
		d.scrollOffset = 0
	} else {
		found := false
		if prevSelectedID > 0 {
			for i, p := range d.panes {
				if p.ID == prevSelectedID {
					d.selected = i
					found = true
					break
				}
			}
		}
		if !found {
			if d.selected >= len(d.panes) {
				d.selected = len(d.panes) - 1
			}
			if d.selected < 0 {
				d.selected = 0
			}
		}

		// Adjust scrollOffset to keep d.selected within the visible window [scrollOffset, scrollOffset+maxVisible).
		if d.selected < d.scrollOffset {
			d.scrollOffset = d.selected
		}
		if d.selected >= d.scrollOffset+maxVisible {
			d.scrollOffset = d.selected - maxVisible + 1
		}
		if maxOffset := len(d.panes) - maxVisible; d.scrollOffset > maxOffset {
			if maxOffset < 0 {
				maxOffset = 0
			}
			d.scrollOffset = maxOffset
		}
		if d.scrollOffset < 0 {
			d.scrollOffset = 0
		}
	}

	var sb strings.Builder
	// Hide cursor, enable SGR mouse tracking, clear screen, move home
	sb.WriteString("\x1b[?25l\x1b[?1000h\x1b[?1006h\x1b[2J\x1b[H")

	// Fleet summary header banner
	var needsInputCount, failedCount, doneCount, workingCount, idleCount int
	for _, p := range d.panes {
		switch p.Status {
		case protocol.StatusNeedsInput:
			needsInputCount++
		case protocol.StatusFailed:
			failedCount++
		case protocol.StatusDone:
			doneCount++
		case protocol.StatusWorking:
			workingCount++
		default:
			idleCount++
		}
	}

	var summaryParts []string
	if needsInputCount > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d needs input", needsInputCount))
	}
	if failedCount > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d failed", failedCount))
	}
	if workingCount > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d working", workingCount))
	}
	if doneCount > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d done", doneCount))
	}
	if idleCount > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d idle", idleCount))
	}

	summary := strings.Join(summaryParts, "  •  ")
	if len(d.panes) == 0 {
		summary = "no active panes"
	}

	banner := fmt.Sprintf("  [ wideboi dashboard ]  %s", summary)
	banner = padOrTruncate(banner, cols)
	sb.WriteString("\x1b[1;36m" + banner + "\x1b[0m\r\n")

	// Table column header
	header := fmt.Sprintf("  %-9s %-12s %-24s %s", "PANE ID", "STATUS", "TITLE", "CWD")
	header = padOrTruncate(header, cols)
	sb.WriteString("\x1b[7m" + header + "\x1b[0m\r\n")

	if len(d.panes) == 0 {
		msg := padOrTruncate("   (no active terminal panes)", cols)
		sb.WriteString("\r\n" + msg + "\r\n")
	} else {
		startIdx := d.scrollOffset
		endIdx := startIdx + maxVisible
		if endIdx > len(d.panes) {
			endIdx = len(d.panes)
		}

		for i := startIdx; i < endIdx; i++ {
			p := d.panes[i]
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
			if len([]rune(title)) > 24 {
				title = string([]rune(title)[:21]) + "..."
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
			line = padOrTruncate(line, cols)
			sb.WriteString(rowStart + line + rowEnd + "\r\n")
		}
	}

	// Instructions footer
	footer := "  [j/k/↑/↓] Select   [Enter/Click] Jump   [C-b x] Close"
	footer = padOrTruncate(footer, cols)
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

// HandleInput processes a protocol.MsgInput event on the dashboard pane.
// It decodes raw bytes or encoded Key to a KeyEvent and dispatches to HandleKey.
func (d *Dashboard) HandleInput(m protocol.MsgInput) (int, bool) {
	var key uv.KeyEvent
	if !m.Key.IsZero() {
		key = m.Key.Decode()
	} else if len(m.Data) > 0 {
		if len(m.Data) == 1 && (m.Data[0] == '\r' || m.Data[0] == '\n') {
			key = uv.KeyPressEvent{Code: 13}
		} else if len(m.Data) == 1 && m.Data[0] == 'j' {
			key = uv.KeyPressEvent{Code: 'j'}
		} else if len(m.Data) == 1 && m.Data[0] == 'k' {
			key = uv.KeyPressEvent{Code: 'k'}
		}
	}
	if key == nil {
		return 0, false
	}
	return d.HandleKey(key)
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
			// Header rows are row 0 (summary banner) and row 1 (column header).
			// Data rows start at row 2, offset by scrollOffset.
			clickedRow := ev.Y - 2
			idx := d.scrollOffset + clickedRow
			if clickedRow >= 0 && idx >= 0 && idx < len(d.panes) {
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
