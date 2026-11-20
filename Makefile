BIN     := bin/squadrn
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/squadrn/squadrn/internal/cli.Version=$(VERSION)

.PHONY: build race e2e cover-gates cover-gates-windows vuln lint fmt check validate hooks clean

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/squadrn

race:
	go test -race ./...

# Installs a build of this checkout through the real installer into a sandbox and drives the main
# flows with fake agent CLIs (e2e/README.md). Needs git, ssh-keygen and, on Unix, tmux.
e2e:
	go test -tags e2e -count=1 -v ./e2e/

# Minimum statement coverage per package: the guard is the security boundary.
COVER_GATES := guard:95 githook:85 grid:70 doctor:70 install:80 release:80 update:95 ui:90 worktree:75 proc:85 fsx:75 usage:90 inuse:85
# On Windows some Unix-only tests are skipped; the security-critical gates stay high.
COVER_GATES_WINDOWS := guard:93 githook:70 install:75 release:65 update:95 ui:90 proc:70 fsx:65 usage:90 inuse:65

cover-gates:
	@scripts/cover-gates.sh $(COVER_GATES)

# Run on Windows (CI job "windows", through Git Bash).
cover-gates-windows:
	@scripts/cover-gates.sh $(COVER_GATES_WINDOWS)

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.10.0 ./...

lint:
	golangci-lint run ./...
	golangci-lint run --build-tags e2e ./e2e/
	GOOS=windows go vet ./...
	GOOS=windows go vet -tags e2e ./e2e/
	shellcheck -s sh plugin/bin/squadrn-hook scripts/*.sh .githooks/*
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
	rm -rf bin build dist
