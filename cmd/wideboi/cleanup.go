package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// isSessionActive checks if the socket answers a dial.
func isSessionActive(sock string) bool {
	conn, err := net.DialTimeout("unix", sock, 100*time.Millisecond)
	if err == nil {
		conn.Close()
		return true
	}
	return false
}

// runCleanup removes legacy logs and artifacts of dead sessions in dir.
func runCleanup(w io.Writer, dir string) error {
	// 1. Unconditionally remove legacy shared logs.
	legacy := []string{"server.log", "client.log"}
	for _, l := range legacy {
		path := filepath.Join(dir, l)
		if err := os.Remove(path); err == nil {
			fmt.Fprintf(w, "removed legacy %s\n", l)
		}
	}

	// Track which sessions are active to avoid repeated dials.
	activeCache := make(map[string]bool)
	isActive := func(name string) bool {
		if active, ok := activeCache[name]; ok {
			return active
		}
		active := isSessionActive(filepath.Join(dir, name+".sock"))
		activeCache[name] = active
		return active
	}

	// 2. Clean up dead sockets.
	socks, err := filepath.Glob(filepath.Join(dir, "*.sock"))
	if err == nil {
		for _, sock := range socks {
			name := strings.TrimSuffix(filepath.Base(sock), ".sock")
			if !isActive(name) {
				if err := os.Remove(sock); err == nil {
					fmt.Fprintf(w, "removed dead socket %s\n", filepath.Base(sock))
				}
			}
		}
	}

	// 3. Clean up dead logs.
	logs, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err == nil {
		for _, log := range logs {
			base := filepath.Base(log)
			// e.g. default.server.log -> default
			name := strings.TrimSuffix(base, ".server.log")
			name = strings.TrimSuffix(name, ".client.log")

			// If trim didn't change anything, it's not a session log (or was legacy, already gone)
			if name == base {
				continue
			}

			if !isActive(name) {
				if err := os.Remove(log); err == nil {
					fmt.Fprintf(w, "removed dead log %s\n", base)
				}
			}
		}
	}

	return nil
}
