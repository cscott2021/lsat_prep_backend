.PHONY: run build test clean

run:
	go run ./cmd/server

build:
	go build -o bin/server ./cmd/server

test:
	go test ./...

clean:
	rm -rf bin/

deps:
	go mod tidy
	go mod download
