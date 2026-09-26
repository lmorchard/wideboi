.PHONY: check check-targets quick test linux-test web-test web-build proto proto-check race lint fmt fmt-check seam-check build desktop desktop-app run tidy verify-exit smoke golden attach-check traffic print-go-version

# Stamped into the binary at build time so a released artifact can say
# what it is. VERSION falls back to a placeholder outside a tagged
# checkout, which is what a `go build` with no tags in sight produces.
VERSION ?= $(shell git describe --tags --always --dirty --match 'v[0-9]*' 2>/dev/null || echo "v0.0.0-dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

# The release workflow reads the Go pin from here rather than
# hardcoding it, so CI and a local build cannot drift onto different
# toolchains and disagree about what compiles.
print-go-version:
	@go list -f '{{.Module.GoVersion}}' ./cmd/wideboi 2>/dev/null || sed -n 's/^go //p' go.mod

# check is the pre-merge / CI gate, so every target it depends on must be
# read-only. fmt is deliberately NOT one of them: it rewrites files, and a
# gate that mutates the tree it is judging cannot be trusted by either CI
# or a reviewer. Run `make fmt` yourself; `make check` only tells you.
#
# verify-exit is included even though it costs ~18s (against a fraction of
# a second for the rest of check combined): it's the only target in this
# file that exercises the real binary in a real pty, and it's what proves
# the exit-status/teardown contract that the other tests only exercise in
# isolation. build's output (bin/wideboi) is gitignored, so verify-exit is
# still read-only with respect to the tree check is judging.
#
# For the edit loop, use `make quick` below -- check is the gate, not the
# thing to run on save.
#
# check delegates to a parallel sub-make so everyone gets the speedup
# without having to remember -j. The targets are independent; build is a
# shared prerequisite and a single make invocation runs it once.
# CHECK_JOBS=1 restores a fully serial run.
#
# This only became safe once the suites stopped colliding: a plain
# wideboi attaches to whatever answers the default socket, so
# attach-check's server used to capture every session smoke started.
# See WIDEBOI_SOCK in cmd/wideboi/main.go.
CHECK_JOBS ?= 8
check:
	@$(MAKE) --no-print-directory -j$(CHECK_JOBS) check-targets

check-targets: fmt-check lint seam-check test web-test web-accept race verify-exit smoke attach-check

# quick is the inner-loop tier: everything that does not spawn the real
# binary in a real pty, and no race detector. Run this on save; run check
# before pushing.
#
# There is deliberately no fast-package list. Once the Go tests stopped
# waiting out fixed grace periods the whole suite is a few seconds, so the
# useful boundary is "Go tests" vs "race + the pty suites" -- which is
# stable, where a hand-maintained list of fast packages would drift. A new
# package joins quick automatically; if someone adds a slow test, quick
# gets slower, which is noticeable and self-correcting rather than silent.
quick: fmt-check lint seam-check test web-test

test: web/dist
	go test ./...

# linux-test runs the Go tests in a Linux container at the go.mod pin.
# Sockets and ptys behave differently there -- a peer hanging up reads
# ECONNRESET rather than EOF, and a pty can outlive its session leader --
# so socket- and pty-lifecycle changes want a run here before pushing
# (docs/LESSONS.md). LINUX_TEST_ARGS narrows it, e.g. -run TestWait.
linux-test: web/dist
	docker run --rm -v "$(CURDIR)":/src -w /src golang:$$($(MAKE) -s print-go-version) \
		go test -count=1 $(LINUX_TEST_ARGS) ./...

web-test: web/dist
	cd web && npm test

# Exercise the actual web app in Chromium with a controlled WebSocket peer.
web-accept: build
	cd web && npm run test:browser

# The race detector belongs in the gate: a data race that only appears
# under load is exactly what a green suite hides. -count=1 defeats `go
# test`'s result cache, which otherwise reports stale "ok" on a rerun of
# an already-cached package -- silently skipping the very detector this
# target exists to run. Roughly 3x slower than plain `test`, which is
# worth it here.
race: web/dist
	go test -race -count=1 ./...

lint: web/dist
	go vet ./...

# fmt rewrites. fmt-check reports and fails. Keep them separate.
fmt:
	gofmt -l -w .

fmt-check:
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then \
		echo "gofmt needed for:"; echo "$$out" | sed 's/^/  /'; \
		echo "run: make fmt"; \
		exit 1; \
	fi

# seam-check enforces the spec's "internal/client must not import
# internal/server" invariant in both directions, with Plan 1's existing
# crossings allowlisted in the script so it catches the next one.
seam-check:
	./scripts/seam-check.sh

build: web/dist
	go build -ldflags "$(LDFLAGS)" -o bin/wideboi ./cmd/wideboi

# The desktop executable also supports the regular CLI/server commands, so
# sessions it starts use the same artifact and need no second bundled binary.
# Match the .app's minimum macOS version. Without these CGO flags, the linker
# targets macOS 11 even when the current SDK compiled Wails objects for 13+.
ifeq ($(shell uname -s),Darwin)
DESKTOP_CGO_ENV := MACOSX_DEPLOYMENT_TARGET=13.0 CGO_CFLAGS="-O2 -g -mmacosx-version-min=13.0" CGO_LDFLAGS="-O2 -g -mmacosx-version-min=13.0"
endif
desktop: web/dist
	$(DESKTOP_CGO_ENV) go build -tags desktop,production -ldflags "$(LDFLAGS)" -o bin/wideboi-desktop ./cmd/wideboi

# A macOS bundle for Finder and release archives. Signing and notarization
# are not configured yet.
desktop-app: desktop
	./scripts/package-desktop-macos.sh bin/wideboi-desktop bin/wideboi.app

run: build
	./bin/wideboi

tidy:
	go mod tidy

# verify-exit runs scripts/ptycheck.py against the built binary in a real
# pty. For each case it asserts three things:
#
#   1. The process dies BY the signal sent rather than exiting normally.
#      On its own this is weak -- the kernel's default disposition
#      satisfies it too, so an unarmed binary passes.
#   2. The alt-screen exit sequence reached the pty before that death.
#      This is what distinguishes wideboi's restore-then-re-raise from
#      a bare default kill, and what gives assertion 1 its meaning.
#   3. Nothing wideboi spawned outlived it: its server and that server's
#      pane shells. Teardown is the pty hangup, so a nohup'd job in a
#      pane is deliberately not asserted on.
#
# The size matrix includes the no-winsize (0x0) case that used to panic
# before main.go clamped width/height. The signal matrix covers the armed
# set that can be delivered without a core dump; SIGQUIT is armed too but
# left out here because its default disposition dumps core.
#
# ptycheck.py itself never hangs (bounded waits, SIGKILL-and-reap on
# timeout), so this target fails loudly rather than wedging CI or a dev
# machine.
#
# The six invocations are independent and run concurrently: each owns
# its own wideboi, its own pty and its own socket path, and ptycheck's
# stray scan is scoped by parentage so it no longer reports the other
# five as leaks. 17.1s serial, ~4s fanned out.
#
# Every one of these is a background job, which is exactly why the
# signal-disposition pin in ptylib.spawn_in_pty exists: POSIX has the
# shell ignore SIGINT/SIGQUIT for an asynchronous list, and a test that
# asserts "died by SIGINT" cannot run under a disposition where SIGINT
# does nothing.
verify-exit: build
	@pids=""; \
	for size in 80x24 4x2 1x1 0x0; do \
		python3 scripts/ptycheck.py --size $$size --signal SIGTERM & \
		pids="$$pids $$!"; \
	done; \
	for sig in SIGINT SIGHUP; do \
		python3 scripts/ptycheck.py --size 80x24 --signal $$sig & \
		pids="$$pids $$!"; \
	done; \
	rc=0; for p in $$pids; do wait $$p || rc=1; done; exit $$rc

# Regenerate the golden wire snapshot. Review the diff before committing.
golden: build
	python3 scripts/golden.py --update

# Scripted acceptance checks, asserted on the bytes wideboi writes to the
# pty. See scripts/smoke.py for why the wire is the right layer.
smoke: build
	python3 scripts/smoke.py
	python3 scripts/golden.py

# attach-check drives the session lifecycle across processes: `wideboi
# server` in the background, `wideboi attach` and plain `wideboi` on a
# pty, detach and reattach, quit, kill-session, and an owner killed
# outright.
#
# It used to be the only target that serialised anything: smoke ran the
# in-process binary, so a wire format that could not encode a coloured
# cell passed every case in it -- protocol.CellData.Style carried a
# uv.Style, whose colour fields are interfaces gob refuses to encode,
# and `attach` died on the first coloured prompt with the whole unit
# suite green. Since #25 a plain wideboi is a client of a server it
# spawned, so smoke is on the wire too; this target is for what only
# more than one process can show.
#
# Costs ~35s, serially, most of it pane shells starting and sessions
# tearing down.
attach-check: build
	python3 scripts/attachcheck.py

# traffic is a measurement run (#179), not a gate: minutes, machine-dependent.
# Tables go to stdout, raw JSON (and --profile pprof files) to tmp/traffic/.
#   make traffic TRAFFIC_ARGS="--seconds 3 --only typing --profile"
traffic: build
	python3 scripts/traffic.py $(TRAFFIC_ARGS)

web/node_modules/.installed: web/package.json web/package-lock.json
	cd web && npm ci
	@touch $@

web/dist: web/node_modules/.installed web/package.json web/package-lock.json $(shell find web/src -type f) web/index.html web/tsconfig.json
	cd web && npm run build

web-build: web/dist

# Regenerate the Go and TypeScript wire bindings after editing
# internal/protocol/wirepb/wideboi.proto. Needs buf and web/node_modules;
# protoc-gen-go runs from go.mod.
proto: web/node_modules/.installed
	buf generate

# Verify that committed bindings were regenerated after schema changes.
# Keep this outside make check so local checks do not require buf.
proto-check: proto
	git diff --exit-code -- internal/protocol/wirepb web/src/gen
