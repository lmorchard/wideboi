// Package testenv keeps a test binary away from the user's real wideboi
// sessions.
//
// The suite runs inside wideboi: agents run it from a pane of the very
// session they are hosted in. Anything that resolves the default session
// -- a command dispatched with no socket, the session directory, the
// auto-cleanup sweep -- would otherwise reach the live one. A palette
// test that typed "quit" ended its own host that way (#277).
package testenv

import (
	"fmt"
	"os"
	"testing"
)

// IsolatedVar names the private directory Run set up, so a test can
// assert the isolation is in place.
const IsolatedVar = "WIDEBOI_TEST_ISOLATED"

// Run is a TestMain body: it points TMPDIR -- and with it the session
// directory, config.DefaultSocketPath and every temp dir -- at a private
// directory, clears the variables that name a session, runs the tests
// and removes the directory. Child processes inherit all of it.
//
//	func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }
//
// The directory is under /tmp, not the inherited TMPDIR: darwin's
// /var/folders paths are long enough to push test sockets past the 104
// bytes a unix socket path may have.
func Run(m *testing.M) int {
	dir, err := os.MkdirTemp("/tmp", "wbtest")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testenv: cannot create a private TMPDIR:", err)
		return 1
	}
	defer os.RemoveAll(dir)

	for k, v := range map[string]string{"TMPDIR": dir, IsolatedVar: dir} {
		if err := os.Setenv(k, v); err != nil {
			fmt.Fprintln(os.Stderr, "testenv:", err)
			return 1
		}
	}
	for _, k := range []string{"WIDEBOI_SOCK", "WIDEBOI_SESSION", "WIDEBOI", "LC_WIDEBOI", "WIDEBOI_PANE_ID"} {
		_ = os.Unsetenv(k)
	}
	return m.Run()
}
