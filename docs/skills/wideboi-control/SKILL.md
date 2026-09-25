---
name: wideboi-control
description: Use when an agent wants to control wideboi panes headlessly (split, send, capture, wait, close, status) to run commands, read their output and exit codes, or drive interactive programs in persistent session panes.
---

# Wideboi Control Skill

Use these CLI subcommands to control wideboi panes headlessly, by integer
pane ID, without attaching the interactive TUI. They talk to the session
server over its UNIX socket. Each prints clean stdout for piping and exits 0
on success, or non-zero with a message on stderr.

## Session targeting

By default, subcommands target the `default` session. To target a
specific session:

- Pass `-L <session-name>` (or `--session <session-name>`)
- Pass `-s <socket-path>` (or `--socket <socket-path>`)
- Or set the environment variable `WIDEBOI_SESSION` or `WIDEBOI_SOCK`

To keep an agent's panes out of your own session, use a dedicated session
name (`-L agent-$$`).

## Subcommands

### `split`: create a pane

`split` creates a pane and prints its ID. **If no server is running for the
session, `split` starts one.** That server runs in the background and exits
when its last pane closes.

```bash
PANE_ID=$(wideboi split)                          # a shell
PANE_ID=$(wideboi split 'make test && echo ok')   # one argument: a shell command line
PANE_ID=$(wideboi split grep "a b" notes.txt)     # several arguments: argv, quoting kept
PANE_ID=$(wideboi split --keep make test)         # keep the pane after make exits
PANE_ID=$(wideboi split --cwd /path/to/repo --after 2 npm run build)
```

Options:
- `--keep`: when the process exits, keep the pane with its screen and exit
  code intact until you `close` it. **Use this whenever you want the output
  or exit code of a command that finishes.** Without it, the pane disappears
  as soon as its process exits, just as a pane in the TUI does.
- `--cwd <dir>`: the working directory for the process.
- `--after <pane-id>`: insert the new column after that pane.

Commands run under `$SHELL -c`. A single argument is passed through as a
shell command line. Several arguments are quoted one by one, so each
arrives as exactly one argument.

`split`'s own flags go **before** the command. Everything from the first
command word on belongs to the command, so `wideboi split --keep make -j4
test` hands `-j4` to make. `--` also ends split's flags.

### `send`: type into a pane

```bash
wideboi send "$PANE_ID" "git status" -e      # type, then Enter (-e / --enter)
wideboi send "$PANE_ID" "partial input"      # type only; no Enter
wideboi send "$PANE_ID" -- "-v"              # text starting with '-' goes after --
wideboi send "$PANE_ID" $'\x03'              # Ctrl-C
wideboi send "$PANE_ID" $'\x04'              # Ctrl-D (EOF)
wideboi send "$PANE_ID" $'\x1b'              # Escape
```

- **Literal:** `send` never adds a newline on its own. Pass `-e` to press
  Enter.
- **Exactly one text argument:** quote text that contains spaces.
  `send 1 echo hi` is an error, not a truncated send.
- **Control keys** are just bytes. Use bash's `$'\xNN'` quoting: Ctrl-<letter>
  is the letter's position in the alphabet (Ctrl-C = `\x03`).
- Sending to a kept pane whose process has exited is an error.

### `capture`: read the screen

```bash
wideboi capture "$PANE_ID"              # visible screen
wideboi capture "$PANE_ID" -S           # plus scrollback (--scrollback)
wideboi capture "$PANE_ID" -S -n 100    # only the last 100 lines (--lines)
```

Trailing spaces are trimmed from each row, and unwritten blank rows at the
bottom are dropped. Wide characters (CJK, emoji) come through once each.
Output is the screen as drawn, so a line longer than the pane is wide
comes back split across rows. Match on short markers, not on long lines.
`capture` still works on a kept pane after its process has exited.

### `wait`: block until the process exits

```bash
wideboi wait "$PANE_ID"; echo "exit code: $?"
wideboi wait --timeout 5m "$PANE_ID"   # gives up with exit 124
```

`wait` exits with **the pane process's own exit code**. A process killed by
a signal reports 128+signal, as a shell does (SIGTERM → 143, SIGHUP → 129).
`--timeout` takes a Go duration (`30s`, `5m`) and exits 124 on expiry, like
`timeout(1)`.

- A kept pane that has already exited answers at once, so `wait` is safe to
  call late.
- A pane that was split **without `--keep`** can only be waited on while
  its process is still running. Once it has exited and gone, `wait` reports
  "pane not found". Use `--keep` for anything you want to wait on.
- Closing a pane while someone waits on it wakes the waiter. It gets the
  hangup's code, or an error if the process outlived the close.

### `close`: remove a pane

```bash
wideboi close "$PANE_ID"
```

`close` hangs up the pane's terminal, as closing a terminal window does. The
process gets SIGHUP, and the pane leaves the layout. Closing the last pane
ends the session.

### `status`: inspect the session

```bash
wideboi status          # table
wideboi status --json   # machine-readable
```

```json
{
  "columns": [{ "pane_id": 1, "width": 80, "height": 20 }],
  "pane_statuses": { "1": "failed" },
  "pane_titles": { "1": "" },
  "pane_metadata": {
    "1": { "pane_id": 1, "cwd": "", "user_vars": null, "exited": true, "exit_code": 3 }
  }
}
```

- Statuses are `idle`, `working`, `needs_input`, `done` and `failed`.
- A kept pane that has exited shows `done` (exit 0) or `failed`, and its
  metadata carries `"exited": true`.
- `exit_code` is only meaningful when `exited` is true.
- Map keys are pane IDs as strings.

## Patterns

### Run a command and get its result

```bash
P=$(wideboi split --keep --cwd "$PWD" make test)
wideboi wait "$P"; RC=$?
wideboi capture "$P" -S -n 200     # the output, still there after exit
wideboi close "$P"
[ "$RC" -eq 0 ] || echo "tests failed with $RC" >&2
```

There's no polling and no sleeping. `wait` returns only after the
process's final output has reached the screen, so the `capture` right after
it sees the end of the run. The one exception is a background job the
command left running that keeps the terminal open. Then `wait` gives up
waiting for the output after about a second, and anything that job prints
later arrives on its own schedule.

### Drive an interactive program

Wait for observed state, not for time. Poll `capture` until the program's
prompt shows up, with a ceiling:

```bash
# wait_for PANE TEXT [TRIES]: poll the pane's last lines until TEXT appears.
wait_for() {
  for _ in $(seq "${3:-100}"); do
    wideboi capture "$1" -n 5 | grep -qF -- "$2" && return 0
    sleep 0.1
  done
  echo "timed out waiting for '$2' in pane $1" >&2; return 1
}

P=$(wideboi split --keep python3)
wait_for "$P" '>>>'
wideboi send "$P" "x = 40 + 2" -e
wideboi send "$P" "print(f'RESULT={x}')" -e
wait_for "$P" 'RESULT=42'
wideboi send "$P" "exit()" -e
wideboi wait "$P"
wideboi close "$P"
```

### Stop a runaway command

```bash
wideboi send "$P" $'\x03'          # Ctrl-C the foreground job
wideboi wait --timeout 10s "$P" || wideboi close "$P"
```

## Smoke test

This runs in an isolated, uniquely named session and needs no server
started beforehand:

```bash
S="smoke-$$"
P=$(wideboi -L "$S" split --keep 'echo MAGIC_SMOKE=$((20 * 2 + 2)); exit 7')
wideboi -L "$S" wait "$P"; RC=$?
OUT=$(wideboi -L "$S" capture "$P")
wideboi -L "$S" close "$P"            # last pane: the session ends
[ "$RC" -eq 7 ] || { echo "smoke: wait exit $RC, want 7" >&2; exit 1; }
echo "$OUT" | grep -q 'MAGIC_SMOKE=42' || { echo "smoke: output was: $OUT" >&2; exit 1; }
echo "smoke: ok"
```
