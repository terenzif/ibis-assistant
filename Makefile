# Nome dell'eseguibile finale
BINARY_NAME=knowledge_server.exe

build:
	go build -o $(BINARY_NAME) ./cmd/server

run:
	go run ./cmd/server -- /run

clean:
	go clean
	rm -f $(BINARY_NAME)