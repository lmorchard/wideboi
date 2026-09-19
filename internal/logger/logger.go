package logger

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

var Log *slog.Logger = slog.Default()

// Init initializes file-based structured logging for component ("server" or "client").
func Init(component string) (*os.File, error) {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("wideboi-%d", os.Getuid()))
	_ = os.MkdirAll(dir, 0700)
	logPath := filepath.Join(dir, fmt.Sprintf("%s.log", component))

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
