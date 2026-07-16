BUILD_DIR := bin
SERVER_PACKAGE := ./cmd/server
SERVER_BINARY := nwsl-season-server
SYNC_PACKAGE := ./cmd/sync
SYNC_BINARY := nwsl-season-sync

TARGET_OS ?= linux
TARGET_ARCH ?= arm64

.PHONY: test fmt vet build build-server build-linux build-linux-server build-sync build-linux-sync clean

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

build: build-server build-sync

build-linux: build-linux-server build-linux-sync

build-server:
	mkdir -p $(BUILD_DIR)
	go build -buildvcs=false -o $(BUILD_DIR)/$(SERVER_BINARY) $(SERVER_PACKAGE)

build-linux-server:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) go build -buildvcs=false -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/$(SERVER_BINARY)-$(TARGET_OS)-$(TARGET_ARCH) $(SERVER_PACKAGE)

build-sync:
	mkdir -p $(BUILD_DIR)
	go build -buildvcs=false -o $(BUILD_DIR)/$(SYNC_BINARY) $(SYNC_PACKAGE)

build-linux-sync:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) go build -buildvcs=false -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/$(SYNC_BINARY)-$(TARGET_OS)-$(TARGET_ARCH) $(SYNC_PACKAGE)

clean:
	rm -rf $(BUILD_DIR)
