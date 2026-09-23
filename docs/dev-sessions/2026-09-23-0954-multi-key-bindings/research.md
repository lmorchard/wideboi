# Research: how control-mode bindings flow today

Documentarian pass, 2026-09-23. Facts only; design lives in `spec.md`.

## Config → BuildBindings

- TOML lib: `github.com/pelletier/go-toml/v2 v2.4.3` (go.mod:10, config.go:16).
- `Keys map[string]string \`toml:"keys"\`` (config.go:28), plain `toml.Unmarshal` (config.go:105-107).
  No `UnmarshalTOML`/`UnmarshalText` anywhere in the repo.
- Layering: file only. `if len(fileCfg.Keys) > 0 { cfg.Keys = fileCfg.Keys }` (config.go:121-123);
  env and flags never touch keys.
- `keys.BuildBindings(cfg.Keys)` at config.go:212-216, error wrapped `"keys configuration: %w"`.
- `parsePrefix` (config.go:228-247) checks only `keys.Reserved`; never looks at bindings.

## BuildBindings (internal/keys/keys.go:352-454)

- Empty/nil map → copy of `Bindings` (:353-357).
- Validate: action in `validActions` (:284-304, includes alias `attn`→`smart_jump`); key
  lowercased+trimmed (:366); empty → error; `Reserved` → error; `isValidKeyName` (:338-348),
  with a `pgdn` → "did you mean pgdown" hint.
- Clone table, overwrite `Key` (:383-389).
- Collision check over every binding's `Key`, digits included (:392-398).
- Alias filter: an alias equal to any `Key` is silently dropped (:400-409). No test covers it.
- Bar labels rebuilt (:412-451): move group `"<h><j><k><l> move"`; `"%s new"`, `"%s kill"`, etc.
  Runs only on the non-empty branch.

## Runtime consumers

- Router (cmd/wideboi/router.go): falls back to `keys.Bindings` if slice empty (:110-113).
  In control mode: prefix first (:102-108), then table order, `CtrlForm()` then
  `MatchString(MatchNames()...)` (:114-124). **Unknown key, any modifier, leaves control mode**
  (:132-133) — so `esc` with no binding still leaves.
- `fire` (:138-171): quit/detach/exit force `control=false`.
- `CtrlForm` (keys.go:230-245): only `Key`, single a-z, not Reserved, not NoRepeat.
- `MatchNames` (keys.go:249-254): `Key` + `Aliases`.
- Status bar: `controlHelp` (client.go:767-786) → `keys.BarItemsFor` (keys.go:259-276).
  Essential items never dropped.
- Help overlay: `helpLines` (internal/client/help.go:18-53). Ungrouped line
  `"%-4s  %s"` of `b.Key`; grouped line label = `HelpKey` or `helpGroupKeys` joined "/"
  (help.go:56-64, members' `Key` in table order).

## Tests

- keys_test.go: `TestTableIsWellFormed` (:15, aliases don't collide), `TestEveryCtrlFormMatchesItsRealByte`
  (:93), `TestEveryPlainFormMatchesItself` (:119), `TestBuildBindings*` (:292-460),
  `TestBarItemsFor` (:463).
- config_test.go: `TestLoadTomlKeysRemapping` (:100), `TestLoadInvalidKeysInToml` (:164),
  `TestLoadConfigExampleToml` (:182, only asserts non-empty bindings).
- router_test.go: `TestControlModeTable` (:78) iterates default table only; no custom-binding router test.
- help_test.go: `TestCustomBindingsInControlHelpAndHelpLines` (:158), `TestHelpGroupLabelIsItsKeys`
  (:208), `TestRemappedPairKeepsItsGroupLabel` (:228).
- help_overlay_test.go: `TestHelpOverlayFitsAt80x24` (:215) — default table only.
- scripts/smoke.py `case_config_file_and_key_remapping` (:596-615) asserts `k kill`, `hjel move`;
  `case_control_mode_names_every_entry_at_80_columns` (:736-753).

## Docs

- README.md:137-155 "Key remapping" rules list.
- config.example.toml:45-79: every action listed **with its default key**; comments on
  focus_left/right mention `(aliases: "left")` / `(aliases: "right")`.
- docs/LESSONS.md:484-502 "A new default binding is a breaking change to every config that uses its key";
  :504-517 "The help overlay is exactly full at 80x24".

## Digits, Essential, NoRepeat

- Digits: not in `validActions` → "unknown action" if named; still in collision check.
- Essential (quit, exit): read only by `BarItemsFor`. No BuildBindings check.
- NoRepeat (toggle_cards): `CtrlForm` returns nothing regardless of key; survives remap.
