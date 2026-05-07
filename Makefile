# Nome degli eseguibili finali
ifeq ($(OS),Windows_NT)
	BINARY_NAME := knowledge_server.exe
	MCP_BRIDGE_NAME := mcp-bridge.exe
	CLEAN_CMD := cmd /C del /Q
	MKDIR_CMD := cmd /C mkdir
	COPY_CMD := cmd /C copy /Y
	COPY_DIR_CMD := cmd /C xcopy /E /I /Y
	NULL_DEV := nul
else
	BINARY_NAME := knowledge_server
	MCP_BRIDGE_NAME := mcp-bridge
	CLEAN_CMD := rm -f
	MKDIR_CMD := mkdir -p
	COPY_CMD := cp
	COPY_DIR_CMD := cp -r
	NULL_DEV := /dev/null
endif

.PHONY: build build-mcp all run clean

build:
	go build -o $(BINARY_NAME) ./cmd/server

build-mcp:
	go build -o $(MCP_BRIDGE_NAME) ./tools/mcp-bridge

all: build build-mcp

dist: all
	-$(MKDIR_CMD) dist 2>$(NULL_DEV)
	$(COPY_CMD) $(BINARY_NAME) dist\ 2>$(NULL_DEV) || $(COPY_CMD) $(BINARY_NAME) dist/
	$(COPY_CMD) $(MCP_BRIDGE_NAME) dist\ 2>$(NULL_DEV) || $(COPY_CMD) $(MCP_BRIDGE_NAME) dist/
	$(COPY_DIR_CMD) rules dist\rules 2>$(NULL_DEV) || $(COPY_DIR_CMD) rules dist/rules
	$(COPY_CMD) sgconfig.yml dist\ 2>$(NULL_DEV) || $(COPY_CMD) sgconfig.yml dist/
	$(COPY_CMD) config_master.json dist\config.json 2>$(NULL_DEV) || $(COPY_CMD) config_master.json dist/config.json

run:
	go run ./cmd/server -- /run

clean:
	go clean
	-$(CLEAN_CMD) "$(BINARY_NAME)" "$(MCP_BRIDGE_NAME)" 2>$(NULL_DEV)

