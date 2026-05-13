.PHONY: build test test-browser test-all run tidy

VERSION := $(shell node -p "require('./npm/package.json').version" 2>/dev/null || echo dev)

build:
	go build -ldflags="-X main.version=$(VERSION)" -o easy_terminal ./cmd

test:
	go test ./...

test-browser: build
	node tests/browser_e2e.mjs

test-all: test test-browser

run:
	go run ./cmd

tidy:
	go mod tidy
