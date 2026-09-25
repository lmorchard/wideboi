package client

import (
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// The status line names this client's layout (#91): an accidental C-b c
// used to leave a session in scroll mode with nothing on screen to say
// so, and it read as a regression.

func tagClient(mode protocol.LayoutMode) *Client {
	c := NewClient(transport.NewInProcChannel(16), 100, 30, "C-b")
	c.SetLayoutMode(mode)
	c.focusPaneID = 1
	return c
}

const plainStatus = "[● 1  ]"

func TestNormalStatusShowsLayoutMode(t *testing.T) {
	for _, tc := range []struct {
		mode protocol.LayoutMode
		name string
	}{{protocol.LayoutCards, "cards"}, {protocol.LayoutScroll, "scroll"}} {
		got, _ := tagClient(tc.mode).statusLineLocked(99)
		if want := tc.name + " · C-b for commands"; !strings.HasSuffix(got, want) {
			t.Errorf("%s: status line %q does not end with %q", tc.name, got, want)
		}
	}
}

// The hint is a fixed string a user learns once; the mode is live state.
// When only one fits, the tag stays.
func TestNormalStatusDropsHintBeforeLayoutTag(t *testing.T) {
	budget := runeLen(plainStatus) + 2 + runeLen("cards")
	if runeLen(plainStatus)+2+runeLen("cards · C-b for commands") <= budget {
		t.Fatal("test setup bug: tag and hint both fit at this budget")
	}
	got, _ := tagClient(protocol.LayoutCards).statusLineLocked(budget)
	if !strings.HasSuffix(got, "cards") || strings.Contains(got, "for commands") {
		t.Errorf("at %d cells got %q; want the tag kept and the hint dropped", budget, got)
	}
}

func TestNormalStatusDropsLayoutTagWhenNothingFits(t *testing.T) {
	budget := runeLen(plainStatus) + 1
	got, _ := tagClient(protocol.LayoutCards).statusLineLocked(budget)
	if strings.Contains(got, "cards") || strings.Contains(got, "for commands") {
		t.Errorf("at %d cells got %q; want neither tag nor hint", budget, got)
	}
	if !strings.Contains(got, "[● 1") {
		t.Errorf("at %d cells the focus readout is gone: %q", budget, got)
	}
}

func TestToggleLayoutUpdatesStatusTag(t *testing.T) {
	c := tagClient(protocol.LayoutCards)
	c.ToggleLayout()
	if got, _ := c.statusLineLocked(99); !strings.Contains(got, "scroll · ") {
		t.Errorf("after toggling from cards the status line is %q; want the scroll tag", got)
	}
}
