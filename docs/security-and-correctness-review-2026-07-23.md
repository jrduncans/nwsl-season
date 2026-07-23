# Security and correctness review — 2026-07-23

Status: **Open**

Reviewed commit: `a93edbc`

This document records the findings from a repository-wide security and
correctness review so they can be prioritized and addressed later. No fixes
were made as part of the review.

## Priority guide

- **P1 — High:** A practical availability or security risk that should be
  addressed before relying on the affected surface in a public deployment.
- **P2 — Medium:** A material correctness, integrity, or hardening issue that
  should be scheduled.
- **P3 — Low:** A defense-in-depth or malformed-input issue with a narrower
  threat model.

## Summary

| ID | Priority | Status | Finding |
| --- | --- | --- | --- |
| SEC-01 | P1 | Resolved | Forecast requests permit unauthenticated resource exhaustion |
| DB-01 | P2 | Open | SQLite safeguards apply to only one pooled connection |
| HTTP-01 | P2 | Open | The HTTP server has no connection deadlines |
| DB-02 | P2 | Open | A season response can mix different database snapshots |
| DATA-01 | P2 | Open | Self-fixtures can be committed as valid upstream data |
| DATA-02 | P3 | Open | Impossible expected-points values are accepted |
| HTTP-02 | P3 | Open | Successful upstream response bodies are unbounded |

## SEC-01: Bound Forecast Lab computation

Priority: **P1 — High**  
Status: **Resolved**

### Location

- [`internal/app/forecast_handler.go`](../internal/app/forecast_handler.go#L64-L78)
- [`internal/simulation/simulation.go`](../internal/simulation/simulation.go#L114-L131)

### Description

Every public request to Forecast Lab synchronously runs 50,000 complete-season
simulations. A model-comparison request performs that work twice. There is no
result cache, concurrency limit, application-level request deadline, or rate
limit.

The simulation does check request cancellation every 100 iterations, which
helps after a client disconnects, but it does not limit the number of active
requests or the resources a connected client can consume.

### Evidence

The existing production-sized benchmark was run once:

```text
go test ./internal/simulation \
  -run '^$' \
  -bench '^BenchmarkRun16TeamSeason$' \
  -benchtime=1x \
  -benchmem

BenchmarkRun16TeamSeason-12  1  936848708 ns/op  1774965000 B/op  7602848 allocs/op
```

On an Apple M4 Pro, one model required approximately:

- 0.94 seconds;
- 1.77 GB of cumulative allocations, not peak resident memory;
- 7.6 million allocations.

A comparison request invokes two simulations sequentially. A modest number of
concurrent requests can therefore saturate CPU and create substantial garbage
collector pressure.

### Impact

- Public availability can be degraded by a small request flood.
- Forecast traffic can interfere with page reads and the background scheduler.
- A reverse proxy alone does not prevent expensive requests that are otherwise
  syntactically valid.

### Recommended remediation

Use several layers rather than relying on only one:

1. Add a small process-wide concurrency semaphore around simulation work.
2. Return `429 Too Many Requests` or `503 Service Unavailable` when the
   simulation queue is full.
3. Give forecast computation an application-level context deadline.
4. Cache deterministic results by fixture snapshot, model, comparison model,
   and canonical fixed outcomes.
5. Consider proxy-level per-client rate limiting as an additional deployment
   control.
6. Profile and reduce per-iteration allocation, especially the repeated game
   and standings construction.

### Completion checks

- [x] Concurrent uncached forecast work is capped at a configurable
      process-wide limit (default: two); excess work receives `429` rather
      than joining a queue.
- [x] An uncached forecast has a configurable application-level deadline
      (default: 15 seconds).
- [x] Successful deterministic results are reused by fixture snapshot,
      simulator inputs, model, and canonical fixed outcomes. The in-memory
      cache is bounded to 128 entries.
- [x] Handler tests show saturated forecast capacity does not prevent
      `/healthz`, `/cache/status`, or ordinary season pages from responding.
- [x] Deadline and overload behavior have HTTP tests; request cancellation is
      propagated to the simulation context.

## DB-01: Configure every SQLite connection

Priority: **P2 — Medium**  
Status: **Open**

### Location

- [`internal/cache/cache.go`](../internal/cache/cache.go#L230-L240)
- [`internal/cache/cache.go`](../internal/cache/cache.go#L336-L344)
- [`internal/cache/cache.go`](../internal/cache/cache.go#L1131-L1166)

### Description

`PRAGMA foreign_keys` and `PRAGMA busy_timeout` are connection-scoped SQLite
settings. The current initialization executes each pragma once through
`*sql.DB`, which configures only the pooled connection selected for those
calls. Connections opened later retain SQLite defaults.

`journal_mode=WAL` is persistent database state, but the other two settings
must be applied to every connection.

### Evidence

A temporary reproduction using the repository's `modernc.org/sqlite` driver
configured a `sql.DB`, held its first connection, and then acquired a second
connection:

```text
connection 1: foreign_keys=1 busy_timeout=5000
connection 2: foreign_keys=0 busy_timeout=0
```

The temporary reproduction file was removed after the check.

### Impact

- `ON DELETE CASCADE` may not remove `game_xg` rows when a write transaction
  receives an unconfigured connection.
- Concurrent writers can fail immediately with `database is locked` instead of
  honoring the intended five-second wait.
- Future code may silently rely on foreign-key guarantees that are not actually
  enforced on every connection.

### Recommended remediation

Apply connection-scoped pragmas through the driver's documented per-connection
DSN options or a connection initialization hook. Restricting the pool to one
connection can also make initialization deterministic, but it reduces
concurrency and still needs a strategy if that connection is replaced.

### Completion checks

- [ ] Two simultaneously held `sql.Conn` values both report
      `foreign_keys=1`.
- [ ] Two simultaneously held `sql.Conn` values both report
      `busy_timeout=5000`.
- [ ] Deleting a game through a non-initial pooled connection cascades to
      `game_xg`.
- [ ] A concurrent-writer test confirms the configured busy timeout is active.

## HTTP-01: Add HTTP server deadlines

Priority: **P2 — Medium**  
Status: **Open**

### Location

- [`cmd/server/main.go`](../cmd/server/main.go#L71-L77)

### Description

The `http.Server` configures only `Addr` and `Handler`. Its header-read, read,
write, and idle deadlines therefore remain zero.

### Impact

When the listener is reachable directly, a slow client can retain connections
and goroutines for an unbounded period. A deployment proxy may impose its own
deadlines, but the application supports binding to non-loopback addresses and
should not depend exclusively on an unstated proxy configuration.

### Recommended remediation

Configure at least:

- `ReadHeaderTimeout`;
- an appropriate `WriteTimeout`;
- `IdleTimeout`;
- `MaxHeaderBytes`.

Choose the write deadline together with the Forecast Lab computation budget so
legitimate bounded forecasts can finish. An application-level forecast context
deadline is still required; a socket write deadline alone does not bound CPU
work before the response is written.

### Completion checks

- [ ] Server timeout values are explicit and documented.
- [ ] Slow-header connections are terminated.
- [ ] Idle keep-alive connections are bounded.
- [ ] A normal and comparison forecast can finish within the configured,
      bounded write window.
- [ ] `cmd/server` has tests for server construction and shutdown behavior.

## DB-02: Read a season as one database snapshot

Priority: **P2 — Medium**  
Status: **Open**

### Location

- [`internal/cache/cache.go`](../internal/cache/cache.go#L724-L751)
- [`internal/app/handler.go`](../internal/app/handler.go#L416-L421)

### Description

`DB.Season` loads teams, games, the last successful sync, xG rows, and xG status
with independent autocommit queries. Those queries do not share a SQLite read
snapshot.

If a fixture refresh commits after `seasonGames` but before `LastSuccess`, the
returned value can contain old games with the new successful run's freshness
and fixture snapshot ID. The handler then uses that ID to load qualification
results for the newer fixtures.

### Impact

During a refresh window, a response can:

- display a freshness time newer than its fixture data;
- display qualification badges proved against a different fixture state;
- combine team and game inventories from different committed states;
- expose transient and difficult-to-reproduce page inconsistencies.

### Recommended remediation

Run the compound read in one read transaction and make the query helpers accept
the transaction through a shared query interface. As an additional invariant,
recalculate the fixture snapshot ID from the loaded teams and games and confirm
that it matches the loaded successful run before returning it.

The xG refresh is intentionally independent of the fixture transaction. A
single read transaction will still return a coherent point-in-time view while
allowing the xG status to describe its independent freshness.

### Completion checks

- [ ] All fields in `SeasonData` are read from one SQLite snapshot.
- [ ] The returned fixture snapshot ID matches the returned teams and games.
- [ ] A concurrent refresh/read test cannot produce mismatched fixture hashes.
- [ ] Qualification badges are never loaded for a different game snapshot.

## DATA-01: Reject self-fixtures before replacement

Priority: **P2 — Medium**  
Status: **Open**

### Location

- [`internal/syncer/syncer.go`](../internal/syncer/syncer.go#L297-L337)
- [`internal/standings/standings.go`](../internal/standings/standings.go#L103-L117)

### Description

Fixture validation confirms that the home and away team IDs both exist, but it
does not require them to be different.

If both IDs refer to the same team, `standings.Calculate` resolves both indexes
to the same row and applies the result twice. A non-draw produces a win and a
loss for the same team; a draw produces two appearances and two draws.

### Impact

An upstream data error can be recorded as a successful replacement and corrupt
standings, strength-of-schedule calculations, forecasts, and fixture inventory
counts until a later correction.

### Recommended remediation

Reject any fixture for which `HomeTeamID == AwayTeamID` during sync validation,
before `ReplaceSeason` is called.

### Completion checks

- [ ] Sync validation rejects a self-fixture.
- [ ] The previous successful cache remains unchanged after rejection.
- [ ] The failed attempt is recorded in `sync_runs`.
- [ ] A direct standings unit test documents safe behavior if malformed domain
      input bypasses the sync layer.

## DATA-02: Bound game-level expected points

Priority: **P3 — Low**  
Status: **Open**

### Location

- [`internal/syncer/syncer.go`](../internal/syncer/syncer.go#L392-L419)
- [`internal/cache/cache.go`](../internal/cache/cache.go#L792-L797)
- [`internal/app/views.go`](../internal/app/views.go#L302-L326)

### Description

Expected-points fields are checked only for finiteness and non-negativity.
Game-level expected points for one team must also be no greater than the three
points available from a match.

An upstream value such as `10` currently passes both normalization and cache
validation, is persisted, and directly affects xPts totals and ordering.

### Impact

A malformed upstream xG response can make the xPts view materially incorrect
while the xG refresh is recorded as successful.

### Recommended remediation

Require both values to be in the inclusive range `[0, 3]` in the sync mapping
and retain the same invariant at the cache boundary. If appropriate to ASA's
definition, also validate the documented relationship between the two teams'
expected points while allowing a small tolerance for rounding.

### Completion checks

- [ ] Values below zero, above three, NaN, and infinities are rejected.
- [ ] Boundary values zero and three are accepted.
- [ ] A failed malformed xG refresh preserves the last good xG snapshot.
- [ ] The expected-points domain constraint is documented with its ASA meaning.

## HTTP-02: Limit upstream success bodies

Priority: **P3 — Low**  
Status: **Open**

### Location

- [`internal/asa/client.go`](../internal/asa/client.go#L193-L244)
- [`internal/asa/client.go`](../internal/asa/client.go#L270-L284)

### Description

Successful ASA responses are decoded without a byte limit or a plausible row
limit. The decoders first materialize all objects as `json.RawMessage` and then
copy every raw object into a string, increasing memory use.

The HTTP client timeout bounds elapsed time, not bytes. A fast oversized
response can consume excessive memory before the timeout expires.

The decoder also accepts a valid top-level JSON value followed by trailing
content because it does not verify end-of-input.

### Impact

An erroneous or compromised upstream endpoint can cause memory exhaustion
during a scheduled or operator-triggered refresh. This has a narrower threat
model than the public Forecast Lab issue because ordinary visitors cannot
choose the production ASA endpoint.

### Recommended remediation

1. Wrap success bodies in an explicit byte-limited reader.
2. Set conservative row-count limits based on the endpoint and expected league
   size.
3. Decode one top-level value and require EOF afterward.
4. Return a clear validation error when either cap is exceeded.
5. Continue preserving the last good cache on rejection.

Choose limits from observed payload sizes with enough headroom for future
seasons rather than using the current response size exactly.

### Completion checks

- [ ] Oversized team, game, and xG bodies are rejected.
- [ ] Excessive array lengths are rejected.
- [ ] Trailing JSON or non-whitespace content is rejected.
- [ ] Rejection records a failure without replacing cached data.
- [ ] Normal checked-in fixtures remain comfortably below the selected limits.

## Verification completed during the review

The following checks passed:

```text
go test ./...
go vet ./...
go test -race ./...
go test -shuffle=on -count=20 ./...
gofmt -l <all Go files>
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

Results:

- All ordinary tests passed.
- All race-enabled tests passed.
- Twenty shuffled test runs passed.
- `go vet` reported no findings.
- All Go files were formatted.
- The official Go vulnerability scanner found no reachable dependency
  vulnerabilities.

## Test-coverage risks

Coverage is not itself a defect, but these low-coverage areas overlap important
initialization and orchestration boundaries:

| Package | Statement coverage |
| --- | ---: |
| `cmd/server` | 0.0% |
| `internal/forecast` | 23.0% |
| `internal/qualification` | 10.0% |
| `internal/scenariorefresh` | 3.5% |
| `internal/fixtures` | 0.0% |
| `internal/operations` | 0.0% |

When addressing the findings above, prioritize regression tests at the server,
database-pool, and background-refresh boundaries instead of relying only on
package-level algorithm tests.

## Suggested order of work

1. **SEC-01:** Protect the public forecast endpoint from resource exhaustion.
2. **DB-01:** Restore SQLite connection invariants across the pool.
3. **HTTP-01:** Add explicit server deadlines.
4. **DB-02:** Make season reads snapshot-consistent.
5. **DATA-01:** Reject self-fixtures.
6. **DATA-02:** Enforce expected-points bounds.
7. **HTTP-02:** Add upstream response limits and strict EOF validation.
