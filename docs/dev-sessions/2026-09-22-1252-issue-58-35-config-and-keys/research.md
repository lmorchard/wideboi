# Codebase Research: Configuration and Keys

Findings for issues #58 and #35.

## 1. Where and how configuration values are parsed and validated today

Configuration is currently read ad hoc via `os.Getenv` and hardcoded fallbacks across `cmd/wideboi/main.go` and `cmd/wideboi/router.go`:

- **`WIDEBOI_SOCK`**:
  - Read in `cmd/wideboi/main.go:39-47` (`defaultSocketPath()`).
  - If set (`main.go:40`), ensures parent dir existence (`os.MkdirAll(filepath.Dir(p), 0700)`, `main.go:41`) and returns it.
  - If empty, defaults to `filepath.Join(os.TempDir(), fmt.Sprintf("wideboi-%d", os.Getuid()), "default.sock")` (`main.go:44-46`).
  - Passed to `runServer(socketPath)` (`main.go:65`), `runAttach(socketPath)` (`main.go:68`), and `run()` (`main.go:277, 280`).

- **`WIDEBOI_LAYOUT`**:
  - Read via `os.Getenv("WIDEBOI_LAYOUT")` in `cmd/wideboi/main.go:132` (in `runServer`) and `cmd/wideboi/main.go:299` (in `run`).
  - Parsed and validated by `parseLayout(name string)` (`cmd/wideboi/main.go:108-117`).
  - Strictly validated: `""` or `"cards"` -> `protocol.LayoutCards` (`main.go:110-111`); `"scroll"` -> `protocol.LayoutScroll` (`main.go:112-113`). Any other string returns error `WIDEBOI_LAYOUT=%q: want "scroll" or "cards"`.
  - Passed to `srv.SetLayout(layoutMode)` (`cmd/wideboi/main.go:144, 328`).

- **`WIDEBOI_PREFIX`**:
  - Read via `os.Getenv("WIDEBOI_PREFIX")` in `cmd/wideboi/main.go:166` (in `runAttach`) and `cmd/wideboi/main.go:289` (in `run`).
  - Defaults to `defaultPrefix = "ctrl+b"` (`cmd/wideboi/router.go:161`) if empty (`main.go:167-169, 290-292`).
  - Parsed and validated by `parsePrefix(name string)` (`cmd/wideboi/router.go:175-192`).
  - Validation rules: lowercases and trims input; allows `"ctrl+space"` (label: `"C-space"`) or single-letter `"ctrl+<a-z>"` (label: `"C-<letter>"`). It explicitly rejects any letter in `keys.Reserved` (`"i"`, `"m"`, `"["`) with an error (`router.go:183-188`). All other formats error.
  - Passed as `prefix` into `router{prefix: prefix}` (`cmd/wideboi/main.go:206, 357`) and as `prefixLabel` into `client.NewClient(..., prefixLabel)` (`cmd/wideboi/main.go:204, 356`).

- **`SHELL`**:
  - Read via `os.Getenv("SHELL")` in `cmd/wideboi/main.go:126` (in `runServer`) and `cmd/wideboi/main.go:283` (in `run`). Fallback default is `"/bin/sh"` (`main.go:128, 285`).
  - Also read directly in `internal/server/server.go:66-68` inside `NewServer(...)` as a fallback if the passed `shell` argument is empty.
  - Passed to `server.NewServer(..., shell, cwd)` (`cmd/wideboi/main.go:143, 327`).

- **CLI flags and args**:
  - No `flag` package or external CLI library used.
  - Manual switch on `os.Args[1]` in `cmd/wideboi/main.go:58-73` for `"version"`, `"--version"`, `"-v"`, `"server"`, `"attach"`. Any unrecognized argument falls through to `run()`.

## 2. Structure and consumption of `internal/keys`

- `internal/keys/keys.go`:
  - `Action` (`keys.go:19-35`): `ActionVerb`, `ActionScroll`, `ActionQuit`, `ActionDetach`, `ActionHelp`, `ActionExit`.
  - `Binding` struct (`keys.go:48-87`):
    - `Key string`
    - `Aliases []string`
    - `Action Action`
    - `Verb protocol.VerbType`
    - `Scroll int`
    - `BarGroup string`
    - `Long string`
    - `NeedsDetach bool`
    - `Essential bool`
    - `NoRepeat bool`
  - `Bindings []Binding` (`keys.go:90-127`): Central static slice of all 13 bindings.
  - `keys.Reserved` (`keys.go:41-45`): map of single-character keys that cannot carry a repeat binding or prefix: `"i"` (tab), `"m"` (enter), `"["` (escape).
  - `(b Binding) CtrlForm() (string, bool)` (`keys.go:135-150`): returns `"ctrl+" + b.Key` if not `NoRepeat`, single letter `a-z`, not in `Reserved`.
  - `(b Binding) MatchNames() []string` (`keys.go:154-159`): returns `[Key, Aliases...]`.
  - `keys.BarItems(detachable bool) (droppable, essential []string)` (`keys.go:164-180`): extracts unique non-empty `BarGroup` strings.

- Consumers:
  - **Router** (`cmd/wideboi/router.go:103-123`): Matches incoming keys against `keys.Bindings` (checking `CtrlForm()` and `MatchNames()`).
  - **Status Bar** (`internal/client/client.go:743-759`): Formats bar items using `keys.BarItems(...)`.
  - **Help Overlay** (`internal/client/help.go:18-36`): Renders key and description using `b.Key` and `b.Long`.

## 3. CLI arguments and subcommand dispatch

- `cmd/wideboi/main.go:58-73`:
  - Matches `os.Args[1]` for `version` / `--version` / `-v`, `server`, `attach`.
  - Default runs `run()`, which probes the socket: if running, attaches; if not, starts in-proc server and client.
  - No `--help`, `--config`, or flags currently parsed.

## 4. Dependencies and paths

- `go.mod`:
  - Standard library only for parsing (no TOML/YAML/etc. dependencies).
  - Direct deps: `charmbracelet/ultraviolet`, `charmbracelet/x/vt`, `creack/pty`.
- Paths:
  - Socket path: `defaultSocketPath()` uses `WIDEBOI_SOCK` or `$TMPDIR/wideboi-<uid>/default.sock`.
  - Log path: `logger.Init` uses `$TMPDIR/wideboi-<uid>/<component>.log`.
  - No XDG path lookup currently in codebase.
