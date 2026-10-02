.PHONY: build clean test test-unit e2e-antigravity cover cover-html cover-check golden fmt fmt-check vet tidy-check ci install check-gh branch pr help

APP_NAME=agentport
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_DATE=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
# cmd/main.go only calls cli.Main, so the gate covers the testable packages.
COVER_PKGS=./cmd/cli/... ./internal/...
COVER_MIN=90
# Adapters are exercised through internal/convert, so coverage is measured across packages.
comma := ,
space := $(empty) $(empty)
LDFLAGS=-ldflags "-X github.com/somaz94/agentport/cmd/cli.Version=$(VERSION) -X github.com/somaz94/agentport/cmd/cli.GitCommit=$(GIT_COMMIT) -X github.com/somaz94/agentport/cmd/cli.BuildDate=$(BUILD_DATE)"

## Build

build: ## Build the binary
	go build $(LDFLAGS) -o bin/$(APP_NAME) ./cmd/

clean: ## Remove build artifacts and coverage files
	rm -rf bin/ coverage.out coverage.raw coverage.html

## Test

test: test-unit ## Run unit tests (alias)

test-unit: ## Run unit tests with coverage
	go test ./... -v -race -cover

AG_LANGUAGE_SERVER ?= /Applications/Antigravity.app/Contents/Resources/bin/language_server

e2e-antigravity: ## Load converted skill fixtures with the Antigravity desktop app's own loader
	AGENTPORT_AG_LANGUAGE_SERVER=$(AG_LANGUAGE_SERVER) go test ./internal/convert/ -run TestAntigravity -v

## Coverage

cover: ## Generate coverage report
	go test ./... -coverprofile=coverage.out
	go tool cover -func=coverage.out

cover-html: cover ## Open coverage report in browser
	go tool cover -html=coverage.out -o coverage.html
	open coverage.html

cover-check: ## Fail when coverage of the testable packages is below COVER_MIN
	go test -count=1 $(COVER_PKGS) -coverpkg=$(subst $(space),$(comma),$(COVER_PKGS)) -coverprofile=coverage.raw > /dev/null
	@# -count=1: a cached binary reports -coverpkg blocks at stale line numbers. Each binary reports
	@# every block, so keep one line per block with its best count.
	@awk 'NR == 1 { print; next } { k = $$1 " " $$2; if (!(k in c) || $$3 > c[k]) c[k] = $$3 } END { for (k in c) print k, c[k] }' coverage.raw > coverage.out
	@go tool cover -func=coverage.out | awk -v min=$(COVER_MIN) '/^total:/ { sub("%", "", $$3); if ($$3 + 0 < min) { printf "coverage %s%% is below %s%%\n", $$3, min; exit 1 } printf "coverage %s%% (min %s%%)\n", $$3, min }'

golden: ## Rewrite golden files from the current output
	AGENTPORT_UPDATE_GOLDEN=1 go test ./...

## Quality

fmt: ## Format code
	go fmt ./...

fmt-check: ## Fail when any file is not gofmt-formatted
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "run make fmt"; exit 1; }

vet: ## Run go vet
	go vet ./...

tidy-check: ## Fail when go.mod or go.sum is not tidy
	go mod tidy -diff

ci: tidy-check fmt-check vet test cover-check build ## Run every check CI runs
	./bin/$(APP_NAME) version

## Install

install: build ## Install to /usr/local/bin
	cp bin/$(APP_NAME) /usr/local/bin/$(APP_NAME)

## Workflow

check-gh: ## Check if gh CLI is installed and authenticated
	@command -v gh >/dev/null 2>&1 || { echo "\033[31m✗ gh CLI not installed. Run: brew install gh\033[0m"; exit 1; }
	@gh auth status >/dev/null 2>&1 || { echo "\033[31m✗ gh CLI not authenticated. Run: gh auth login\033[0m"; exit 1; }
	@echo "\033[32m✓ gh CLI ready\033[0m"

branch: ## Create feature branch (usage: make branch name=feature-name)
	@if [ -z "$(name)" ]; then echo "Usage: make branch name=<feature-name>"; exit 1; fi
	git checkout main
	git pull origin main
	git checkout -b feat/$(name)
	@echo "\033[32m✓ Branch feat/$(name) created\033[0m"

pr: check-gh ## Run tests, push, and create PR (usage: make pr title="Add feature")
	@if [ -z "$(title)" ]; then echo "Usage: make pr title=\"PR title\""; exit 1; fi
	go test ./... -race -cover
	go vet ./...
	git push -u origin $$(git branch --show-current)
	@./scripts/create-pr.sh "$(title)"
	@echo "\033[32m✓ PR created\033[0m"

## Help

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
