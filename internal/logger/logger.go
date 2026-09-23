package logger

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

var Log *slog.Logger = slog.Default()

// Path is where component's log lives. Exposed so an error can point at
// the log of a process whose stderr goes nowhere.
func Path(component string) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("wideboi-%d", os.Getuid()), component+".log")
}

// Init initializes file-based structured logging for component ("server" or "client").
func Init(component string) (*os.File, error) {
	logPath := Path(component)
	_ = os.MkdirAll(filepath.Dir(logPath), 0700)

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}

	handler := slog.NewTextHandler(f, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	Log = slog.New(handler)
	slog.SetDefault(Log)
	return f, nil
}
