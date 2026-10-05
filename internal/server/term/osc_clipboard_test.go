package term_test

import (
	"testing"

	"github.com/lmorchard/wideboi/internal/server/term"
)

func TestOSC52CallsOnClipboard(t *testing.T) {
	g := term.NewVT(40, 10)
	defer g.Close()

	var got []string
	g.OnClipboard(func(text string) { got = append(got, text) })

	// aGVsbG8= is "hello", d29ybGQ= is "world". The read query in the
	// middle must not reach the callback: answering it would hand the
	// host clipboard to the pane.
	seq := "\x1b]52;c;aGVsbG8=\x07" + "\x1b]52;c;?\x07" + "\x1b]52;c;d29ybGQ=\x1b\\"
	if _, err := g.Write([]byte(seq)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(got) != 2 || got[0] != "hello" || got[1] != "world" {
		t.Fatalf("OnClipboard got %q, want [hello world]", got)
	}
}
