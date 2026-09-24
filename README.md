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
| `n` | open a new column |
| `w` | cycle this column's width |
| `x` | kill the focused pane |
| `a` | jump to a pane wanting attention |
| `1`–`9` / `0` | focus the column at that position from the left / the last column |
| `tab` | focus the previously focused pane |
| `c` | switch this terminal between the card fan and the scrolling strip |
| `y` / `u` | move this column one place left / right |
| `?` | show the full help overlay |
| `d` | detach, leaving the session running |
| `q` | end the session: close every pane and stop the server |
| `esc` | leave control mode |
| `ctrl+b` | send a literal `ctrl+b` to the pane, and leave control mode |

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

## Web Client

wideboi includes a browser-based client that uses an HTML5 Canvas to render your terminal windows securely over a WebSocket connection.

To enable the web client, you must explicitly opt-in to the WebSocket server when starting a background session:

```bash
wideboi server --websocket :8080
```

*Or via environment variable: `WIDEBOI_WEBSOCKET=":8080"`*

Once the server is running, the Web Client allows you to view and interact with your terminal multiplexer natively in any modern browser.

> Note: The Vite + Lit Web UI is currently hosted in the `web/` directory and requires running `npm run dev` to serve the static assets locally during development (see [Issue #126](https://github.com/lmorchard/wideboi/issues/126)).

## Configuration

wideboi reads configuration with the following precedence (highest to lowest):

1. **Command-line flags** (`-l`, `-p`, `-L`, `-s`, `--shell`, `--websocket`)
2. **Environment variable overrides** (`WIDEBOI_LAYOUT`, `WIDEBOI_PREFIX`, `WIDEBOI_SESSION`, `WIDEBOI_SOCK`, `WIDEBOI_SHELL`, `WIDEBOI_LOG_LEVEL`, `WIDEBOI_WEBSOCKET`)
3. **Configuration file** (TOML)
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
- `WIDEBOI_SHELL`: shell path override (takes precedence over TOML `shell`)
- `WIDEBOI_LOG_LEVEL`: log verbosity, `trace`, `debug`, `info` (default), `warn` or `error`. Logs go beside the session's socket, as `<socket without .sock>.{client,server}.log` (so `$TMPDIR/wideboi-<uid>/default.server.log` for the default session), and are appended to, so `trace`, which records every message a client receives, is for chasing something specific
- `SHELL`: default shell path (used when shell is not set in config)

## Development

    make quick    # the edit loop: fmt, vet, seam boundary, unit tests (~5s)
    make check    # the gate: adds race detector, exit contract, smoke, attach (~50s)
    make race     # go test -race -count=1 ./..., on its own
    make smoke    # scripted acceptance cases, asserted on the pty wire

`make check` runs its targets in parallel; `CHECK_JOBS=1` forces serial.

`docs/LESSONS.md` is worth reading before changing anything.

## License

MIT — see [LICENSE](LICENSE).
