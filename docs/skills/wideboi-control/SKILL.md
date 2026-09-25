---
name: wideboi-control
description: Use when an agent wants to control wideboi panes headlessly (split, send, capture, close, status) to run commands, inspect terminal output, or manage persistent session panes.
---

# Wideboi Control Skill

Instructions for AI coding agents to control wideboi multiplexer sessions and panes using headless CLI subcommands.

## Overview

wideboi provides scriptable CLI subcommands to manage panes by integer pane ID without attaching an interactive TUI. These commands communicate with the running `wideboi` session server via UNIX domain sockets, returning deterministic exit codes (0 on success, non-zero on error) and clean stdout output for piping.

## Session Targeting

By default, subcommands target the `default` session at `$TMPDIR/wideboi-<uid>/default.sock`.
To target a specific session:
- Pass `-L <session-name>` or `--session <session-name>`
- Pass `-s <socket-path>` or `--socket <socket-path>`
- Or set environment variable `WIDEBOI_SESSION` or `WIDEBOI_SOCK`

## Core Subcommands

### 1. `split` — Create a pane
Spawns a new pane in the layout strip and prints its assigned numeric ID to stdout.

```bash
# Launch default shell in a new pane
PANE_ID=$(wideboi split)

# Launch a specific command in a new pane
PANE_ID=$(wideboi split make test)

# Launch in a specific working directory and placed after an existing pane
PANE_ID=$(wideboi split --cwd /path/to/repo --after 2 npm run build)
```

**Options:**
- `--cwd <dir>`: Sets initial working directory for the child process.
- `--after <pane-id>`: Inserts the new column immediately after the specified pane ID.
- `[command...]`: Command and arguments to execute under `$SHELL -c`. If omitted, starts the user's default shell.

### 2. `send` — Send input to a pane
Sends raw text or input to the specified pane's stdin.

```bash
# Send text without pressing Enter (e.g., partial command or prompt input)
wideboi send $PANE_ID "git status"

# Send command and execute with Enter (-e or --enter appends carriage return \r)
wideboi send $PANE_ID "git status" -e
wideboi send $PANE_ID "npm test" --enter

# Send literal text starting with a dash using '--'
wideboi send $PANE_ID -- "-v"
```

**Key behavior:**
- **Literal by default:** Does NOT append a newline automatically. Always pass `-e` or `--enter` when you want the command to execute immediately.

### 3. `capture` — Read terminal screen output
Captures current terminal output from the specified pane as UTF-8 text to stdout.

```bash
# Capture the visible terminal screen
wideboi capture $PANE_ID

# Capture visible screen plus scrollback history
wideboi capture $PANE_ID --scrollback
wideboi capture $PANE_ID -S

# Capture only the last N lines of output
wideboi capture $PANE_ID -n 50
wideboi capture $PANE_ID -S -n 100
```

**Key behavior:**
- Trailing spaces on each row are trimmed.
- Unwritten empty rows at the bottom of the viewport are trimmed.
- Wide characters (CJK, emojis) are formatted correctly without duplication.

### 4. `close` — Close a pane
Terminates the pane's process group using standard SIGHUP hangup semantics and removes it from the layout.

```bash
wideboi close $PANE_ID
```

### 5. `status` — Inspect session state
Inspect active panes and their metadata.

```bash
# Human-readable table
wideboi status

# Machine-readable JSON
wideboi status --json
```

The JSON payload includes active columns, pane IDs, titles, dimensions, working statuses (`working`, `idle`, `needs_input`, `failed`, `done`), and CWD.

## Common Agent Patterns

### Pattern A: Run a command and poll for completion

```bash
# 1. Start a long-running job in a dedicated pane
PANE_ID=$(wideboi split --cwd "$PWD" npm run build)

# 2. Poll until completion marker or status changes
while true; do
  OUTPUT=$(wideboi capture $PANE_ID -n 10)
  if echo "$OUTPUT" | grep -q "built in"; then
    echo "Build succeeded!"
    break
  fi
  if echo "$OUTPUT" | grep -q "failed"; then
    echo "Build failed!" >&2
    break
  fi
  sleep 1
done

# 3. Clean up the pane when done
wideboi close $PANE_ID
```

### Pattern B: Interactive REPL automation

```bash
# 1. Start an interactive REPL
PANE_ID=$(wideboi split python3)
sleep 0.2

# 2. Send python statements
wideboi send $PANE_ID "x = 40 + 2" -e
wideboi send $PANE_ID "print(f'RESULT: {x}')" -e

# 3. Capture output
OUTPUT=$(wideboi capture $PANE_ID -n 5)
echo "$OUTPUT"

# 4. Exit REPL and close
wideboi send $PANE_ID "exit()" -e
wideboi close $PANE_ID
```

## Smoke Test in a Unique Named Session

To verify that the server, subcommands, and socket transport are working correctly in an isolated session:

```bash
SESSION="smoke-$$-$(date +%s)"

# 1. Start a headless server for the session
./bin/wideboi -L "$SESSION" server > /dev/null 2>&1 &
sleep 0.2

# 2. Start a persistent interactive REPL
REPL_ID=$(./bin/wideboi -L "$SESSION" split python3)
sleep 0.3

# 3. Send computation code and execute with -e
./bin/wideboi -L "$SESSION" send "$REPL_ID" "val = 20 * 2 + 2" -e
./bin/wideboi -L "$SESSION" send "$REPL_ID" "print(f'MAGIC_SMOKE={val}')" -e
sleep 0.2

# 4. Capture and assert the evaluated result
OUTPUT=$(./bin/wideboi -L "$SESSION" capture "$REPL_ID" -n 5)
echo "$OUTPUT" | grep -q "MAGIC_SMOKE=42" || { echo "Smoke test failed: $OUTPUT" >&2; exit 1; }
echo "Smoke test passed: MAGIC_SMOKE=42 found in output."

# 5. Clean up by closing the REPL pane (the session shuts down when the last pane closes)
./bin/wideboi -L "$SESSION" close "$REPL_ID"
```
