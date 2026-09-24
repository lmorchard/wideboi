# Plan: Issue 156 - Make the embedded web build reproducible from the lockfile

## Status
Approved

## Problem
The `web/dist` Make target runs `npm install` and depends on `web/package.json`, sources, and config, but not `web/package-lock.json` (`Makefile:177-178`). A lockfile-only change may leave an old embedded bundle in place, and dependency installation via `npm install` is less deterministic and can modify `package-lock.json`.

## Goals
1. Include `web/package-lock.json` in the target prerequisites so lockfile changes trigger a rebuild of `web/dist`.
2. Use `npm ci` for clean, lockfile-based installation.
3. Separate dependency installation (`web/node_modules`) from the build step (`web/dist`), so editing TypeScript source files runs only `npm run build` without re-running `npm ci`, and `proto` can declare its dependency on `web/node_modules`.
4. Provide an explicit `.PHONY: web-build` target that builds `web/dist`.
5. Ensure the build succeeds from a clean checkout without modifying the lockfile.
6. Verify with `make quick`, `make check`, and lockfile-touch scenarios.

## Tasks
1. Edit `Makefile`:
   - Add `web-build` to `.PHONY`.
   - Add `web/node_modules/.installed` target depending on `web/package.json` and `web/package-lock.json`, running `cd web && npm ci && @touch $@`.
   - Update `web/dist` target to depend on `web/node_modules/.installed`, `web/package.json`, `web/package-lock.json`, `$(shell find web/src -type f)`, `web/index.html`, and `web/tsconfig.json`, running `cd web && npm run build`.
   - Add `web-build: web/dist`.
   - Add `web/node_modules/.installed` prerequisite to `proto` (which already documents "Needs buf and web/node_modules").
2. Self-verification:
   - Test touching `web/package-lock.json`: confirm `make web/dist` runs `npm ci` and rebuilds `web/dist`.
   - Test touching `web/src/focus.ts`: confirm `make web/dist` runs only `npm run build` and skips `npm ci`.
   - Test touching nothing: confirm `make web/dist` reports `web/dist is up to date`.
   - Test clean checkout simulation: remove `web/node_modules` and `web/dist`, run `make build`, verify clean exit and no diff on `web/package-lock.json`.
   - Run `make quick` and full `make check`.
3. Update dev-session notes and wrap up.
