# Nome degli eseguibili finali
ifeq ($(OS),Windows_NT)
	BINARY_NAME := knowledge_server.exe
	MCP_BRIDGE_NAME := mcp-bridge.exe
	CLEAN_CMD := cmd /C del /Q
	MKDIR_CMD := cmd /C mkdir
	COPY_CMD := cmd /C copy /Y
	MOVE_CMD := cmd /C move /Y
	COPY_DIR_CMD := cmd /C xcopy /E /I /Y
	CLEAN_DIR_CMD := cmd /C rmdir /S /Q
	NULL_DEV := nul
else
	BINARY_NAME := knowledge_server
	MCP_BRIDGE_NAME := mcp-bridge
	CLEAN_CMD := rm -f
	MKDIR_CMD := mkdir -p
	COPY_CMD := cp
	MOVE_CMD := mv
	COPY_DIR_CMD := cp -r
	CLEAN_DIR_CMD := rm -rf
	NULL_DEV := /dev/null
endif

.PHONY: all build build-mcp run clean dist

all: dist

build:
	go build -o $(BINARY_NAME) ./cmd/server

build-mcp:
	go build -o $(MCP_BRIDGE_NAME) ./tools/mcp-bridge

dist: build build-mcp
	-$(MKDIR_CMD) dist 2>$(NULL_DEV)
	$(MOVE_CMD) $(BINARY_NAME) dist\ 2>$(NULL_DEV) || $(MOVE_CMD) $(BINARY_NAME) dist/
	$(MOVE_CMD) $(MCP_BRIDGE_NAME) dist\ 2>$(NULL_DEV) || $(MOVE_CMD) $(MCP_BRIDGE_NAME) dist/
	$(COPY_DIR_CMD) rules dist\rules 2>$(NULL_DEV) || $(COPY_DIR_CMD) rules dist/rules
	$(COPY_CMD) sgconfig.yml dist\ 2>$(NULL_DEV) || $(COPY_CMD) sgconfig.yml dist/
	$(COPY_CMD) config_master.json dist\config.json 2>$(NULL_DEV) || $(COPY_CMD) config_master.json dist/config.json

run:
	go run ./cmd/server -- /run

clean:
	go clean
	-$(CLEAN_CMD) "$(BINARY_NAME)" "$(MCP_BRIDGE_NAME)" 2>$(NULL_DEV)
	-$(CLEAN_CMD) dist\$(BINARY_NAME) dist\$(MCP_BRIDGE_NAME) dist\config.json dist\sg.exe dist\surreal.exe 2>$(NULL_DEV) || $(CLEAN_CMD) dist/$(BINARY_NAME) dist/$(MCP_BRIDGE_NAME) dist/config.json dist/sg dist/surreal
	-$(CLEAN_DIR_CMD) dist\rules 2>$(NULL_DEV) || $(CLEAN_DIR_CMD) dist/rules

