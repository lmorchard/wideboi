package client

import (
	"io"
)

// EmitHostNotificationForTest exposes emitHostNotificationTo for testing.
func EmitHostNotificationForTest(w io.Writer, getenv func(string) string, mode, title, msg string) {
	emitHostNotificationTo(w, getenv, mode, title, msg)
}
