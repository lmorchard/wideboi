# wideboi

A scrolling tiling terminal multiplexer for CLI coding agents. Panes keep their
width; open more and the viewport scrolls instead of squeezing what is already
there.

Status: v1. Working, and rough in places — see `docs/BEYOND-V1.md`.

## Build and run

    make build
    ./bin/wideboi

## Keys

wideboi uses a prefix key, like tmux. Press `ctrl+b` to enter control
mode, then a verb. The status bar inverts and the cursor disappears
while control mode is active.

Control mode is sticky: it stays until you press `Escape`, so
`ctrl+b l l l` moves three columns right.

| In control mode | Action |
| --- | --- |
| `h` / `l` | focus left / right (arrow keys work too) |
| `n` | new column |
| `w` | cycle column width |
| `x` | kill focused pane |
| `j` | jump to the pane that wants attention |
| `u` / `d` | scroll the focused pane's history |
| `q` | quit |
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

## Development

    make check    # fmt, vet, seam boundary, unit tests, race detector, exit contract, smoke
    make race     # go test -race -count=1 ./..., on its own (~3x the cost of `test`)
    make smoke    # scripted acceptance cases, asserted on the pty wire

`docs/LESSONS.md` is worth reading before changing anything.
