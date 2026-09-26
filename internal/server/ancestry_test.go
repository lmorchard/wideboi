package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// comm can contain spaces, and a row that does not parse is dropped
// rather than guessed at: shifted fields once turned a kill path into a
// mass kill, and this parser must never be the start of another.
func TestParsePsTable(t *testing.T) {
	out := strings.Join([]string{
		"    1     0 /sbin/launchd",
		"  412     1 /Applications/Visual Studio Code.app/Contents/MacOS/Visual Studio Code Helper",
		"  500   412 zsh",
		"  bad   412 junk",
		"  600",
		"",
	}, "\n")
	table := parsePsTable(out)
	if len(table) != 3 {
		t.Fatalf("parsed %d rows, want 3: %v", len(table), table)
	}
	if got := table[412]; got.ppid != 1 || !strings.HasSuffix(got.comm, "Visual Studio Code Helper") {
		t.Errorf("row 412 = %+v", got)
	}
	if got := table[500]; got.ppid != 412 || got.comm != "zsh" {
		t.Errorf("row 500 = %+v", got)
	}
}

func TestProcessAncestryOfSelf(t *testing.T) {
	got := processAncestry(os.Getpid())
	names := strings.Split(got, "<-")
	if len(names) < 2 {
		t.Fatalf("ancestry %q: want this process and at least its parent", got)
	}
	// ps truncates comm on some platforms; compare a prefix.
	self := filepath.Base(os.Args[0])
	if len(self) > 15 {
		self = self[:15]
	}
	if !strings.HasPrefix(names[0], self) {
		t.Errorf("ancestry %q does not start with %q", got, self)
	}
}

func TestProcessAncestryUnknownPID(t *testing.T) {
	if got := processAncestry(0); got != "unknown" {
		t.Errorf("processAncestry(0) = %q, want unknown", got)
	}
}

// A shutdown names who asked for it: the requester's pid from its hello.
func TestShutdownRecordsRequesterPID(t *testing.T) {
	asker := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	s := newBareServer(asker)
	s.SetPeerPID(asker, 4242)

	asker.ClientSend <- protocol.MsgShutdown{}
	runLoopUntilReturn(t, s, asker)

	_, attrs := s.CloseReason()
	found := false
	for i := 0; i+1 < len(attrs); i += 2 {
		if attrs[i] == "requesterPID" && attrs[i+1] == uint32(4242) {
			found = true
		}
	}
	if !found {
		t.Errorf("attrs %v lack requesterPID=4242", attrs)
	}
}
