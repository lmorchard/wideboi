package commands_test

import (
	"os"
	"testing"

	"github.com/lmorchard/wideboi/internal/testenv"
)

// Keeps every test here away from the user's real sessions: this suite
// runs inside wideboi, and a test that reaches the live default session
// can end it (#277).
func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }
