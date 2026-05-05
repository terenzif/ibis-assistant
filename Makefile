# Nome degli eseguibili finali
ifeq ($(OS),Windows_NT)
BINARY_NAME := knowledge_server.exe
MCP_BRIDGE_NAME := mcp-bridge.exe
CLEAN_CMD := cmd /C del /Q
NULL_DEV := nul
else
BINARY_NAME := knowledge_server
MCP_BRIDGE_NAME := mcp-bridge
CLEAN_CMD := rm -f
NULL_DEV := /dev/null
endif

.PHONY: build build-mcp all run clean

build:
	go build -o $(BINARY_NAME) ./cmd/server

build-mcp:
	go build -o $(MCP_BRIDGE_NAME) ./tools/mcp-bridge

all: build build-mcp


run:
	go run ./cmd/server -- /run

clean:
	go clean
	-$(CLEAN_CMD) "$(BINARY_NAME)" "$(MCP_BRIDGE_NAME)" 2>$(NULL_DEV)

