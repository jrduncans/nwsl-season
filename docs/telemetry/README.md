# Telemetry catalog

The source-controlled telemetry catalog is defined in
[`telemetry/registry`](../../telemetry/registry) using OpenTelemetry semantic
conventions and validated with OpenTelemetry Weaver. It is the intended
contract for telemetry names, types, and meanings; the Go instrumentation is
the implementation of that contract.

The [generated catalog](catalog/README.md) keeps each domain's attributes beside
its HTTP metrics, spans, and events. It covers shared resources and errors,
HTTP, cache, forecast, qualification, scenarios, scheduling, and
synchronization. The hand-written [Honeycomb query cookbook](honeycomb-query-cookbook.md)
turns that contract into investigation recipes without coupling the registry to
one observability product.

## Developer workflow

Install [Mise](https://mise.jdx.dev/), then run:

```sh
make telemetry-check-code
make telemetry-check
make telemetry-generate
make telemetry-check-generated
```

`telemetry-check-code` verifies that literal application telemetry emitted by
Go is represented in the registry. `telemetry-check` runs that coverage check
and validates the registry with Weaver's future-facing rules.
`telemetry-generate` refreshes the checked-in Markdown reference.
`telemetry-check-generated` is the CI drift check and fails when generation
changes a tracked catalog file.

The Make targets invoke Weaver through Mise, which installs and runs the
version declared in [`mise.toml`](../../mise.toml). Do not install or version
Weaver separately for this repository.

The code coverage check deliberately ignores attribute literals ending in a
dot because those are prefixes completed from finite code values. Their
concrete names, such as qualification result-count fields, must still be listed
explicitly in the registry so they remain searchable.

Both registry validation and generation download the pinned OpenTelemetry
semantic-conventions 1.43.0 dependency, so they require network access until a
future slice packages or vendors that dependency.

Do not edit files under `docs/telemetry/catalog` directly. Update the registry
or templates and regenerate them instead. The query cookbook is intentionally
maintained by hand; update it when investigation workflows or emitted behavior
change.
