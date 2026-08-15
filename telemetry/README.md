# Telemetry registry

This directory contains the source OpenTelemetry semantic-convention registry
and the Weaver templates used to generate the project's searchable telemetry
catalog.

- `registry/manifest.yaml` pins the OpenTelemetry semantic-conventions
  dependency to 1.43.0, matching the version emitted by `otelhttp` and used by
  the application's telemetry package.
- `registry/*.yaml` defines the application contract. It uses Weaver's v1
  registry model while the v2 definition language remains alpha.
- `templates/registry/markdown` generates the checked-in reference under
  `docs/telemetry/catalog`.

Run `make telemetry-check-generated` before committing a catalog change. See
[`docs/telemetry/README.md`](../docs/telemetry/README.md) for the developer
workflow and current coverage.
