# Spec: Fix CLI client column resize content blanking

## Problem Statement

When resizing a column in the CLI client, the contents start blanking out a little while after each change.

## Investigation Goals

1. Reproduce or identify the mechanism causing pane contents to blank out after resizing a column in the CLI client.
2. Determine where the blanking occurs (server emulator/reflow/grid resize vs client-side layout/rendering/damage tracking).
3. Fix the root cause so pane contents remain intact and visible during and after column resizing.
4. Add regression test(s) to verify that column resizing does not blank or truncate contents inappropriately.
