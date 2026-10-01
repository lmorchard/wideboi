# Spec: Issue 335 Synthesize Terminal Environment Queries

## Motivation & Background
CLI utilities and TUI frameworks (e.g. Neovim, Emacs, `fzf`, `bat`, `delta`, `lazygit`, Gum) query the terminal at startup to discover styling, background theme, and dimension metrics:
- `OSC 10 ; ?` — Foreground color query
- `OSC 11 ; ?` — Background color query (used to detect light vs. dark mode)
- `OSC 4 ; N ; ?` — Color palette register query
- `CSI 14 t` — Window pixel size (`\e[14t`)
- `CSI 16 t` — Cell pixel size (`\e[16t`)
- `CSI 18 t` — Text area size in characters (`\e[18t`)
- `CSI ? 996 n` — Query dark/light theme mode

Because wideboi runs child processes against a headless virtual terminal (`x/vt`) that does not synthesize replies to these host queries, child programs either:
1. Block on a 1-second timeout waiting for a reply, creating visible startup stutter.
2. Fall back to wrong color scheme defaults (e.g. Neovim assuming a light background in a dark terminal).

## Requirements

1. **PTY Stream Query Interception (`internal/server/queries.go`)**:
   - Intercept bytes read from `p.pty.Master` in the PTY reader pump before passing them to the emulator.
   - Scan for query sequences:
     - `OSC 10;?` -> Synthesize `\x1b]10;rgb:rrrr/gggg/bbbb<term>`
     - `OSC 11;?` -> Synthesize `\x1b]11;rgb:rrrr/gggg/bbbb<term>`
     - `OSC 4;N;?` -> Synthesize `\x1b]4;N;rgb:rrrr/gggg/bbbb<term>` (where N in 0..255)
     - `CSI 14t` -> Synthesize `\x1b[4;<height>;<width>t` using `cols * cellWidth` and `rows * cellHeight`
     - `CSI 16t` -> Synthesize `\x1b[6;<cellHeight>;<cellWidth>t`
     - `CSI 18t` -> Synthesize `\x1b[8;<rows>;<cols>t`
     - `CSI ? 996 n` -> Synthesize DSR 997 response `\x1b[?997;1n` (dark) or `\x1b[?997;2n` (light)
   - Preserve exact query string terminator: `\x07` (BEL) vs `\x1b\\` (ST).
   - Strip matched query bytes from the stream passed to the emulator (`cleaned`).
   - Write synthesized reply bytes directly back to child stdin (`p.pty.WriteBounded(replies, ptyWriteTimeout)`).

2. **Theme Awareness**:
   - `QueryTheme` holds `FgColor`, `BgColor`, and `IsDark`.
   - Provide standard default RGB colors (`1e1e/1e1e/1e1e` background, `d4d4/d4d4/d4d4` foreground).
   - Support configuring/updating the theme via `SetTheme(t QueryTheme)`.

3. **Robustness & Performance**:
   - Buffer in-flight / split escape sequences across chunk boundaries without unbounded growth (capped partial buffer).
   - Ensure the scan loop always makes forward progress (`i++` or returns when partial), avoiding tight loops.
   - Non-query bytes are passed directly to `p.grid.Write(cleaned)`.

4. **Testing**:
   - Comprehensive unit tests in `internal/server/queries_test.go` verifying all query types, both `ST` and `BEL` terminators, dark and light mode responses, non-query passthrough, and packet splitting.
