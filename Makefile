# Nome dell'eseguibile finale
BINARY_NAME=knowledge_server
ifeq ($(OS),Windows_NT)
    BINARY_NAME=knowledge_server.exe
endif

build:
	go build -o $(BINARY_NAME) ./cmd/server

run:
	go run ./cmd/server -- /run

clean:
	go clean
	rm -f knowledge_server knowledge_server.exe
