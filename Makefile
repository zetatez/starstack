# StarStack build / dev / deploy helpers.
#
# Common targets:
#   make build        build the local Go binary into ./bin/starstack
#   make run          run the API locally (LISTEN=:8290 by default)
#   make web-dev      start the Vite dev server (proxies /api -> :8290)
#   make web-build    build the frontend into web/dist
#   make test         run Go tests + vet
#   make lint         run staticcheck (if installed); falls back to go vet
#   make fmt          gofmt the Go source, prettier-agnostic (see fmt-full)
#   make docker       build the Docker image
#   make up           docker compose up -d --build (deploy/)
#   make down         docker compose stop + remove (deploy/)
#   make clean        remove local build artifacts

SHELL            := /bin/sh
GO               ?= go
VERSION          ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
REGISTRY         ?=
IMAGE_NAME       ?= starstack
IMAGE            := $(if $(REGISTRY),$(REGISTRY)/,)$(IMAGE_NAME):$(VERSION)
COMPOSE_DIR      := deploy
COMPOSE          := docker compose -f $(COMPOSE_DIR)/docker-compose.yml

LDFLAGS          := -s -w -X main.version=$(VERSION)
BINDIR           := bin
API_LISTEN       ?= :8290

.PHONY: all build run web-dev web-build test lint fmt clean docker \
        up down logs ps image

all: build web-build        ## default: local binaries + frontend dist
	@echo "built backend and frontend"

## ---- backend ----

build:                   ## compile the Go server binary
	@mkdir -p $(BINDIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="$(LDFLAGS)" \
		-o $(BINDIR)/starstack ./cmd/starstack

run: build                ## run the API locally (LISTEN=:8290 default)
	LISTEN=$(API_LISTEN) $(BINDIR)/starstack

## ---- frontend ----

web-install:
	cd web && npm install --no-audit --no-fund

web-build:                ## build the frontend into web/dist
	cd web && npm run build

web-dev:                  ## vite dev server (proxy /api -> :8290)
	cd web && npm run dev

## ---- quality ----

test:                     ## run Go tests and vet
	$(GO) test ./...
	$(GO) vet ./...

lint:                     ## staticcheck if available, else go vet
	@if command -v staticcheck >/dev/null 2>&1; then \
		staticcheck ./...; \
	else \
		echo "staticcheck not found; running go vet"; \
		$(GO) vet ./...; \
	fi

fmt:                      ## gofmt the Go source
	gofmt -w $(shell find . -name '*.go' -not -path './web/node_modules/*')
	@echo "formatted"

## ---- docker / deploy ----

image:                    ## build the Docker image
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) -f $(COMPOSE_DIR)/Dockerfile .

docker: image
	@echo "image ready: $(IMAGE)"

up:                       ## build & start stack (deploy/docker-compose.yml)
	$(COMPOSE) up -d --build

down:                     ## stop & remove stack
	$(COMPOSE) down

logs:                     ## tail stack logs
	$(COMPOSE) logs -f --tail=100

ps:
	$(COMPOSE) ps

## ---- clean ----

clean:                    ## remove local build artifacts
	rm -rf $(BINDIR) web/dist web/node_modules
	@echo "cleaned"

help:                     ## show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'