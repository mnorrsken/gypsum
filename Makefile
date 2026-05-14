APP_NAME := gypsum
CMD_PATH := ./cmd/wiki
REGISTRY ?= ghcr.io/mnorrsken/gypsum
TAG      ?= latest
IMAGE    := $(REGISTRY):$(TAG)

HELM_RELEASE := gypsum
HELM_CHART   := ./charts/gypsum
HELM_NS      := gypsum

HTMX_VERSION   := 2.0.4
ALPINE_VERSION := 3.14.9
STATIC_DIR     := web/static

.PHONY: help fmt tidy vet test build run clean docker-build docker-run deploy vendor-js

help:
	@echo "Targets:"
	@echo "  make fmt                        - Format Go source"
	@echo "  make tidy                       - Tidy Go modules"
	@echo "  make vet                        - Run go vet static analysis"
	@echo "  make test                       - Run unit tests"
	@echo "  make build                      - Build binaries to ./bin/"
	@echo "  make run                        - Run wiki server locally on :8080"
	@echo "  make clean                      - Remove build artifacts"
	@echo "  make docker-build               - Build Docker image (REGISTRY=... TAG=...)"
	@echo "  make docker-run                 - Run Docker container on :8080"
	@echo "  make deploy [REGISTRY=...] [TAG=...] - Build, push, and helm upgrade"
	@echo "  make vendor-js                  - Download htmx and Alpine.js"

fmt:
	gofmt -w ./cmd ./internal

tidy:
	go mod tidy

vet:
	go vet ./...

test:
	go test ./...

build:
	mkdir -p bin
	go build -o bin/$(APP_NAME) $(CMD_PATH)
	go build -o bin/mcp-proxy ./cmd/mcp-proxy
	GOOS=windows GOARCH=amd64 go build -o bin/$(APP_NAME).exe $(CMD_PATH)
	GOOS=windows GOARCH=amd64 go build -o bin/mcp-proxy.exe ./cmd/mcp-proxy

run:
	go run $(CMD_PATH)

clean:
	rm -rf bin

docker-build:
	docker build -t $(IMAGE) .

docker-run:
	docker run --rm -p 8080:8080 -v $(PWD)/data:/app/data $(IMAGE)

vendor-js:
	curl -sL https://unpkg.com/htmx.org@$(HTMX_VERSION)/dist/htmx.min.js -o $(STATIC_DIR)/htmx.min.js
	curl -sL https://unpkg.com/alpinejs@$(ALPINE_VERSION)/dist/cdn.min.js -o $(STATIC_DIR)/alpine.min.js

deploy:
#	docker build -t $(IMAGE) .
#	docker push $(IMAGE)
	helm upgrade --install $(HELM_RELEASE) $(HELM_CHART) \
		-n $(HELM_NS) \
		-f values.yaml \
		--set image.repository=$(REGISTRY) \
		--set image.tag=$(TAG)
