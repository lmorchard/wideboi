# Unified Configuration, TOML Config File, and Control Mode Key Remapping

**Goal:** Unify wideboi's configuration into a single structured configuration layer (defaults -> config file -> env -> CLI flags) backed by a TOML configuration file, and allow remapping control-mode keys while preserving keyboard invariants and status bar budgets.

**Source:** GitHub Issues #58 and #35

## Current state

- Configuration is read ad hoc via `os.Getenv` and hardcoded fallbacks in multiple places:
  - `WIDEBOI_SOCK` in `cmd/wideboi/main.go:39-47`.
  - `WIDEBOI_LAYOUT` in `cmd/wideboi/main.go:132, 299`, parsed by `parseLayout` (`main.go:108-117`).
  - `WIDEBOI_PREFIX` in `cmd/wideboi/main.go:166, 289`, parsed by `parsePrefix` (`cmd/wideboi/router.go:175-192`).
  - `SHELL` in `cmd/wideboi/main.go:126, 283` and `internal/server/server.go:66-68`.
- Subcommand dispatch is handled by manual `os.Args[1]` switching in `cmd/wideboi/main.go:58-73`; no standard flag parser or `--help` output exists.
- Keys are defined statically as `keys.Bindings` in `internal/keys/keys.go:90-127`.
  - `keys.Reserved` (`keys.go:41-45`) prohibits `"i"`, `"m"`, and `"["` because their control bytes collide with Tab, Enter, and Escape.
  - `keys.BarItems` (`keys.go:164-180`) extracts `BarGroup` strings for the status bar, which operates under a strict 79-column budget.
  - Help overlay (`internal/client/help.go:18-36`) lists `b.Key` and `b.Long`.
- There are no config file dependencies or XDG lookup conventions in the project today (`go.mod:5-28`).

## Desired end state

1. **Unified `config` package (`internal/config`):**
   - Holds a single typed `Config` struct representing all configuration options:
     - `Socket string` (path to unix domain socket)
     - `Layout string` (`"cards"` or `"scroll"`)
     - `Prefix string` (`"ctrl+b"`, `"ctrl+space"`, etc.)
     - `Shell string` (path to shell binary)
     - `ConfigFile string` (path to config file used, if any)
     - `Keys map[string]string` (action name -> key character/name)
   - Resolves configuration with strict precedence:
     1. Hardcoded defaults
     2. Config file (`$XDG_CONFIG_HOME/wideboi/config.toml` or `~/.config/wideboi/config.toml`, or path from `--config`)
     3. Environment variables (`WIDEBOI_SOCK`, `WIDEBOI_LAYOUT`, `WIDEBOI_PREFIX`, `SHELL`)
     4. Command line flags
   - Validates all fields once at load time, failing early with clear error messages.
2. **Configuration file (TOML):**
   - Parsed using `github.com/pelletier/go-toml/v2`.
   - Default discovery: `$XDG_CONFIG_HOME/wideboi/config.toml` (fallback `~/.config/wideboi/config.toml`). If missing at default path, ignored without error. If explicit `--config` flag is passed and missing/invalid, reports error and exits.
   - Example schema:
     ```toml
     prefix = "ctrl+b"
     layout = "cards"
     socket = "/tmp/custom.sock"
     shell = "/bin/zsh"

     [keys]
     focus_left = "h"
     focus_right = "l"
     scroll_down = "j"
     scroll_up = "k"
     new_column = "n"
     cycle_width = "w"
     kill_pane = "x"
     smart_jump = "a"
     toggle_cards = "c"
     detach = "d"
     quit = "q"
     help = "?"
     exit = "esc"
     ```
3. **CLI flags and help (`flag.FlagSet`):**
   - Flags supported across commands:
     - `-c`, `--config <path>`: config file path
     - `-l`, `--layout <cards|scroll>`: layout mode
     - `-p`, `--prefix <key>`: prefix key
     - `-s`, `--socket <path>`: socket path
     - `--shell <path>`: shell path
     - `-v`, `--version`: print version
     - `-h`, `--help`: print usage detailing subcommands, flags, env vars, and config file locations
   - Subcommands remain `server`, `attach`, or default (probe/run). Flags can precede or follow subcommands.
4. **Key remapping & validation in `internal/keys`:**
   - Expose canonical action identifiers:
     `focus_left`, `focus_right`, `scroll_down`, `scroll_up`, `new_column`, `cycle_width`, `kill_pane`, `smart_jump` (alias `attn`), `toggle_cards`, `detach`, `quit`, `help`, `exit`.
   - Remapping constructs a custom `KeyMap` / `[]Binding` slice replacing default keys.
   - Validation rules:
     - Key names must be valid ultraviolet names or printable single characters.
     - Keys cannot be in `keys.Reserved` (`"i"`, `"m"`, `"["`).
     - No key collisions: two actions cannot be bound to the same key.
     - Unknown action names in `[keys]` error with a list of valid actions.
   - UI consistency:
     - Status bar `BarGroup` labels update to reflect remapped keys (e.g. `"<key> kill"`, `"<key> width"`). If moving keys, labels stay within length constraints.
     - Help overlay dynamically displays updated keys and descriptions.
     - Ctrl repeat forms (`CtrlForm()`) automatically generate for single-character keys (`a-z`) not in `Reserved`.

## Design decisions

- **Decision:** Use TOML with `github.com/pelletier/go-toml/v2`.
  - **Why:** Strict, unambiguous syntax without whitespace pitfalls. Standard format for CLI and terminal configuration. `go-toml/v2` is well-maintained, fast, and strict.
  - **Rejected:** JSON (inflexible, comments disallowed, poor UX for manual config editing); YAML (indentation quirks, complex parser edge cases).
- **Decision:** Resolve configuration into a dedicated `internal/config` package once at startup.
  - **Why:** Solves #58. Eliminates duplicate `os.Getenv` calls between `runServer`, `runAttach`, and `run()`. Single source of truth for validation and default values.
  - **Rejected:** Continuing to read `os.Getenv` at call sites or embedding config logic directly in `cmd/wideboi/main.go`.
- **Decision:** Use standard Go `flag.FlagSet` for CLI parsing.
  - **Why:** No heavyweight dependencies (like cobra), standard Go idiom, supports `-flag` and `--flag`, custom Usage output.
  - **Rejected:** Manual `os.Args` slicing (too brittle for multiple flags and options); external CLI frameworks (unnecessary overhead for wideboi's simple command structure).
- **Decision:** Action-to-key mapping for `[keys]` table.
  - **Why:** Users think in terms of "I want kill to be 'k'". Preserves action semantics, flags, detach requirements, and automatic ctrl chord generation without burdening the user with internal binding fields.
  - **Rejected:** Freeform list of binding structs in TOML (overly verbose, exposes internal fields like `NeedsDetach`, `NoRepeat`, `ActionVerb`).
- **Decision:** Strict collision and reserved-key rejection.
  - **Why:** As documented in `LESSONS.md` and #35, bindings to `i`, `m`, `[` silently break repeat chords, and duplicate bindings cause unpredictable routing. Validating at load time turns silent bugs into actionable errors.
  - **Rejected:** Silently ignoring invalid keys or letting later bindings shadow earlier ones.

## Patterns to follow

- PTY and key handling conventions in `internal/keys/keys.go:41-45, 135-159`.
- Strict string validation pattern in `parsePrefix` (`cmd/wideboi/router.go:175-192`) and `parseLayout` (`cmd/wideboi/main.go:108-117`).
- Wire type and package seam enforcement (`make seam-check`): `internal/config` must not violate client/server isolation.
- Test coverage with `-count=1` per `LESSONS.md`.

## What we're NOT doing

- Not adding interactive keybinding customization / runtime rebinding UI inside the terminal.
- Not implementing multi-session daemon directory discovery (#27); this spec supports socket path configuration which #27 will build upon.
- Not adding scriptable command execution or user-defined macro bindings beyond the existing actions.
- Not supporting arbitrary shell commands as keybindings.
- Not adding YAML or JSON config alternatives (TOML is the single supported config format).

## Open questions

*(None. All design decisions resolved.)*
