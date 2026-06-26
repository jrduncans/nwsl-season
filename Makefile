BUILD_DIR := bin
SERVER_PACKAGE := ./cmd/server
SERVER_BINARY := nwsl-season-server

TARGET_OS ?= linux
TARGET_ARCH ?= arm64

.PHONY: test fmt vet build-server build-linux-server clean

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

build-server:
	mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(SERVER_BINARY) $(SERVER_PACKAGE)

build-linux-server:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) go build -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/$(SERVER_BINARY)-$(TARGET_OS)-$(TARGET_ARCH) $(SERVER_PACKAGE)

clean:
	rm -rf $(BUILD_DIR)
