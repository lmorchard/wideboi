package client

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/commands"
)

type paletteState struct {
	query    string
	commands []commands.Command
	matches  []commands.Command
	selected int
}

// InPalette reports whether the command palette overlay is active.
func (c *Client) InPalette() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.palette != nil
}

// StartPalette opens the command palette overlay.
func (c *Client) StartPalette() {
	c.mu.Lock()
	defer c.mu.Unlock()
	all := commands.DefaultRegistry.All()
	c.palette = &paletteState{
		commands: all,
		matches:  all,
		selected: 0,
	}
}

// PaletteEdit updates the palette search filter.
func (c *Client) PaletteEdit(text string, backspace bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.palette
	if s == nil {
		return
	}
	if backspace && s.query != "" {
		_, n := utf8.DecodeLastRuneInString(s.query)
		s.query = s.query[:len(s.query)-n]
	} else if !backspace {
		s.query += text
	}
	s.matches = filterPaletteCommands(s.commands, s.query)
	if s.selected >= len(s.matches) {
		s.selected = max(0, len(s.matches)-1)
	}
}

// PaletteNavigate moves the selection up (-1) or down (+1).
func (c *Client) PaletteNavigate(dir int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.palette
	if s == nil || len(s.matches) == 0 {
		return
	}
	s.selected += dir
	if s.selected < 0 {
		s.selected = len(s.matches) - 1
	} else if s.selected >= len(s.matches) {
		s.selected = 0
	}
}

// PaletteCommit runs the selected command and closes the palette.
func (c *Client) PaletteCommit(ctx context.Context, inv commands.Invocation) error {
	c.mu.Lock()
	s := c.palette
	if s == nil {
		c.mu.Unlock()
		return nil
	}
	var targetCmd string
	if len(s.matches) > 0 && s.selected >= 0 && s.selected < len(s.matches) {
		targetCmd = s.matches[s.selected].Name
	}
	c.palette = nil
	if inv.CallerPaneID == 0 {
		inv.CallerPaneID = c.focusPaneID
	}
	c.mu.Unlock()

	if targetCmd == "" {
		return nil
	}
	return commands.DefaultRegistry.Execute(ctx, inv, targetCmd)
}

// SelectedPaletteCommand returns the name of the currently selected command in the palette.
func (c *Client) SelectedPaletteCommand() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.palette != nil && len(c.palette.matches) > 0 && c.palette.selected >= 0 && c.palette.selected < len(c.palette.matches) {
		return c.palette.matches[c.palette.selected].Name
	}
	return ""
}

// PaletteCancel closes the command palette overlay.
func (c *Client) PaletteCancel() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.palette = nil
}

func filterPaletteCommands(cmds []commands.Command, query string) []commands.Command {
	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" {
		return cmds
	}
	var out []commands.Command
	for _, cmd := range cmds {
		if strings.Contains(strings.ToLower(cmd.Name), q) ||
			strings.Contains(strings.ToLower(cmd.Description), q) ||
			strings.Contains(strings.ToLower(cmd.Category), q) {
			out = append(out, cmd)
		}
	}
	return out
}

func drawPaletteOverlay(scr uv.Screen, cols, rows int, s *paletteState) {
	if cols <= 0 || rows <= 0 || s == nil {
		return
	}

	boxW := 60
	if maxW := cols - 1; boxW > maxW {
		boxW = maxW
	}
	boxH := 16
	if boxH > rows {
		boxH = rows
	}
	if boxW < 10 || boxH < 5 {
		return
	}

	x0 := (cols - boxW) / 2
	y0 := (rows - boxH) / 2
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}

	style := uv.Style{Attrs: uv.AttrReverse}
	contentW := boxW - 4

	title := " Command Palette "
	padTitle := boxW - 2 - runeLen(title)
	if padTitle < 0 {
		padTitle = 0
	}
	leftPad := padTitle / 2
	rightPad := padTitle - leftPad

	top := "┌" + strings.Repeat("─", leftPad) + title + strings.Repeat("─", rightPad) + "┐"
	bottom := "└" + strings.Repeat("─", boxW-2) + "┘"
	sep := "├" + strings.Repeat("─", boxW-2) + "┤"

	compose.WriteStyled(scr, x0, y0, top, style)
	compose.WriteStyled(scr, x0, y0+boxH-1, bottom, style)

	// Search input line
	searchPrompt := "> " + s.query + "_"
	lineText := truncateRunes(searchPrompt, contentW)
	if pad := contentW - runeLen(lineText); pad > 0 {
		lineText += strings.Repeat(" ", pad)
	}
	compose.WriteStyled(scr, x0, y0+1, "│ "+lineText+" │", style)
	compose.WriteStyled(scr, x0, y0+2, sep, style)

	// Items list
	maxItems := boxH - 4
	visibleMatches := s.matches
	scrollOffset := 0
	if s.selected >= maxItems {
		scrollOffset = s.selected - maxItems + 1
	}
	if scrollOffset > 0 && scrollOffset < len(visibleMatches) {
		visibleMatches = visibleMatches[scrollOffset:]
	}

	for i := 0; i < maxItems; i++ {
		curY := y0 + 3 + i
		if i < len(visibleMatches) {
			actualIdx := scrollOffset + i
			cmd := visibleMatches[i]
			isSelected := actualIdx == s.selected

			itemText := fmt.Sprintf("%-14s %s", cmd.Name, cmd.Description)
			truncated := truncateRunes(itemText, contentW)
			if pad := contentW - runeLen(truncated); pad > 0 {
				truncated += strings.Repeat(" ", pad)
			}

			if isSelected {
				compose.WriteStyled(scr, x0, curY, "│ ", style)
				compose.WriteStyled(scr, x0+2, curY, truncated, uv.Style{Attrs: uv.AttrBold})
				compose.WriteStyled(scr, x0+2+contentW, curY, " │", style)
			} else {
				compose.WriteStyled(scr, x0, curY, "│ "+truncated+" │", style)
			}
		} else if i == 0 && len(s.matches) == 0 {
			msg := "(no matching commands)"
			truncated := truncateRunes(msg, contentW)
			if pad := contentW - runeLen(truncated); pad > 0 {
				truncated += strings.Repeat(" ", pad)
			}
			compose.WriteStyled(scr, x0, curY, "│ "+truncated+" │", style)
		} else {
			blank := strings.Repeat(" ", contentW)
			compose.WriteStyled(scr, x0, curY, "│ "+blank+" │", style)
		}
	}
}
