# Research: Issue #23 — Support OSC 9;4 Progress

## 1. Existing Status Architecture in Wideboi

### Status Representation
- `internal/server/term/grid.go:111-134`:
  ```go
  type PaneStatus int
  const (
      StatusIdle PaneStatus = iota
      StatusWorking
      StatusNeedsInput
      StatusDone
      StatusFailed
  )
  ```
  Glyphs:
  - `StatusWorking`: `"»"`
  - `StatusNeedsInput`: `"!"`
  - `StatusDone`: `"✓"`
  - `StatusFailed`: `"✗"`
  - `StatusIdle` (or default): `" "`

### Current OSC 133 Handler & Latch
- `internal/server/term/grid.go:220-266`:
  ```go
  g.em.RegisterOscHandler(133, func(data []byte) bool {
      parts := strings.Split(string(data), ";")
      if len(parts) < 2 {
          return false
      }
      var st PaneStatus
      switch parts[1] {
      case "A", "B":
          st = StatusNeedsInput
      case "C":
          st = StatusWorking
      case "D":
          st = StatusDone
          if len(parts) > 2 && parts[2] != "" && parts[2] != "0" {
              st = StatusFailed
          }
      default:
          return false
      }
      g.status.Store(int32(st))
      g.sawOSC133.Store(true)
      return true
  })
  ```

### The Heuristic and Latch
- `internal/server/term/grid.go:277-280`:
  ```go
  if !g.sawOSC133.Load() {
      g.status.Store(int32(StatusWorking))
  }
  ```
- `internal/server/term/grid.go:292-304`:
  ```go
  func (g *vtGrid) Status() PaneStatus {
      st := PaneStatus(g.status.Load())
      if !g.sawOSC133.Load() && st == StatusWorking {
          idle := g.idleTimeout
          if idle <= 0 {
              idle = DefaultIdleTimeout
          }
          if t := g.lastWriteTime.Load(); t != nil && time.Since(*t) > idle {
              return StatusIdle
          }
      }
      return st
  }
  ```
  Once `sawOSC133` is true, the activity heuristic on `Write()` and the 3s decay to `StatusIdle` in `Status()` are disabled.

## 2. OSC 9;4 Protocol & Agent Behavior

### OSC 9 / 9;4 Format
- ConEmu / Windows Terminal progress reporting protocol.
- Sequence: `ESC ] 9 ; 4 ; <state> [; <progress>] <ST|BEL>`
- States:
  - `0`: Remove / clear progress indicator (normal/idle state).
  - `1`: Normal progress with integer percentage `0..100`.
  - `2`: Error / failed state.
  - `3`: Indeterminate / busy state (task/turn active).
  - `4`: Warning / paused state.

### `x/vt` Dispatch Behavior
- `x/vt` registers handlers by command number: `RegisterOscHandler(cmd int, handler func([]byte) bool)`.
- When `ESC ] 9 ; 4 ; 3 ; BEL` is parsed, `cmd == 9`.
- As with OSC 133, `x/vt` passes the whole payload: `data` is `"9;4;3;"`.
- `strings.Split(string(data), ";")`:
  - `parts[0]` = `"9"`
  - `parts[1]` = subtype (`"4"` for progress; other subtypes exist like desktop notifications in iTerm2 where `parts[1]` is message text)
  - `parts[2]` = state (`"0"`, `"1"`, `"2"`, `"3"`, `"4"`)
  - `parts[3]` = optional progress value (e.g. `"50"`)

### Claude Code Capability Gating
- Inspected from binary `v2.1.280`:
  ```javascript
  function isProgressReportingSupported() {
    if (!process.stdout.isTTY) return false;
    if (process.env.WT_SESSION) return false;
    if (process.env.ConEmuANSI || process.env.ConEmuPID || process.env.ConEmuTask) return true;
    let v = semver.coerce(process.env.TERM_PROGRAM_VERSION);
    if (!v) return false;
    if (process.env.TERM_PROGRAM === "ghostty") return semver.gte(v.version, "1.2.0");
    if (process.env.TERM_PROGRAM === "iTerm.app") return semver.gte(v.version, "3.6.6");
    return false;
  }
  ```
- Wideboi spawns child processes inheriting `os.Environ()` (`internal/server/ptyx/pane.go:49`).
  Preserving parent environment ensures `TERM_PROGRAM` reaches Claude Code.

## 3. Precedence & Latch Generalization

- Both OSC 133 and OSC 9;4 are authoritative status sources.
- When both are emitted in the same pane (e.g. an interactive shell emitting OSC 133 that invokes a CLI emitting OSC 9;4, or vice versa):
  - Precedence: last writer wins for `g.status`.
  - Latch: rename `sawOSC133` to `sawAuthoritativeStatus` (or keep both setting a unified `authoritativeStatus atomic.Bool`).
  - Either valid OSC 133 OR valid OSC 9;4 latches the authoritative status mode.
  - Malformed or unrecognized payloads in either must NOT latch.
