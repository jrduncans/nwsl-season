BUILD_DIR := bin
SERVER_PACKAGE := ./cmd/server
SERVER_BINARY := nwsl-season-server
SYNC_PACKAGE := ./cmd/sync
SYNC_BINARY := nwsl-season-sync
BACKTEST_PACKAGE := ./cmd/backtest
BACKTEST_BINARY := nwsl-season-backtest
EVALUATION_SEASONS := 2016 2017 2018 2019 2021 2022 2023 2024 2025

TARGET_OS ?= linux
TARGET_ARCH ?= arm64

.PHONY: test fmt vet backtest backfill-evaluation-data model-evaluation build build-server build-linux build-linux-server build-sync build-linux-sync build-backtest build-linux-backtest clean

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

backtest:
	go run $(BACKTEST_PACKAGE)

# This is intentionally explicit: it contacts ASA and replaces each historical
# cache snapshot before the evaluator is allowed to replace evidence artifacts.
backfill-evaluation-data:
	@for season in $(EVALUATION_SEASONS); do \
		go run $(SYNC_PACKAGE) -season $$season -stage "Regular Season" -force -require-xg || exit $$?; \
	done

model-evaluation:
	$(MAKE) backfill-evaluation-data
	$(MAKE) backtest

build: build-server build-sync build-backtest

build-linux: build-linux-server build-linux-sync build-linux-backtest

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

build-backtest:
	mkdir -p $(BUILD_DIR)
	go build -buildvcs=false -o $(BUILD_DIR)/$(BACKTEST_BINARY) $(BACKTEST_PACKAGE)

build-linux-backtest:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) go build -buildvcs=false -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/$(BACKTEST_BINARY)-$(TARGET_OS)-$(TARGET_ARCH) $(BACKTEST_PACKAGE)

clean:
	rm -rf $(BUILD_DIR)
