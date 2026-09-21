.PHONY: check test race lint fmt fmt-check seam-check build run tidy verify-exit smoke golden attach-check print-go-version

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
check: fmt-check lint seam-check test race verify-exit smoke attach-check

test:
	go test ./...

# The race detector belongs in the gate: a data race that only appears
# under load is exactly what a green suite hides. -count=1 defeats `go
# test`'s result cache, which otherwise reports stale "ok" on a rerun of
# an already-cached package -- silently skipping the very detector this
# target exists to run. Roughly 3x slower than plain `test`, which is
# worth it here.
race:
	go test -race -count=1 ./...

lint:
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

build:
	go build -ldflags "$(LDFLAGS)" -o bin/wideboi ./cmd/wideboi

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
#   3. Nothing wideboi spawned outlived it -- including a nohup'd
#      background job the harness types into a pane first, because the
#      pane shells exit on their own when the master closes and so
#      cannot detect a skipped teardown by themselves.
#
# The size matrix includes the no-winsize (0x0) case that used to panic
# before main.go clamped width/height. The signal matrix covers the armed
# set that can be delivered without a core dump; SIGQUIT is armed too but
# left out here because its default disposition dumps core.
#
# ptycheck.py itself never hangs (bounded waits, SIGKILL-and-reap on
# timeout), so this target fails loudly rather than wedging CI or a dev
# machine.
verify-exit: build
	@for size in 80x24 4x2 1x1 0x0; do \
		python3 scripts/ptycheck.py --size $$size --signal SIGTERM || exit 1; \
	done; \
	for sig in SIGINT SIGHUP; do \
		python3 scripts/ptycheck.py --size 80x24 --signal $$sig || exit 1; \
	done

# Regenerate the golden wire snapshot. Review the diff before committing.
golden: build
	python3 scripts/golden.py --update

# Scripted acceptance checks, asserted on the bytes wideboi writes to the
# pty. See scripts/smoke.py for why the wire is the right layer.
smoke: build
	python3 scripts/smoke.py
	python3 scripts/golden.py

# attach-check drives the client/server pair over a real Unix socket:
# `wideboi server` in the background, `wideboi attach` on a pty, and the
# detach/reattach cycle between them.
#
# smoke is structurally blind to this seam. It only ever runs the
# in-process binary, so no message it exercises is ever serialised, and
# a wire format that cannot encode a coloured cell passes every case in
# it. That is not hypothetical: protocol.CellData.Style carried a
# uv.Style, whose colour fields are interfaces gob refuses to encode,
# and `attach` died on the first coloured prompt with the whole unit
# suite green.
#
# Costs ~60s, dominated by waiting for real login shells to print real
# prompts. It earns that by being the only target that proves the
# feature works at all.
attach-check: build
	python3 scripts/attachcheck.py
