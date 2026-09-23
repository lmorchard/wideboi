package client

import (
	"context"
	"image"
	"slices"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/keys"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// IDs deliberately not in position order, so a test cannot pass by
// confusing the two.
func outOfOrderClient(t *testing.T, cols int, ids ...int) (*Client, *transport.InProcChannel) {
	t.Helper()
	ch := transport.NewInProcChannel(64)
	cli := NewClient(ch, cols, 24, "C-b")
	columns := make([]protocol.ColumnData, len(ids))
	for i, id := range ids {
		columns[i] = protocol.ColumnData{PaneID: id, Width: 40, Height: 22}
	}
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns: columns, FocusPaneID: ids[0], Layout: protocol.LayoutScroll,
	})
	return cli, ch
}

// A digit resolves against the client's strip and goes out as the same
// MsgFocusPane a click sends. Out of range sends nothing.
func TestFocusColumnSendsPaneAtPosition(t *testing.T) {
	cli, ch := outOfOrderClient(t, 100, 5, 2, 9)
	ctx := context.Background()
	for _, tc := range []struct {
		n    int
		want []int
	}{
		{1, []int{5}},
		{2, []int{2}},
		{3, []int{9}},
		{keys.LastColumn, []int{9}},
		{4, nil},
		{0, nil},
	} {
		cli.FocusColumn(ctx, tc.n)
		if got := focusRequests(sent(ch)); !slices.Equal(got, tc.want) {
			t.Errorf("FocusColumn(%d) sent %v, want %v", tc.n, got, tc.want)
		}
	}
}

// The header names the position you would press, not just the ID.
func TestHeaderShowsColumnPosition(t *testing.T) {
	cli, _ := outOfOrderClient(t, 100, 5, 2)
	scr := newFakeHostScreen(100, 24)
	cli.Draw(scr)

	for id, want := range map[int]string{5: " 1 [5]", 2: " 2 [2]"} {
		p := placementFor(cli, id)
		got := regionText(scr, image.Rect(p.Dst.Min.X, 0, p.Dst.Max.X, 1))
		if !strings.HasPrefix(got, want) {
			t.Errorf("pane %d header = %q, want prefix %q", id, got, want)
		}
	}
}
