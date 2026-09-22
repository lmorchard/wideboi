# Notes: Session 2026-09-22-1252-issue-58-35-config-and-keys

## Goals
Address issues #58 (Configuration as a topic: env knobs accumulating ad hoc) and #35 (Config file, and key remapping for control mode).

## Key Decisions
- Format: TOML using `github.com/pelletier/go-toml/v2`.
- Precedence: defaults -> config file -> env -> CLI flags.
- Discovery: `--config` flag, or `$XDG_CONFIG_HOME/wideboi/config.toml`, fallback `~/.config/wideboi/config.toml`.
- CLI Parsing: Go stdlib `flag.FlagSet`.
- Key Remapping: Action-to-key mapping via `[keys]` table. Remapped keys update status bar groups, help overlay descriptions, and ctrl repeat chords. Strict rejection of reserved keys (`i`, `m`, `[`), unmatchable names, and collisions.

## What Shipped
1. `internal/keys`:
   - Canonical action name constants (`ActionNameFocusLeft`, `ActionNameKillPane`, etc.).
   - `BuildBindings(custom map[string]string) ([]Binding, error)`: validates remappings against `keys.Reserved` (`i`, `m`, `[`), detects duplicates/collisions, rejects unknown actions, dynamically computes `BarGroup` labels without expanding status bar budget.
   - `BarItemsFor(bindings, detachable)`: supports arbitrary binding tables for status bar rendering.
2. `internal/config`:
   - `Config` and `ConfigFlags` structs.
   - `DefaultConfigPath` and `DefaultSocketPath`.
   - `Load(flags, getenv)` resolving precedence (defaults -> TOML file -> env -> flags) with centralized validation for layouts, prefixes, paths, and keys.
3. `cmd/wideboi` and `internal/client`:
   - CLI flags via `flag.FlagSet`: `-c/--config`, `-l/--layout`, `-p/--prefix`, `-s/--socket`, `--shell`, `-v/--version`, `-h/--help`.
   - Comprehensive `--help` output documenting usage, subcommands, flags, env vars, and default paths.
   - Single-call configuration resolution in `main()`, eliminating duplicated `os.Getenv` logic in `runServer`, `runAttach`, and `run`.
   - Custom bindings wired through `Client.SetBindings` and `router.bindings`, reflected in status bar menu and help overlay.
4. Verification:
   - Full test coverage across `internal/keys`, `internal/config`, `cmd/wideboi`, `internal/client`.
   - End-to-end smoke test case in `scripts/smoke.py` (`config file and key remapping`).
   - 4 repeated passes of `make check` ensuring zero concurrency or timing flakes.

