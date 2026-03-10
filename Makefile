# Nome dell'eseguibile finale
!if "$(OS)" == "Windows_NT"
BINARY_NAME=knowledge_server.exe
MCP_BRIDGE_NAME=mcp-bridge.exe
!else
BINARY_NAME=knowledge_server
MCP_BRIDGE_NAME=mcp-bridge
!endif

build:
	go build -o $(BINARY_NAME) ./cmd/server

build-mcp:
	go build -o $(MCP_BRIDGE_NAME) ./tools/mcp-bridge

all: build build-mcp


run:
	go run ./cmd/server -- /run

clean:
	go clean
	-del /Q $(BINARY_NAME) $(MCP_BRIDGE_NAME) 2>nul
	-rm -f $(BINARY_NAME) $(MCP_BRIDGE_NAME) 2>nul

