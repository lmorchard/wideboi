# Dev Session Notes: Issue 156 - Make the embedded web build reproducible from the lockfile

- Worktree: `.worktrees/issue-156-reproducible-web-build` (branch `issue-156-reproducible-web-build`)
- Issue: https://github.com/lmorchard/wideboi/issues/156

## Summary of Changes
1. **Explicit Prerequisites and Lockfile Pinning:**
   - Added `web/node_modules/.installed` as a Makefile stamp target depending on `web/package.json` and `web/package-lock.json`. It runs `cd web && npm ci && @touch $@`. Using a stamp file inside `node_modules/` ensures that an interrupted or failed `npm ci` cannot leave an un-stamp'd directory that masks an incomplete install.
   - Updated `web/dist` target to depend on `web/node_modules/.installed`, `web/package.json`, `web/package-lock.json`, `$(shell find web/src -type f)`, `web/index.html`, and `web/tsconfig.json`. It runs `cd web && npm run build`.
   - By decoupling dependency installation (`npm ci`) from bundle compilation (`npm run build`), editing TypeScript sources rebuilds the bundle without wiping and reinstalling node modules every time. Touching or modifying `web/package-lock.json` triggers a clean `npm ci` install and subsequent bundle rebuild.
2. **Explicit Web Build Target:**
   - Added `web-build: web/dist` and included `web-build` in `.PHONY`.
3. **Proto Prerequisite:**
   - Added `web/node_modules/.installed` prerequisite to `proto` (which invokes `buf generate` using `web/node_modules/.bin/protoc-gen-es`).

## Verification
- Verified touching `web/package-lock.json` rebuilds `web/node_modules/.installed` via `npm ci` and rebuilds `web/dist`.
- Verified touching a TypeScript source file (`web/src/focus.ts`) skips `npm ci` and only runs `npm run build`.
- Verified clean build (`rm -rf web/node_modules web/dist bin/wideboi && make build`) succeeds and leaves `git status` clean (no lockfile modification).
- Verified `make quick`, `make web-test`, `make proto-check`, and `make check` (all 37 smoke tests, 25 attach tests, 6 exit-signal tests, unit tests, and race detector) pass.
