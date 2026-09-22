# wideboi

A scrolling tiling terminal multiplexer for CLI coding agents. Panes keep their
width; open more and the viewport scrolls instead of squeezing what is already
there.

Status: v1. Working, and rough in places — see the [open issues](https://github.com/lmorchard/wideboi/issues) for what is known and parked.

## Build and run

    make build
    ./bin/wideboi

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
| `d` | detach, leaving the session running (socket sessions only) |
| `q` | quit wideboi and close every pane |
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
    make check    # the gate: adds race detector, exit contract, smoke, attach (~12s)
    make race     # go test -race -count=1 ./..., on its own
    make smoke    # scripted acceptance cases, asserted on the pty wire

`make check` runs its targets in parallel; `CHECK_JOBS=1` forces serial.

`docs/LESSONS.md` is worth reading before changing anything.
