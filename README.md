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
process is killed — the whole session is torn down with it, so a
forgotten `wideboi` never leaves agents running where you cannot see
them. Once detached, a session lives until `ctrl+b q` from any client,
`wideboi kill-session`, or a signal to the server.

`wideboi server` and `wideboi attach` start the two halves separately;
a session started with `wideboi server` is never tied to a terminal.

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
| `?` | show the full help overlay |
| `d` | detach, leaving the session running |
| `q` | end the session: close every pane and stop the server |
| `esc` | leave control mode |
| `ctrl+b` | send a literal `ctrl+b` to the pane, and leave control mode |

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

## Configuration

wideboi reads configuration with the following precedence (highest to lowest):

1. **Command-line flags** (`-l`, `-p`, `-s`, `--shell`)
2. **Environment variable overrides** (`WIDEBOI_LAYOUT`, `WIDEBOI_PREFIX`, `WIDEBOI_SOCK`, `WIDEBOI_SHELL`)
3. **Configuration file** (TOML)
4. **Defaults** (including `$SHELL` or `/bin/sh`)

### Config file

By default, wideboi looks for a config file at:

- `$XDG_CONFIG_HOME/wideboi/config.toml` (typically `~/.config/wideboi/config.toml`)

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
scroll_up  = "u"
focus_left = "h"
```

Rules for key remapping:
- Keys `i`, `m`, and `[` are reserved by wideboi because their control bytes decode as Tab, Enter, and Escape, which breaks repeat chords.
- No two actions may be assigned to the same key.
- Remapped single letters `a-z` automatically receive matching `ctrl+<letter>` repeat chords.

### CLI Flags

```
Flags:
  -c, --config <path>    Path to TOML configuration file
                         (default: $XDG_CONFIG_HOME/wideboi/config.toml)
  -l, --layout <mode>    Layout strategy: "cards" (default) or "scroll"
  -p, --prefix <key>     Control mode prefix key: "ctrl+<letter>" or "ctrl+space"
                         (default: "ctrl+b")
  -s, --socket <path>    Unix domain socket path
                         (default: $TMPDIR/wideboi-<uid>/default.sock)
      --shell <path>     Shell executable to launch in panes
                         (default: $SHELL or /bin/sh)
  -v, --version          Print version and exit
  -h, --help             Show help text and exit
```

### Environment variables

- `WIDEBOI_LAYOUT`: layout mode (`cards` or `scroll`)
- `WIDEBOI_PREFIX`: prefix key (`ctrl+<letter>` or `ctrl+space`)
- `WIDEBOI_SOCK`: unix domain socket path override
- `WIDEBOI_SHELL`: shell path override (takes precedence over TOML `shell`)
- `SHELL`: default shell path (used when shell is not set in config)

## Development

    make quick    # the edit loop: fmt, vet, seam boundary, unit tests (~5s)
    make check    # the gate: adds race detector, exit contract, smoke, attach (~50s)
    make race     # go test -race -count=1 ./..., on its own
    make smoke    # scripted acceptance cases, asserted on the pty wire

`make check` runs its targets in parallel; `CHECK_JOBS=1` forces serial.

`docs/LESSONS.md` is worth reading before changing anything.
