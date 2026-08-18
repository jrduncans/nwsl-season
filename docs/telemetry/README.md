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
make telemetry-live-check
```

`telemetry-check-code` verifies that production instrumentation uses the
generated package instead of raw application attribute or signal names.
`telemetry-check` runs that enforcement check and validates the registry with
Weaver's future-facing rules.
`telemetry-generate` refreshes the checked-in Markdown reference and the Go
conventions package under `internal/telemetry/nwslconv`.
`telemetry-check-generated` is the CI drift check and fails when either
generated artifact differs from the checked-in result.
`telemetry-live-check` starts Weaver and a pinned, development-only
OpenTelemetry Collector in Docker, then drives a deterministic fake-ASA sync
and local-SQLite page request through the application's real OTLP/HTTP
exporters. The Collector bridges those signals to Weaver's OTLP/gRPC listener,
and the target fails on contract violations or when Weaver receives no trace
or metric samples. It does not need Honeycomb credentials or contact ASA.

The Make targets invoke Weaver through Mise, which installs and runs the
version declared in [`mise.toml`](../../mise.toml). Do not install or version
Weaver separately for this repository.

Finite aggregate fields, including qualification and scenario result counts,
use explicit generated helpers. Dynamic construction of application attribute
keys is not permitted.

Both registry validation and generation download the pinned OpenTelemetry
semantic-conventions 1.43.0 dependency, so they require network access until a
future slice packages or vendors that dependency.

The runtime check additionally requires Docker. Its Collector image is pinned
by version and multi-platform digest in `telemetry/live-check.sh`. The bridge
exists only for local validation and CI; it is not part of the production or
Honeycomb export path.

### Runtime-check ports

The runtime check binds three loopback ports. Another process, or a second
runtime check using the same values, can prevent it from starting:

| Default | Purpose | Override |
| --- | --- | --- |
| `14317` | Weaver OTLP/gRPC receiver | `WEAVER_LIVE_CHECK_OTLP_PORT` |
| `14318` | Collector OTLP/HTTP receiver | `WEAVER_LIVE_CHECK_HTTP_PORT` |
| `14320` | Weaver health and stop endpoint | `WEAVER_LIVE_CHECK_ADMIN_PORT` |

On Linux, the Collector uses Docker host networking so it can reach Weaver's
loopback-only gRPC listener. On Docker Desktop, the script publishes the
Collector receiver on loopback and reaches Weaver through
`host.docker.internal`. The three host-side ports and their overrides are the
same in both modes.

All three values must be distinct integers from 1 through 65535. The script
rejects invalid or duplicate values before starting either process. If a valid
port is already occupied, Weaver or Docker will fail during startup and the
runtime check will report the relevant logs.

Use a separate port triplet for each parallel run, or to avoid a local
conflict:

```sh
WEAVER_LIVE_CHECK_OTLP_PORT=15317 \
WEAVER_LIVE_CHECK_HTTP_PORT=15318 \
WEAVER_LIVE_CHECK_ADMIN_PORT=15320 \
make telemetry-live-check
```

### Updating the Collector image

The Collector reference is intentionally a tag plus a multi-platform digest so
local and CI checks run the same artifact. No dependency bot currently watches
the shell variable, so this update is manual. Review the pin when upgrading
Weaver, when an applicable Collector security or reliability fix is released,
when OTLP compatibility requires it, and periodically during dependency
maintenance.

1. Review the [official Collector releases](https://github.com/open-telemetry/opentelemetry-collector-releases/releases)
   and their notes. Pay particular attention to OTLP receiver/exporter and
   configuration changes. The Docker tag is the release tag without its
   leading `v`.
2. Resolve the tag to its multi-platform manifest digest. This deliberately
   avoids a platform-specific digest:

   ```sh
   collector_version=0.159.0
   collector_digest=$(docker buildx imagetools inspect \
     "otel/opentelemetry-collector:${collector_version}" \
     --format '{{json .Manifest}}' | jq -r '.digest')
   printf '%s@%s\n' \
     "otel/opentelemetry-collector:${collector_version}" \
     "$collector_digest"
   ```

3. Update only `collector_image` near the top of
   `telemetry/live-check.sh`, keeping the human-readable tag and resolved
   digest together. Do not replace the digest with one copied from a local
   single-platform image.
4. Keep the bridge's `compression: none` setting unless the pinned Weaver
   version is confirmed to accept the replacement. Then verify the update:

   ```sh
   make telemetry-live-check
   make telemetry-check-generated
   shellcheck telemetry/live-check.sh
   ```

If the update also requires a Weaver change, update its pin in `mise.toml`
separately so review and failures can distinguish the two dependencies.

Do not edit files under `docs/telemetry/catalog` or
`internal/telemetry/nwslconv` directly. Update the registry or templates and
regenerate them instead. The query cookbook is intentionally maintained by
hand; update it when investigation workflows or emitted behavior change.
