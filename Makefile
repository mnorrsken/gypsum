APP_NAME := gypsum
CMD_PATH := ./cmd/wiki

.PHONY: help fmt tidy test build run clean docker-build docker-run

help:
	@echo "Targets:"
	@echo "  make fmt          - Format Go source"
	@echo "  make tidy         - Tidy Go modules"
	@echo "  make test         - Run unit tests"
	@echo "  make build        - Build binary to ./bin/gypsum"
	@echo "  make run          - Run wiki server locally on :8080"
	@echo "  make clean        - Remove build artifacts"
	@echo "  make docker-build - Build Docker image"
	@echo "  make docker-run   - Run Docker container on :8080"

fmt:
	gofmt -w ./cmd ./internal

tidy:
	go mod tidy

test:
	go test ./...

build:
	mkdir -p bin
	go build -o bin/$(APP_NAME) $(CMD_PATH)

run:
	go run $(CMD_PATH)

clean:
	rm -rf bin

docker-build:
	docker build -t $(APP_NAME):latest .

docker-run:
	docker run --rm -p 8080:8080 -v $(PWD)/data:/app/data $(APP_NAME):latest
