# wideboi

A scrolling tiling terminal multiplexer for CLI coding agents. Panes keep their
width; open more and the viewport scrolls instead of squeezing what is already
there.

Status: v1. Working, and rough in places — see the [open issues](https://github.com/lmorchard/wideboi/issues) for what is known and parked.

<video src="https://github.com/lmorchard/wideboi/raw/refs/heads/main/docs/wideboi-2.mp4" width="100%" controls></video>

## Build and run

    make build
    ./bin/wideboi

The session runs in a background server, so you can detach from it and
come back — the panes, and whatever your agents are doing in them, keep
running:

    ./bin/wideboi               # start a session, or reattach to the running one
    # ctrl+b d                  # detach; the session keeps running
    ./bin/wideboi               # back where you left it
    ./bin/wideboi kill-session  # end it: close every pane and stop the server

A session you started belongs to that terminal until you detach it. If
the terminal goes away first — you close the window, or the `wideboi`
process is killed — the whole session is torn down with it. Each pane's
terminal is hung up, as tmux does, so the agents in it get SIGHUP and a
forgotten `wideboi` never leaves them running where you cannot see them.
A job you deliberately detached from the terminal (`nohup`, `disown`,
`setsid`) keeps running, as it would after closing any terminal. Once
detached, a session lives until `ctrl+b q` from any client,
`wideboi kill-session`, or a signal to the server.

`wideboi server` and `wideboi attach` start the two halves separately;
a session started with `wideboi server` is never tied to a terminal.

### Several sessions

Give a session a name with `-L` to run more than one side by side:

    ./bin/wideboi -L work       # start or reattach the session called "work"
    ./bin/wideboi ls            # list running sessions
    ./bin/wideboi -L work kill-session

With no name, the session is `default`. Every command takes `-L`, and
`WIDEBOI_SESSION` or `session = "..."` in the config file set it too. A
named session's socket is `$TMPDIR/wideboi-<uid>/<name>.sock`. To use a
socket somewhere else, pass `-s <path>` instead; setting both at the
same level is an error. `ls` only lists sessions in that directory.

Starting two `wideboi` at once for one session (two terminals opened
together, or a restored set of tabs) gives you one session with both
attached, not an error.

A session survives a rebuild. If the new build changed the wire protocol,
it refuses the old server and says so, with the server's pid. Attach to
that session with a build that matches it, or start a new one alongside with
`-L <name>`.

### Scripting and agent control

Sessions can be controlled headlessly by scripts and agents using pane IDs without attaching an interactive client:

    # Run a command in a new pane (starting the session if none is running);
    # --keep holds the pane, screen and exit code, after the command exits
    PANE_ID=$(./bin/wideboi split --keep make test)

    # Block until it exits, and exit with its exit code (124 on --timeout)
    ./bin/wideboi wait $PANE_ID

    # Read current visible terminal text (-S includes scrollback, -n limits lines)
    ./bin/wideboi capture $PANE_ID

    # Send input text (use -e or --enter to append Enter; quote text with spaces)
    ./bin/wideboi send $PANE_ID "git status" -e

    # Close the pane using hangup semantics
    ./bin/wideboi close $PANE_ID

All control subcommands accept `-L <name>` and `-s <path>` to target a specific session and report errors on stderr if a pane ID does not exist or the server is unreachable. `wait` exits with the pane process's code; the others exit 0 on success and 1 on error. `status --json` reports pane statuses by name (`idle`, `working`, `needs_input`, `done`, `failed`) and, for kept panes, `exited` and `exit_code`. See [`docs/skills/wideboi-control/SKILL.md`](docs/skills/wideboi-control/SKILL.md) for full agent skill instructions and integration patterns.

## Keys

wideboi uses a prefix key, like tmux. Press `ctrl+b` to enter control
mode, then a verb. The status bar inverts and the cursor disappears
while control mode is active.

Control mode is per-keystroke sticky: hold `Ctrl` on a verb to stay in the
mode and repeat the verb, press it unmodified to act and leave. `Escape` leaves
without doing anything. For the full list, press `?` in control mode.

| In control mode | Action |
| --- | --- |
| `h` / `l` | focus the column to the left / right (arrow keys work too) |
| `j` / `k` | scroll this pane's history down / up |
| `/` | search the focused pane's screen and scrollback |
| `n` | open a new column |
| `w` | cycle this column's width |
| `x` | kill the focused pane |
| `a` | jump to a pane wanting attention |
| `s` | open or focus pane status dashboard |
| `1`–`9` / `0` | focus the column at that position from the left / the last column |
| `tab` | focus the previously focused pane |
| `c` | switch this terminal between the card fan and the scrolling strip |
| `S` | claim session terminal size for this window |
| `y` / `u` | move this column one place left / right |
| `?` | show the full help overlay |
| `d` | detach, leaving the session running |
| `q` | end the session: close every pane and stop the server |
| `esc` | leave control mode |
| `ctrl+b` | send a literal `ctrl+b` to the pane, and leave control mode |

After `/`, type a case-sensitive plain-text query and press Enter. `n` and
`N` move to the next and previous match; Enter keeps the selected view,
Escape restores the view from before the search, and Ctrl+g returns to live
output. Search stays in this client and never sends the query or navigation
keys to the child. Matches use physical terminal rows: text split by a soft
wrap is searched on each row separately. Unicode queries match exactly, and
history that has rolled out of the scrollback buffer is no longer searchable.
Browser search is tracked in [issue #222](https://github.com/lmorchard/wideboi/issues/222).

Each pane's header starts with its position, then its ID in brackets:
` 2 [7]` is the second column from the left, pane 7. The digit keys go by
position, so a column you move with `y` / `u` renumbers.

Layout is per client: `c` flips only the terminal you press it in, and
every attach starts from your configured layout (`--layout`,
`WIDEBOI_LAYOUT` or `layout` in the config file; cards by default). The
status bar shows which one you are in, right before the `ctrl+b` hint.

`ctrl+b` is the only key wideboi keeps for itself. Everything else goes
to the focused pane, including `ctrl+c`, `ctrl+q`, `ctrl+w`, `ctrl+l`
and `PageUp`.

### Changing the prefix

Set `WIDEBOI_PREFIX` to any `ctrl+<letter>`, or to `ctrl+space`:

```
WIDEBOI_PREFIX=ctrl+a wideboi
```

**If you run wideboi inside tmux, change it.** tmux's own default prefix
is `ctrl+b`, and whichever of the two is outermost will swallow it.
`ctrl+a` is the conventional alternative.

If your chosen prefix letter also has a ctrl repeat form of its own —
`WIDEBOI_PREFIX=ctrl+l` collides with the ctrl repeat for focus-right —
the doubled prefix still wins: `ctrl+l ctrl+l` sends one literal
`ctrl+l` to the pane and leaves control mode, rather than moving focus
right twice.

## Mouse

wideboi captures the mouse by default:

- **Click** a pane, its header, or a card to focus it.
- **Wheel** scrolls the history of whichever pane is under the pointer,
  without moving focus.
- **Drag** inside a pane to select text. The selection stays inside the
  pane you started in, and releasing the button copies it to your
  clipboard.

Copying uses OSC 52, so it lands on the clipboard of the machine you are
sitting at, even when wideboi is running over SSH. Your terminal has to
allow it: in iTerm2, turn on *Settings → General → Selection →
Applications in terminal may access clipboard*. Inside tmux, set
`set -g set-clipboard on`.

Programs that ask for the mouse themselves, like `vim` with `mouse=a` or
`htop`, get it: clicks, drags and the wheel over the focused pane go to
the program. The click that focuses such a pane is not passed on. To
select text in one of these panes, use your terminal's own selection
bypass (usually `Shift`-drag, or `Option`-drag in macOS terminals).

To leave the mouse to your terminal entirely, put `mouse = false` in your
config file.

## Multi-client sizing and navigation

When multiple clients connect to a session:

- **First client sets geometry:** The first client to attach establishes the
  session dimensions (`rows` and initial column layout) and becomes the active
  size owner.
- **Viewers attach without shrinking PTYs:** Subsequent clients (such as a
  smaller terminal or browser window) attach as viewers. Their attach and window
  resizes do not shrink or reflow the hosted PTYs for other clients.
- **Bottom-anchored viewports:** When a client's viewport fits fewer rows than
  the hosted pane, the viewport anchors to the bottom lines where shell prompts,
  agent outputs, and the cursor live. In the web UI, mouse wheel scrolling
  pans through the active terminal grid before entering scrollback history; in
  the terminal client, the bottom lines remain visible while `k` and mouse wheel
  navigate scrollback history.
- **Claiming size:** Any client can explicitly take size ownership:
  - In a terminal client: press `ctrl+b S` in control mode.
  - In the web UI: click the **Fit to Window** button in the toolbar.
- **Stable disconnects:** When the size owner disconnects, the session
  dimensions remain locked at their current geometry.

## Web client

`make build` bundles the browser client into `bin/wideboi`. Start a separate
server with the HTTP and WebSocket listener bound to your own machine:

```bash
make build
./bin/wideboi server --websocket 127.0.0.1:8080
```

Open the `http://127.0.0.1:8080/#token=...` link printed by the server in a
browser on the same machine. The server serves the client and WebSocket endpoint
on that address; no separate Vite server is needed. To attach a terminal client
to the same session, run `./bin/wideboi attach` in another terminal. End the
session with `./bin/wideboi kill-session`.

When a pane's terminal grid is taller than its browser viewport, scroll within
the pane to reach the bottom of the live screen. The wheel pans that viewport;
Alt+wheel navigates terminal scrollback. When the whole live screen fits, the
wheel navigates scrollback as before. Panning the live screen does not resize
the terminal or change another client's view.

In card layout, the **Card width** selector can show a pane in 40, 60, or 80
columns, capped at its terminal width. Choose **Terminal** to show its full
width. A narrower card scrolls horizontally within the live terminal grid;
its scrollbar or a horizontal trackpad gesture reaches the hidden columns.
This is a local viewing choice and does not resize the PTY.

The listener is disabled unless you set `--websocket`,
`WIDEBOI_WEBSOCKET`, or `websocket` in the config file. All three accept an
address such as `127.0.0.1:8080`. Binding to `:8080` listens on network
interfaces beyond loopback. The built-in server uses plain HTTP and WebSocket;
`:8080`, `0.0.0.0:8080`, and other non-loopback addresses expose that
unencrypted service to the network. wideboi prints a warning for those binds.
For access from another machine, use an HTTPS/WSS reverse proxy and keep the
token private.

When no token is configured, the server generates one at startup and prints a
`#token=...` link once to its stderr. It also stores the token in an owner-only
`<socket without .sock>.web-token` file. For the default session, that is
`$TMPDIR/wideboi-<uid>/default.web-token`; this file lets you retrieve the
token when the server's stderr is no longer available:

```bash
cat "${TMPDIR:-/tmp}/wideboi-$(id -u)/default.web-token"
```

Open `http://127.0.0.1:8080/` and paste that value into the connection form.
The token file is removed on normal shutdown, and `wideboi cleanup` removes
files left by dead sessions.

Open the link to hand the token to the browser, or open the base URL and enter
the token in the connection form. The browser removes the fragment from its
history entry immediately and keeps the token in page memory for reconnects.
It sends the token in the WebSocket handshake subprotocol, leaving the
connection URL clean. Reloading the page clears the in-memory token, so use
the original link or enter it again. Older `?token=...` links still work and
are cleaned from the history entry when opened. Avoid query-token links: the
query is sent in the HTTP request and may be recorded by proxies or logs.

To choose a token, set `--websocket-token <token>`,
`WIDEBOI_WEBSOCKET_TOKEN`, or `websocket_token` in the config file. Enter that
value in the browser's connection form. To rotate a configured token, change
its value and restart the server; to rotate a generated token, restart the
server. The current token is required for new WebSocket connections. Keep the
startup link private: anyone holding it can control the terminal session.

For frontend development only, run `cd web && npm run dev` to use Vite's
development server alongside a wideboi server. Normal builds use the embedded
client.

### Remote browser access

Keep wideboi bound to loopback and put an HTTPS reverse proxy on the same host.
For example, with nginx and a certificate for `wideboi.example.com`:

```nginx
server {
    listen 443 ssl;
    server_name wideboi.example.com;
    ssl_certificate /path/to/fullchain.pem;
    ssl_certificate_key /path/to/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

Start wideboi with `--websocket 127.0.0.1:8080`, then open
`https://wideboi.example.com/`. The browser uses WSS automatically. Keep the
proxy's `Host` header intact so the WebSocket origin check accepts the same
host. Terminate TLS at the proxy, restrict who can reach it, and avoid logging
WebSocket handshake headers containing the token. The generated startup link
points to loopback; for remote access, read the token from the owner-only
`.web-token` file described above and enter it in the HTTPS page's connection
form. Treat the token and any token-bearing link as terminal access credentials.

## Configuration

wideboi reads configuration with the following precedence (highest to lowest):

1. **Command-line flags** (`-l`, `-p`, `-L`, `-s`, `--shell`, `--websocket`, `--websocket-token`, `--disable-auto-cleanup`)
2. **Environment variable overrides** (`WIDEBOI_LAYOUT`, `WIDEBOI_PREFIX`, `WIDEBOI_SESSION`, `WIDEBOI_SOCK`, `WIDEBOI_SHELL`, `WIDEBOI_LOG_LEVEL`, `WIDEBOI_AUTO_CLEANUP`, `WIDEBOI_WEBSOCKET`, `WIDEBOI_WEBSOCKET_TOKEN`)
3. **Configuration file** (TOML, including `websocket` and `websocket_token`)
4. **Defaults** (including `$SHELL` or `/bin/sh`)

### Config file

By default, wideboi looks for a config file at:

- `$XDG_CONFIG_HOME/wideboi/config.toml` (typically `~/.config/wideboi/config.toml`)

It also checks for a `.wideboi.toml` file in the current working directory, which acts as a project-specific config that overrides the default one.

You can pass a custom config file path using `-c` or `--config`:

```
wideboi -c /path/to/custom-config.toml
```

See [`config.example.toml`](config.example.toml) for an annotated example configuration file.

To open a project loadout when a new session starts, add `[[startup]]` entries
to `.wideboi.toml` (or your main config). Each entry creates one column in
order. A `command` runs through the configured shell; omit it for an
interactive shell. Optional `width` is the column width in cells (20–4096).

```toml
[[startup]]
command = "nvim"
width = 100

[[startup]] # interactive shell

[[startup]]
command = "claude"
width = 80
```

The project list replaces the main config's list. It applies only when the
server creates its first panes; attaching to an existing session leaves its
columns as they are. Without a list, wideboi starts two shell columns.

### Key remapping

You can remap control-mode verbs in the `[keys]` table of your `config.toml`:

```toml
prefix = "ctrl+a"
layout = "cards"

[keys]
kill_pane  = "k"
scroll_up  = "e"
focus_left = ["h", "left"]   # several keys: the first is shown in the bar
detach     = []              # unbind
```

Rules for key remapping:
- A value is one key, a list of keys, or `[]` to unbind the action.
- Setting an action replaces all of its default keys, including the arrow keys on `focus_left` and `focus_right`. List every key you want to keep.
- The first key in a list is the one the status bar and help overlay show.
- `quit` cannot be unbound: it is the only way to end the session.
- Keys `i`, `m`, and `[` are reserved by wideboi because their control bytes decode as Tab, Enter, and Escape, which breaks repeat chords.
- No two actions may share a key, even when the other action is left at its defaults. The error names the other action so you can remap it too.
- Every single letter `a-z` bound to an action gets a matching `ctrl+<letter>` repeat chord, except on `toggle_cards`: repeating a toggle only undoes it, and `ctrl+c` stays a way out of control mode.
- The digit keys `0`-`9` are fixed and cannot be remapped, or used for another action.

### CLI Flags

```
Flags:
  -c, --config <path>    Path to TOML configuration file
                         (default: $XDG_CONFIG_HOME/wideboi/config.toml)
  -l, --layout <mode>    Starting layout for this client: "cards" (default) or "scroll"
  -p, --prefix <key>     Control mode prefix key: "ctrl+<letter>" or "ctrl+space"
                         (default: "ctrl+b")
  -L, --session <name>   Session to start or attach to (default: "default");
                         its socket is $TMPDIR/wideboi-<uid>/<name>.sock
  -s, --socket <path>    Unix domain socket path, instead of a session name
      --websocket <addr> Address for WebSocket server (e.g. ":8080")
      --websocket-token <token> Token required for WebSocket connections
      --shell <path>     Shell executable to launch in panes
                         (default: $SHELL or /bin/sh)
  -v, --version          Print version and exit
  -h, --help             Show help text and exit
```

### Environment variables

- `WIDEBOI_LAYOUT`: starting layout for this client (`cards` or `scroll`)
- `WIDEBOI_PREFIX`: prefix key (`ctrl+<letter>` or `ctrl+space`)
- `WIDEBOI_SESSION`: session name override
- `WIDEBOI_SOCK`: unix domain socket path override, instead of a session name
- `WIDEBOI_WEBSOCKET`: address for the HTTP and WebSocket listener (disabled by default)
- `WIDEBOI_WEBSOCKET_TOKEN`: token required for WebSocket connections (generated at server startup if unset)
- `WIDEBOI_SHELL`: shell path override (takes precedence over TOML `shell`)
- `WIDEBOI_LOG_LEVEL`: log verbosity, `trace`, `debug`, `info` (default), `warn` or `error`. Logs go beside the session's socket, as `<socket without .sock>.{client,server}.log` (so `$TMPDIR/wideboi-<uid>/default.server.log` for the default session), and are appended to, so `trace`, which records every message a client receives, is for chasing something specific
- `WIDEBOI_AUTO_CLEANUP`: automatically remove dead sockets, tokens, and session logs on clean exit (`1` / `true` by default; set to `0` / `false` to preserve logs for inspection)
- `SHELL`: default shell path (used when shell is not set in config)

## Development

    make quick    # fmt, vet, seam boundary, Go and web unit tests
    make check    # adds browser acceptance, race, exit contract, smoke, attach
    make race     # go test -race -count=1 ./..., on its own
    make smoke    # scripted acceptance cases, asserted on the pty wire
    make proto    # regenerate the Go and TypeScript wire bindings

`make check` runs its targets in parallel; `CHECK_JOBS=1` forces serial.
For the browser acceptance test, install Chromium once with
`cd web && npx playwright install chromium`. CI installs it automatically.

`docs/LESSONS.md` is worth reading before changing anything.

### Wire protocol

`docs/PROTOCOL.md` describes the messages, the framing, and the update and
patch rules in more detail.

Both transports carry Protocol Buffers. The schema in
`internal/protocol/wirepb/wideboi.proto` defines a `ClientMessage` and a
`ServerMessage` envelope, each a `oneof` over the message types. Unix socket
messages have a four-byte big-endian length prefix; WebSocket messages are one
binary frame each.

The Go code keeps its own structs in `internal/protocol` and converts them at
the transport edge in `internal/protocol/codec.go`. When you add a field to a
message, add it to the schema and the codec: `TestCodecRoundTripsEveryField`
fails if the codec drops it. The browser uses the generated TypeScript types
directly.

The generated code is committed. After editing the schema, install
[`buf`](https://buf.build/docs/installation), run `npm ci --prefix web`, then
`make proto`. `protoc-gen-go` runs from `go.mod`, so it needs no install;
the TypeScript generator (`@bufbuild/protoc-gen-es`) needs Node 22 or later.

## License

MIT — see [LICENSE](LICENSE).
