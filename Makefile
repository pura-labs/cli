.PHONY: build test test-race test-verbose e2e fmt fmt-check vet tidy tidy-check check clean

BINARY = pura
MODULE = github.com/pura-labs/cli

build:
	go build -o $(BINARY) ./cmd/pura

test:
	go test ./...

test-race:
	go test -race ./...

test-verbose:
	go test -v ./...

# Regenerate SURFACE.txt from the cobra tree. Run this after adding /
# renaming a command or flag; commit the diff so reviewers see UX changes.
surface: build
	./$(BINARY) _surface > SURFACE.txt
	@echo "SURFACE.txt refreshed."

# Check that the committed SURFACE.txt matches the current tree. Fails if
# the code drifted from the checked-in surface — typically means you ran
# `make surface` but forgot to commit.
check-surface: build
	@./$(BINARY) _surface > SURFACE.txt.actual
	@diff -u SURFACE.txt SURFACE.txt.actual || (rm SURFACE.txt.actual; echo "SURFACE.txt is stale. Run 'make surface' and commit."; exit 1)
	@rm SURFACE.txt.actual
	@echo "SURFACE.txt is up to date."

# E2E suite: runs against a live Pura instance (wrangler dev by default).
# Skips silently when PURA_E2E_URL isn't reachable or PURA_E2E_TOKEN isn't set.
# Run with: make e2e PURA_E2E_URL=http://localhost:8787 PURA_E2E_TOKEN=sk_pura_...
e2e: build
	go test -tags=e2e -count=1 -v ./e2e/...

# Contract tests: compare live-server response shapes against committed
# fixtures (SSOT drift guard, PLAN §8.5). Skipped when PURA_E2E_URL
# points at nothing. Regenerate fixtures with UPDATE_CONTRACTS=1.
contract-check:
	go test -tags=contract -count=1 -v ./internal/api/...

# goreleaser dry run — produces dist/ locally without publishing.
# Use to verify the ldflags, archive layout, and checksum file before
# cutting a real release.
release-snapshot: completions
	@command -v goreleaser >/dev/null 2>&1 || { echo "goreleaser not installed. See https://goreleaser.com/install/"; exit 1; }
	goreleaser release --snapshot --clean --skip=publish

# Regenerate bash / zsh / fish completions under ./completions/
# (release archives include them).
completions:
	@./scripts/gen-completions.sh

fmt:
	go fmt ./...

fmt-check:
	@gofmt -l . | grep -v vendor | tee /dev/stderr | (! read)

vet:
	go vet ./...

tidy:
	go mod tidy

tidy-check:
	go mod tidy && git diff --exit-code go.mod go.sum

# Coverage gate — ensures each non-exempt internal package stays
# above 70%. Set COVERAGE_FLOOR=<n> to tighten.
coverage:
	@./scripts/coverage-gate.sh

check: fmt vet test-race coverage check-surface
	@echo "All checks passed."

clean:
	rm -f $(BINARY)
