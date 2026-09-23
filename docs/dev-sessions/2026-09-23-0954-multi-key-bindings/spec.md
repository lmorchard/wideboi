# Multi-key bindings for control-mode verbs Spec

**Goal:** Let a user give any control-mode verb several keys, or none, from the
`[keys]` table, e.g. `focus_left = ["h", "left"]`.

**Source:** Les, conversation of 2026-09-23.

## Current state

See `research.md`. The load-bearing facts:

- `Config.Keys` is `map[string]string` (config.go:28), one key per action.
- The only multi-key bindings are the hardcoded `Aliases` on `focus_left`
  (`left`) and `focus_right` (`right`) (keys.go:152-155).
- When a remap takes an alias's key, `BuildBindings` drops the alias silently
  (keys.go:400-409). No test covers this.
- `CtrlForm` derives the repeat chord from `Key` only (keys.go:230-245).
- Help and bar labels read `b.Key` (help.go:18-64, keys.go:412-451).
- In control mode, an unknown key already leaves the mode (router.go:132-133).

## Desired end state

```toml
[keys]
focus_left  = ["h", "left"]   # list: first is primary, rest are aliases
scroll_up   = "k"             # string: same as ["k"]
detach      = []              # empty list: verb is unbound
```

- **Replace semantics.** Any entry for an action replaces *all* of its default
  keys, built-in aliases included. `focus_left = "h"` means the left arrow no
  longer focuses left.
- **Primary key.** The first entry is the primary key. It is the only key shown
  in the status bar and the help overlay. The rest are aliases.
- **Ctrl repeat on every key that can have one.** Every key on a binding gets
  the `ctrl+<letter>` repeat form if it is a single a-z letter, not `Reserved`,
  and the binding is not `NoRepeat`. With `focus_left = ["h", "g"]`, both
  `ctrl+h` and `ctrl+g` repeat.
- **Unbinding.** An empty list removes the binding from the returned slice, so
  the router, bar and help never see it. `quit = []` is an error, because quit is
  the only way to end a session. Unbinding `exit` is allowed, because an unknown
  key already leaves control mode.
- **Collisions are errors.** If a key appears on two bindings, loading fails.
  That covers primaries, aliases and digits, and it includes an action you left
  at its defaults. The error names both actions. When the other action still
  has its defaults, the error says to remap it as well, e.g.:
  `key "left" for "new_column" is already used by "focus_left" (a default); remap focus_left too`.
  A key listed twice on the same action is also an error.
- **Bar and help labels** use primary keys only.
  - The move group is built from the primaries of the four move verbs that are
    still bound. It is dropped if none are.
  - Help group labels join the primaries of the members that are still bound.

## Design decisions

- **Config decodes `Keys` as `map[string]any` and normalizes it to
  `map[string][]string` in `internal/config`. `BuildBindings` takes
  `map[string][]string`.**
  - **Why:** this is plain go-toml v2 decoding with a type switch, and there is
    no custom unmarshaller in the repo. The keys package stays TOML-free.
  - **Rejected:** a `KeyList` type with a custom unmarshaller. It is
    library-specific, and no other code in the repo does it.
  - A non-string, or a list element that is not a string, is an error naming
    the action.
- **Replace, not merge.**
  - **Why:** Les's call. This is the only way to free a default alias such as
    the left arrow.
  - **Rejected:** a string keeps the built-in aliases and a list replaces them.
    Two meanings for one field is surprising.
- **Collisions are errors, including against defaults; alias filtering is
  removed.**
  - **Why:** Les's call. The silent drop is how a key stops working without the
    user ever being told. It also matches the existing rule that "the table and
    `[keys]` share one namespace" (LESSONS.md:484-502).
- **`quit` cannot be unbound; `exit` can.**
  - **Why:** without quit there is no way to end the session. Unbinding exit
    loses nothing, because router.go:132-133 already treats an unknown key as a
    way out.
- **Only the primary key is shown in the bar and help.**
  - **Why:** the bar is 77 of 79 cells and the help overlay is exactly full at
    80x24 (LESSONS.md:504-517). Listing aliases would overflow both.
- **`CtrlForm() (string, bool)` becomes `CtrlForms() []string`**, with one entry
  per key that qualifies. The router loop checks all of them.

## Patterns to follow

- Keep the validation order and error style of `BuildBindings`
  (keys.go:360-380), and apply it to each key in a list.
- Keep the `"keys configuration: %w"` wrapping (config.go:212-216).
- For the ctrl-form tests, decode real bytes through `uv.EventDecoder`, as
  `TestEveryCtrlFormMatchesItsRealByte` does (keys_test.go:93). Extend it to
  cover aliases.
- Build router tests with custom bindings in the style of `TestControlModeTable`
  (router_test.go:78). No such test exists yet, so add one.
- Extend the smoke remap case at scripts/smoke.py:596-615 with a list entry, so
  that a real config file drives an alias end to end.

## Compatibility

- **`config.example.toml` lists every action with its default key.** Under
  replace semantics, `focus_left = "h"` would lose the left arrow. Update it to
  `focus_left = ["h", "left"]` and `focus_right = ["l", "right"]`, and have
  `TestLoadConfigExampleToml` assert that the loaded bindings equal the defaults.
  That test is the guard against the example drifting.
- Anyone who copied the example into their own config loses the arrows until
  they update it. A remap onto `left` or `right` that used to succeed silently
  now fails. Both go in the PR description as compat breaks.

## What we're NOT doing

- Showing aliases in the bar or help overlay.
- New default keys, including up/down for scrolling (LESSONS.md:484-502).
- Remapping the digit bindings.
- Setting keys from env vars or flags.
- Checking the prefix against bindings or ctrl forms. The router's
  prefix-first order still decides that.
- Unbinding the prefix itself.
- Fixing what happens when both `attn` and `smart_jump` are set, which already
  resolves by map order. Record it in memory instead.
- Removing the duplicate `parsePrefix` in router.go.

## Open questions

None. Everything the brainstorm raised was settled, as recorded above.
