package main

import (
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/lmorchard/wideboi/internal/config"
)

// listSessions names the live sessions in dir, sorted: the *.sock files
// with valid session names that answer a dial. A socket nobody answers
// is a dead server's leftover. It is skipped, not removed; the next
// server to bind that name reclaims it under the lock.
func listSessions(dir string) ([]string, error) {
	socks, err := filepath.Glob(filepath.Join(dir, "*.sock"))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, sock := range socks { // Glob sorts
		name := strings.TrimSuffix(filepath.Base(sock), ".sock")
		if !config.ValidSessionName(name) { // only what -L can address
			continue
		}
		conn, err := net.DialTimeout("unix", sock, time.Second)
		if err != nil {
			continue
		}
		conn.Close()
		names = append(names, name)
	}
	return names, nil
}

// runList prints the live sessions, one per line: `wideboi ls`.
func runList(w io.Writer) error {
	names, err := listSessions(config.SessionDir())
	if err != nil {
		return err
	}
	for _, n := range names {
		fmt.Fprintln(w, n)
	}
	return nil
}
