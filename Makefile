.PHONY: build test build-go build-cli run-controller run-apiserver help

# Build all Go services, webserver and CLI
build: build-go build-cli

build-go:
	@echo "Building Go Operator, API Server and Web Server..."
	go build -o bin/apiserver ./cmd/apiserver
	go build -o bin/controller ./cmd/controller
	go build -o bin/webserver ./cmd/webserver

build-cli:
	@echo "Building hpc CLI..."
	go build -o bin/hpc ./cmd/hpc-cli

test:
	go test ./...

run-apiserver: build-go
	./bin/apiserver -port 8090

run-controller: build-go
	./bin/controller

help:
	@echo "AILS Cloud-Native HPC System Build Command:"
	@echo "  make build         - Build apiserver, controller, webserver and hpc CLI"
	@echo "  make run-apiserver - Run Go API server locally"
	@echo "  make run-controller- Run Go Operator locally"
