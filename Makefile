BUILD_DIR := bin
SERVER_PACKAGE := ./cmd/server
SERVER_BINARY := nwsl-season-server
SYNC_PACKAGE := ./cmd/sync
SYNC_BINARY := nwsl-season-sync
BACKTEST_PACKAGE := ./cmd/backtest
BACKTEST_BINARY := nwsl-season-backtest
TARGET_OS ?= linux
TARGET_ARCH ?= arm64
GOLANGCI_LINT ?= mise exec -- golangci-lint
GOVULNCHECK ?= mise exec -- govulncheck
WEAVER ?= mise exec -- weaver
TELEMETRY_REGISTRY := ./telemetry/registry
TELEMETRY_TEMPLATES := ./telemetry/templates
TELEMETRY_DOCS := ./docs/telemetry/catalog
TELEMETRY_GO := ./internal/telemetry/nwslconv

.PHONY: verify test fmt vet lint race vuln telemetry-check-code telemetry-check telemetry-generate telemetry-check-generated telemetry-live-check backtest backfill-evaluation-data model-evaluation build build-server build-linux build-linux-server build-sync build-linux-sync build-backtest build-linux-backtest clean

verify: fmt lint vet test

test:
	go test ./...

fmt:
	$(GOLANGCI_LINT) fmt ./...

vet:
	go vet ./...

lint:
	$(GOLANGCI_LINT) run ./...

race:
	go test -race ./...

vuln:
	$(GOVULNCHECK) ./...

telemetry-check-code:
	sh ./telemetry/check-code-coverage.sh

telemetry-check: telemetry-check-code
	$(WEAVER) --future registry check -r $(TELEMETRY_REGISTRY)

telemetry-generate:
	$(WEAVER) registry generate -r $(TELEMETRY_REGISTRY) --templates $(TELEMETRY_TEMPLATES) markdown $(TELEMETRY_DOCS)
	$(WEAVER) registry generate -r $(TELEMETRY_REGISTRY) --templates $(TELEMETRY_TEMPLATES) go $(TELEMETRY_GO)
	gofmt -w $(TELEMETRY_GO)/*.go

telemetry-check-generated: telemetry-check
	@set -eu; \
	telemetry_generated_dir="$$(mktemp -d)"; \
	trap 'rm -r "$$telemetry_generated_dir"' EXIT; \
	mkdir -p "$$telemetry_generated_dir/docs" "$$telemetry_generated_dir/go"; \
	$(WEAVER) registry generate -r $(TELEMETRY_REGISTRY) --templates $(TELEMETRY_TEMPLATES) markdown "$$telemetry_generated_dir/docs"; \
	$(WEAVER) registry generate -r $(TELEMETRY_REGISTRY) --templates $(TELEMETRY_TEMPLATES) go "$$telemetry_generated_dir/go"; \
	gofmt -w "$$telemetry_generated_dir/go"/*.go; \
	diff -ru "$(TELEMETRY_DOCS)" "$$telemetry_generated_dir/docs"; \
	diff -ru "$(TELEMETRY_GO)" "$$telemetry_generated_dir/go"

telemetry-live-check:
	sh ./telemetry/live-check.sh

backtest:
	go run $(BACKTEST_PACKAGE)

# This is intentionally explicit: it contacts ASA in one sequential process and
# replaces only the supported historical regular-season snapshots.
backfill-evaluation-data:
	go run $(SYNC_PACKAGE) -backfill-historical

model-evaluation:
	$(MAKE) backfill-evaluation-data
	$(MAKE) backtest

build: build-server build-sync build-backtest

build-linux: build-linux-server build-linux-sync build-linux-backtest

build-server:
	mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(SERVER_BINARY) $(SERVER_PACKAGE)

build-linux-server:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) go build -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/$(SERVER_BINARY)-$(TARGET_OS)-$(TARGET_ARCH) $(SERVER_PACKAGE)

build-sync:
	mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(SYNC_BINARY) $(SYNC_PACKAGE)

build-linux-sync:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) go build -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/$(SYNC_BINARY)-$(TARGET_OS)-$(TARGET_ARCH) $(SYNC_PACKAGE)

build-backtest:
	mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BACKTEST_BINARY) $(BACKTEST_PACKAGE)

build-linux-backtest:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) go build -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/$(BACKTEST_BINARY)-$(TARGET_OS)-$(TARGET_ARCH) $(BACKTEST_PACKAGE)

clean:
	rm -rf $(BUILD_DIR)
