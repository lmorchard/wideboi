# wideboi Manual

This manual describes how to install, configure, operate, and automate wideboi.

---

## Table of Contents

1. [Introduction](#1-introduction)
2. [Installation and Basic Usage](#2-installation-and-basic-usage)
3. [Sessions and Architecture](#3-sessions-and-architecture)
4. [Terminal Client and Key Bindings](#4-terminal-client-and-key-bindings)
5. [Mouse and Clipboard](#5-mouse-and-clipboard)
6. [Multi-Client Sizing and Layouts](#6-multi-client-sizing-and-layouts)
7. [Headless Control and Automation](#7-headless-control-and-automation)
8. [Web Client and Remote Access](#8-web-client-and-remote-access)
9. [Configuration](#9-configuration)
10. [Development and Wire Protocol](#10-development-and-wire-protocol)

---

## 1. Introduction

wideboi is a scrolling tiling terminal multiplexer designed for command-line coding agents.

In standard terminal multiplexers, adding new panes reduces the width of existing panes. In wideboi, panes keep their configured width. When you open new panes, wideboi places them side by side. The screen viewport scrolls horizontally across the row of panes.

wideboi supports:
- Full pane width retention with horizontal navigation.
- Terminal user interface (TUI) with cards mode and scrolling strip mode.
- Headless scripting commands to spawn, inspect, send input to, and close panes.
- Multi-client attachment over local Unix domain sockets.
- Web client support over WebSocket connections.
- Clean session teardown and process management.

---

## 2. Installation and Basic Usage

### Download a Pre-Built Release

Download the archive for your operating system and CPU architecture from [GitHub Releases](https://github.com/lmorchard/wideboi/releases).

Extract the archive and move the `wideboi` binary to a directory listed in your `$PATH`:

```bash
tar -xzf wideboi_*.tar.gz
chmod +x wideboi
sudo mv wideboi /usr/local/bin/
```

### Build from Source

Prerequisites:
- Go 1.22 or later
- Node.js 22 or later (required to compile web frontend assets)
- A POSIX operating system (Linux or macOS)

Run `make build` from the repository root:

```bash
make build
```

This compiles the executable to `bin/wideboi`.

### Start and Stop a Session

To start a new session or attach to a running session, run:

```bash
wideboi
```

To detach from the session while leaving all panes running:
- Press `Ctrl+b` followed by `d`.

To reconnect to the running session:
- Run `wideboi`.

To stop the session, close all panes, and shut down the server:
- Run `wideboi kill-session`, or
- Press `Ctrl+b` followed by `q` inside the attached client.

---

## 3. Sessions and Architecture

### Client-Server Model

wideboi separates session management from user display:
- **Server process (`wideboi server`):** Manages pseudoterminals (PTYs), executes shell processes, maintains scrollback buffers, and routes input/output.
- **Client process (`wideboi attach` or `wideboi`):** Connects to the server over a Unix domain socket, renders the terminal interface, and captures keyboard and mouse events.

When you run `wideboi`:
1. It looks for an existing session server socket.
2. If the socket exists and responds, wideboi attaches to that server.
3. If no server is running, wideboi starts a background server process and then attaches.

### Session Lifecycle and Teardown

A session started interactively attaches to the parent terminal window.
- If you close the terminal window or terminate the client process before detaching, wideboi tears down the entire session.
- During teardown, the server closes the PTY master for each pane. The operating system kernel sends `SIGHUP` to the foreground process group.
- Background tasks started with `nohup`, `disown`, or `setsid` continue running, matching standard Unix terminal behavior.
- Detached sessions remain active until you run `wideboi kill-session` or press `Ctrl+b q` in a connected client.

### Named Sessions

You can run multiple isolated sessions on the same system. Use the `-L <name>` flag to name a session:

```bash
# Start or attach to a session named "project-a"
wideboi -L project-a

# List all running sessions
wideboi ls

# Stop the session named "project-a"
wideboi -L project-a kill-session
```

Session naming rules:
- If you omit `-L`, wideboi uses the name `default`.
- You can set a default session name with the `WIDEBOI_SESSION` environment variable or the `session = "<name>"` setting in your configuration file.
- The default socket path is `$TMPDIR/wideboi-<uid>/<name>.sock`.
- To specify an explicit socket file path, pass `-s <path>`. You cannot combine `-L` and `-s`.
- The `ls` command lists sessions stored in the default runtime directory.

---

## 4. Terminal Client and Key Bindings

### Control Mode

wideboi uses a prefix key to control the multiplexer, similar to tmux. The default prefix is `Ctrl+b`.

1. Press `Ctrl+b` to enter control mode.
2. The status bar inverts its colors and the terminal cursor hides.
3. Press a command key to execute an action.

Control mode is sticky on control keys:
- If you hold `Ctrl` while pressing an action key (such as `Ctrl+h` or `Ctrl+l`), wideboi executes the action and stays in control mode. You can repeat the command without pressing `Ctrl+b` again.
- If you press an action key without `Ctrl` (such as `h` or `l`), wideboi executes the action and exits control mode immediately.
- Press `Escape` or `Ctrl+c` to exit control mode without taking an action.
- Press `Ctrl+b` while in control mode to send a literal `Ctrl+b` character to the focused pane.

All keys other than the prefix key pass directly to the focused pane program, including `Ctrl+c`, `Ctrl+w`, `Ctrl+l`, and `PageUp`.

### Default Key Bindings

The table below lists all default keys available in control mode:

| Key | Action |
| --- | --- |
| `h` / `Left` | Focus the column to the left |
| `l` / `Right` | Focus the column to the right |
| `j` | Scroll the focused pane down in history |
| `k` | Scroll the focused pane up in history |
| `/` | Start text search across the pane screen and scrollback |
| `n` | Create a new pane column |
| `w` | Cycle the column width of the focused pane |
| `x` | Close the focused pane |
| `a` | Jump focus to the next pane requesting attention |
| `s` | Open or focus the pane status dashboard |
| `1`–`9` | Focus the column at that index (from left to right) |
| `0` | Focus the last column on the right |
| `Tab` | Focus the previously active pane |
| `c` | Toggle client layout between cards mode and scrolling strip mode |
| `S` | Claim session geometry ownership for this client |
| `y` | Move the focused column one position to the left |
| `u` | Move the focused column one position to the right |
| `?` | Display the full help overlay |
| `d` | Detach this client from the session |
| `q` | Stop the session and terminate all panes |
| `Esc` | Exit control mode |
| `Ctrl+b` | Send literal `Ctrl+b` to the active pane and exit control mode |

### Search Mode

Press `/` in control mode to search the focused pane:
1. Type your plain-text search query. Search is case-sensitive.
2. Press `Enter` to search.
3. Press `n` to jump to the next match.
4. Press `N` to jump to the previous match.
5. Press `Enter` to confirm the selected view position.
6. Press `Esc` to cancel search and restore the previous viewport.
7. Press `Ctrl+g` to return immediately to live terminal output.

Search properties:
- Search runs locally in the client and never sends input to the shell process.
- Search operates on physical terminal rows. Text broken by soft word-wrap is searched on each row individually.
- Unicode characters match exact rune values.
- Text purged from the scrollback buffer cannot be searched.

### Changing the Prefix Key

Set the `WIDEBOI_PREFIX` environment variable or configure `prefix` in your configuration file:

```bash
WIDEBOI_PREFIX=ctrl+a wideboi
```

Supported prefix values:
- `ctrl+<letter>` (for example, `ctrl+a`, `ctrl+x`)
- `ctrl+space`

*Note:* If you run wideboi inside tmux, change the wideboi prefix from `ctrl+b` to `ctrl+a` to avoid key conflicts.

---

## 5. Mouse and Clipboard

wideboi enables mouse tracking by default.

### Mouse Operations

- **Left Click:** Focuses a pane, header, or card.
- **Scroll Wheel:** Scrolls history in the pane under the pointer without changing keyboard focus.
- **Click and Drag:** Selects text inside the pane. When you release the mouse button, wideboi copies the selected text to your system clipboard.

### Clipboard Integration (OSC 52)

wideboi copies text using the ANSI OSC 52 sequence. This places copied text directly onto your local system clipboard, even over an SSH session.

Terminal settings for OSC 52:
- **iTerm2:** Enable *Settings → General → Selection → Applications in terminal may access clipboard*.
- **tmux:** Add `set -g set-clipboard on` to your `tmux.conf`.

### Terminal Application Pass-Through

When an application in a pane enables mouse tracking (such as `vim`, `less`, or `htop`), wideboi forwards mouse events directly to that program.
- The initial click that focuses the pane is absorbed by wideboi. Subsequent clicks, drags, and wheel actions pass to the application.
- To bypass wideboi and make a native terminal selection, hold `Shift` (or `Option` on macOS) while dragging the mouse.
- To disable wideboi mouse handling completely, set `mouse = false` in your configuration file.

---

## 6. Multi-Client Sizing and Layouts

### Layout Modes

wideboi provides two viewing modes:
1. **Cards Layout (`cards`):** Panes stack horizontally with overlap. The active pane displays prominently, and neighboring panes show as card edges.
2. **Scrolling Strip Layout (`scroll`):** Panes sit side by side in a continuous horizontal strip. The viewport scrolls left and right as you move focus.

The layout mode is local to each client. Changing layout in one terminal with `Ctrl+b c` does not change other connected clients.

### Multi-Client Sizing Rules

Multiple clients can attach to the same session simultaneously:
- **Geometry Owner:** The first client that attaches establishes the session dimensions (rows and pane layout).
- **Viewer Mode:** Subsequent clients connect as viewers. Viewer window sizes do not shrink or alter the server terminal dimensions for other users.
- **Bottom Anchoring:** When a viewer window has fewer vertical rows than the server grid, the viewport anchors to the bottom rows. Shell prompts, command inputs, and active outputs remain visible.
- **Claiming Size:** Any client can assume size ownership:
  - In a terminal client: Press `Ctrl+b S`.
  - In the web client: Click **Fit to Window**.
- **Per-pane width:** Each client keeps its own width and horizontal pan for each pane in both layouts. Terminal `Ctrl+b w`, `o`, and `p` adjust the focused width; `H` and `L` pan by `pan_step` cells (10 by default). The web toolbar edits the focused pane width. A viewer's width edit only changes its viewport.
- **Owning PTY sizes:** A claim applies that client's per-pane widths to the PTYs. Further width edits by the owner resize the matching PTY immediately. A later claim transfers ownership. Terminal `Ctrl+b f` and the web **Follow PTY** control toggle whether all local widths track PTY width changes; manual width edits turn following off.
- **Cursor reveal:** Background output leaves each pane's horizontal pan alone. Typing or pasting moves the pan just enough to show the cursor, including its resulting position.
- **Disconnection:** When the current size owner disconnects, the session preserves its established dimensions.

---

## 7. Headless Control and Automation

wideboi provides CLI subcommands to control sessions from external scripts, automated workflows, and AI coding agents.

### Subcommands

All control commands support `-L <name>` or `-s <path>` to target a specific session. Commands exit with status code `0` on success and `1` on failure, except `wait` which exits with the pane process's exit code (or `124` on timeout). Running `split` will automatically start a background session server if one is not already running.

#### `split`
Creates a new pane and runs a command:

```bash
# Open an interactive shell pane and get its ID
PANE_ID=$(wideboi split)

# Open a pane running a specific command
PANE_ID=$(wideboi split make test)

# Open a command and keep the pane, screen, and exit code after it exits
PANE_ID=$(wideboi split --keep make test)

# Specify working directory or placement after an existing pane
PANE_ID=$(wideboi split --cwd /path/to/repo --after 2 npm test)
```

The command prints the new pane ID integer to stdout.

#### `wait`
Blocks until a pane's child process exits:

```bash
# Wait for the command to finish, exiting with the process's exit code
wideboi wait $PANE_ID

# Wait with a timeout (exits 124 if timed out)
wideboi wait $PANE_ID --timeout 30s
```

#### `send`
Sends keystrokes or text input to a pane:

```bash
# Send text to a pane
wideboi send $PANE_ID "git status"

# Send text followed by an Enter key (-e or --enter)
wideboi send $PANE_ID "make build" -e
```

#### `capture`
Reads screen text from a pane:

```bash
# Capture visible terminal screen
wideboi capture $PANE_ID

# Include scrollback history (-S or --scrollback)
wideboi capture $PANE_ID -S

# Limit output to the last N lines (-n <lines>)
wideboi capture $PANE_ID -S -n 50
```

#### `close`
Closes a pane using standard hangup semantics:

```bash
wideboi close $PANE_ID
```

For agent skill specifications, refer to [`docs/skills/wideboi-control/SKILL.md`](skills/wideboi-control/SKILL.md).

---

## 8. Web Client and Remote Access

The wideboi binary includes an embedded web application.

### Start the Web Server

Start wideboi with the `--websocket` option:

```bash
wideboi server --websocket 127.0.0.1:8080
```

When started, the server outputs an access link containing an authentication token:

```
http://127.0.0.1:8080/#token=<generated-token>
```

Open this URL in your web browser. The server serves the HTML/JS application and establishes a WebSocket connection.

### Security and Authentication

- **Loopback Binding:** Always bind to `127.0.0.1:8080` for local access. Binding to `:8080` or `0.0.0.0:8080` allows unencrypted network access. wideboi prints a security warning if you bind to non-loopback addresses.
- **Generated Token:** If you do not specify a token, wideboi generates a secure random token at startup.
- **Token File:** The server writes the current token to an owner-readable file at `$TMPDIR/wideboi-<uid>/<session-name>.web-token`. For the default session, view it with:
  ```bash
  cat "${TMPDIR:-/tmp}/wideboi-$(id -u)/default.web-token"
  ```
- **Custom Token:** Specify a fixed token with `--websocket-token <token>`, the `WIDEBOI_WEBSOCKET_TOKEN` variable, or `websocket_token = "<token>"` in the config file.
- **In-Memory Storage:** The web client reads the `#token=...` fragment, strips it from the browser address bar immediately, and stores it in page memory. Reloading the browser tab requires re-entering the token or reopening the full link.

### Web Client Features

- **Viewport Scrolling:** Mouse wheel scrolling in a pane pans through active rows if the browser window is shorter than the server terminal. Hold `Alt` while scrolling the wheel to navigate scrollback history.
- **Card Width Selector:** In card layout, select 40, 60, 80, or Terminal columns. If a card is narrower than the terminal grid, horizontal scrolling or trackpad gestures pan across columns without resizing the underlying PTY.
- **Fit to Window:** Click **Fit to Window** in the top bar to claim terminal geometry ownership for your browser dimensions.
- **Pane History Search:** Search the focused pane's screen and scrollback history by pressing `Ctrl+b /` (or your configured prefix then `/`), pressing `Ctrl+F` (or `Cmd+F` on macOS), or clicking **Search** in the toolbar.
  - Type your search query into the bottom overlay input and press `Enter` to find matches.
  - Keystrokes in the search bar stay local to the browser and are never forwarded to the child terminal process.
  - Navigate between matches with `n` (next) and `N` (previous) or click the **▲ Prev** / **▼ Next** buttons.
  - Press `Enter` or click **Keep** to retain the current scrolled view.
  - Press `Escape` or click **Restore** to cancel search and restore the view prior to searching.
  - Press `Ctrl+G` or click **Live** to jump back to live terminal output.
  - Each connected browser maintains its own local query and search navigation state without interfering with other viewers.

### Narrow browser view

At 480 CSS pixels or less, the browser shows one pane at a time. Use the pane
selector or the previous/next buttons to move between panes. Drag the terminal
to pan through its existing columns and rows. A tap focuses the command field;
touch gestures do not become mouse events in the terminal.

Type or paste a command or response in the field, then use **Send** to transmit
the text. **Enter** is separate, so you can review the text before submitting
it. The key buttons provide Esc, Tab, arrows, Backspace, Enter, and a one-shot
Ctrl modifier with C, D, and Z shortcuts. Hardware keyboards still work.

Opening the on-screen keyboard reduces the visible pane area, which stays
anchored to the latest output while you are at the bottom. It does not resize
the session's terminal grid. The phone reports its initial size when it first
attaches, but does not change an established session's size as a viewer.

### Remote HTTPS Reverse Proxy Setup

To connect to wideboi securely from another computer, keep wideboi bound to `127.0.0.1:8080` and use an HTTPS reverse proxy (such as nginx) with TLS termination:

```nginx
server {
    listen 443 ssl;
    server_name wideboi.example.com;

    ssl_certificate /etc/letsencrypt/live/wideboi.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/wideboi.example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

Connect to `https://wideboi.example.com/` and enter your token in the web connection form.

---

## 9. Configuration

### Configuration Precedence

wideboi resolves configuration options in this order (highest priority first):
1. Command-line flags
2. Environment variables
3. Configuration files
4. Default settings

### Configuration File Locations

wideboi searches for configuration files in the following order:
1. File path provided via `-c` or `--config <path>`.
2. Project configuration: `.wideboi.toml` in the current working directory.
3. User configuration: `$XDG_CONFIG_HOME/wideboi/config.toml` (defaults to `~/.config/wideboi/config.toml`).

A `.wideboi.toml` file in your repository overrides global settings in user configuration.

### Configuration Format (TOML)

Example configuration file:

```toml
prefix = "ctrl+b"
layout = "cards"
mouse = true
shell = "/bin/bash"
websocket = "127.0.0.1:8080"
auto_cleanup = true

# Startup columns created when a new session starts
[[startup]]
command = "nvim"
width = 100

[[startup]]
# Plain interactive shell

[[startup]]
command = "claude"
width = 80

[keys]
kill_pane  = "k"
scroll_up  = "e"
focus_left = ["h", "left"]
detach     = []            # Unbind action
```

### Key Remapping Rules

You can remap control-mode action keys in the `[keys]` table:
- **Binding Syntax:** Assign a string key name (e.g. `"x"`), an array of keys (e.g. `["h", "left"]`), or an empty array `[]` to unbind.
- **Replacement:** Specifying an action replaces all of its default keys.
- **Help Overlay:** The first key in an array appears in the status bar and help menu.
- **Quit Action:** The `quit` action cannot be unbound.
- **Reserved Keys:** You cannot bind `i`, `m`, or `[` because their terminal control codes conflict with `Tab`, `Enter`, and `Escape`.
- **No Collisions:** Two actions cannot share the same key.
- **Automatic Repeats:** Lowercase letter bindings can receive a `Ctrl+<letter>` sticky repeat chord. Toggles and uppercase `H`/`L` pan bindings do not. Uppercase and lowercase keys are distinct in custom mappings.
- **Fixed Keys:** Number keys `0` through `9` are fixed for column indexing and cannot be rebound.

### Command-Line Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-c, --config <path>` | Path to TOML configuration file | `~/.config/wideboi/config.toml` |
| `-l, --layout <mode>` | Initial layout mode (`cards` or `scroll`) | `cards` |
| `-p, --prefix <key>` | Control mode prefix key | `ctrl+b` |
| `-L, --session <name>` | Session name | `default` |
| `-s, --socket <path>` | Direct path to Unix domain socket | Derived from session name |
| `--websocket <addr>` | Address for HTTP and WebSocket listener | Disabled |
| `--websocket-token <token>` | Security token for WebSocket access | Auto-generated |
| `--shell <path>` | Shell executable for new panes | `$SHELL` or `/bin/sh` |
| `--disable-auto-cleanup` | Keep runtime files on clean exit | Cleanup enabled |
| `-v, --version` | Display version information and exit | — |
| `-h, --help` | Display command-line help text and exit | — |

### Environment Variables

| Variable | Description |
| --- | --- |
| `WIDEBOI_LAYOUT` | Starting client layout (`cards` or `scroll`) |
| `WIDEBOI_PREFIX` | Prefix key sequence (e.g. `ctrl+a`) |
| `WIDEBOI_SESSION` | Default session name |
| `WIDEBOI_SOCK` | Direct Unix domain socket path |
| `WIDEBOI_WEBSOCKET` | Web server bind address |
| `WIDEBOI_WEBSOCKET_TOKEN` | Web server security token |
| `WIDEBOI_SHELL` | Executable path for pane shells |
| `WIDEBOI_LOG_LEVEL` | Log level: `trace`, `debug`, `info`, `warn`, `error` |
| `WIDEBOI_AUTO_CLEANUP` | Remove sockets and logs on clean exit (`1` or `0`) |
| `SHELL` | Default fallback shell path |

### Logging and Cleanup

- Logs are written to `$TMPDIR/wideboi-<uid>/<session-name>.server.log` and `.client.log`.
- By default (`auto_cleanup = true`), wideboi deletes server sockets, tokens, and logs when a session ends cleanly.
- Set `WIDEBOI_AUTO_CLEANUP=0` or `--disable-auto-cleanup` to preserve logs for troubleshooting.
- Run `wideboi cleanup` to remove stale sockets and temporary files from inactive sessions.

---

## 10. Development and Wire Protocol

### Common Make Targets

| Target | Description |
| --- | --- |
| `make quick` | Runs code formatters, vetting, and unit tests |
| `make check` | Runs full test suite (quick, race, smoke, attach, playwright) |
| `make race` | Runs Go test suite with the race detector enabled |
| `make smoke` | Runs PTY wire byte acceptance tests |
| `make proto` | Recompiles Go and TypeScript wire protocol definitions |

Before making changes to the codebase, read [`docs/LESSONS.md`](LESSONS.md).

### Wire Protocol Overview

wideboi uses Protocol Buffers for communication between clients and the session server:
- Schema definition: `internal/protocol/wirepb/wideboi.proto`.
- Unix socket transport uses 4-byte big-endian length-prefixed frames.
- WebSocket transport sends single binary frames.
- Go structs are converted in `internal/protocol/codec.go`.
- For complete protocol details and message definitions, read [`docs/PROTOCOL.md`](PROTOCOL.md).
