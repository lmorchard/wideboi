#!/usr/bin/env python3
"""Render the Plan 6 mode-indicator options as real ANSI, at real widths.

A status bar is made of terminal cells and SGR attributes, so the honest
mockup is the terminal itself -- a browser would be showing a picture of
the thing rather than the thing.

    ./mockup-mode-indicator.py           # 80 and 100 columns
    ./mockup-mode-indicator.py 72 80 120

Each option prints twice, normal mode then command mode, over three rows
of placeholder pane content so the bar is seen in context. The cell cost
of each is printed beside it, because at 80 columns the verb menu does
not fit every option and that is the whole argument.
"""

import sys

RESET = "\033[0m"
REVERSE = "\033[7m"
DIM = "\033[2m"


def fg(n):
    return f"\033[38;5;{n}m"


def bg(n):
    return f"\033[48;5;{n}m"


# Colours chosen to survive both light and dark terminals: 33 is a mid
# blue, 208 a mid orange. Neither is pure-bright, so neither vanishes
# against a white background the way 39/226 would.
BLUE, ORANGE, GREY = 33, 208, 244

# The v1 verbs, rebound to unmodified keys. Ordered most essential
# first; the tail is dropped when the budget runs short, exactly as
# client.helpFor does today.
VERBS = [
    "h/l focus",
    "n new",
    "w width",
    "x kill",
    "j jump",
    "u/d scroll",
]
QUIT = "q quit"
EXIT = "esc exit"

CONTEXT = "focus: pane 1  [2 »]"
HINT = "C-b for commands"


class Bar:
    """A status line built from (text, sgr) segments.

    Visible width is tracked separately from the escape codes, because
    padding to the terminal's width has to count cells and len() counts
    bytes of escape sequence too.
    """

    def __init__(self):
        self.segs = []
        self.width = 0

    def add(self, text, sgr=""):
        self.segs.append((text, sgr))
        self.width += len(text)
        return self

    def render(self, cols, fill=""):
        pad = max(0, cols - self.width)
        out = "".join(f"{sgr}{text}{RESET}" if sgr else text for text, sgr in self.segs)
        return out + (f"{fill}{' ' * pad}{RESET}" if fill and pad else " " * pad)


def verb_menu(budget):
    """As many verbs as fit, quit and exit always included.

    Mirrors client.helpFor: reserve the non-negotiable tail first, admit
    segments in order until the next one would overflow, and report what
    was dropped so the mockup can say so out loud.
    """
    tail = [QUIT, EXIT]
    used = sum(len(s) for s in tail) + 2 * len(tail)
    taken = []
    for v in VERBS:
        if used + len(v) + 2 > budget:
            break
        used += len(v) + 2
        taken.append(v)
    dropped = VERBS[len(taken):]
    return "  ".join(taken + tail), dropped


# --- the four options ------------------------------------------------
#
# Each returns (bar, fill_sgr, dropped_verbs) for a mode and width. The
# spread is deliberate: from "no styling at all" to "the whole row
# changes", so the cost/legibility tradeoff is visible rather than
# argued.


def option_a(mode, cols):
    """Reverse-video badge, unstyled menu."""
    b = Bar()
    if mode == "normal":
        b.add(CONTEXT)
        menu, dropped = "", []
        b.add(" " * max(0, cols - 1 - b.width - len(HINT))).add(HINT, DIM)
        return b, "", []
    b.add(" COMMAND ", REVERSE)
    menu, dropped = verb_menu(cols - 1 - b.width - 2)
    b.add("  " + menu)
    return b, "", dropped


def option_b(mode, cols):
    """The whole row inverts. No badge -- the row is the badge."""
    b = Bar()
    if mode == "normal":
        b.add(CONTEXT)
        b.add(" " * max(0, cols - 1 - b.width - len(HINT))).add(HINT, DIM)
        return b, "", []
    menu, dropped = verb_menu(cols - 1)
    b.add(menu, REVERSE)
    return b, REVERSE, dropped


def option_c(mode, cols):
    """vim-style, text only. No styling anywhere."""
    b = Bar()
    if mode == "normal":
        b.add(CONTEXT)
        b.add(" " * max(0, cols - 1 - b.width - len(HINT))).add(HINT)
        return b, "", []
    b.add("-- COMMAND --")
    menu, dropped = verb_menu(cols - 1 - b.width - 2)
    b.add("  " + menu)
    return b, "", dropped


def option_d(mode, cols):
    """Unlabelled colour cap. Most compact, least self-explanatory."""
    b = Bar()
    if mode == "normal":
        b.add("  ", bg(BLUE)).add(" " + CONTEXT)
        b.add(" " * max(0, cols - 1 - b.width - len(HINT))).add(HINT, DIM)
        return b, "", []
    b.add("  ", bg(ORANGE)).add(" ")
    menu, dropped = verb_menu(cols - 1 - b.width)
    b.add(menu)
    return b, "", dropped


OPTIONS = [
    ("A", "Reverse-video badge", option_a),
    ("B", "Whole bar inverts", option_b),
    ("C", "Text only, no colour", option_c),
    ("D", "Unlabelled colour cap", option_d),
]


def pane_rows(cols):
    """Three rows of placeholder content, so the bar is seen in context."""
    left = max(1, (cols - 1) // 2)
    rows = []
    for text in ("$ claude --resume", "· thinking about the parser", "$ "):
        a = text.ljust(left)[:left]
        b = ("· running tests" if text.startswith("·") else "$ ").ljust(
            cols - left - 1
        )[: cols - left - 1]
        rows.append(f"{DIM}{a}{RESET}{fg(GREY)}│{RESET}{DIM}{b}{RESET}")
    return rows


def show(cols):
    print(f"\n{'=' * cols}")
    print(f"{cols} columns".center(cols))
    print("=" * cols)
    for key, name, fn in OPTIONS:
        print(f"\n{fg(GREY)}── {key}. {name} {'─' * max(0, cols - len(name) - 7)}{RESET}")
        for mode in ("normal", "command"):
            for row in pane_rows(cols):
                print(row)
            bar, fill, dropped = fn(mode, cols)
            print(bar.render(cols - 1, fill))
            note = f"   {mode}: {bar.width} cells"
            if dropped:
                note += f"  {fg(ORANGE)}drops {', '.join(dropped)}{RESET}"
            print(f"{fg(GREY)}{note}{RESET}")
            print()


def main():
    widths = [int(a) for a in sys.argv[1:]] or [80, 100]
    for c in widths:
        show(c)
    print(
        f"{fg(GREY)}Full menu is "
        f"{len('  '.join(VERBS + [QUIT, EXIT]))} cells before any badge.{RESET}"
    )


if __name__ == "__main__":
    main()
