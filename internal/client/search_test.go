package client

import (
	"context"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

func searchClient(t *testing.T, offset, historyLen int) (*Client, *transport.InProcChannel) {
	t.Helper()
	tp := transport.NewInProcChannel(32)
	c := NewClient(tp, 80, 24, "C-b")
	c.HandleServerMsg(protocol.MsgLayoutSnapshot{Columns: []protocol.ColumnData{{PaneID: 1, Width: 40, Height: 3}}})
	c.HandleServerMsg(protocol.MsgPaneUpdate{PaneID: 1, Cols: 40, Rows: 3, ScrollOffset: offset, ScrollbackLen: historyLen})
	return c, tp
}

func takeSearchMessage(t *testing.T, tp *transport.InProcChannel) any {
	t.Helper()
	select {
	case m := <-tp.ClientSend:
		return m
	default:
		t.Fatal("missing client message")
		return nil
	}
}

func TestSearchScreenScrollbackNavigationAndRestore(t *testing.T) {
	c, tp := searchClient(t, 2, 4)
	ctx := context.Background()
	c.StartSearch()
	c.SearchEdit("needle", false)
	c.SearchCommit(ctx)
	_ = takeSearchMessage(t, tp)
	c.HandleServerMsg(protocol.MsgHistorySnapshot{PaneID: 1, ScrollbackLen: 4,
		Rows: []string{"needle old", "other", "needle middle", "other", "needle screen", "other", ""}})
	if got := takeSearchMessage(t, tp).(protocol.MsgScroll); !got.SetAbsolute || got.Offset != 0 {
		t.Fatalf("screen match scroll = %+v, want absolute 0", got)
	}
	if c.search.selected != 2 || len(c.search.matches) != 3 {
		t.Fatalf("matches = %+v selected %d", c.search.matches, c.search.selected)
	}
	c.SearchNavigate(ctx, -1)
	_ = takeSearchMessage(t, tp)
	c.HandleServerMsg(protocol.MsgHistorySnapshot{PaneID: 1, ScrollbackLen: 4,
		Rows: []string{"needle old", "other", "needle middle", "other", "needle screen", "other", ""}})
	if got := takeSearchMessage(t, tp).(protocol.MsgScroll); !got.SetAbsolute || got.Offset != 2 {
		t.Fatalf("previous scroll = %+v, want absolute 2", got)
	}
	c.SearchEnd(ctx, true, false)
	if got := takeSearchMessage(t, tp).(protocol.MsgScroll); !got.SetAbsolute || got.Offset != 2 || c.search != nil {
		t.Fatalf("restore = %+v, want original offset 2 and closed search", got)
	}
}

func TestSearchNoMatchAndHistoryReplacement(t *testing.T) {
	c, tp := searchClient(t, 0, 3)
	ctx := context.Background()
	c.StartSearch()
	c.SearchEdit("本", false)
	c.SearchCommit(ctx)
	_ = takeSearchMessage(t, tp)
	c.HandleServerMsg(protocol.MsgHistorySnapshot{PaneID: 1, ScrollbackLen: 3, Rows: []string{"old", "line", "gone", "live"}})
	if len(tp.ClientSend) != 0 || !strings.Contains(c.searchStatusLocked(), "no match") {
		t.Fatal("no-match search scrolled or hid its status")
	}
	c.SearchNavigate(ctx, 1)
	_ = takeSearchMessage(t, tp)
	c.HandleServerMsg(protocol.MsgHistorySnapshot{PaneID: 1, ScrollbackLen: 3, Rows: []string{"new", "本 here", "line", "live"}})
	if got := takeSearchMessage(t, tp).(protocol.MsgScroll); !got.SetAbsolute || got.Offset != 2 {
		t.Fatalf("new history scroll = %+v, want absolute 2", got)
	}
	if c.search.matches[0].row != 1 {
		t.Fatalf("match after replacement = %+v", c.search.matches)
	}
}

func TestTwoClientsSearchSamePaneIndependently(t *testing.T) {
	ctx := context.Background()
	first, firstWire := searchClient(t, 0, 4)
	second, secondWire := searchClient(t, 0, 4)
	for _, tc := range []struct {
		c     *Client
		wire  *transport.InProcChannel
		query string
	}{
		{first, firstWire, "alpha"}, {second, secondWire, "beta"},
	} {
		tc.c.StartSearch()
		tc.c.SearchEdit(tc.query, false)
		tc.c.SearchCommit(ctx)
		if req := takeSearchMessage(t, tc.wire).(protocol.MsgHistoryRequest); req.PaneID != 1 {
			t.Fatalf("request = %+v", req)
		}
	}
	snapshot := protocol.MsgHistorySnapshot{PaneID: 1, ScrollbackLen: 4,
		Rows: []string{"alpha", "beta", "other", "other", "live", "", ""}}
	first.HandleServerMsg(snapshot)
	second.HandleServerMsg(snapshot)
	if got := takeSearchMessage(t, firstWire).(protocol.MsgScroll); !got.SetAbsolute || got.Offset != 4 {
		t.Fatalf("first scroll = %+v", got)
	}
	if got := takeSearchMessage(t, secondWire).(protocol.MsgScroll); !got.SetAbsolute || got.Offset != 3 {
		t.Fatalf("second scroll = %+v", got)
	}
	first.SearchEnd(ctx, false, true)
	if got := takeSearchMessage(t, firstWire).(protocol.MsgScroll); !got.SetAbsolute || got.Offset != 0 {
		t.Fatalf("first live scroll = %+v", got)
	}
	if second.search == nil || second.search.query != "beta" {
		t.Fatal("second client's search was changed")
	}
}

func TestFindHistoryMatchesPhysicalRowsAndUnicode(t *testing.T) {
	rows := []string{"error 本", "er", "ror", "error error"}
	got := findHistoryMatches(rows, "error")
	want := []historyMatch{{0, 0}, {3, 0}, {3, 6}}
	if len(got) != len(want) {
		t.Fatalf("matches = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("match %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if got := findHistoryMatches(rows, "本"); len(got) != 1 || got[0] != (historyMatch{0, 6}) {
		t.Fatalf("Unicode match = %+v", got)
	}
	if got := findHistoryMatches(rows, "error"); got[0].row != 0 {
		t.Fatal("unexpected match")
	}
	if got := findHistoryMatches(rows, "error"+"er"); len(got) != 0 {
		t.Fatalf("matched across physical rows: %+v", got)
	}
}
