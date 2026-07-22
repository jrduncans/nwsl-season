# Phase 2: ASA client

## Goal

Fetch one NWSL regular season from the American Soccer Analysis API and turn the
JSON response into Go values. Do not add SQLite yet.

API reference: [ASA `/nwsl/games`](https://app.americansocceranalysis.com/api/v1/__docs__/#/National%20Women's%20Soccer%20League%20(NWSL)//nwsl/games-GET).

## Planned shape

Create `internal/asa` with:

- A `Client` containing the base URL and an `http.Client`.
- A `Games` method accepting explicit filters such as season and stage.
- Response structs matching ASA's wire format.
- Errors that include operation and HTTP status, but never dump enormous bodies.

Inject `http.Client` or the transport rather than calling `http.Get`. Tests can
then use `httptest.Server` and remain offline.

## Suggested sequence

1. Save a small, representative API response in `internal/asa/testdata`.
2. Write a decoding test before writing the client.
3. Implement URL query construction for `season_name=YYYY` and
   `stage_name=Regular Season`.
4. Handle non-2xx responses and malformed JSON.
5. Add `cmd/sync` temporarily printing fetched game counts. It will gain database
   responsibilities in Phase 3.

Keep ASA DTOs separate from domain types. An API field can change without forcing
the rest of the application to speak ASA's vocabulary.

## Questions to explore

- Why should every outbound request have a context and timeout?
- What is the difference between a missing JSON field, `null`, and a zero value?
- Which game fields are stable identifiers, and which are display values?
- How are postponed, abandoned, and not-yet-played matches represented?

## Exit criteria

- Tests exercise success, non-2xx, and malformed JSON responses.
- A command can fetch a chosen season and report its game count.
- No live API call is required for `go test ./...`.
