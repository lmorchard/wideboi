//go:build desktop

package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/desktop"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
	"github.com/lmorchard/wideboi/web"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed desktop-ui/manager.html
var desktopUI embed.FS

const desktopBuild = true

type desktopSession struct {
	dir    string
	conn   *transport.ClientSocketConn
	cancel context.CancelFunc
	done   chan struct{}
}

type desktopApp struct {
	app        *application.App
	server     *http.Server
	listener   net.Listener
	token      string
	baseURL    string
	createMu   sync.Mutex
	mu         sync.Mutex
	owned      map[string]*desktopSession
	windows    map[string]*application.WebviewWindow
	manager    *application.WebviewWindow
	dialogOpen bool
	quitting   bool
}

type desktopSessionInfo struct {
	Name  string `json:"name"`
	Owned bool   `json:"owned"`
	Dir   string `json:"dir,omitempty"`
}

func runDesktop() error {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return fmt.Errorf("desktop secret: %w", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	d := &desktopApp{
		listener: listener, token: hex.EncodeToString(secret),
		baseURL: "http://" + listener.Addr().String(),
		owned:   make(map[string]*desktopSession),
		windows: make(map[string]*application.WebviewWindow),
	}
	d.app = application.New(application.Options{
		Name: "wideboi", Description: "Local wideboi sessions",
		ShouldQuit: d.shouldQuit,
		OnShutdown: d.shutdown,
	})
	mux := http.NewServeMux()
	mux.Handle("/ws", &desktop.Gateway{Token: d.token})
	mux.HandleFunc("/manager", d.serveManager)
	mux.HandleFunc("/api/", d.serveAPI)
	dist, err := web.DistFS()
	if err != nil {
		listener.Close()
		return err
	}
	mux.Handle("/", http.FileServer(dist))
	d.server = &http.Server{Handler: mux}
	go func() { _ = d.server.Serve(listener) }()
	defer d.server.Close()

	menu := application.NewMenu()
	file := menu.AddSubmenu("File")
	file.Add("Show Sessions").OnClick(func(*application.Context) { d.showManager() })
	file.Add("Quit wideboi").OnClick(func(*application.Context) { d.requestQuit() })
	d.app.Menu.Set(menu)
	d.showManager()
	return d.app.Run()
}

func (d *desktopApp) serveManager(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, err := desktopUI.ReadFile("desktop-ui/manager.html")
	if err != nil {
		http.Error(w, "manager unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (d *desktopApp) serveAPI(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")), []byte(d.token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/sessions":
		names, err := listSessions(config.SessionDir())
		if err != nil {
			d.writeError(w, err)
			return
		}
		result := make([]desktopSessionInfo, 0, len(names))
		d.mu.Lock()
		for _, name := range names {
			item := desktopSessionInfo{Name: name}
			if owned, ok := d.owned[name]; ok {
				item.Owned, item.Dir = true, owned.dir
			}
			result = append(result, item)
		}
		d.mu.Unlock()
		d.writeJSON(w, result)
	case r.Method == http.MethodPost && r.URL.Path == "/api/pick-directory":
		home, _ := os.UserHomeDir()
		dir, err := d.app.Dialog.OpenFile().CanChooseDirectories(true).CanChooseFiles(false).
			SetTitle("Choose a project folder").SetDirectory(home).PromptForSingleSelection()
		if err != nil {
			d.writeError(w, err)
			return
		}
		d.writeJSON(w, map[string]string{"dir": dir})
	case r.Method == http.MethodPost && r.URL.Path == "/api/create":
		var input struct {
			Dir   string `json:"dir"`
			Force bool   `json:"force"`
		}
		if !d.decode(w, r, &input) {
			return
		}
		name, matches, err := d.create(input.Dir, input.Force)
		if len(matches) > 0 && !input.Force {
			w.WriteHeader(http.StatusConflict)
			d.writeJSON(w, map[string]any{"matches": matches})
			return
		}
		if err != nil {
			d.writeError(w, err)
			return
		}
		d.openSession(name)
		d.writeJSON(w, map[string]string{"name": name})
	case r.Method == http.MethodPost && r.URL.Path == "/api/open":
		var input struct {
			Name string `json:"name"`
		}
		if !d.decode(w, r, &input) {
			return
		}
		if !config.ValidSessionName(input.Name) {
			http.Error(w, "invalid session", http.StatusBadRequest)
			return
		}
		conn, err := net.DialTimeout("unix", config.SessionSocketPath(input.Name), time.Second)
		if err != nil {
			d.writeError(w, err)
			return
		}
		conn.Close()
		d.openSession(input.Name)
		d.writeJSON(w, map[string]bool{"ok": true})
	case r.Method == http.MethodPost && r.URL.Path == "/api/keep":
		var input struct {
			Name string `json:"name"`
		}
		if !d.decode(w, r, &input) {
			return
		}
		if err := d.release(input.Name); err != nil {
			d.writeError(w, err)
			return
		}
		d.writeJSON(w, map[string]bool{"ok": true})
	case r.Method == http.MethodPost && r.URL.Path == "/api/stop":
		var input struct {
			Name string `json:"name"`
		}
		if !d.decode(w, r, &input) {
			return
		}
		if !config.ValidSessionName(input.Name) {
			http.Error(w, "invalid session", http.StatusBadRequest)
			return
		}
		if err := runKillSession(config.Config{Socket: config.SessionSocketPath(input.Name)}); err != nil {
			d.writeError(w, err)
			return
		}
		d.writeJSON(w, map[string]bool{"ok": true})
	case r.Method == http.MethodPost && r.URL.Path == "/api/quit":
		d.writeJSON(w, map[string]bool{"ok": true})
		go d.requestQuit()
	default:
		http.NotFound(w, r)
	}
}

func (d *desktopApp) decode(w http.ResponseWriter, r *http.Request, value any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(value); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return false
	}
	return true
}

func (d *desktopApp) writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func (d *desktopApp) writeError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusBadRequest)
}

func (d *desktopApp) create(directory string, force bool) (string, []string, error) {
	d.createMu.Lock()
	defer d.createMu.Unlock()
	if directory == "" {
		var err error
		directory, err = os.UserHomeDir()
		if err != nil {
			return "", nil, err
		}
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Stat(directory)
	if err != nil {
		return "", nil, err
	}
	if !info.IsDir() {
		return "", nil, fmt.Errorf("%s is not a directory", directory)
	}
	matches := d.projectMatches(directory)
	if len(matches) > 0 && !force {
		return "", matches, nil
	}

	names, err := listSessions(config.SessionDir())
	if err != nil {
		return "", nil, err
	}
	used := make(map[string]bool, len(names))
	for _, name := range names {
		used[name] = true
	}
	base := sessionBase(filepath.Base(directory))
	name, err := availableSessionName(base, used)
	if err != nil {
		return "", nil, err
	}
	socket := config.SessionSocketPath(name)
	conn, exited, err := spawnServerInDir(socket, []string{"-L", name}, directory)
	if err != nil {
		return "", nil, err
	}
	if err := handshakeServer(conn, socket); err != nil {
		conn.Close()
		return "", nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	client := transport.NewClientSocketConn(conn, 256)
	client.RunPumps(ctx)
	owned := &desktopSession{dir: directory, conn: client, cancel: cancel, done: make(chan struct{})}
	ready := make(chan struct{})
	go func() {
		first := true
		for range client.ServerSendChan() {
			if first {
				close(ready)
				first = false
			}
		}
		close(owned.done)
	}()
	// The lifetime owner is not a visible client. Zero dimensions let the
	// first real window establish PTY size on its own MsgAttach.
	if !client.SendClient(ctx, protocol.MsgAttach{}) {
		client.Close()
		cancel()
		return "", nil, errors.New("session owner connection closed")
	}
	select {
	case <-ready:
	case <-owned.done:
		client.Close()
		cancel()
		return "", nil, errors.New("session exited during startup")
	case <-time.After(5 * time.Second):
		client.Close()
		cancel()
		return "", nil, errors.New("session did not start")
	}
	d.mu.Lock()
	d.owned[name] = owned
	d.mu.Unlock()
	go func() {
		<-exited
		d.mu.Lock()
		if d.owned[name] == owned {
			delete(d.owned, name)
		}
		d.mu.Unlock()
		cancel()
	}()
	return name, nil, nil
}

func sessionBase(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	result := strings.Trim(b.String(), ".-")
	if result == "" {
		return "session"
	}
	return result
}

// Unix socket paths have a small fixed limit (103 bytes on macOS). Reserve
// room for the session directory, extension, and a collision suffix.
func availableSessionName(base string, used map[string]bool) (string, error) {
	const maxSocketPath = 103
	maxName := maxSocketPath - len(config.SessionSocketPath(""))
	if maxName < 1 {
		return "", fmt.Errorf("session directory path is too long for a Unix socket")
	}
	for n := 1; ; n++ {
		suffix := ""
		if n > 1 {
			suffix = fmt.Sprintf("-%d", n)
		}
		if len(suffix) >= maxName {
			return "", fmt.Errorf("no valid session name fits the Unix socket path")
		}
		prefix := base
		if len(prefix) > maxName-len(suffix) {
			prefix = strings.TrimRight(prefix[:maxName-len(suffix)], ".-")
		}
		if prefix == "" {
			prefix = "s"
		}
		name := prefix + suffix
		if !used[name] {
			return name, nil
		}
	}
}

func (d *desktopApp) projectMatches(dir string) []string {
	names, err := listSessions(config.SessionDir())
	if err != nil {
		return nil
	}
	var matches []string
	d.mu.Lock()
	for _, name := range names {
		if owned, ok := d.owned[name]; ok && owned.dir == dir {
			matches = append(matches, name)
		}
	}
	d.mu.Unlock()
	for _, name := range names {
		already := false
		for _, match := range matches {
			if match == name {
				already = true
				break
			}
		}
		if already {
			continue
		}
		var out strings.Builder
		if runStatus(config.Config{Socket: config.SessionSocketPath(name)}, true, &out) != nil {
			continue
		}
		var status statusOutput
		if json.Unmarshal([]byte(out.String()), &status) != nil {
			continue
		}
		if filepath.Clean(status.SessionCWD) == dir {
			matches = append(matches, name)
			continue
		}
		for _, meta := range status.PaneMetadata {
			if filepath.Clean(meta.CWD) == dir {
				matches = append(matches, name)
				break
			}
		}
	}
	return matches
}

func (d *desktopApp) release(name string) error {
	d.mu.Lock()
	owned := d.owned[name]
	d.mu.Unlock()
	if owned == nil {
		return errors.New("session is not owned by the desktop app")
	}
	ctx, cancel := context.WithTimeout(context.Background(), detachCeiling)
	defer cancel()
	if !owned.conn.SendClient(ctx, protocol.MsgDetach{}) {
		return errors.New("could not detach session")
	}
	select {
	case <-owned.done:
		if err := owned.conn.Err(); err != nil {
			return err
		}
	case <-ctx.Done():
		return errors.New("session did not acknowledge detach")
	}
	owned.conn.Close()
	owned.cancel()
	d.mu.Lock()
	if d.owned[name] == owned {
		delete(d.owned, name)
	}
	d.mu.Unlock()
	return nil
}

// keepOwned is the quit dialog's "Keep sessions running" action. Detaching
// only owner connections leaves independently attached sessions untouched.
func (d *desktopApp) keepOwned() error {
	d.mu.Lock()
	names := make([]string, 0, len(d.owned))
	for name := range d.owned {
		names = append(names, name)
	}
	d.mu.Unlock()
	for _, name := range names {
		if err := d.release(name); err != nil {
			return err
		}
	}
	return nil
}

func (d *desktopApp) stopOwned(name string, owned *desktopSession) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownCeiling)
	defer cancel()
	_ = owned.conn.SendClient(ctx, protocol.MsgShutdown{})
	select {
	case <-owned.done:
	case <-ctx.Done():
	}
	owned.conn.Close()
	owned.cancel()
	d.mu.Lock()
	if d.owned[name] == owned {
		delete(d.owned, name)
	}
	d.mu.Unlock()
}

func (d *desktopApp) showManager() {
	d.mu.Lock()
	if d.quitting {
		d.mu.Unlock()
		return
	}
	if d.manager != nil {
		window := d.manager
		d.mu.Unlock()
		window.Show()
		window.Focus()
		return
	}
	window := d.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "sessions", Title: "wideboi Sessions", Width: 760, Height: 600,
		URL: d.baseURL + "/manager#token=" + d.token,
	})
	d.manager = window
	d.mu.Unlock()
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		d.mu.Lock()
		last := len(d.windows) == 0 && !d.quitting
		d.mu.Unlock()
		if last {
			event.Cancel()
			go d.requestQuit()
		}
	})
	window.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		d.mu.Lock()
		if d.manager == window {
			d.manager = nil
		}
		d.mu.Unlock()
	})
}

func (d *desktopApp) openSession(name string) {
	d.mu.Lock()
	if existing := d.windows[name]; existing != nil {
		d.mu.Unlock()
		existing.Show()
		existing.Focus()
		return
	}
	window := d.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "session-" + name, Title: "wideboi: " + name, Width: 1100, Height: 720,
		URL: d.baseURL + "/?session=" + url.QueryEscape(name) + "#token=" + d.token,
	})
	d.windows[name] = window
	d.mu.Unlock()
	window.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		d.mu.Lock()
		if d.windows[name] == window {
			delete(d.windows, name)
		}
		last := len(d.windows) == 0 && !d.quitting
		d.mu.Unlock()
		if last {
			go d.showManager()
		}
	})
}

func (d *desktopApp) shouldQuit() bool {
	d.mu.Lock()
	if d.quitting {
		d.mu.Unlock()
		return true
	}
	count := len(d.owned)
	if count == 0 {
		d.quitting = true
	}
	d.mu.Unlock()
	if count == 0 {
		return true
	}
	go d.showQuitDialog(count)
	return false
}

func (d *desktopApp) requestQuit() {
	if d.shouldQuit() {
		d.mu.Lock()
		d.quitting = true
		d.mu.Unlock()
		d.app.Quit()
	}
}

func (d *desktopApp) showQuitDialog(count int) {
	d.mu.Lock()
	if d.dialogOpen || d.quitting {
		d.mu.Unlock()
		return
	}
	d.dialogOpen = true
	d.mu.Unlock()
	dialog := d.app.Dialog.Question().SetTitle("Quit wideboi").
		SetMessage(fmt.Sprintf("%d desktop-owned session(s) are running. Stopping them will also disconnect other attached clients.", count))
	dialog.AddButton("Stop sessions and quit").OnClick(func() {
		d.mu.Lock()
		d.quitting = true
		d.mu.Unlock()
		d.app.Quit()
	})
	dialog.AddButton("Keep sessions running and quit").OnClick(func() {
		if err := d.keepOwned(); err != nil {
			d.app.Dialog.Error().SetTitle("Could not keep session").SetMessage(err.Error()).Show()
			d.mu.Lock()
			d.dialogOpen = false
			d.mu.Unlock()
			return
		}
		d.mu.Lock()
		d.quitting = true
		d.mu.Unlock()
		d.app.Quit()
	})
	dialog.AddButton("Cancel").SetAsCancel().OnClick(func() {
		d.mu.Lock()
		d.dialogOpen = false
		d.mu.Unlock()
	})
	dialog.Show()
}

func (d *desktopApp) shutdown() {
	d.mu.Lock()
	d.quitting = true
	sessions := make(map[string]*desktopSession, len(d.owned))
	for name, owned := range d.owned {
		sessions[name] = owned
	}
	d.mu.Unlock()
	var wg sync.WaitGroup
	for name, owned := range sessions {
		wg.Add(1)
		go func() { defer wg.Done(); d.stopOwned(name, owned) }()
	}
	wg.Wait()
}
