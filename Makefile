BIN     := bin/sprawl
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X sprawling-agents/internal/cli.Version=$(VERSION)

.PHONY: build test race cover cover-gates vuln lint fmt check validate hooks clean

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/sprawl

test:
	go test ./...

race:
	go test -race ./...

cover:
	go test -coverprofile=coverage.out ./internal/...
	go tool cover -func=coverage.out | tail -1

# Minimum statement coverage per package: the guard is the security boundary.
COVER_GATES := guard:95 githook:85 grid:70 doctor:70 install:80 release:80 update:95 ui:90 worktree:75 proc:85 fsx:75
# On Windows some Unix-only tests are skipped; the security-critical gates stay high.
COVER_GATES_WINDOWS := guard:93 githook:70 install:75 release:70 update:95 ui:90 proc:85 fsx:65

cover-gates:
	@scripts/cover-gates.sh $(COVER_GATES)

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.10.0 ./...

lint:
	golangci-lint run ./...
	shellcheck -s sh plugin/bin/sprawl-hook scripts/*.sh .githooks/*
	actionlint .github/workflows/*.yml

fmt:
	golangci-lint fmt ./...

validate:
	@if command -v claude >/dev/null; then claude plugin validate . && claude plugin validate plugin; else echo "claude not installed; skipping plugin validation"; fi

check: lint race cover-gates validate

# Commit message checks for this clone (see AGENTS.md, Commits).
hooks:
	git config core.hooksPath .githooks

clean:
	rm -rf bin build dist coverage.out
