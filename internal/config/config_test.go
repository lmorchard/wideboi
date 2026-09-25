package config_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/keys"
	"github.com/lmorchard/wideboi/internal/logger"
	"github.com/lmorchard/wideboi/internal/protocol"
)

func mockEnv(m map[string]string) func(string) string {
	return func(k string) string {
		return m[k]
	}
}

// defaultFlags exercises built-in defaults without reading files from the
// test runner's working directory. Discovery has dedicated tests below.
func defaultFlags() config.ConfigFlags {
	return config.ConfigFlags{ConfigFile: os.DevNull}
}

func TestLoadDefaults(t *testing.T) {
	cfg, bindings, err := config.Load(defaultFlags(), mockEnv(nil))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if cfg.Layout != "cards" {
		t.Errorf("Layout = %q, want cards", cfg.Layout)
	}
	if cfg.LayoutMode != protocol.LayoutCards {
		t.Errorf("LayoutMode = %v, want LayoutCards", cfg.LayoutMode)
	}
	if cfg.Prefix != "ctrl+b" {
		t.Errorf("Prefix = %q, want ctrl+b", cfg.Prefix)
	}
	if cfg.PrefixLabel != "C-b" {
		t.Errorf("PrefixLabel = %q, want C-b", cfg.PrefixLabel)
	}
	if cfg.Socket == "" {
		t.Errorf("Socket should not be empty")
	}
	if cfg.Shell == "" {
		t.Errorf("Shell should not be empty")
	}
	if len(bindings) != len(keys.Bindings) {
		t.Errorf("len(bindings) = %d, want %d", len(bindings), len(keys.Bindings))
	}
}

func TestLoadPrecedence(t *testing.T) {
	tmpDir := t.TempDir()
	tomlPath := filepath.Join(tmpDir, "config.toml")
	tomlContent := `
layout = "scroll"
prefix = "ctrl+a"
socket = "/tmp/toml.sock"
shell = "/bin/tomlsh"
websocket = ":8080"
websocket_token = "secret1"
`
	if err := os.WriteFile(tomlPath, []byte(tomlContent), 0600); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{
		"WIDEBOI_LAYOUT":          "cards",
		"WIDEBOI_PREFIX":          "ctrl+x",
		"WIDEBOI_WEBSOCKET":       ":8081",
		"WIDEBOI_WEBSOCKET_TOKEN": "secret2",
		"SHELL":                   "/bin/envsh",
	}

	flags := config.ConfigFlags{
		ConfigFile:     tomlPath,
		Prefix:         "ctrl+k",
		Websocket:      ":8082",
		WebsocketToken: "secret3",
	}

	cfg, _, err := config.Load(flags, mockEnv(env))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	// Layout: env WIDEBOI_LAYOUT="cards" overrides TOML "scroll"
	if cfg.Layout != "cards" {
		t.Errorf("Layout = %q, want 'cards' from env", cfg.Layout)
	}
	// Prefix: flag "ctrl+k" overrides env "ctrl+p" and TOML "ctrl+a"
	if cfg.Prefix != "ctrl+k" {
		t.Errorf("Prefix = %q, want 'ctrl+k' from flag", cfg.Prefix)
	}
	if cfg.PrefixLabel != "C-k" {
		t.Errorf("PrefixLabel = %q, want 'C-k'", cfg.PrefixLabel)
	}
	// Socket: from TOML "/tmp/toml.sock"
	if cfg.Socket != "/tmp/toml.sock" {
		t.Errorf("Socket = %q, want '/tmp/toml.sock' from TOML", cfg.Socket)
	}

	flagsNoOverride := config.ConfigFlags{ConfigFile: tomlPath}
	cfgNoOverride, _, _ := config.Load(flagsNoOverride, mockEnv(nil))
	if cfgNoOverride.Websocket != ":8080" {
		t.Errorf("Websocket = %q, want ':8080' from TOML when no env/flag", cfgNoOverride.Websocket)
	}

	cfgEnvOverride, _, _ := config.Load(flagsNoOverride, mockEnv(env))
	if cfgEnvOverride.Websocket != ":8081" {
		t.Errorf("Websocket = %q, want ':8081' from env when no flag", cfgEnvOverride.Websocket)
	}

	// Shell: from TOML "/bin/tomlsh"
	if cfg.Shell != "/bin/tomlsh" {
		t.Errorf("Shell = %q, want '/bin/tomlsh' from TOML", cfg.Shell)
	}
	// Websocket: flag overrides env and TOML
	if cfg.Websocket != ":8082" {
		t.Errorf("Websocket = %q, want ':8082' from flag", cfg.Websocket)
	}
	// WebsocketToken: flag overrides env and TOML
	if cfg.WebsocketToken != "secret3" {
		t.Errorf("WebsocketToken = %q, want 'secret3' from flag", cfg.WebsocketToken)
	}
}

func TestLoadTomlKeysRemapping(t *testing.T) {
	tmpDir := t.TempDir()
	tomlPath := filepath.Join(tmpDir, "config.toml")
	tomlContent := `
[keys]
kill_pane = "k"
scroll_up = "e"
`
	if err := os.WriteFile(tomlPath, []byte(tomlContent), 0600); err != nil {
		t.Fatal(err)
	}

	flags := config.ConfigFlags{ConfigFile: tomlPath}
	_, bindings, err := config.Load(flags, mockEnv(nil))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	for _, b := range bindings {
		if b.ActionName == keys.ActionNameKillPane && b.Key != "k" {
			t.Errorf("kill_pane key = %q, want k", b.Key)
		}
		if b.ActionName == keys.ActionNameScrollUp && b.Key != "e" {
			t.Errorf("scroll_up key = %q, want e", b.Key)
		}
	}
}

func TestLoadExplicitConfigNotFound(t *testing.T) {
	flags := config.ConfigFlags{ConfigFile: "/nonexistent/path/to/config.toml"}
	_, _, err := config.Load(flags, mockEnv(nil))
	if err == nil {
		t.Error("expected error for nonexistent explicit config file, got nil")
	}
}

func TestLoadInvalidLayout(t *testing.T) {
	flags := config.ConfigFlags{Layout: "invalid"}
	_, _, err := config.Load(flags, mockEnv(nil))
	if err == nil {
		t.Error("expected error for invalid layout, got nil")
	} else if !strings.Contains(err.Error(), "layout") {
		t.Errorf("error should mention layout, got: %v", err)
	}
}

func TestLoadInvalidPrefix(t *testing.T) {
	// Reserved key
	flags := config.ConfigFlags{Prefix: "ctrl+i"}
	_, _, err := config.Load(flags, mockEnv(nil))
	if err == nil {
		t.Error("expected error for reserved prefix, got nil")
	} else if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("error should mention reserved, got: %v", err)
	}

	// Invalid prefix format
	flags = config.ConfigFlags{Prefix: "alt+b"}
	_, _, err = config.Load(flags, mockEnv(nil))
	if err == nil {
		t.Error("expected error for non-ctrl prefix, got nil")
	}
}

func TestLoadInvalidKeysInToml(t *testing.T) {
	tmpDir := t.TempDir()
	tomlPath := filepath.Join(tmpDir, "config.toml")
	tomlContent := `
[keys]
kill_pane = "i"
`
	if err := os.WriteFile(tomlPath, []byte(tomlContent), 0600); err != nil {
		t.Fatal(err)
	}

	flags := config.ConfigFlags{ConfigFile: tomlPath}
	_, _, err := config.Load(flags, mockEnv(nil))
	if err == nil {
		t.Error("expected error for reserved key in TOML, got nil")
	}
}

func TestLoadConfigExampleToml(t *testing.T) {
	examplePath := filepath.Join("..", "..", "config.example.toml")
	if _, err := os.Stat(examplePath); err != nil {
		t.Fatalf("config.example.toml not found at %s: %v", examplePath, err)
	}

	flags := config.ConfigFlags{ConfigFile: examplePath}
	cfg, bindings, err := config.Load(flags, mockEnv(nil))
	if err != nil {
		t.Fatalf("failed to load config.example.toml: %v", err)
	}
	if cfg.Layout != "cards" {
		t.Errorf("expected layout cards, got %s", cfg.Layout)
	}
	if cfg.Prefix != "ctrl+b" {
		t.Errorf("expected prefix ctrl+b, got %s", cfg.Prefix)
	}
	// The example lists every action with its defaults, so it must load
	// exactly the default table. A setting replaces all of an action's
	// keys, so an example that forgets an alias silently drops it.
	if !reflect.DeepEqual(bindings, keys.Bindings) {
		for i := range min(len(bindings), len(keys.Bindings)) {
			if !reflect.DeepEqual(bindings[i], keys.Bindings[i]) {
				t.Errorf("config.example.toml binding %d = %+v, want the default %+v", i, bindings[i], keys.Bindings[i])
			}
		}
		if len(bindings) != len(keys.Bindings) {
			t.Errorf("config.example.toml loads %d bindings, want %d", len(bindings), len(keys.Bindings))
		}
	}
}

func TestLoadDefaultConfigReadErrorNotIgnored(t *testing.T) {
	tmpDir := t.TempDir()
	// Create a directory where the config file is expected, so os.ReadFile fails
	configPath := filepath.Join(tmpDir, "wideboi", "config.toml")
	if err := os.MkdirAll(configPath, 0700); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{
		"XDG_CONFIG_HOME": tmpDir,
	}

	// Implicit discovery (empty flags.ConfigFile)
	_, _, err := config.Load(config.ConfigFlags{}, mockEnv(env))
	if err == nil {
		t.Error("expected error when default config path is an unreadable directory, got nil")
	}
}

func TestLoadWidthPresets(t *testing.T) {
	tmpDir := t.TempDir()
	tomlPath := filepath.Join(tmpDir, "config.toml")
	tomlContent := `
width_presets = [50, 75, 100]
`
	if err := os.WriteFile(tomlPath, []byte(tomlContent), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := config.Load(config.ConfigFlags{ConfigFile: tomlPath}, mockEnv(nil))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if len(cfg.WidthPresets) != 3 || cfg.WidthPresets[0] != 50 || cfg.WidthPresets[1] != 75 || cfg.WidthPresets[2] != 100 {
		t.Errorf("WidthPresets = %v, want [50, 75, 100]", cfg.WidthPresets)
	}

	// Invalid presets (below 20)
	invalidToml := `
width_presets = [10, 80]
`
	_ = os.WriteFile(tomlPath, []byte(invalidToml), 0600)
	_, _, err = config.Load(config.ConfigFlags{ConfigFile: tomlPath}, mockEnv(nil))
	if err == nil {
		t.Errorf("expected error for invalid width_presets, got nil")
	}
}

// Mouse capture is on unless the file says otherwise. The field is a
// pointer so an absent key and an explicit false are distinguishable;
// a plain bool would read an absent key as "off".
func TestLoadMouse(t *testing.T) {
	cfg, _, err := config.Load(defaultFlags(), mockEnv(nil))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if !cfg.MouseEnabled {
		t.Error("MouseEnabled = false by default, want true")
	}

	tomlPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(tomlPath, []byte("mouse = false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = config.Load(config.ConfigFlags{ConfigFile: tomlPath}, mockEnv(nil))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.MouseEnabled {
		t.Error("MouseEnabled = true with mouse = false in the file")
	}
}

func TestLoadAutoCleanup(t *testing.T) {
	// 1. Defaults to true
	cfg, _, err := config.Load(defaultFlags(), mockEnv(nil))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if !cfg.AutoCleanupEnabled {
		t.Error("AutoCleanupEnabled = false by default, want true")
	}

	// 2. TOML auto_cleanup = false disables it
	tomlPathFalse := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(tomlPathFalse, []byte("auto_cleanup = false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = config.Load(config.ConfigFlags{ConfigFile: tomlPathFalse}, mockEnv(nil))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.AutoCleanupEnabled {
		t.Error("AutoCleanupEnabled = true with auto_cleanup = false in TOML")
	}

	// 3. TOML auto_cleanup = true enables it explicitly
	tomlPathTrue := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(tomlPathTrue, []byte("auto_cleanup = true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = config.Load(config.ConfigFlags{ConfigFile: tomlPathTrue}, mockEnv(nil))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if !cfg.AutoCleanupEnabled {
		t.Error("AutoCleanupEnabled = false with auto_cleanup = true in TOML")
	}

	// 4. Environment variable false/0/no/off disables it
	for _, val := range []string{"false", "0", "no", "off", "FALSE"} {
		cfg, _, err = config.Load(defaultFlags(), mockEnv(map[string]string{"WIDEBOI_AUTO_CLEANUP": val}))
		if err != nil {
			t.Fatalf("Load() with WIDEBOI_AUTO_CLEANUP=%q error: %v", val, err)
		}
		if cfg.AutoCleanupEnabled {
			t.Errorf("AutoCleanupEnabled = true with WIDEBOI_AUTO_CLEANUP=%q", val)
		}
	}

	// 5. Environment variable true/1/yes/on enables it
	for _, val := range []string{"true", "1", "yes", "on", "TRUE"} {
		cfg, _, err = config.Load(defaultFlags(), mockEnv(map[string]string{"WIDEBOI_AUTO_CLEANUP": val}))
		if err != nil {
			t.Fatalf("Load() with WIDEBOI_AUTO_CLEANUP=%q error: %v", val, err)
		}
		if !cfg.AutoCleanupEnabled {
			t.Errorf("AutoCleanupEnabled = false with WIDEBOI_AUTO_CLEANUP=%q", val)
		}
	}

	// 6. Invalid environment variable returns an error
	_, _, err = config.Load(defaultFlags(), mockEnv(map[string]string{"WIDEBOI_AUTO_CLEANUP": "maybe"}))
	if err == nil {
		t.Error("Load() want error for WIDEBOI_AUTO_CLEANUP='maybe', got nil")
	}

	// 7. Environment variable overrides TOML
	cfg, _, err = config.Load(config.ConfigFlags{ConfigFile: tomlPathFalse}, mockEnv(map[string]string{"WIDEBOI_AUTO_CLEANUP": "true"}))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !cfg.AutoCleanupEnabled {
		t.Error("AutoCleanupEnabled = false when env WIDEBOI_AUTO_CLEANUP='true' overrides TOML false")
	}
}

func TestLoadLogLevel(t *testing.T) {
	dir := t.TempDir()
	env := func(m map[string]string) func(string) string {
		return func(k string) string {
			if k == "XDG_CONFIG_HOME" {
				return dir // no config file here unless a case writes one
			}
			return m[k]
		}
	}

	cfg, _, err := config.Load(defaultFlags(), env(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("default log level = %v, want INFO", cfg.LogLevel)
	}

	file := filepath.Join(dir, "c.toml")
	if err := os.WriteFile(file, []byte(`log_level = "debug"`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = config.Load(config.ConfigFlags{ConfigFile: file}, env(nil))
	if err != nil {
		t.Fatalf("Load with log_level: %v", err)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("toml log_level = %v, want DEBUG", cfg.LogLevel)
	}

	cfg, _, err = config.Load(config.ConfigFlags{ConfigFile: file}, env(map[string]string{"WIDEBOI_LOG_LEVEL": "trace"}))
	if err != nil {
		t.Fatalf("Load with WIDEBOI_LOG_LEVEL: %v", err)
	}
	if cfg.LogLevel != logger.LevelTrace {
		t.Errorf("WIDEBOI_LOG_LEVEL=trace gave %v, want TRACE (env overrides toml)", cfg.LogLevel)
	}

	if _, _, err := config.Load(defaultFlags(), env(map[string]string{"WIDEBOI_LOG_LEVEL": "verbose"})); err == nil {
		t.Error("an unknown WIDEBOI_LOG_LEVEL was accepted")
	}
}

func loadToml(t *testing.T, content string) ([]keys.Binding, error) {
	t.Helper()
	tomlPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(tomlPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	_, bindings, err := config.Load(config.ConfigFlags{ConfigFile: tomlPath}, mockEnv(nil))
	return bindings, err
}

func TestLoadTomlKeyLists(t *testing.T) {
	bindings, err := loadToml(t, "[keys]\nfocus_left = [\"h\", \"g\"]\ndetach = []\n")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	var sawLeft bool
	for _, b := range bindings {
		switch b.ActionName {
		case keys.ActionNameFocusLeft:
			sawLeft = true
			if b.Key != "h" || !reflect.DeepEqual(b.Aliases, []string{"g"}) {
				t.Errorf("focus_left Key=%q Aliases=%q, want h and [g]", b.Key, b.Aliases)
			}
		case keys.ActionNameDetach:
			t.Error("detach is still bound after detach = []")
		}
	}
	if !sawLeft {
		t.Error("focus_left is missing")
	}
}

func TestLoadTomlKeysRejectsNonStrings(t *testing.T) {
	for _, content := range []string{
		"[keys]\nkill_pane = 5\n",
		"[keys]\nkill_pane = [\"x\", 5]\n",
	} {
		_, err := loadToml(t, content)
		if err == nil || !strings.Contains(err.Error(), "kill_pane") {
			t.Errorf("%q: err = %v, want one naming kill_pane", content, err)
		}
	}
}

func TestLoadTomlQuitUnbound(t *testing.T) {
	_, err := loadToml(t, "[keys]\nquit = []\n")
	if err == nil || !strings.Contains(err.Error(), "cannot be unbound") {
		t.Errorf("err = %v, want one saying quit cannot be unbound", err)
	}
}

// emptyConfigHome keeps a developer's own ~/.config/wideboi/config.toml
// out of a test that asserts where the socket resolves.
func emptyConfigHome(t *testing.T, env map[string]string) map[string]string {
	t.Helper()
	out := map[string]string{"XDG_CONFIG_HOME": t.TempDir()}
	for k, v := range env {
		out[k] = v
	}
	return out
}

func TestSessionNameMapsToASocketInTheSessionDir(t *testing.T) {
	cfg, _, err := config.Load(config.ConfigFlags{ConfigFile: os.DevNull, Session: "work"}, mockEnv(emptyConfigHome(t, nil)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := filepath.Join(config.SessionDir(), "work.sock")
	if cfg.Socket != want || config.SessionSocketPath("work") != want {
		t.Errorf("Socket = %q, SessionSocketPath = %q, want %q", cfg.Socket, config.SessionSocketPath("work"), want)
	}
	if config.DefaultSocketPath() != config.SessionSocketPath("default") {
		t.Errorf("DefaultSocketPath = %q, want the session called default", config.DefaultSocketPath())
	}
}

// Each precedence layer may name a session or give a path. A later
// layer overrides an earlier one; both in one layer is ambiguous.
func TestSessionAndSocketLayering(t *testing.T) {
	cases := []struct {
		name    string
		toml    string
		env     map[string]string
		flags   config.ConfigFlags
		want    string // socket; empty when wantErr
		session string // resolved Session; empty when a path won
		wantErr string
	}{
		{name: "default", want: config.DefaultSocketPath(), session: "default"},
		{name: "toml session", toml: `session = "a"`, want: config.SessionSocketPath("a"), session: "a"},
		{name: "env path beats toml session", toml: `session = "a"`,
			env: map[string]string{"WIDEBOI_SOCK": "/tmp/x.sock"}, want: "/tmp/x.sock"},
		{name: "flag session beats env path", env: map[string]string{"WIDEBOI_SOCK": "/tmp/x.sock"},
			flags: config.ConfigFlags{Session: "b"}, want: config.SessionSocketPath("b"), session: "b"},
		{name: "flag path beats env session", env: map[string]string{"WIDEBOI_SESSION": "a"},
			flags: config.ConfigFlags{Socket: "/tmp/y.sock"}, want: "/tmp/y.sock"},
		{name: "env session", env: map[string]string{"WIDEBOI_SESSION": "a"}, want: config.SessionSocketPath("a"), session: "a"},
		{name: "both flags", flags: config.ConfigFlags{Session: "b", Socket: "/tmp/y.sock"}, wantErr: "not both"},
		{name: "both env", env: map[string]string{"WIDEBOI_SESSION": "a", "WIDEBOI_SOCK": "/tmp/x.sock"}, wantErr: "not both"},
		{name: "both toml", toml: "session = \"a\"\nsocket = \"/tmp/x.sock\"", wantErr: "not both"},
		{name: "bad name", flags: config.ConfigFlags{Session: "../up"}, wantErr: "session name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags := tc.flags
			if tc.toml != "" {
				flags.ConfigFile = filepath.Join(t.TempDir(), "config.toml")
				if err := os.WriteFile(flags.ConfigFile, []byte(tc.toml), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				flags.ConfigFile = os.DevNull
			}
			cfg, _, err := config.Load(flags, mockEnv(emptyConfigHome(t, tc.env)))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Socket != tc.want {
				t.Errorf("Socket = %q, want %q", cfg.Socket, tc.want)
			}
			if cfg.Session != tc.session {
				t.Errorf("Session = %q, want %q", cfg.Session, tc.session)
			}
		})
	}
}

func TestSessionNameValidation(t *testing.T) {
	for _, ok := range []string{"work", "a.b", "x_1", "default", "9", "a-b"} {
		if !config.ValidSessionName(ok) {
			t.Errorf("ValidSessionName(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", ".hidden", "-x", "a/b", "../up", "sp ace"} {
		if config.ValidSessionName(bad) {
			t.Errorf("ValidSessionName(%q) = true, want false", bad)
		}
	}
}

func TestSessionNameRecognisesOnlySessionDirSockets(t *testing.T) {
	if name, ok := config.SessionName(config.SessionSocketPath("work")); !ok || name != "work" {
		t.Errorf("SessionName(work's socket) = %q, %v; want work, true", name, ok)
	}
	if _, ok := config.SessionName("/tmp/elsewhere.sock"); ok {
		t.Error("a socket outside the session dir was taken for a named session")
	}
	if _, ok := config.SessionName(filepath.Join(config.SessionDir(), "bad name.sock")); ok {
		t.Error("an invalid name in the session dir was taken for a named session")
	}
}

func TestLoadProjectConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	if err := os.WriteFile(".wideboi.toml", []byte("layout = \"scroll\"\n[[startup]]\ncommand = \"nvim\"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	xdgDir := filepath.Join(tmpDir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdgDir, "wideboi"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdgDir, "wideboi", "config.toml"), []byte("layout = \"cards\"\nprefix = \"ctrl+j\"\n[[startup]]\ncommand = \"htop\"\n[[startup]]\n"), 0600); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{
		"XDG_CONFIG_HOME": xdgDir,
	}

	cfg, _, err := config.Load(config.ConfigFlags{}, mockEnv(env))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Layout != "scroll" {
		t.Errorf("Layout = %q, want 'scroll' from project config overriding 'cards' from XDG", cfg.Layout)
	}
	if cfg.Prefix != "ctrl+j" {
		t.Errorf("Prefix = %q, want 'ctrl+j' from XDG (not overridden)", cfg.Prefix)
	}
	if len(cfg.Startup) != 1 || cfg.Startup[0].Command != "nvim" {
		t.Errorf("Startup = %+v, want project loadout replacing global loadout", cfg.Startup)
	}
}

func TestStartupPanesConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	data := "[[startup]]\ncommand = \"nvim\"\nwidth = 100\n[[startup]]\n[[startup]]\ncommand = \"claude\"\nwidth = 80\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(config.ConfigFlags{ConfigFile: path}, mockEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Startup) != 3 || cfg.Startup[0].Command != "nvim" || cfg.Startup[0].Width != 100 || cfg.Startup[1].Command != "" || cfg.Startup[2].Command != "claude" || cfg.Startup[2].Width != 80 {
		t.Fatalf("startup panes = %+v", cfg.Startup)
	}

	for _, bad := range []string{
		"[[startup]]\nwidth = 19\n",
		"[[startup]]\nwidth = 4097\n",
		"[[startup]]\nwidth = 1000000000\n",
		"[[startup]]\ncommand = \"   \"\n",
	} {
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := config.Load(config.ConfigFlags{ConfigFile: path}, mockEnv(nil)); err == nil {
			t.Errorf("expected invalid startup pane to fail: %q", bad)
		}
	}
	if err := os.WriteFile(path, []byte("[[startup]]\nwidth = 4096\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := config.Load(config.ConfigFlags{ConfigFile: path}, mockEnv(nil)); err != nil {
		t.Errorf("maximum startup width should be valid: %v", err)
	}
}

func TestConfigTheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	data := `
[theme]
working = "cyan"
needs_input = "yellow"
done = "green"
failed = "red"
focus = "bright_cyan"
divider = "dim"
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(config.ConfigFlags{ConfigFile: path}, mockEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Theme.Working != "cyan" {
		t.Errorf("cfg.Theme.Working = %q, want 'cyan'", cfg.Theme.Working)
	}
	if cfg.Theme.NeedsInput != "yellow" {
		t.Errorf("cfg.Theme.NeedsInput = %q, want 'yellow'", cfg.Theme.NeedsInput)
	}
	if cfg.Theme.Done != "green" {
		t.Errorf("cfg.Theme.Done = %q, want 'green'", cfg.Theme.Done)
	}
	if cfg.Theme.Failed != "red" {
		t.Errorf("cfg.Theme.Failed = %q, want 'red'", cfg.Theme.Failed)
	}
	if cfg.Theme.Focus != "bright_cyan" {
		t.Errorf("cfg.Theme.Focus = %q, want 'bright_cyan'", cfg.Theme.Focus)
	}
	if cfg.Theme.Divider != "dim" {
		t.Errorf("cfg.Theme.Divider = %q, want 'dim'", cfg.Theme.Divider)
	}
}
