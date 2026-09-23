# Multi-key bindings Implementation Plan

**Goal:** Allow a `[keys]` entry to be a string, a list, or an empty list.
Any entry replaces all of that action's default keys. The first key is the
primary; the rest are aliases. Every a-z key gets a ctrl repeat. Collisions
are errors, and so is unbinding `quit`.

**Approach:**
- `internal/keys` takes `map[string][]string` and owns every rule.
- `internal/config` decodes `[keys]` as `map[string]any` and normalizes it
  into that shape.
- The router checks every ctrl form, not just one.
- Help and bar already read `b.Key`, which becomes the primary, and unbound
  bindings are absent from the slice. So neither needs code changes.

**Tech stack:** Go, go-toml v2, ultraviolet key events, the Python pty smoke
harness.

---

## Phase 1: Multi-key bindings in `internal/keys` and the router

After this phase, `BuildBindings` handles lists, unbinding, the quit guard and
the new collision rules, and the router fires on every alias and every ctrl
form. Nothing reaches this from a config file yet.

**Files:**
- Modify: `internal/keys/keys.go`
  - Change the `BuildBindings` signature.
  - Replace `CtrlForm` with `CtrlForms`.
  - Remove the alias filter.
  - Add the collision error helper.
- Modify: `cmd/wideboi/router.go:118` to use `CtrlForms()`.
- Modify: `internal/config/config.go:213`, a temporary adapter. `cfg.Keys` is
  still `map[string]string` in this phase, so wrap each value as `[]string{v}`.
  Phase 2 replaces the adapter.
- Test: `internal/keys/keys_test.go`, `cmd/wideboi/router_test.go`,
  `internal/client/help_test.go`
  - Convert the existing `map[string]string{…}` call sites to
    `map[string][]string{k: {v}}`.
  - Update every use of `CtrlForm()`.
  - Add the new tests listed below.

**Key changes:**

```go
// CtrlForms returns the repeat chord for every key on this binding that
// has one: a single a-z letter outside Reserved, unless NoRepeat.
func (b Binding) CtrlForms() []string {
	if b.NoRepeat {
		return nil
	}
	var out []string
	for _, k := range b.MatchNames() {
		if len(k) != 1 || k[0] < 'a' || k[0] > 'z' {
			continue
		}
		if _, bad := Reserved[k]; bad {
			continue
		}
		out = append(out, "ctrl+"+k)
	}
	return out
}
```

The router loop (router.go:114-124) becomes:

```go
if forms := b.CtrlForms(); len(forms) > 0 && ev.MatchString(forms...) {
	return r.fire(b, true)
}
```

`BuildBindings(custom map[string][]string) ([]Binding, error)`:

1. **Validate.** Keep the existing unknown-action and per-key checks: empty,
   reserved, unrecognized name, and the `pgdn` hint, now applied to each key in
   the list. Two new checks:
   - `quit` with an empty list: `action "quit" cannot be unbound: it is the only way to end the session`.
   - The same key twice in one list: `key %q is listed twice for action %q`.
2. **Apply.** Build `out` from `Bindings` in table order:
   - An action with an empty list is skipped.
   - An action with keys gets `Key = ks[0]`, and `Aliases` is set to
     `slices.Clone(ks[1:])` if `len(ks) > 1`, otherwise `nil`. That replaces
     the default aliases.
   - Actions not in `custom` are unchanged.
3. **Collide.** Walk `out`, and each binding's `MatchNames()`, into
   `seen map[string]string` (key to action). On a repeat, return
   `collisionError(key, prev, cur, normalized)`. The old alias filter at
   keys.go:400-409 is deleted.
4. **Labels.** Build the move group from the primaries of whichever of
   focus_left, scroll_down, scroll_up and focus_right are still in `out`, in
   that order. The per-action labels are unchanged, because they already read
   `b.Key`, which is now the primary.

```go
// collisionError explains a key held by two bindings. At least one of
// them was configured -- the default table has no collisions
// (TestTableIsWellFormed) -- and the hint names the one that was not.
func collisionError(key, a, b string, configured map[string][]string) error {
	_, aSet := configured[a]
	_, bSet := configured[b]
	switch {
	case aSet && bSet:
		return fmt.Errorf("duplicate key %q assigned to both %q and %q", key, a, b)
	case aSet || bSet:
		mine, other := a, b
		if bSet {
			mine, other = b, a
		}
		if _, remappable := validActions[other]; !remappable {
			return fmt.Errorf("duplicate key %q for %q: %q holds it and cannot be remapped", key, mine, other)
		}
		return fmt.Errorf("duplicate key %q for %q: %q holds it by default; remap %s too", key, mine, other, other)
	}
	return fmt.Errorf("duplicate key %q assigned to both %q and %q", key, a, b)
}
```

`configured` is keyed by canonical action name, which is the `normalized` map
from step 1.

**New tests (write first, watch them fail):**
- `keys_test.go`:
  - `TestBuildBindingsListSetsPrimaryAndAliases`: `focus_left: {"h", "g"}`
    gives Key `h` and Aliases `[g]`. `CtrlForms()` gives `[ctrl+h ctrl+g]`.
  - `TestBuildBindingsSettingReplacesDefaultAliases`: `focus_left: {"h"}`
    gives nil Aliases, so `left` is gone.
  - `TestBuildBindingsEmptyListUnbinds`: `detach: {}` means no binding has
    ActionName detach, and `BarItemsFor(b, true)` has no `d detach`.
  - `TestBuildBindingsEmptyListUnbindsExit`: `exit: {}` succeeds.
  - `TestBuildBindingsQuitCannotBeUnbound`: `quit: {}` is an error containing
    `cannot be unbound`.
  - `TestBuildBindingsKeyListedTwice`: `focus_left: {"h", "h"}` is an error
    containing `listed twice`.
  - `TestBuildBindingsAliasCollidesWithDefault`: `new_column: {"n", "left"}`
    is an error containing `"focus_left" holds it by default; remap focus_left too`.
    This is the case the old alias filter used to swallow silently.
  - `TestBuildBindingsAliasCollidesWithDigit`: `kill_pane: {"x", "1"}` is an
    error containing `cannot be remapped`.
  - `TestBuildBindingsFreedDefaultCanBeTaken`: `focus_left: {"h"}` plus
    `new_column: {"n", "left"}` succeeds.
  - `TestBuildBindingsMoveGroupSkipsUnbound`: `scroll_down: {}` gives the
    move label `hkl move`.
  - `TestCustomCtrlFormsMatchTheirRealBytes`: decode each byte through
    `uv.EventDecoder`, like `TestEveryCtrlFormMatchesItsRealByte`, over
    `BuildBindings({focus_left: {"h", "g"}})`. This covers the alias's
    `ctrl+g` (0x07).
  - Update `TestEveryCtrlFormMatchesItsRealByte` to loop over `CtrlForms()`,
    taking the letter from `name[len("ctrl+"):]`.
- `router_test.go`:
  - `TestCustomAliasAndItsCtrlFormFire`: a router with
    `bindings: BuildBindings({focus_left: {"h", "g"}})`. In control mode,
    `key('g')` gives routeVerb FocusLeft and leaves control mode, and
    `ctrl('g')` gives FocusLeft and stays in control mode.
  - `TestReplacedDefaultAliasIsUnknown`: `focus_left: {"h"}`. In control mode,
    `uv.KeyPressEvent{Code: uv.KeyLeft}` gives routeIgnore and leaves control
    mode.
- `help_test.go`:
  - `TestHelpShowsOnlyPrimaryAndOmitsUnbound`: with
    `new_column: {"n", "enter"}` and `detach: {}`, the help lines contain
    `n     open a new column` and do not contain `enter` or `detach`.

**Verification — automated:**
- [x] New tests fail before the implementation, for the stated reason (record in notes.md) — **aliases unset, `left` still bound, `g`/`ctrl+g` unknown, empty list panicked the shape-only stub**
- [x] `go test ./internal/keys ./cmd/wideboi ./internal/client -run 'BuildBindings|CtrlForm|Custom|Replaced|Help' -v` passes — **all three packages ok**
- [x] `make quick` passes — **fmt, vet, seam OK, all packages ok**

**Verification — manual:**
- [x] None. Nothing here is reachable from a config file yet.

---

## Phase 2: Strings and lists from `config.toml`

After this phase, `[keys]` accepts a string, a list, or an empty list from the
config file, and `config.example.toml` loads exactly the defaults.

**Files:**
- Modify: `internal/config/config.go`
  - Change `Keys` to `map[string]any` with the `toml:"keys"` tag.
  - Add `keyLists`.
  - Call `keys.BuildBindings(lists)` and drop the Phase 1 adapter.
- Modify: `config.example.toml:45-79`
  - Use `focus_left = ["h", "left"]` and `focus_right = ["l", "right"]`.
  - Rewrite the comment block to explain strings, lists, `[]`, replace
    semantics, and the ctrl repeat on every a-z key.
- Test: `internal/config/config_test.go`

**Key changes:**

```go
// keyLists turns the decoded [keys] table into one key list per action.
// A string is a one-key list; an empty list unbinds the action.
func keyLists(raw map[string]any) (map[string][]string, error) {
	out := make(map[string][]string, len(raw))
	for act, v := range raw {
		switch v := v.(type) {
		case string:
			out[act] = []string{v}
		case []any:
			list := make([]string, 0, len(v))
			for _, e := range v {
				s, ok := e.(string)
				if !ok {
					return nil, fmt.Errorf("action %q: list entries must be quoted key names, got %v", act, e)
				}
				list = append(list, s)
			}
			out[act] = list
		default:
			return nil, fmt.Errorf("action %q: want a key name or a list of key names, got %v", act, v)
		}
	}
	return out, nil
}
```

The call site keeps the `"keys configuration: %w"` wrapping for both
`keyLists` and `BuildBindings` errors.

**New tests (write first):**
- `TestLoadTomlKeyLists`: this config gives focus_left Key `h` with Aliases
  `[g]`, and no detach binding.

  ```toml
  [keys]
  focus_left = ["h", "g"]
  detach = []
  ```

- `TestLoadTomlKeysRejectsNonStrings`: `kill_pane = 5` and `kill_pane = ["x", 5]`
  are both errors containing `kill_pane`.
- `TestLoadTomlQuitUnbound`: `quit = []` is an error containing
  `cannot be unbound`.
- Update `TestLoadConfigExampleToml` to assert
  `reflect.DeepEqual(bindings, keys.Bindings)`. This is the structural guard
  that the documented example is exactly the defaults. Before the example is
  edited, it should fail on focus_left's Aliases.
- The existing `TestLoadTomlKeysRemapping` and `TestLoadInvalidKeysInToml`
  keep passing unchanged, because string values still work.

**Verification — automated:**
- [x] `TestLoadConfigExampleToml` fails before `config.example.toml` is edited, on the aliases — **bindings 0 and 1 had `Aliases:[]`, want `[left]`/`[right]`**
- [x] `go test ./internal/config -v` passes — **ok**
- [x] `make quick` passes — **fmt, vet, seam OK, all packages ok**

**Verification — manual:**
- [x] Les: with `focus_left = ["h", "g"]` in a scratch config, `C-b g` moves focus left and `C-b ctrl+g ctrl+g` moves twice — **Les confirmed "works"**

---

## Phase 3: Smoke coverage and user docs

After this phase, a real config file drives an alias end to end over a pty, and
the README documents the new rules.

**Files:**
- Modify: `scripts/smoke.py`. Add
  `case_config_key_list_alias_moves_focus` after
  `case_config_file_and_key_remapping`, and register it in `CASES`.
- Modify: `README.md:137-155`. Update the example and the rules list.

**Key changes:**

```python
def case_config_key_list_alias_moves_focus(fail):
    # A list entry's second key is an alias; typing it must move focus.
    # Startup focuses pane 1 on the left, as case_click_focuses_pane relies on.
    with tempfile.NamedTemporaryFile("w", suffix=".toml", delete=False) as f:
        f.write('[keys]\nfocus_right = ["l", "g"]\n')
        cfg_path = f.name
    try:
        s = Session(args=["-c", cfg_path])
        before = focus_pane_id(s.output(), s.rows)
        s.type("\x02g")  # C-b g -> the alias for focus_right
        after = focus_pane_id(s.output(), s.rows)
        s.close()
        if before is None or after is None:
            fail("no focus-pane-id observed around the alias")
            return
        if after == before:
            fail(f"alias g for focus_right left focus on pane {before}")
    finally:
        os.unlink(cfg_path)
```

The README example becomes:

```toml
[keys]
kill_pane  = "k"
scroll_up  = "e"
focus_left = ["h", "left"]   # several keys: the first is shown in the bar
detach     = []              # unbind
```

The rules list gains these points:
- Setting an action replaces all of its default keys, including the arrow keys
  on focus_left and focus_right.
- The first key in a list is the one the bar and help show.
- `[]` unbinds an action; `quit` cannot be unbound.
- Every single a-z letter on a binding gets a ctrl repeat.
- A key held by two actions is an error, even when the other action is at its
  defaults.

**Verification — automated:**
- [x] The new smoke case fails with the alias removed from its config (`focus_right = "l"`), then passes with it restored — **red: "alias g for focus_right left focus on pane 1"; green after restore**
- [x] `make check` passes, run 4 times (the parallel suite has a known ~1-in-6 load flake; see memory) — **4/4 exit 0; smoke 36/36 and attach 17/17 each run**

**Verification — manual:**
- [ ] Les reads the README "Key remapping" section and the `config.example.toml` comments
