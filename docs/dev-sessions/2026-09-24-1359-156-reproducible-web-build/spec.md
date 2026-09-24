# Spec: Issue 156 - Make the embedded web build reproducible from the lockfile

## Problem
The `web/dist` Make target runs `npm install` and depends on `web/package.json`, sources, and config, but not `web/package-lock.json` (`Makefile:174-175`, currently lines 177-178). A lockfile-only change may leave an old embedded bundle in place, and dependency installation is less deterministic than the lockfile allows.

## Proposed work
- Include `web/package-lock.json` in the target prerequisites.
- Use `npm ci` for a clean, lockfile-based build.
- Consider an explicit web build target if that makes local Go-only checks cheaper or clearer.

## Done when
- A lockfile change triggers a rebuilt `web/dist`.
- The build succeeds from a clean checkout without modifying the lockfile.
- CI and local builds use the same dependency versions.
