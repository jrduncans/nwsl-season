# Telemetry registry

This directory contains the source OpenTelemetry semantic-convention registry
and the Weaver templates used to generate the project's searchable telemetry
catalog.

- `registry/manifest.yaml` pins the OpenTelemetry semantic-conventions
  dependency to 1.43.0, matching the version emitted by `otelhttp` and used by
  the application's telemetry package.
- `registry/*.yaml` defines the shared, HTTP, and application-domain contract.
  It uses Weaver's v1 registry model while the v2 definition language remains
  alpha.
- `templates/registry/markdown` generates domain-oriented checked-in reference
  pages under `docs/telemetry/catalog`.
- `templates/registry/go` generates the checked-in `internal/telemetry/nwslconv`
  package: signal names, attribute keys and helpers, and bounded enum values.
- `check-code-coverage.sh` prohibits production code from bypassing the
  generated package with raw application attributes, spans, or events.
- `live-check.sh` and `live-check-collector.yaml` run deterministic application
  telemetry through a development-only OTLP/HTTP-to-OTLP/gRPC bridge and grade
  it against the registry with Weaver Live Check.

Run `make telemetry-check`, `make telemetry-check-generated`, and
`make telemetry-live-check` before
committing a registry or generated-code change. See
[`docs/telemetry/README.md`](../docs/telemetry/README.md) for the developer
workflow, runtime-check port overrides, Collector image update procedure,
generated catalog, and hand-written Honeycomb query cookbook.
