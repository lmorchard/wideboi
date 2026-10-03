package client

import (
	"fmt"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/keys"
	"github.com/lmorchard/wideboi/internal/protocol"
)

type badgeSpan struct {
	paneID  int
	isFocus bool
	status  protocol.PaneStatus
	badge   string
	startX  int
	width   int
}

func (c *Client) badgeSpansLocked(budget int) []badgeSpan {
	ids := c.openPaneIDsLocked()
	spans := make([]badgeSpan, 0, len(ids))
	x := 0
	for i, id := range ids {
		isFocus := id == c.focusPaneID
		st := c.displayStatusLocked(id)
		badge := FormatBadge(id, isFocus, st)
		needed := runeLen(badge)
		if i > 0 {
			needed++
		}
		if x+needed > budget {
			break
		}
		if i > 0 {
			x++
		}
		w := runeLen(badge)
		spans = append(spans, badgeSpan{
			paneID:  id,
			isFocus: isFocus,
			status:  st,
			badge:   badge,
			startX:  x,
			width:   w,
		})
		x += w
	}
	return spans
}

func formatScrollTag(offset int, unread bool) string {
	if unread {
		return fmt.Sprintf(" [scroll +%d ⤓]", offset)
	}
	return fmt.Sprintf(" [scroll +%d]", offset)
}

// drawStatusBarLocked paints the bottom row(s) of the screen.
func (c *Client) drawStatusBarLocked(scr uv.Screen) {
	budget := c.cols - 1
	if budget <= 0 {
		return
	}
	y := c.rows - 1

	if c.search != nil {
		statusText := truncateRunes(c.searchStatusLocked(), budget)
		compose.WriteStyled(scr, 0, y, statusText, uv.Style{Attrs: uv.AttrReverse})
		return
	}

	if c.prompt != nil {
		statusText := truncateRunes(c.promptStatusLocked(), budget)
		compose.WriteStyled(scr, 0, y, statusText, uv.Style{Attrs: uv.AttrReverse})
		return
	}

	if c.controlMode {
		if c.rows >= 3 {
			c.drawControlHintsLocked(scr, budget, c.rows-2)
			c.drawNormalStatusBarLocked(scr, budget, y)
		} else {
			c.drawControlHintsLocked(scr, budget, y)
		}
		return
	}

	c.drawNormalStatusBarLocked(scr, budget, y)
}

func (c *Client) drawNormalStatusBarLocked(scr uv.Screen, budget, y int) {
	// Clear the status row budget with blank spaces first.
	compose.WriteString(scr, 0, y, strings.Repeat(" ", budget))

	spans := c.badgeSpansLocked(budget)
	x := 0
	for _, span := range spans {
		if span.startX > x {
			compose.WriteString(scr, x, y, " ")
		}
		c.writeBadgeLocked(scr, span.startX, y, span.paneID, span.isFocus, span.status)
		x = span.startX + span.width
	}

	// Scroll indicator if focused pane is scrolled
	if pu, ok := c.paneUpdates[c.focusPaneID]; ok && pu.ScrollOffset > 0 {
		scrollTag := formatScrollTag(pu.ScrollOffset, pu.UnreadOutput)
		if x+runeLen(scrollTag) <= budget {
			compose.WriteString(scr, x, y, scrollTag)
			x += runeLen(scrollTag)
		}
	}

	// Right side: layout mode & key hint
	mode := c.layoutModeLocked().String()
	fullRight := mode + " · " + c.prefixLabel + " for commands"
	compactRight := mode

	if pad := budget - x - runeLen(fullRight); pad >= 2 {
		rightX := budget - runeLen(fullRight)
		compose.WriteStyled(scr, rightX, y, fullRight, c.theme.Dim)
	} else if pad := budget - x - runeLen(compactRight); pad >= 2 {
		rightX := budget - runeLen(compactRight)
		compose.WriteStyled(scr, rightX, y, compactRight, c.theme.Dim)
	}
}

// statusBarBadgeAtLocked returns the pane ID of the badge at column clickX on
// the status bar, or 0 if none was hit. c.mu must be held.
func (c *Client) statusBarBadgeAtLocked(clickX int) int {
	for _, span := range c.badgeSpansLocked(c.cols - 1) {
		if clickX >= span.startX && clickX < span.startX+span.width {
			return span.paneID
		}
	}
	return 0
}

func (c *Client) drawControlHintsLocked(scr uv.Screen, budget, y int) {
	// Fill row budget with the hints background.
	compose.WriteStyled(scr, 0, y, strings.Repeat(" ", budget), c.theme.ControlHints)

	items := controlHelpItems(budget, c.detachable, c.bindings)
	x := 0
	for i, item := range items {
		if i > 0 {
			if x+2 > budget {
				break
			}
			compose.WriteStyled(scr, x, y, "  ", c.theme.ControlHints)
			x += 2
		}
		itemLen := runeLen(item)
		if x+itemLen > budget {
			break
		}
		if idx := strings.Index(item, " "); idx > 0 {
			key := item[:idx]
			desc := item[idx:]
			compose.WriteStyled(scr, x, y, key, c.theme.ControlKey)
			x += runeLen(key)
			compose.WriteStyled(scr, x, y, desc, c.theme.ControlDesc)
			x += runeLen(desc)
		} else {
			compose.WriteStyled(scr, x, y, item, c.theme.ControlKey)
			x += itemLen
		}
	}
}

// controlHelpItems returns the menu entries that fit in budget.
func controlHelpItems(budget int, detachable bool, custom ...[]keys.Binding) []string {
	bindings := keys.Bindings
	if len(custom) > 0 && len(custom[0]) > 0 {
		bindings = custom[0]
	}
	droppable, essential := keys.BarItemsFor(bindings, detachable)

	tail := strings.Join(essential, "  ")
	used := runeLen(tail)

	taken := make([]string, 0, len(droppable))
	for _, v := range droppable {
		if used+2+runeLen(v) > budget {
			break
		}
		used += 2 + runeLen(v)
		taken = append(taken, v)
	}

	return append(taken, essential...)
}

func controlHelp(budget int, detachable bool, custom ...[]keys.Binding) string {
	items := controlHelpItems(budget, detachable, custom...)
	return strings.Join(items, "  ")
}
