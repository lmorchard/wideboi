// Package config resolves, validates, and provides access to wideboi's
// configuration from defaults, TOML config files, environment variables,
// and command-line flags.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lmorchard/wideboi/internal/keys"
	"github.com/lmorchard/wideboi/internal/logger"
	"github.com/lmorchard/wideboi/internal/protocol"
	toml "github.com/pelletier/go-toml/v2"
)

// Config represents the resolved, fully-validated configuration for wideboi.
type Config struct {
	Socket         string `toml:"socket"`
	Websocket      string `toml:"websocket"`
	WebsocketToken string `toml:"websocket_token"`
	// Session is the resolved session name, or empty when a socket path
	// was chosen instead. Socket is what is used.
	Session      string              `toml:"session"`
	Layout       string              `toml:"layout"`
	LayoutMode   protocol.LayoutMode `toml:"-"`
	Prefix       string              `toml:"prefix"`
	PrefixLabel  string              `toml:"-"`
	Shell        string              `toml:"shell"`
	WidthPresets []int               `toml:"width_presets"`
	Keys         map[string]any      `toml:"keys"`
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
	ConfigFile     string
	Layout         string
	Prefix         string
	Socket         string
	Session        string
	Websocket      string
	WebsocketToken string
	Shell          string
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

// SessionDir holds the sockets of named sessions, and so their locks
// and logs.
func SessionDir() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("wideboi-%d", os.Getuid()))
}

// SessionSocketPath is where the session called name listens.
func SessionSocketPath(name string) string {
	return filepath.Join(SessionDir(), name+".sock")
}

// DefaultSocketPath is the session a bare wideboi means.
func DefaultSocketPath() string {
	return SessionSocketPath("default")
}

var sessionNameRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

// validateSessionName keeps a name a single, visible path element that
// cannot be mistaken for a flag.
func validateSessionName(name string) error {
	if !sessionNameRE.MatchString(name) {
		return fmt.Errorf("session name %q: use letters, digits, '_', '-' and '.', not starting with '.' or '-'", name)
	}
	return nil
}

// ValidSessionName reports whether -L could address name. Shared with
// `wideboi ls`, so both apply one rule.
func ValidSessionName(name string) bool {
	return validateSessionName(name) == nil
}

// SessionName reports the name a user would pass to -L for socket, if
// it is a named session's socket at all.
func SessionName(socket string) (string, bool) {
	if filepath.Dir(socket) != SessionDir() || !strings.HasSuffix(socket, ".sock") {
		return "", false
	}
	name := strings.TrimSuffix(filepath.Base(socket), ".sock")
	return name, ValidSessionName(name)
}

// applySessionLayer applies one precedence layer's choice of session:
// a name, a path, or neither. Both at once is ambiguous, so it is an
// error rather than a silent preference.
func applySessionLayer(cfg *Config, layer, session, socket string) error {
	switch {
	case session != "" && socket != "":
		return fmt.Errorf("%s sets both a session name (%q) and a socket path (%q); set one, not both", layer, session, socket)
	case session != "":
		if err := validateSessionName(session); err != nil {
			return fmt.Errorf("%s: %w", layer, err)
		}
		cfg.Socket = SessionSocketPath(session)
		cfg.Session = session
	case socket != "":
		cfg.Socket = socket
		cfg.Session = ""
	}
	return nil
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
		Layout:         "cards",
		Prefix:         "ctrl+b",
		Socket:         DefaultSocketPath(),
		Session:        "default",
		Shell:          getenv("SHELL"),
		Websocket:      "",
		WebsocketToken: "",
	}
	if cfg.Shell == "" {
		cfg.Shell = "/bin/sh"
	}

	// 2. Discover or read TOML config files
	applyFile := func(cfgFile string, explicit bool) error {
		if cfgFile == "" {
			return nil
		}
		data, err := os.ReadFile(cfgFile)
		if err != nil {
			if explicit || !os.IsNotExist(err) {
				return fmt.Errorf("config file %q: %w", cfgFile, err)
			}
			// Implicit file not existing is ignored
			return nil
		}
		var fileCfg Config
		if err := toml.Unmarshal(data, &fileCfg); err != nil {
			return fmt.Errorf("parsing config file %q: %w", cfgFile, err)
		}
		if fileCfg.Layout != "" {
			cfg.Layout = fileCfg.Layout
		}
		if fileCfg.Prefix != "" {
			cfg.Prefix = fileCfg.Prefix
		}
		if err := applySessionLayer(&cfg, "config file "+cfgFile, fileCfg.Session, fileCfg.Socket); err != nil {
			return err
		}
		if fileCfg.Shell != "" {
			cfg.Shell = fileCfg.Shell
		}
		if len(fileCfg.Keys) > 0 {
			if cfg.Keys == nil {
				cfg.Keys = make(map[string]any)
			}
			for k, v := range fileCfg.Keys {
				cfg.Keys[k] = v
			}
		}
		if fileCfg.Layout != "" {
			cfg.Layout = fileCfg.Layout
		}
		if fileCfg.Prefix != "" {
			cfg.Prefix = fileCfg.Prefix
		}
		if err := applySessionLayer(&cfg, "config file "+cfgFile, fileCfg.Session, fileCfg.Socket); err != nil {
			return err
		}
		if fileCfg.Shell != "" {
			cfg.Shell = fileCfg.Shell
		}
		if fileCfg.Mouse != nil {
			cfg.Mouse = fileCfg.Mouse
		}
		if len(fileCfg.WidthPresets) > 0 {
			cfg.WidthPresets = fileCfg.WidthPresets
		}
		if fileCfg.Websocket != "" {
			cfg.Websocket = fileCfg.Websocket
		}
		if fileCfg.WebsocketToken != "" {
			cfg.WebsocketToken = fileCfg.WebsocketToken
		}
		if fileCfg.LogLevelName != "" {
			cfg.LogLevelName = fileCfg.LogLevelName
		}

		if cfg.ConfigFile == "" {
			cfg.ConfigFile = cfgFile
		} else {
			cfg.ConfigFile += ", " + cfgFile
		}
		return nil
	}

	if flags.ConfigFile != "" {
		if err := applyFile(flags.ConfigFile, true); err != nil {
			return Config{}, nil, err
		}
	} else {
		if err := applyFile(DefaultConfigPath(getenv), false); err != nil {
			return Config{}, nil, err
		}
		if err := applyFile(".wideboi.toml", false); err != nil {
			return Config{}, nil, err
		}
	}

	// 3. Environment variables
	if envLayout := getenv("WIDEBOI_LAYOUT"); envLayout != "" {
		cfg.Layout = envLayout
	}
	if envWS := getenv("WIDEBOI_WEBSOCKET"); envWS != "" {
		cfg.Websocket = envWS
	}
	if envToken := getenv("WIDEBOI_WEBSOCKET_TOKEN"); envToken != "" {
		cfg.WebsocketToken = envToken
	}
	if envPrefix := getenv("WIDEBOI_PREFIX"); envPrefix != "" {
		cfg.Prefix = envPrefix
	}
	if err := applySessionLayer(&cfg, "environment", getenv("WIDEBOI_SESSION"), getenv("WIDEBOI_SOCK")); err != nil {
		return Config{}, nil, err
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
	if flags.Websocket != "" {
		cfg.Websocket = flags.Websocket
	}
	if flags.WebsocketToken != "" {
		cfg.WebsocketToken = flags.WebsocketToken
	}
	if flags.Prefix != "" {
		cfg.Prefix = flags.Prefix
	}
	if err := applySessionLayer(&cfg, "command line", flags.Session, flags.Socket); err != nil {
		return Config{}, nil, err
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
	lists, err := keyLists(cfg.Keys)
	if err != nil {
		return Config{}, nil, fmt.Errorf("keys configuration: %w", err)
	}
	bindings, err := keys.BuildBindings(lists)
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

// keyLists turns the decoded [keys] table into one key list per action.
// A string is a one-key list; an empty list unbinds the action.
func keyLists(raw map[string]any) (map[string][]string, error) {
	out := make(map[string][]string, len(raw))
	for act, v := range raw {
		switch v := v.(type) {
		case string:
			out[act] = []string{v}
		case []any:
			list := make([]string, 0, len(v))
			for _, e := range v {
				s, ok := e.(string)
				if !ok {
					return nil, fmt.Errorf("action %q: list entries must be quoted key names, got %v", act, e)
				}
				list = append(list, s)
			}
			out[act] = list
		default:
			return nil, fmt.Errorf("action %q: want a key name or a list of key names, got %v", act, v)
		}
	}
	return out, nil
}
