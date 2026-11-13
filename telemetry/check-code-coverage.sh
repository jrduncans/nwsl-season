#!/bin/sh

set -eu

repository_root=$(
	unset CDPATH
	cd -- "$(dirname -- "$0")/.."
	pwd
)

cd "$repository_root"

# Searches production Go: no tests and no generated conventions.
rg_production() {
	rg --glob='*.go' --glob='!**/*_test.go' --glob='!internal/telemetry/nwslconv/**' "$@"
}

if matches=$(rg_production \
	'attribute\.(String|Int|Int64|Bool|Float64|StringSlice|Key)\("nwsl\.' \
	cmd internal); then
	echo "raw nwsl.* attribute constructors are prohibited outside the generated package:"
	printf '%s\n' "$matches"
	exit 1
fi

# Domain packages may retain attribute.KeyValue as a transport type, but all
# constructors belong in nwslconv. telemetry.go is the sole exception for
# standard OTel resource and exception attributes.
if matches=$(rg_production --glob='!internal/telemetry/telemetry.go' \
	'attribute\.(String|Int|Int64|Bool|Float64|StringSlice|Key)\(' \
	cmd internal); then
	echo "OpenTelemetry attribute constructors outside telemetry plumbing and generated conventions are prohibited:"
	printf '%s\n' "$matches"
	exit 1
fi

# Keep tracer creation behind the application's telemetry package so the AST
# contract below can recognize every application-created span.
if matches=$(rg_production --glob='!internal/telemetry/telemetry.go' \
	'"go\.opentelemetry\.io/otel"' cmd internal); then
	echo "direct OpenTelemetry tracer access is prohibited outside telemetry plumbing:"
	printf '%s\n' "$matches"
	exit 1
fi

# Parse production call sites so multiline calls, new domains, and new signal
# names cannot bypass the registry by falling outside a hand-maintained regex.
go test ./internal/telemetrycontract \
	-run '^TestProductionTelemetryUsesGeneratedConventions$' \
	-count=1

echo "production Go instrumentation uses generated telemetry conventions"
