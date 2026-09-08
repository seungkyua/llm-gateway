# llm-gateway build/test/docker targets.
#
# Usage:
#   make build OS=linux ARCH=amd64
#   make test
#   make docker

BINARY_NAME    := llm-gateway
CMD_DIR        := ./cmd/llm-gateway
BUILD_DIR      := ./bin

# Cross-compile target platform; defaults to the host platform.
OS   ?= $(shell go env GOOS)
ARCH ?= $(shell go env GOARCH)

DOCKER_IMAGE     ?= llm-gateway
DOCKER_PLATFORMS ?= linux/amd64,linux/arm64

.PHONY: build test vet docker clean

build:
	CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) go build -o $(BUILD_DIR)/$(BINARY_NAME)-$(OS)-$(ARCH) $(CMD_DIR)

test:
	go test ./... -count=1

vet:
	go vet ./...

docker:
	docker buildx build --platform $(DOCKER_PLATFORMS) -t $(DOCKER_IMAGE) .

clean:
	rm -rf $(BUILD_DIR)
