package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lmorchard/wideboi/internal/config"
)

func TestUntrustedProjectConfigIgnoresSensitiveSettings(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}

	env := mockEnv(map[string]string{
		"HOME": tmp,
	})

	// Create an untrusted .wideboi.toml with sensitive + safe settings
	untrustedTOML := `
layout = "scroll"
pan_step = 5
shell = "/bin/evil-shell"
socket = "/tmp/evil.sock"
websocket = "0.0.0.0:9999"
websocket_token = "evil-token"
tls = false
theme = { focus = "#ff0000" }

[[startup]]
command = "echo synthetic-marker"
width = 80

[[macros]]
name = "evil-macro"
steps = [{ text = "rm -rf /" }]
`
	if err := os.WriteFile(".wideboi.toml", []byte(untrustedTOML), 0600); err != nil {
		t.Fatal(err)
	}

	// 1. Load without trust: sensitive settings must be completely ignored
	cfg, _, err := config.Load(config.ConfigFlags{ConfigFile: ""}, env)
	if err != nil {
		t.Fatalf("load untrusted: %v", err)
	}

	if cfg.ProjectTrusted {
		t.Error("expected ProjectTrusted to be false")
	}

	// Sensitive: Startup commands
	if len(cfg.Startup) > 0 {
		t.Errorf("untrusted project was able to configure startup commands: %+v", cfg.Startup)
	}
	// Sensitive: Shell override
	if cfg.Shell == "/bin/evil-shell" {
		t.Errorf("untrusted project was able to override shell: %q", cfg.Shell)
	}
	// Sensitive: Socket redirect
	if cfg.Socket == "/tmp/evil.sock" {
		t.Errorf("untrusted project was able to redirect socket: %q", cfg.Socket)
	}
	// Sensitive: Websocket listener
	if cfg.Websocket == "0.0.0.0:9999" {
		t.Errorf("untrusted project was able to expose websocket: %q", cfg.Websocket)
	}
	// Sensitive: Websocket token
	if cfg.WebsocketToken == "evil-token" {
		t.Errorf("untrusted project was able to set websocket token: %q", cfg.WebsocketToken)
	}
	// Sensitive: Disabling TLS
	if !cfg.TLSEnabled {
		t.Error("untrusted project was able to disable TLS")
	}
	// Sensitive: Macros
	for _, m := range cfg.Macros {
		if m.Name == "evil-macro" {
			t.Errorf("untrusted project was able to define macro: %+v", m)
		}
	}

	// Safe: Appearance and layout settings MUST still load
	if cfg.Layout != "scroll" {
		t.Errorf("safe layout not loaded: got %q, want 'scroll'", cfg.Layout)
	}
	if cfg.PanStep != 5 {
		t.Errorf("safe pan_step not loaded: got %d, want 5", cfg.PanStep)
	}
	if cfg.Theme.Focus != "#ff0000" {
		t.Errorf("safe theme not loaded: got %q, want '#ff0000'", cfg.Theme.Focus)
	}

	// 2. Explicitly trust project via TrustProject
	absPath, hash, err := config.TrustProject(".wideboi.toml", env)
	if err != nil {
		t.Fatalf("trust project: %v", err)
	}
	if hash == "" || absPath == "" {
		t.Fatalf("unexpected trust result: path=%q hash=%q", absPath, hash)
	}

	// Load with trust: now all settings apply
	cfgTrusted, _, err := config.Load(config.ConfigFlags{}, env)
	if err != nil {
		t.Fatalf("load trusted: %v", err)
	}
	if !cfgTrusted.ProjectTrusted {
		t.Error("expected ProjectTrusted to be true after TrustProject")
	}
	if len(cfgTrusted.Startup) == 0 || cfgTrusted.Startup[0].Command != "echo synthetic-marker" {
		t.Errorf("trusted startup command not loaded: %+v", cfgTrusted.Startup)
	}
	if cfgTrusted.Shell != "/bin/evil-shell" {
		t.Errorf("trusted shell not loaded: %q", cfgTrusted.Shell)
	}
	if cfgTrusted.Websocket != "0.0.0.0:9999" {
		t.Errorf("trusted websocket not loaded: %q", cfgTrusted.Websocket)
	}
	if cfgTrusted.WebsocketToken != "evil-token" {
		t.Errorf("trusted token not loaded: %q", cfgTrusted.WebsocketToken)
	}
	if cfgTrusted.TLSEnabled {
		t.Error("trusted disable-tls not applied")
	}

	// 3. Modifying file content invalidates trust checksum
	modifiedTOML := untrustedTOML + "\n# Modified after trusting\n"
	if err := os.WriteFile(".wideboi.toml", []byte(modifiedTOML), 0600); err != nil {
		t.Fatal(err)
	}

	cfgTampered, _, err := config.Load(config.ConfigFlags{}, env)
	if err != nil {
		t.Fatalf("load tampered: %v", err)
	}
	if cfgTampered.ProjectTrusted {
		t.Error("expected modified file to revert to untrusted")
	}
	if len(cfgTampered.Startup) > 0 {
		t.Error("tampered file commands should be ignored")
	}

	// 4. UntrustProject removes entry
	if _, err := config.UntrustProject(".wideboi.toml", env); err != nil {
		t.Fatalf("untrust: %v", err)
	}

	// 5. One-off trust via flag or environment variable
	cfgFlagTrusted, _, err := config.Load(config.ConfigFlags{TrustProject: true}, env)
	if err != nil {
		t.Fatalf("load flag trusted: %v", err)
	}
	if !cfgFlagTrusted.ProjectTrusted {
		t.Error("expected flag TrustProject to trust project")
	}
	if len(cfgFlagTrusted.Startup) == 0 {
		t.Error("expected flag TrustProject to load startup commands")
	}

	envWithTrust := mockEnv(map[string]string{
		"HOME":                  tmp,
		"WIDEBOI_TRUST_PROJECT": "true",
	})
	cfgEnvTrusted, _, err := config.Load(config.ConfigFlags{}, envWithTrust)
	if err != nil {
		t.Fatalf("load env trusted: %v", err)
	}
	if !cfgEnvTrusted.ProjectTrusted {
		t.Error("expected WIDEBOI_TRUST_PROJECT=true to trust project")
	}

	// 6. Explicitly specified config file (--config / -c) is always trusted
	explicitPath := filepath.Join(tmp, "explicit.toml")
	if err := os.WriteFile(explicitPath, []byte(untrustedTOML), 0600); err != nil {
		t.Fatal(err)
	}
	cfgExplicit, _, err := config.Load(config.ConfigFlags{ConfigFile: explicitPath}, env)
	if err != nil {
		t.Fatalf("load explicit: %v", err)
	}
	if !cfgExplicit.ProjectTrusted {
		t.Error("expected explicit ConfigFile to be trusted")
	}
	if len(cfgExplicit.Startup) == 0 {
		t.Error("expected explicit ConfigFile to load startup commands")
	}
}

func TestPromptProjectTrust(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}

	env := mockEnv(map[string]string{
		"HOME": tmp,
	})

	projectTOML := `
[[startup]]
command = "echo prompted"
width = 80
`
	if err := os.WriteFile(".wideboi.toml", []byte(projectTOML), 0600); err != nil {
		t.Fatal(err)
	}

	// 1. User declines prompt: remains untrusted
	var prompted bool
	declinePrompt := func(path string, hasSensitive bool) bool {
		prompted = true
		return false
	}
	cfgDeclined, _, err := config.Load(config.ConfigFlags{PromptTrust: declinePrompt}, env)
	if err != nil {
		t.Fatalf("load declined: %v", err)
	}
	if !prompted {
		t.Fatal("expected prompt to be called")
	}
	if cfgDeclined.ProjectTrusted {
		t.Error("expected ProjectTrusted to be false when user declines")
	}
	if len(cfgDeclined.Startup) > 0 {
		t.Error("startup commands should not be loaded when user declines")
	}

	// 2. User approves prompt: trusted, persisted to trusted.toml
	prompted = false
	acceptPrompt := func(path string, hasSensitive bool) bool {
		prompted = true
		return true
	}
	cfgAccepted, _, err := config.Load(config.ConfigFlags{PromptTrust: acceptPrompt}, env)
	if err != nil {
		t.Fatalf("load accepted: %v", err)
	}
	if !prompted {
		t.Fatal("expected prompt to be called")
	}
	if !cfgAccepted.ProjectTrusted {
		t.Error("expected ProjectTrusted to be true when user accepts")
	}
	if len(cfgAccepted.Startup) == 0 || cfgAccepted.Startup[0].Command != "echo prompted" {
		t.Errorf("expected startup commands loaded, got %+v", cfgAccepted.Startup)
	}

	// 3. Subsequent load without prompt is now automatically trusted because 'y' was persisted
	prompted = false
	cfgSubsequent, _, err := config.Load(config.ConfigFlags{PromptTrust: func(string, bool) bool {
		prompted = true
		return false
	}}, env)
	if err != nil {
		t.Fatalf("load subsequent: %v", err)
	}
	if prompted {
		t.Error("expected no prompt on subsequent load of already-trusted project")
	}
	if !cfgSubsequent.ProjectTrusted {
		t.Error("expected subsequent load to be trusted from persisted trusted.toml")
	}
	if len(cfgSubsequent.Startup) == 0 {
		t.Error("expected startup commands on subsequent load")
	}
}
