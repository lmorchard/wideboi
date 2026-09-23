package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

var Log *slog.Logger = slog.Default()

// LevelTrace is below Debug, for lines that fire per message or per
// frame. slog has no trace level of its own; its levels are integers
// with room between them for exactly this.
//
// The distinction matters because the log files are appended to
// forever: a per-pane-update line at Debug wrote about 60 lines a
// second for as long as a session ran.
const LevelTrace = slog.LevelDebug - 4

// ParseLevel maps a configured name onto a level. Empty means Info.
//
// An unknown name is an error rather than a fallback: a typo that
// quietly behaves like the default is indistinguishable from not
// having set anything (docs/LESSONS.md).
func ParseLevel(name string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "trace":
		return LevelTrace, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("log level %q: want trace, debug, info, warn or error", name)
	}
}

// Path is where component's log for the session at socket lives:
// beside the socket, named after it. Per session, so several sessions
// do not interleave in one file, and a test harness's private socket
// directory gets private logs. Exposed so an error can point at the log
// of a process whose stderr goes nowhere.
func Path(socket, component string) string {
	return strings.TrimSuffix(socket, ".sock") + "." + component + ".log"
}

// Init initializes file-based structured logging to path, recording
// level and above.
func Init(path string, level slog.Level) (*os.File, error) {
	_ = os.MkdirAll(filepath.Dir(path), 0700)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}

	Log = slog.New(newHandler(f, level))
	slog.SetDefault(Log)
	return f, nil
}

func newHandler(w io.Writer, level slog.Level) slog.Handler {
	return slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
		// Without this, LevelTrace prints as "DEBUG-4": it reads as a
		// debug line and greps as one.
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey && len(groups) == 0 {
				if l, ok := a.Value.Any().(slog.Level); ok && l == LevelTrace {
					a.Value = slog.StringValue("TRACE")
				}
			}
			return a
		},
	})
}
