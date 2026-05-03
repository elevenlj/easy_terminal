.PHONY: build test test-browser test-all run tidy

build:
	go build -o easy_terminal ./cmd

test:
	go test ./...

test-browser: build
	node tests/browser_e2e.mjs

test-all: test test-browser

run:
	go run ./cmd

tidy:
	go mod tidy
