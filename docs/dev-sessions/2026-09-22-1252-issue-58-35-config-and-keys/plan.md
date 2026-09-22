# Unified Configuration, TOML Config File, and Control Mode Key Remapping Implementation Plan

**Goal:** Unify wideboi's configuration into a single structured configuration layer (`internal/config`) with TOML file support, standard CLI flags, and customizable control-mode key remapping (`internal/keys`).

**Approach:**
- Allow remapping of control-mode verbs in `internal/keys` through an action-to-key map, validating against `keys.Reserved` and key collisions while maintaining the 79-column status bar budget.
- Introduce `internal/config` using `github.com/pelletier/go-toml/v2` to resolve configuration once from defaults -> TOML file -> environment -> CLI flags.
- Wire the resolved config and custom bindings into `cmd/wideboi` and `internal/client`, replacing ad-hoc `os.Getenv` calls and adding standard CLI flags with informative `--help`.

**Tech stack:** Go, `github.com/pelletier/go-toml/v2`, Go stdlib `flag.FlagSet`.

---

## Phase 1: Key Remapping and Validation in `internal/keys`

Add action-to-key remapping to `internal/keys`. Users specify remappings via canonical action names (e.g., `kill_pane = "k"`). The package validates keys against `keys.Reserved`, checks for duplicate key assignments, ensures ultraviolet compatibility, and generates updated `BarGroup` labels without expanding the status bar footprint.

**Files:**
- Modify: `internal/keys/keys.go` — Add action identifiers, `BuildBindings(custom map[string]string) ([]Binding, error)`, and `BarItemsFor(bindings []Binding, detachable bool)`.
- Test: `internal/keys/keys_test.go` — Add unit tests for key validation, collision detection, reserved key rejection, and dynamic bar item generation.

**Key changes:**
- Canonical action name constants:
  `ActionNameFocusLeft = "focus_left"`
  `ActionNameFocusRight = "focus_right"`
  `ActionNameScrollDown = "scroll_down"`
  `ActionNameScrollUp = "scroll_up"`
  `ActionNameNewColumn = "new_column"`
  `ActionNameCycleWidth = "cycle_width"`
  `ActionNameKillPane = "kill_pane"`
  `ActionNameSmartJump = "smart_jump"` (and alias `"attn"`)
  `ActionNameToggleCards = "toggle_cards"`
  `ActionNameHelp = "help"`
  `ActionNameDetach = "detach"`
  `ActionNameQuit = "quit"`
  `ActionNameExit = "exit"`
- `BuildBindings(custom map[string]string) ([]Binding, error)`:
  Validates custom keys, merges onto the base template, ensures no collisions, and builds dynamic `BarGroup` strings.
- `BarItemsFor(bindings []Binding, detachable bool) (droppable, essential []string)`:
  Generalizes `BarItems` to take an arbitrary `[]Binding` slice. `BarItems(detachable)` calls `BarItemsFor(Bindings, detachable)`.

```go
func BuildBindings(custom map[string]string) ([]Binding, error) {
    // 1. Validate custom action names against known actions
    // 2. Validate custom keys: non-empty, not in Reserved ("i", "m", "[")
    // 3. Clone default bindings template and apply new keys
    // 4. Validate no two bindings share the same Key
    // 5. Compute dynamic BarGroup labels (e.g. "<key> kill", "<h><j><k><l> move")
    // 6. Return new []Binding
}
```

**Verification — automated:**
- [x] `go test -v -count=1 ./internal/keys` passes
- [x] `make seam-check` passes
- [x] `make quick` passes

**Verification — manual:**
- [x] Verify `keys.Reserved` rejection error message clearly names the forbidden control byte.

---

## Phase 2: TOML Dependency and `internal/config` Package

Add `github.com/pelletier/go-toml/v2` to `go.mod`. Implement `internal/config` to resolve configuration with strict precedence: defaults -> TOML file -> env vars -> CLI flags. Validate layout, prefix, socket, shell, and key bindings in one place.

**Files:**
- Modify: `go.mod`, `go.sum` — Add `github.com/pelletier/go-toml/v2`.
- Create: `internal/config/config.go` — Define `Config`, `ConfigFlags`, `DefaultConfig`, path discovery (`XDG_CONFIG_HOME`), `Load`, and validation.
- Create: `internal/config/config_test.go` — Test TOML file reading, discovery fallback, environment overrides, flag overrides, precedence, and error handling.

**Key changes:**
- `Config` struct:
  ```go
  type Config struct {
      Socket     string            `toml:"socket"`
      Layout     string            `toml:"layout"`
      Prefix     string            `toml:"prefix"`
      Shell      string            `toml:"shell"`
      Keys       map[string]string `toml:"keys"`
      ConfigFile string            `toml:"-"`
  }
  ```
- `ConfigFlags` struct for CLI flag values passed in from `main`.
- `Load(flags ConfigFlags, getenv func(string) string) (Config, []keys.Binding, error)`:
  - Resolves socket path fallback: `$WIDEBOI_SOCK` -> config file `socket` -> `$TMPDIR/wideboi-<uid>/default.sock`.
  - Resolves layout: flag -> `$WIDEBOI_LAYOUT` -> config file `layout` -> `"cards"`. Validates `"cards"` or `"scroll"`.
  - Resolves prefix: flag -> `$WIDEBOI_PREFIX` -> config file `prefix` -> `"ctrl+b"`. Validates prefix syntax and `keys.Reserved`.
  - Resolves shell: flag -> `$SHELL` -> config file `shell` -> `"/bin/sh"`.
  - Resolves keys: config file `[keys]` -> `keys.BuildBindings(...)`.
  - Finds config file: `flags.ConfigFile` -> `$XDG_CONFIG_HOME/wideboi/config.toml` -> `~/.config/wideboi/config.toml`. If `flags.ConfigFile` was specified but missing, returns error; if discovered default path is missing, ignores without error.

**Verification — automated:**
- [x] `go test -v -count=1 ./internal/config` passes
- [x] `make seam-check` passes
- [x] `make quick` passes

**Verification — manual:**
- [x] Verify precedence order in tests: flag overrides env, env overrides toml, toml overrides defaults.

---

## Phase 3: CLI Flags & Subcommands in `cmd/wideboi` and `internal/client`

Integrate `internal/config` into `cmd/wideboi/main.go` and `cmd/wideboi/router.go`. Add `flag.FlagSet` for CLI flags (`-c/--config`, `-l/--layout`, `-p/--prefix`, `-s/--socket`, `--shell`, `-v/--version`, `-h/--help`). Wire custom bindings to the client and router.

**Files:**
- Modify: `cmd/wideboi/main.go` — Parse flags, call `config.Load`, pass resolved config and bindings to `runServer`, `runAttach`, and `run()`. Remove duplicate `os.Getenv` logic.
- Modify: `cmd/wideboi/router.go` — Accept custom `[]keys.Binding` in router initialization.
- Modify: `internal/client/client.go` — Add `SetBindings(b []keys.Binding)` or store custom `bindings` on `Client`, used by `controlHelp()`.
- Modify: `internal/client/help.go` — Use `c.bindings` in `helpLines()`.
- Create/Modify: `cmd/wideboi/cli_test.go` — Tests for CLI flags, help text, and error handling.

**Key changes:**
- CLI flags definition:
  - `-c`, `-config`: config file path
  - `-l`, `-layout`: layout mode (`cards` or `scroll`)
  - `-p`, `-prefix`: prefix key
  - `-s`, `-socket`: socket path
  - `-shell`: shell binary path
  - `-v`, `-version`: display version info
  - `-h`, `-help`: display help text
- Subcommand handling:
  - `wideboi [flags]`
  - `wideboi [flags] server [flags]`
  - `wideboi [flags] attach [flags]`
  - `wideboi [flags] version`
- Clean help output documenting all flags, environment variables, config file paths, and subcommands.

**Verification — automated:**
- [x] `go test -v -count=1 ./cmd/wideboi` passes
- [x] `go test -v -count=1 ./internal/client` passes
- [x] `make seam-check` passes
- [x] `make quick` passes

**Verification — manual:**
- [x] Run `./bin/wideboi -h` and verify clean, well-formatted help output.

---

## Phase 4: End-to-End Verification and Smoke Tests

Verify wideboi end-to-end with a sample TOML configuration file, verifying key remappings, layout override, prefix override, and the full test suite.

**Files:**
- Test: `testdata/test_config.toml` (temporary or test fixture)

**Verification — automated:**
- [x] `make quick` passes
- [x] `make check` passes (including race, seam, smoke, attach, exit contract)
- [x] Repeat `make check` multiple times to verify no concurrency flakes per `LESSONS.md` (verified 4/4 runs)

**Verification — manual:**
- [x] Verify launching `./bin/wideboi -c testdata/test_config.toml` starts with remapped keys in status bar and help overlay (`?`).
