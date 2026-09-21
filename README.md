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

## Development

    make quick    # the edit loop: fmt, vet, seam boundary, unit tests (~5s)
    make check    # the gate: adds race detector, exit contract, smoke, attach (~12s)
    make race     # go test -race -count=1 ./..., on its own
    make smoke    # scripted acceptance cases, asserted on the pty wire

`make check` runs its targets in parallel; `CHECK_JOBS=1` forces serial.

`docs/LESSONS.md` is worth reading before changing anything.
