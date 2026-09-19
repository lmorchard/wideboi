# wideboi

A scrolling tiling terminal multiplexer for CLI coding agents. Panes keep their
width; open more and the viewport scrolls instead of squeezing what is already
there.

Status: v1. Working, and rough in places — see `docs/BEYOND-V1.md`.

## Build and run

    make build
    ./bin/wideboi

## Your terminal must send Option as Meta

wideboi's verbs are bound to `alt`. On macOS most terminals send Option as a
composed character (`˜`, `∆`) rather than as Meta, and **wideboi will appear to
ignore every shortcut** until you change that.

| Terminal | Setting |
| --- | --- |
| Terminal.app | Settings → Profiles → Keyboard → *Use Option as Meta Key* |
| iTerm2 | Settings → Profiles → Keys → Left Option key → *Esc+* |
| Ghostty | `macos-option-as-alt = true` |
| WezTerm | `send_composed_key_when_left_alt_is_pressed = false` |

To check: press `alt+n`. A new column should open.

## Keys

| Key | Action |
| --- | --- |
| `alt+h` / `alt+l` | focus left / right |
| `alt+n` | new column |
| `alt+w` | cycle column width |
| `alt+x` | kill focused pane |
| `alt+j` | jump to the pane that wants attention |
| `alt+u` / `alt+d` | scroll the focused pane's history |
| `alt+q` | quit |

Everything else goes to the focused pane.

## Development

    make check    # fmt, vet, seam boundary, unit tests, exit contract, smoke
    make smoke    # scripted acceptance cases, asserted on the pty wire

`docs/LESSONS.md` is worth reading before changing anything.
