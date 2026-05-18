.PHONY: all build build-mcp run clean dist test lint

all: dist

build:
	go run ./tools/makehelper build

build-mcp:
	go run ./tools/makehelper build-mcp

dist: build build-mcp
	go run ./tools/makehelper dist

run:
	go run ./cmd/server -- /run

clean:
	go clean
	go run ./tools/makehelper clean

test:
	go run ./tools/makehelper test

lint:
	go run ./tools/makehelper lint

