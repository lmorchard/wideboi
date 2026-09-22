package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/keys"
	"github.com/lmorchard/wideboi/internal/protocol"
)

func mockEnv(m map[string]string) func(string) string {
	return func(k string) string {
		return m[k]
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, bindings, err := config.Load(config.ConfigFlags{}, mockEnv(nil))
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
`
	if err := os.WriteFile(tomlPath, []byte(tomlContent), 0600); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{
		"WIDEBOI_LAYOUT": "cards",
		"WIDEBOI_PREFIX": "ctrl+p",
	}

	flags := config.ConfigFlags{
		ConfigFile: tomlPath,
		Prefix:     "ctrl+k",
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
	// Shell: from TOML "/bin/tomlsh"
	if cfg.Shell != "/bin/tomlsh" {
		t.Errorf("Shell = %q, want '/bin/tomlsh' from TOML", cfg.Shell)
	}
}

func TestLoadTomlKeysRemapping(t *testing.T) {
	tmpDir := t.TempDir()
	tomlPath := filepath.Join(tmpDir, "config.toml")
	tomlContent := `
[keys]
kill_pane = "k"
scroll_up = "u"
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
		if b.ActionName == keys.ActionNameScrollUp && b.Key != "u" {
			t.Errorf("scroll_up key = %q, want u", b.Key)
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
	if len(bindings) == 0 {
		t.Error("expected non-empty bindings from config.example.toml")
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
