# uzushio. `make` builds and tests; `make check` is what CI asks.
#
# Every target here is a command a person can also type. The Makefile exists to
# name them, not to hide them.

GO ?= go
BIN ?= bin/uzushio
GOLANGCI ?= golangci-lint
DOCDAG ?= docdag
CMOA ?= cmoa

.PHONY: all build test vet lint generate check docdag conform clean

all: build test vet lint

build:
	$(GO) build -o $(BIN) ./cmd/uzushio

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

lint:
	$(GOLANGCI) run ./...

# generate rewrites every artefact this repository derives from code. It needs
# a cmoa on PATH: the harness surfaces are CMoA's vocabulary, not ours, and the
# embedded copy is refreshed from the binary that owns it.
generate:
	$(GO) generate ./...
	$(GO) run ./cmd/uzushio docdag-config --out docdag.yaml
	$(GO) run ./internal/fixture/gen -out lint

# check regenerates and refuses a difference. It is the one target that says
# whether the committed artefacts are the ones this code produces.
#
# `git add -N` runs first because `git diff` does not look at untracked paths.
# A regeneration that writes a fixture nobody staged would otherwise leave the
# new file sitting on disk, show git nothing, and pass. Intent-to-add puts it
# into the comparison without committing anything.
check: generate
	git add -N -- docdag.yaml lint internal/surfaces/cmoa-surfaces.json
	git diff --exit-code -- docdag.yaml lint internal/surfaces/cmoa-surfaces.json

# docdag runs the engine over both corpora: the specification vault at the root
# and the decision records under docs/adr.
docdag:
	$(DOCDAG) validate
	$(DOCDAG) lint --all
	$(DOCDAG) validate --config docs/adr/docdag.yaml

# conform runs the shell conformance tests, which are the executable half of
# the clauses they are named after.
conform:
	@set -eu; \
	find tests/conform -name test.sh | sort | while read -r t; do \
		echo "--- $$t"; \
		sh "$$t"; \
	done

clean:
	rm -f $(BIN)
