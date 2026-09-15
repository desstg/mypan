.PHONY: lint lint-install test build build-nofuse docker-build docker-up docker-down docker-save docker-push

GOLANGCI_LINT_VERSION ?= v2.12.2
GOLANGCI_LINT ?= $(shell command -v golangci-lint 2>/dev/null || echo "$(shell go env GOPATH)/bin/golangci-lint")

DOCKER_IMAGE ?= litepan-go:dev
DOCKER_PLATFORM ?= linux/amd64
DOCKER_EXPORT ?= dist/$(DOCKER_IMAGE).tar.gz

# 发布到 Docker Hub
VERSION ?= v1.0.1
RELEASE_IMAGE ?= desstg/mypan
RELEASE_TAG ?= latest
RELEASE_PLATFORMS ?= linux/amd64,linux/arm64

lint:
	@GOWORK=off "$(GOLANGCI_LINT)" run -c .golangci.yml ./...

lint-install:
	@go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

test:
	@GOWORK=off go test -race ./...

build:
	@GOWORK=off go build -tags fuse ./...

build-nofuse:
	@GOWORK=off go build ./...

docker-build:
	@mkdir -p dist
	docker build --platform $(DOCKER_PLATFORM) \
		--build-arg VERSION=$(VERSION) \
		-t $(DOCKER_IMAGE) .

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down

docker-save: docker-build
	@mkdir -p dist
	docker save $(DOCKER_IMAGE) | gzip > $(DOCKER_EXPORT)

# 构建并推送到 Docker Hub。会同时打 latest 和版本号两个 tag。
#   单架构（群晖 x86 机型推荐，快很多）：
#     make docker-push RELEASE_PLATFORMS=linux/amd64
#   多架构（需要 buildx + QEMU binfmt，NAS 上构建 arm64 会比较慢）：
#     make docker-push
docker-push:
	docker buildx build --platform $(RELEASE_PLATFORMS) \
		--build-arg VERSION=$(VERSION) \
		-t $(RELEASE_IMAGE):$(RELEASE_TAG) \
		-t $(RELEASE_IMAGE):$(VERSION) \
		--push .
