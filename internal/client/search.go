package client

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lmorchard/wideboi/internal/protocol"
)

type historyMatch struct{ row, col int }

// Search is a client-local mode. The server supplies a read-only history
// snapshot and retains only its existing per-client scroll offset.
type searchState struct {
	paneID, priorOffset, priorHistoryLen int
	query                                string
	input                                bool
	waiting                              bool
	pending                              int // 0: first match, +1/-1: navigate
	matches                              []historyMatch
	selected                             int
}

func findHistoryMatches(rows []string, query string) []historyMatch {
	if query == "" {
		return nil
	}
	var matches []historyMatch
	for y, row := range rows {
		for start := 0; start < len(row); {
			idx := strings.Index(row[start:], query)
			if idx < 0 {
				break
			}
			pos := start + idx
			matches = append(matches, historyMatch{y, utf8.RuneCountInString(row[:pos])})
			start = pos + len(query)
		}
	}
	return matches
}

func (c *Client) StartSearch() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.focusPaneID == 0 {
		return
	}
	pu := c.paneUpdates[c.focusPaneID]
	c.search = &searchState{paneID: c.focusPaneID, priorOffset: pu.ScrollOffset,
		priorHistoryLen: pu.ScrollbackLen, input: true, selected: -1}
}

func (c *Client) SearchEdit(text string, backspace bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.search; s != nil && s.input {
		if backspace && s.query != "" {
			_, n := utf8.DecodeLastRuneInString(s.query)
			s.query = s.query[:len(s.query)-n]
		} else if !backspace {
			s.query += text
		}
	}
}

func (c *Client) SearchCommit(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.search; s != nil && s.input {
		s.input = false
		s.pending = 0
		s.waiting = true
		if !c.transport.SendClient(ctx, protocol.MsgHistoryRequest{PaneID: s.paneID}) {
			s.waiting = false
		}
	}
}

func (c *Client) SearchNavigate(ctx context.Context, direction int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.search; s != nil && !s.input && !s.waiting {
		s.pending = direction
		s.waiting = true
		if !c.transport.SendClient(ctx, protocol.MsgHistoryRequest{PaneID: s.paneID}) {
			s.waiting = false
		}
	}
}

func (c *Client) SearchEnd(ctx context.Context, restore, live bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.search
	if s == nil {
		return
	}
	if restore {
		c.scrollSearchToLocked(ctx, s, s.priorOffset, s.priorHistoryLen, s.priorOffset > 0)
	} else if live {
		c.scrollSearchToLocked(ctx, s, 0, 0, false)
	}
	c.search = nil
}

func (c *Client) applyHistoryLocked(snapshot protocol.MsgHistorySnapshot) {
	s := c.search
	if s == nil || s.paneID != snapshot.PaneID || s.input || !s.waiting {
		return
	}
	s.waiting = false
	s.matches = findHistoryMatches(snapshot.Rows, s.query)
	if len(s.matches) == 0 {
		s.selected = -1
		return
	}
	if s.selected < 0 || s.pending == 0 {
		s.selected = len(s.matches) - 1
	} else {
		s.selected = (s.selected + s.pending + len(s.matches)) % len(s.matches)
	}
	match := s.matches[s.selected]
	target := snapshot.ScrollbackLen - match.row
	target = max(0, min(target, snapshot.ScrollbackLen))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	c.scrollSearchToLocked(ctx, s, target, snapshot.ScrollbackLen, true)
}

func (c *Client) scrollSearchToLocked(ctx context.Context, s *searchState, target, historyLen int, anchor bool) {
	c.transport.SendClient(ctx, protocol.MsgScroll{PaneID: s.paneID, SetAbsolute: true, Offset: target,
		AnchorHistory: anchor, HistoryLen: historyLen})
}

func (c *Client) searchStatusLocked() string {
	s := c.search
	if s.input {
		return fmt.Sprintf("search /%s_  Enter find · Esc cancel", s.query)
	}
	if s.waiting {
		return fmt.Sprintf("search /%s  searching…", s.query)
	}
	if len(s.matches) == 0 {
		return fmt.Sprintf("search /%s  no match · Esc restore · Ctrl+g live", s.query)
	}
	m := s.matches[s.selected]
	return fmt.Sprintf("search %d/%d row %d char %d /%s · n/N next/prev · Enter keep · Esc restore · Ctrl+g live",
		s.selected+1, len(s.matches), m.row+1, m.col+1, s.query)
}
