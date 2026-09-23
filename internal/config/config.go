// Package config resolves, validates, and provides access to wideboi's
// configuration from defaults, TOML config files, environment variables,
// and command-line flags.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/lmorchard/wideboi/internal/keys"
	"github.com/lmorchard/wideboi/internal/logger"
	"github.com/lmorchard/wideboi/internal/protocol"
	toml "github.com/pelletier/go-toml/v2"
)

// Config represents the resolved, fully-validated configuration for wideboi.
type Config struct {
	Socket       string              `toml:"socket"`
	Layout       string              `toml:"layout"`
	LayoutMode   protocol.LayoutMode `toml:"-"`
	Prefix       string              `toml:"prefix"`
	PrefixLabel  string              `toml:"-"`
	Shell        string              `toml:"shell"`
	WidthPresets []int               `toml:"width_presets"`
	Keys         map[string]string   `toml:"keys"`
	// Mouse is a pointer so an absent key reads as the default (on)
	// rather than as false. Read MouseEnabled, not this.
	Mouse        *bool `toml:"mouse"`
	MouseEnabled bool  `toml:"-"`
	// LogLevelName is what was configured; LogLevel is it resolved.
	LogLevelName string     `toml:"log_level"`
	LogLevel     slog.Level `toml:"-"`
	ConfigFile   string     `toml:"-"`
}

// ConfigFlags contains command-line flag overrides passed into Load.
type ConfigFlags struct {
	ConfigFile string
	Layout     string
	Prefix     string
	Socket     string
	Shell      string
}

// DefaultConfigPath returns the standard XDG path for the wideboi config file.
func DefaultConfigPath(getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	if xdg := getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "wideboi", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "wideboi", "config.toml")
}

// DefaultSocketPath returns the standard Unix domain socket path for wideboi.
func DefaultSocketPath() string {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("wideboi-%d", os.Getuid()))
	return filepath.Join(dir, "default.sock")
}

// Load loads and validates configuration by resolving precedence:
// Defaults -> TOML file -> Environment variables -> Command-line flags.
func Load(flags ConfigFlags, getenv func(string) string) (Config, []keys.Binding, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	// 1. Defaults.
	// SHELL is a general POSIX environment variable set by login shells, so it
	// serves as the baseline default when not configured in TOML. WIDEBOI_SHELL
	// is the wideboi-specific environment override that takes precedence over TOML.
	cfg := Config{
		Layout: "cards",
		Prefix: "ctrl+b",
		Socket: DefaultSocketPath(),
		Shell:  getenv("SHELL"),
	}
	if cfg.Shell == "" {
		cfg.Shell = "/bin/sh"
	}

	// 2. Discover or read TOML config file
	cfgFile := flags.ConfigFile
	explicitFile := cfgFile != ""
	if !explicitFile {
		cfgFile = DefaultConfigPath(getenv)
	}

	if cfgFile != "" {
		data, err := os.ReadFile(cfgFile)
		if err != nil {
			if explicitFile || !os.IsNotExist(err) {
				return Config{}, nil, fmt.Errorf("config file %q: %w", cfgFile, err)
			}
			// Default discovered file not existing is ignored
		} else {
			var fileCfg Config
			if err := toml.Unmarshal(data, &fileCfg); err != nil {
				return Config{}, nil, fmt.Errorf("parsing config file %q: %w", cfgFile, err)
			}
			if fileCfg.Layout != "" {
				cfg.Layout = fileCfg.Layout
			}
			if fileCfg.Prefix != "" {
				cfg.Prefix = fileCfg.Prefix
			}
			if fileCfg.Socket != "" {
				cfg.Socket = fileCfg.Socket
			}
			if fileCfg.Shell != "" {
				cfg.Shell = fileCfg.Shell
			}
			if len(fileCfg.Keys) > 0 {
				cfg.Keys = fileCfg.Keys
			}
			if fileCfg.Mouse != nil {
				cfg.Mouse = fileCfg.Mouse
			}
			if len(fileCfg.WidthPresets) > 0 {
				cfg.WidthPresets = fileCfg.WidthPresets
			}
			if fileCfg.LogLevelName != "" {
				cfg.LogLevelName = fileCfg.LogLevelName
			}
			cfg.ConfigFile = cfgFile
		}
	}

	// 3. Environment variables
	if envLayout := getenv("WIDEBOI_LAYOUT"); envLayout != "" {
		cfg.Layout = envLayout
	}
	if envPrefix := getenv("WIDEBOI_PREFIX"); envPrefix != "" {
		cfg.Prefix = envPrefix
	}
	if envSock := getenv("WIDEBOI_SOCK"); envSock != "" {
		cfg.Socket = envSock
	}
	if envShell := getenv("WIDEBOI_SHELL"); envShell != "" {
		cfg.Shell = envShell
	}
	if envLevel := getenv("WIDEBOI_LOG_LEVEL"); envLevel != "" {
		cfg.LogLevelName = envLevel
	}

	// 4. Command line flags
	if flags.Layout != "" {
		cfg.Layout = flags.Layout
	}
	if flags.Prefix != "" {
		cfg.Prefix = flags.Prefix
	}
	if flags.Socket != "" {
		cfg.Socket = flags.Socket
	}
	if flags.Shell != "" {
		cfg.Shell = flags.Shell
	}

	// 5. Validation
	// Layout
	switch strings.ToLower(strings.TrimSpace(cfg.Layout)) {
	case "", "cards":
		cfg.Layout = "cards"
		cfg.LayoutMode = protocol.LayoutCards
	case "scroll":
		cfg.Layout = "scroll"
		cfg.LayoutMode = protocol.LayoutScroll
	default:
		return Config{}, nil, fmt.Errorf("layout %q: want \"scroll\" or \"cards\"", cfg.Layout)
	}

	// Prefix
	prefix, prefixLabel, err := parsePrefix(cfg.Prefix)
	if err != nil {
		return Config{}, nil, err
	}
	cfg.Prefix = prefix
	cfg.PrefixLabel = prefixLabel

	// Socket
	if cfg.Socket == "" {
		cfg.Socket = DefaultSocketPath()
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Socket), 0700); err != nil {
		return Config{}, nil, fmt.Errorf("ensuring socket directory: %w", err)
	}

	// Shell
	if cfg.Shell == "" {
		cfg.Shell = "/bin/sh"
	}

	// Log level
	level, err := logger.ParseLevel(cfg.LogLevelName)
	if err != nil {
		return Config{}, nil, err
	}
	cfg.LogLevel = level

	// Mouse
	cfg.MouseEnabled = cfg.Mouse == nil || *cfg.Mouse

	// Keys
	bindings, err := keys.BuildBindings(cfg.Keys)
	if err != nil {
		return Config{}, nil, fmt.Errorf("keys configuration: %w", err)
	}

	// Width presets
	for _, p := range cfg.WidthPresets {
		if p < 20 {
			return Config{}, nil, fmt.Errorf("width_presets: invalid preset %d (must be at least 20)", p)
		}
	}

	return cfg, bindings, nil
}

func parsePrefix(name string) (prefix, label string, err error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "ctrl+b", "C-b", nil
	}
	if name == "ctrl+space" {
		return "ctrl+space", "C-space", nil
	}
	if strings.HasPrefix(name, "ctrl+") && len(name) == 6 {
		letter := name[5:6]
		if letter >= "a" && letter <= "z" {
			if why, bad := keys.Reserved[letter]; bad {
				return "", "", fmt.Errorf("prefix %q is reserved: ctrl+%s is %s, so it has no repeat form",
					name, letter, why)
			}
			return name, "C-" + letter, nil
		}
	}
	return "", "", fmt.Errorf("prefix %q: want \"ctrl+<a-z>\" or \"ctrl+space\"", name)
}
