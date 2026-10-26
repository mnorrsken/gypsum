APP_NAME := gypsum
CMD_PATH := ./cmd/wiki
REGISTRY ?= ghcr.io/mnorrsken/gypsum
TAG      ?= latest
IMAGE    := $(REGISTRY):$(TAG)

# Strip the symbol table (-s) and DWARF debug info (-w); -trimpath drops
# absolute build paths from the binary. Shaves ~30 % off binary size with no
# runtime impact (panic stack traces still resolve via .gopclntab).
GO_LDFLAGS := -s -w
GO_BUILD   := go build -trimpath -ldflags="$(GO_LDFLAGS)"

HELM_RELEASE := gypsum
HELM_CHART   := ./charts/gypsum
HELM_NS      := gypsum

HTMX_VERSION    := 2.0.4
ALPINE_VERSION  := 3.14.9
MERMAID_VERSION := 11.12.0
LUCIDE_VERSION  := 1.50.0
# Lucide icons bundled into web/static/icons.svg (one <symbol> per icon).
ICONS := menu search camera lock lock-open sun moon house file-text files clock \
	network sticky-note key-round plus book-open graduation-cap star pencil \
	share-2 history x chevron-right upload trash-2 image folder-open ellipsis \
	eye copy check archive archive-restore
STATIC_DIR      := web/static

.PHONY: help fmt tidy vet vuln test ui-test build run clean docker-build docker-run deploy vendor-js vendor-icons

help:
	@echo "Targets:"
	@echo "  make fmt                        - Format Go source"
	@echo "  make tidy                       - Tidy Go modules"
	@echo "  make vet                        - Run go vet static analysis"
	@echo "  make vuln                       - Run govulncheck"
	@echo "  make test                       - Run unit tests"
	@echo "  make ui-test                    - Run browser tests in headless Chrome (UI_SCREENSHOTS=dir saves screenshots)"
	@echo "  make build                      - Build binaries to ./bin/"
	@echo "  make run                        - Run wiki server locally on :8080"
	@echo "  make clean                      - Remove build artifacts"
	@echo "  make docker-build               - Build Docker image (REGISTRY=... TAG=...)"
	@echo "  make docker-run                 - Run Docker container on :8080"
	@echo "  make deploy [REGISTRY=...] [TAG=...] - Build, push, and helm upgrade"
	@echo "  make vendor-js                  - Download htmx, Alpine.js and Mermaid"
	@echo "  make vendor-icons               - Rebuild the Lucide icon sprite (web/static/icons.svg)"

fmt:
	gofmt -w ./cmd ./internal

tidy:
	go mod tidy

vet:
	go vet ./...

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

test:
	go test ./...

ui-test:
	go test -tags uitest -count=1 ./internal/uitest/

build:
	mkdir -p bin
	$(GO_BUILD) -o bin/$(APP_NAME) $(CMD_PATH)
	$(GO_BUILD) -o bin/mcp-proxy ./cmd/mcp-proxy
	GOOS=windows GOARCH=amd64 $(GO_BUILD) -o bin/$(APP_NAME).exe $(CMD_PATH)
	GOOS=windows GOARCH=amd64 $(GO_BUILD) -o bin/mcp-proxy.exe ./cmd/mcp-proxy

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
	curl -sL https://unpkg.com/mermaid@$(MERMAID_VERSION)/dist/mermaid.min.js -o $(STATIC_DIR)/mermaid.min.js

# Each Lucide SVG becomes a <symbol>; stroke styling comes from the .icon CSS
# class, so the attributes on the original <svg> element are dropped.
vendor-icons:
	@{ \
	  echo '<svg xmlns="http://www.w3.org/2000/svg">'; \
	  echo '<!-- Lucide icons v$(LUCIDE_VERSION) (https://lucide.dev), ISC License.'; \
	  echo '     Copyright (c) Lucide Contributors 2022; portions copyright (c) Cole Bemis 2013-2022 (Feather, MIT). -->'; \
	  for name in $(ICONS); do \
	    curl -sfL https://unpkg.com/lucide-static@$(LUCIDE_VERSION)/icons/$$name.svg \
	      | awk -v id=$$name 'body && /<\/svg>/ { exit } body { print } /^>/ && !body { body = 1; print "<symbol id=\"" id "\" viewBox=\"0 0 24 24\">" }' \
	      || exit 1; \
	    echo '</symbol>'; \
	  done; \
	  echo '</svg>'; \
	} > $(STATIC_DIR)/icons.svg

deploy:
#	docker build -t $(IMAGE) .
#	docker push $(IMAGE)
	helm upgrade --install $(HELM_RELEASE) $(HELM_CHART) \
		-n $(HELM_NS) \
		-f values.yaml \
		--set image.repository=$(REGISTRY) \
		--set image.tag=$(TAG)
