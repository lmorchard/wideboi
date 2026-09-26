# wideboi

wideboi is a scrolling tiling terminal multiplexer for command-line coding agents.

Panes keep their configured width. When you open new panes, the screen viewport scrolls horizontally across them instead of squeezing existing panes.

Status: v1. See the [open issues](https://github.com/lmorchard/wideboi/issues) for known issues and ongoing work.

<video src="https://github.com/lmorchard/wideboi/raw/refs/heads/main/docs/wideboi-2.mp4" width="100%" controls></video>

---

## Quick Start

### 1. Download a Release

Download the latest release archive for your platform (Linux or macOS) from [GitHub Releases](https://github.com/lmorchard/wideboi/releases).

Extract the archive and move the `wideboi` binary to a directory in your `PATH`:

```bash
tar -xzf wideboi_*.tar.gz
chmod +x wideboi
sudo mv wideboi /usr/local/bin/
```

### 2. Start or Reattach to a Session

```bash
wideboi
```

This command attaches to an existing default session or starts a new background session.

### 3. Detach from a Session

Press `Ctrl+b` and then press `d`.

Your panes and running processes continue running in the background.

### 4. Stop a Session

```bash
wideboi kill-session
```

This closes all panes and stops the session server. You can also press `Ctrl+b` and then press `q` inside an attached client.

---

## Essential Keys

wideboi uses `Ctrl+b` as its default control prefix key.

Press `Ctrl+b` to enter control mode, then press an action key:

| Key | Action |
| --- | --- |
| `h` / `l` | Focus the column to the left / right (arrow keys work too) |
| `j` / `k` | Scroll history down / up |
| `/` | Search text in screen and scrollback |
| `n` | Open a new pane column |
| `w` | Cycle column width |
| `o` / `p` | Shrink / grow this client's pane width by 10 cells |
| `H` / `L` | Pan the focused pane left / right by 10 cells |
| `f` | Toggle following PTY widths for all panes |
| `S` | Claim PTY sizing using this client's pane widths |
| `x` | Close the focused pane |
| `c` | Switch layout between cards mode and scrolling strip mode |
| `d` | Detach from the session |
| `q` | Stop the session and close all panes |
| `?` | Show the full help overlay |

To repeat navigation actions quickly, hold `Ctrl` while pressing the action key (for example, `Ctrl+h` or `Ctrl+l`).

---

## Layout Modes

wideboi provides two client layout modes:
- **Cards Mode (`cards`):** Panes stack horizontally like overlapping cards. The focused pane is shown in full, and adjacent panes peek out as edges.
- **Scrolling Strip Mode (`scroll`):** Panes sit side by side in a wide horizontal row. The viewport scrolls as you change focus.

Press `Ctrl+b` and then press `c` to toggle between modes. Each connected client can choose its own layout mode independently.

Each client also keeps its own width and horizontal pan for each pane, in either layout. Width edits only resize the PTY when that client owns session sizing. Claim with `Ctrl+b S` to take ownership; another client can claim it later. PTY output leaves the pan in place, while typing or pasting brings the cursor into view. Set `pan_step` in the config to change the default 10-cell pan distance.

---

## Scripting and Agent Control

You can control sessions directly from scripts and AI coding agents without attaching an interactive terminal:

```bash
# Run a command in a new pane (starting the session if none is running);
# --keep holds the pane, screen, and exit code after the command exits
PANE_ID=$(wideboi split --keep make test)

# Block until the command exits, and exit with its exit code
wideboi wait $PANE_ID

# Send input text with Enter (-e)
wideboi send $PANE_ID "git status" -e

# Capture current terminal text (-S includes scrollback)
wideboi capture $PANE_ID

# Close the pane
wideboi close $PANE_ID
```

All control commands accept `-L <session-name>` and `-s <socket-path>` to target specific sessions. `wait` exits with the pane process's exit code; the others exit 0 on success and 1 on error. `status --json` reports pane statuses by name (`idle`, `working`, `needs_input`, `done`, `failed`) and, for kept panes, `exited` and `exit_code`. See [`docs/skills/wideboi-control/SKILL.md`](docs/skills/wideboi-control/SKILL.md) for full agent skill instructions and integration patterns.

---

## Web Client

wideboi includes an embedded browser client:

```bash
wideboi server --websocket 127.0.0.1:8080
```

Open the link printed on startup (which contains the security token) in your web browser:

```
http://127.0.0.1:8080/#token=<generated-token>
```

You can attach terminal clients to the same session simultaneously with `wideboi attach`. The web client supports layout modes (cards or scroll), interactive mouse navigation, and client-local history search (`Ctrl+b /` or `Ctrl+F`).

---

## Desktop Client (preview)

The Wails desktop app manages local sessions and opens each session in its own
window. It can attach to sessions started from the CLI. Sessions started from
the desktop app belong to it: closing a client window leaves them running,
while quitting offers to stop them or keep them running for later reattachment.

Build and run on macOS:

```bash
make desktop-app
open bin/wideboi.app
```

On Linux, install the GTK4 and WebKitGTK 6.0 development packages required by
Wails v3, then run `make desktop` and `./bin/wideboi-desktop`. The desktop
build still supports the regular CLI commands, including `server`, `attach`,
and `kill-session`. The macOS bundle is a local, unsigned build.

---

## Complete Manual

For complete details on configuration, architecture, key remapping, multi-client sizing, and remote access, read the full manual:

📖 **[wideboi Manual (`docs/MANUAL.md`)](docs/MANUAL.md)**

The manual covers:
- Session architecture, socket paths, and process teardown semantics
- Key remapping rules and custom prefixes
- Mouse controls and OSC 52 clipboard sharing
- Multi-client geometry ownership and viewer anchoring
- Secure remote access and HTTPS reverse proxy configurations
- Configuration files (`.wideboi.toml` and user configs) and startup loadouts
- CLI flags and environment variables reference

---

## Development

### Build from Source

Prerequisites:
- Go 1.22 or later
- Node.js 22 or later (for the embedded web frontend)

Compile the binary with `make build`:

```bash
make build
```

This compiles the executable to `bin/wideboi`.

### Test Targets

```bash
make quick    # Run code formatting, vetting, and unit tests
make check    # Run all checks, race tests, and acceptance suites
make race     # Run tests with race detection
make smoke    # Run PTY acceptance tests
make proto    # Rebuild protobuf wire protocol code
```

Before contributing code changes, please read [`docs/LESSONS.md`](docs/LESSONS.md) and [`docs/PROTOCOL.md`](docs/PROTOCOL.md).

---

## License

MIT — see [LICENSE](LICENSE).
