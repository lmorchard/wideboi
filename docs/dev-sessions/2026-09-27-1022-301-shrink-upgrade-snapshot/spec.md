# Spec: #301 shrink the upgrade snapshot

## Background & Problem
The in-place upgrade serializes every pane's grid snapshot, scrollback included, as per-cell JSON (`term.GridSnapshot` → `protocol.LineData`, written by `execFn` in `internal/server/upgrade.go`). That costs about 212 bytes per cell. In a heavy session (8 panes × 120×40 × 10,000 styled scrollback lines):

- **A 2,041 MB (2 GB) state file** in `$TMPDIR`.
- **~6.3s to encode and write it** in the old process, **with `s.mu` held**. Client messages that arrive in that window wait in the conn loop and die with the socket at exec, losing keystrokes typed during the window.
- **~18.6s for `RestoreState`** in the new process before it answers handshakes.
- Handshake ceiling had to be raised to 60s in #299 just to accommodate this delay.

## Goals
- Dramatically reduce the upgrade snapshot size (target: >= 95-99% reduction, down to ~10-20 MB or less in the heavy benchmark).
- Dramatically reduce encode time under `s.mu` (target: sub-100ms or faster).
- Dramatically reduce `RestoreState` time (target: sub-second).
- Preserve exact terminal fidelity across upgrade: visible screen, cursor, modes, scrollback lines, text content, width (including wide characters and continuation cells), and style attributes.
- Ensure backwards compatibility or safe upgrade paths if relevant (note: `UpgradeState` is ephemeral between parent and child exec).

## Non-Goals
- Eliminating scrollback preservation during upgrade (we want to keep scrollback if we can preserve it cheaply and quickly).
- Redesigning `protocol.LineData` on the client-server wire protocol (the upgrade state is purely server-to-server via `WIDEBOI_RESTORE_STATE`).
