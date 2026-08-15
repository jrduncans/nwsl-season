# Telemetry catalog

The source-controlled telemetry catalog is defined in
[`telemetry/registry`](../../telemetry/registry) using OpenTelemetry semantic
conventions and validated with OpenTelemetry Weaver. It is the intended
contract for telemetry names, types, and meanings; the Go instrumentation is
the implementation of that contract.

The [generated catalog](catalog/README.md) currently covers the shared
resource and error attributes, HTTP client and server spans, and optional HTTP
metrics. Sync, scheduler, cache, qualification, scenario, and forecast domains
will be added in subsequent slices.

## Developer workflow

Install the Weaver version recorded in `.weaver-version`, then run:

```sh
make telemetry-check
make telemetry-generate
make telemetry-check-generated
```

`telemetry-check` validates the registry with Weaver's future-facing rules.
`telemetry-generate` refreshes the checked-in Markdown reference.
`telemetry-check-generated` is the CI drift check and fails when generation
changes a tracked catalog file.

Both registry validation and generation download the pinned OpenTelemetry
semantic-conventions 1.43.0 dependency, so they require network access until a
future slice packages or vendors that dependency.

Do not edit files under `docs/telemetry/catalog` directly. Update the registry
or templates and regenerate them instead.
