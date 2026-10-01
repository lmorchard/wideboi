# Research: Issue 335 Terminal Environment Queries

- Query Specifications:
  - OSC 10 / 11: Format `\x1b]10;?<ST|BEL>` and `\x1b]11;?<ST|BEL>`.
    Replies use `rgb:rrrr/gggg/bbbb` (16-bit color channels per X11 spec).
    Default dark background: `1e1e/1e1e/1e1e`. Default foreground: `d4d4/d4d4/d4d4`.
  - OSC 4: Palette colors 0..255. Format `\x1b]4;<index>;?<ST|BEL>`.
    Reply: `\x1b]4;<index>;rgb:rrrr/gggg/bbbb<ST|BEL>`.
  - CSI 14t: Window pixel dimensions. Format `\x1b[14t`.
    Reply: `\x1b[4;<height>;<width>t`.
  - CSI 16t: Cell pixel dimensions. Format `\x1b[16t`.
    Reply: `\x1b[6;<height>;<width>t`.
  - CSI 18t: Text area size in character cells. Format `\x1b[18t`.
    Reply: `\x1b[8;<rows>;<cols>t`.
  - CSI ? 996 n: Light/dark mode query.
    Reply: DSR 997 -> `\x1b[?997;1n` (dark) or `\x1b[?997;2n` (light).
- PTY Stream Interception:
  - Child writes queries to stdout (master Read).
  - Interceptor matches query, generates reply, and writes reply to master (slave stdin).
  - Matched query is removed from the slice sent to `grid.Write`.
