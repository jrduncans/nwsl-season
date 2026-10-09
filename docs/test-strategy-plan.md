# Test strategy plan

Status: proposed (2026-10-08). This plan adds browser and end-to-end tests with
`playwright-go`, closes the gaps found in the October 2026 test assessment, and
assigns each task to the cheapest model that can finish it reliably.

Each task below is self-contained, so you can start each one in a fresh
session. Read **Conventions for every task** (§5) before starting any task.
§6 explains how to run the plan.

## 1. Where the suite stands

Measured on `main` at the time of writing:

- 78 test files and 635 `Test*` functions. Statement coverage is 86.4% across
  all packages (`go test -coverpkg=./... ./...`). The full suite takes about
  55s cold, most of it compilation. `go test -race ./...` is clean.
- **Strengths:** the proof engine is checked against independent answers
  (`TestCutoffDecisionMatchesMaximumOracle`,
  `TestForcedPointsEliminationMatchesExhaustiveCompletions`). Tests use a real
  SQLite cache instead of mocks, and sync invariants are tested directly (for
  example `TestRunRejectsIncompleteKnownScheduleAndPreservesExistingRows`).
- **Gaps this plan closes:**
  1. About 2,400 lines of JavaScript (`internal/app/static/explore.js`,
     `standings.js`) have no automated tests. Browser behavior depends on a
     manual checklist in `docs/history-logic-guide.md` ("For browser
     verification…"). That checklist is driven by `TestHistoryPreview`, which is
     gated by an environment variable, returns without reporting a skip, and
     writes to a macOS-only `/private/tmp` path.
  2. The rule against publishing qualification indicators from an incomplete
     inventory is tested only as a predicate (`completeInventory`). The branch
     that turns it into unresolved rows
     (`internal/qualification/refresher.go`, the `unresolvedRows` returns) never
     runs in a test. The no-help batch path and
     `RecordQualificationFailure` / `RecordScenarioFailure` have 0% coverage.
  3. CI runs only `vet`, `test-clinching` and `test`. It skips the race suite
     and `govulncheck`, both of which `AGENTS.md` requires. The
     `b.Fatalf("compute-budget …")` checks in the clinching and scenario
     benchmarks never run in CI.
  4. `make test-clinching` leaves out `./internal/clinching` and
     `./internal/qualification`.
  5. The same feature lists are copied into `AGENTS.md`, Makefile comments, a
     roughly 600-character CI step name, and the History guide.
  6. `internal/app` tests check rendered HTML with 270 `strings.Contains`
     calls. These pass when text appears in the wrong place.
  7. Smaller: `internal/scheduleload` has no tests of its own. There are no
     fuzz tests (`forecaststate.ParseV2` parses URL state). Two exported
     functions have no callers (`cache.(*DB).RefreshSnapshot`,
     `forecast.NewResultsPoissonHomeHistoryV1`). Only `internal/app` is kept
     from importing `internal/asa`, and only because nothing imports it today;
     no rule enforces it.

## 2. Target test layers

| Layer | Owns | Size | Runs |
| --- | --- | --- | --- |
| Go unit/integration (unchanged) | Proof engine and brute-force checks, sync/cache invariants, forecasts, Explore calculations | Thousands | Every PR (`make test`) |
| Go HTTP contract (trimmed) | Escaping, relative links and proxy prefix, headers/CSP, status codes, no-script fallbacks, query validation, cache-only fallback | Fewer, scoped to one page section | Every PR |
| JS unit, run in the browser | Pure chart geometry: bounds, point sizing, logo collisions, Outlier sizing | Table-driven | Every PR (`make test-e2e`) |
| Browser tests on a pre-filled cache | Interactions, URL state and Back/Forward, keyboard, 1280px and 390px, no console or CSP errors | About 30–60 | Every PR (`make test-e2e`) |
| Full E2E with a fake ASA | About 6 journeys from sync to page | Few | Every PR while `make test-e2e` stays under about 4 min, otherwise on `main` and nightly |

The rule for choosing a layer: an assertion about what the server sends stays
a Go test. An assertion about what a user sees after JavaScript runs becomes a
browser test. Domain logic never moves into browser tests.

## 3. Choosing models

API prices, used here as a stand-in for subscription usage (input/output per
million tokens): Haiku 5.5 $0.10/$0.50, Sonnet 5.5 $2/$10, Opus 5.5 $4/$20,
Fable 5.1 $10/$50. Relative to Sonnet, that is about 1/20 for Haiku, 2× for
Opus and 5× for Fable.

| Model | Use for | Tasks |
| --- | --- | --- |
| **Haiku 5.5** | Mechanical edits that commands can verify: Makefile, workflow YAML, linter config | T1 |
| **Sonnet 5.5** (default) | Well-specified implementation, including the browser harness once T4 provides the server seam; test writing; docs | T2, T3, T5, T6, T7, T8, T9, T10, T11, T12 |
| **Opus 5.5** | Cross-package architecture, lifecycle and concurrency, and reviewing integration-sensitive diffs | T4; reviews of T2, T4, T6, T7, T8, T9, T10, T11 |
| **Fable 5.1** | Not assigned. Use it only to escalate (see below) | — |

Rules that keep usage low:

- **Judge cost per finished task, not per session.** A Haiku task that needs
  three tries costs more than one Sonnet run. If a Haiku or Sonnet task fails
  its verification twice, restart it one tier up instead of iterating further.
  Use Fable only if an Opus task stalls twice on the same root cause.
- **Reviews read the diff, not the repository.** Start a fresh Opus session
  with the review prompt in §5. It reads the task section and `git diff`,
  which costs far less than writing the code.
- **Use a fresh session per task.** Task sections are self-contained, so
  carrying earlier conversation forward only adds cost.
- **Run tasks in parallel only when their file scopes don't overlap.** §4
  lists the waves.

## 4. Order and parallelism

```text
Wave A (parallel):  T1 Haiku   T2 Sonnet   T3 Sonnet   T4 Opus   T5 Sonnet
Wave B:             T6 Sonnet                (needs T1, T4, T5)
Wave C (parallel):  T7 Sonnet   T10 Sonnet   (each needs T6)
Wave D (parallel):  T8 Sonnet   T9 Sonnet    (each needs T6, T7)
Wave E:             T11 Sonnet               (needs T8, T9)
Wave F:             T12 Sonnet               (needs T1–T11)
```

The plan front-loads T1–T3: they are cheap and close real gaps even if the
browser work is postponed. Within a wave, the tasks' file lists don't overlap.
Merge each task before starting tasks that depend on it.

## 5. Conventions for every task

- Branch from up-to-date `main` as `test-plan/tN-short-name`. Open one PR per
  task.
- Stay inside the task's **Files** list. If the task needs more, stop and
  report.
- If a new test reveals a real product bug, don't fix it inside a test task.
  Mark the test with `t.Skip("bug: <one line>; see <issue>")` only if you file
  the issue in the same PR. Otherwise leave the test failing and stop. Report
  either way.
- Never call the real ASA API from a test. Never read `config.env`. Set
  `NWSL_CONFIG_FILE=/dev/null` for local runs.
- Before handoff, run the checks in `AGENTS.md`: `golangci-lint fmt ./...`,
  `make lint`, `make vet`, `make test`, and `govulncheck ./...`. Also run
  `go test -race ./...` when the task touches goroutines, the scheduler or HTTP
  lifecycle. Once T6 has landed, also run `make test-e2e` whenever templates,
  JS, CSS or `internal/app` change.
- Hand off: files changed, behavior added, commands run with results, any
  deviation, open questions.

**Implementation prompt** (start a session with the task's model, chosen in
the app's model picker next to the message box):

> Implement task TN from `docs/test-strategy-plan.md`. Read §5 and the TN
> section first. Treat the decisions listed there as settled and stay within
> its Files list. Stop and report on any stop condition. Run its verification
> and the AGENTS.md checks, then commit to `test-plan/tN-…` and push.

**Review prompt** (fresh Opus session):

> Review the diff on branch `test-plan/tN-…` against task TN in
> `docs/test-strategy-plan.md`. Check each decision, the file scope, and each
> acceptance criterion. Look for assertions that would still pass if the
> behavior broke. Run the task's verification with `-count=1`. List the
> findings first, most severe first, citing file and line.

## 6. Running the plan

Use one **Sonnet 5.5 coordinator session per wave**, running locally in the
Claude desktop app (Code tab, this project's folder). The coordinator doesn't write code. It starts each task as a subagent on
the model §3 assigns, has the Opus reviews done, passes findings back to the
implementer, and reports. You merge the PRs between waves. That gives six
checkpoints (waves A–F) instead of about 20 hand-run sessions, and it keeps
each coordinator's context small.

### Before each wave

1. **Update `main`.** Merge the previous wave's PRs on GitHub. Then, in the
   desktop app's Terminal panel (or any terminal) in the project folder, run:

   ```sh
   git switch main && git pull
   ```

2. **One-time setup (first wave only): create `.claude/settings.local.json`**
   in the project folder with the content below. Claude Code reads this file
   for every session in this project, in the CLI and the desktop app. It
   does two things. The `env` block sets `NWSL_CONFIG_FILE=/dev/null` for
   the coordinator and all its subagents, so none of them can touch the
   1Password-backed `config.env`. The `allow` list pre-approves the commands
   the tasks run, so background subagents don't stall on permission prompts.
   The `git` entries cover what the tasks need and leave out force pushes
   and anything that rewrites history.

   ```json
   {
     "env": { "NWSL_CONFIG_FILE": "/dev/null" },
     "permissions": {
       "allow": [
         "Bash(go *)",
         "Bash(make *)",
         "Bash(golangci-lint *)",
         "Bash(govulncheck *)",
         "Bash(git status*)",
         "Bash(git diff*)",
         "Bash(git log*)",
         "Bash(git switch *)",
         "Bash(git checkout -b test-plan/*)",
         "Bash(git add *)",
         "Bash(git commit *)",
         "Bash(git push origin test-plan/*)",
         "Bash(git push -u origin test-plan/*)",
         "Bash(gh pr create *)",
         "Bash(gh pr view *)",
         "Bash(gh pr checks *)"
       ]
     }
   }
   ```

   The file is machine-local; don't commit it. If `git status` shows it as
   untracked, add `.claude/settings.local.json` to `.git/info/exclude`.
3. **Start the coordinator.** In the desktop app, open a new Code session on
   this project's folder, pick **Sonnet 5.5** in the model picker, and paste
   the coordinator prompt below. Subagents get their models from the prompt,
   so the picker only sets the coordinator's.
4. **From wave B on, install Chromium once**, before starting the wave. Do
   this yourself in the Terminal panel, not in a Claude session: the download
   goes to `~/Library/Caches/ms-playwright`, which a session's sandbox can't
   write to. After T6 has merged, run:

   ```sh
   make e2e-install
   ```

   It downloads about 150 MB and only needs repeating when a task upgrades
   `playwright-go`. If a later e2e run fails with "Executable doesn't exist",
   run it again. If e2e tests fail with "Operation not permitted" when
   binding a port or launching Chromium, the session sandbox is blocking
   them. Tell the coordinator, and allow local port binding for the session
   (`sandbox.network.allowLocalBinding: true` in the session's sandbox
   settings) or let it re-run that one command outside the sandbox with your
   approval.

### Usage limits

- Parallel subagents use the same total amount as running the tasks one
  after another, but they spend it faster, so they reach a rolling usage
  limit sooner. If you're close to a limit, lower the `max parallel` value in
  the prompt below. `1` runs the wave one task at a time.
- If usage runs out partway through a wave, nothing committed is lost.
  Implementers commit after each step, and their worktrees and branches stay
  on disk. Start a new coordinator with the same prompt; its first step
  resumes from whatever branches and PRs exist.
- Wave A is the heaviest: five tasks, one of them on Opus (T4). If usage is
  tight, run T1–T3 first and T4–T5 in a later session.

### Coordinator prompt

Change the wave letter, and optionally `max parallel`:

> Coordinate wave **A** of `docs/test-strategy-plan.md` (§4 lists the waves,
> §6 explains this role). Max parallel: **5**. Don't implement anything
> yourself.
>
> 1. **Resume first.** For each task in the wave, check for an existing
>    `test-plan/tN-…` branch (local or remote), worktree or PR. Skip tasks
>    whose PR is open with its review finished. Restart partial tasks from
>    their branch, telling the implementer what is already committed.
> 2. **Implement.** Spawn each remaining task as a background subagent with
>    the model §3 assigns, `isolation: "worktree"`, and the §5 implementation
>    prompt. Run at most the max-parallel number at once. Tell implementers
>    to commit after each step and to push and open a PR when their
>    verification passes. You have my permission to push `test-plan/*`
>    branches and open PRs.
> 3. **Review.** When an implementer finishes, if the task names a reviewer,
>    spawn an Opus subagent with the §5 review prompt. Send blocking findings
>    back to the same implementer with SendMessage. Allow at most two fix
>    rounds. After that, restart the task one model tier up, or ask me if it
>    is already on Opus.
> 4. **Stop conditions.** If a task hits a stop condition, stop that task and
>    report it. Don't work around it.
> 5. **Report.** Finish with a table: task, model used, PR link, CI status,
>    review outcome, deviations, open questions. Don't merge anything.

---

## T1 — CI, Make targets and lint guardrails

- **Model:** Haiku 5.5. **Review:** none. Every change is checked by a
  command. **Depends on:** nothing. **Size:** S.
- **Files:** `Makefile`, `.github/workflows/test.yml`, new
  `.github/workflows/vulncheck.yml`, `.golangci.yml`, `AGENTS.md` (only the
  lines named below), `docs/test-strategy-plan.md` (this section only).

Steps:

1. `Makefile`:
   - Add `./internal/clinching ./internal/qualification` to the
     `test-clinching` target.
   - Add a `test-guards` target:
     `go test -run '^$$' -bench . -benchtime=1x ./internal/clinching ./internal/scenarios ./internal/simulation`.
     It takes about 15s, and its budgets count work rather than time, so it
     doesn't depend on the CI machine's speed.
   - Add a local-only `test-coverage` target. It runs
     `go test -coverpkg=./... -coverprofile=work/coverage.out ./...` and then
     `go tool cover -func=work/coverage.out`, so per-function coverage prints
     without extra steps. `work/` is git-ignored. CI does not run this target;
     it is for local and agent use.
   - Add the new targets (`test-guards` and `test-coverage`) to `.PHONY`.
   - Replace the long feature-list comments above `test-explore` and
     `test-clinching` with one line each that points to the History guide and
     the clinching guide.
2. `.github/workflows/test.yml`:
   - Shorten every step name to five words or fewer, for example "Clinching
     regressions" and "All packages".
   - Add a `make test-guards` step.
   - Add a separate `race` job that runs `make race`.
   - The full-suite step runs `make test`. Every CI step is a Make target, so
     workflow YAML contains no raw `go` commands. CI uploads no coverage
     artifact; coverage comes from `make test-coverage` locally.
3. New `vulncheck.yml`:
   - Triggers: pull requests that change `go.mod` or `go.sum`, pushes to
     `main`, a weekly schedule, and `workflow_dispatch`.
   - Steps: checkout, `actions/setup-go` (with `go-version-file: go.mod`),
     `jdx/mise-action`, then `make vuln`.
   - Copy the action versions already used in `golangci-lint.yml`.
4. `.golangci.yml`: enable `depguard` with a rule that forbids
   `github.com/jrduncans/nwsl-season/internal/asa` in files under
   `internal/app/` and `internal/history/`. The message should say that page
   requests must read the cache. Confirm the rule works by temporarily adding
   the import, seeing `make lint` fail, then reverting.
5. `AGENTS.md`:
   - Replace the long `make test-explore` sentence under "Documentation
     routing" with one sentence that names the target and points to the History
     guide's Explore workspace section.
   - Change "update the active guide, `AGENTS.md`, `Makefile`, and CI together"
     to "update the active guide; update `AGENTS.md`, `Makefile` and CI only
     when a command or required check changes."

**Acceptance criteria:**

- `make test-clinching`, `make test-guards`, `make test-coverage` and `make lint`
  pass. `make test-coverage` writes `work/coverage.out` and prints per-function
  coverage.
- The workflow YAML parses (`actionlint` if available, otherwise a careful
  read). Every workflow run line is a Make target, and no workflow uploads a
  coverage artifact.
- No workflow step name is longer than 40 characters.

**Stop if** any benchmark guard fails on `main`. That is a product finding to
report, not something to fix here.

## T2 — Refresher invariant tests

- **Model:** Sonnet 5.5. **Review:** Opus (diff only). **Depends on:**
  nothing. **Size:** M.
- **Files:** `internal/qualification/refresher_test.go`,
  `internal/scenariorefresh/refresher_test.go`. New `_test.go` files in those
  two packages are allowed.

Read first: `docs/clinching-logic-guide.md` and both `refresher.go` files.
Each test calls `Refresh` (or whichever exported entry point publishes
results) with a fake `Store` that records its calls. Use small invented
seasons of 4 teams with `GamesPerTeam: 6`, as in
`TestCompleteInventoryRequiresEveryDoubleRoundRobinFixture`.

**Qualification tests to add:**

1. When the inventory is incomplete (a fixture is missing), every team and
   achievement gets `ProofIncompleteSchedule` with reason "fixture inventory
   is incomplete". No row is clinched or eliminated, and `ReplaceQualification`
   receives exactly those rows.
2. The same happens for a fixture in an unsafe state, and for an invalid
   kickoff order (reason "fixture kickoff order is invalid").
3. When the calculation returns an error, `RecordQualificationFailure` is
   called once and `ReplaceQualification` is never called.
4. When `ReplaceQualification` returns an error, `RecordQualificationFailure`
   is called with a storage-classified error and `Refresh` returns the error.
5. The no-help batch runs: a season where one team is `NotClinched` but still
   alive produces a `NoHelp` path whose state is not "budget exhausted".

**Scenario tests to add:** the same as cases 3 and 4, for
`RecordScenarioFailure`.

**Acceptance criteria:**

- `go test -count=1 -cover ./internal/qualification ./internal/scenariorefresh`
  passes.
- Cross-package coverage
  (`go test -coverpkg=./internal/... ./internal/qualification ./internal/scenariorefresh`)
  shows `unresolvedRows`, `RecordQualificationFailure` (called on the fake),
  and the `EvaluateNoHelpBatch` call site as executed.
- Each test fails if its key line is removed. Check this once by hand and say
  so in the handoff.

**Not in scope:** changing refresher code.

## T3 — Small unit gaps and dead code

- **Model:** Sonnet 5.5. **Review:** none. **Depends on:** nothing.
  **Size:** S.
- **Files:** new `internal/scheduleload/scheduleload_test.go`, new
  `internal/forecaststate/fuzz_test.go`, `internal/cache/cache.go` (delete
  only), `internal/forecast/results_poisson_history.go` (delete only, and
  remove the file if it becomes empty).

Steps:

1. `scheduleload`: write table tests for `Calculate`, `Congestion` and
   `RecoveryPressure`. Cover:
   - rest durations exactly at, just below and just above each threshold in
     the code;
   - the first match of a season (no previous match);
   - two matches on the same day;
   - kickoffs on either side of a DST change;
   - the error path in `Calculate`.

   Work out the expected values from the formulas by hand and write the
   arithmetic in a comment for each row.
2. `forecaststate`: write `FuzzParseV2`. Seed it from the cases in the
   existing tests. The properties to check:
   - it never panics;
   - when parsing succeeds, encoding the state and parsing it again gives the
     same `State`;
   - an unsupported model ID is always rejected.

   Run the fuzzer locally for 60s with `-fuzz FuzzParseV2 -fuzztime 60s`.
   Commit any failing inputs it finds under `testdata/fuzz`.
3. Delete `(*DB).RefreshSnapshot` and `NewResultsPoissonHomeHistoryV1`.
   Before deleting each one, confirm it has no callers with
   `grep -rn` across the whole repository, including docs.

**Acceptance criteria:** the new tests pass; package coverage for
`scheduleload` is above 90%; `make lint` reports no unused code.

**Stop if** the fuzzer finds a failure in production code. Report the input;
don't fix it here.

## T4 — Server composition seam and ASA base URL

- **Model:** Opus 5.5. **Review:** Opus (fresh session, with `-race`).
  **Depends on:** nothing. **Size:** M.
- **Files:** `cmd/server/main.go`, `cmd/server/main_test.go`, a new package
  `internal/server` (implementation and tests), `internal/config/config.go`
  and its test, `internal/scheduler/scheduler.go` and its tests,
  `config.env.example`, `README.md` (configuration table only).

**Goal:** a test can build the real server (cache, syncer, refreshers,
scheduler and `app` handler) inside the test process, pointed at a fake ASA,
and can trigger scheduler work synchronously. Production behavior stays the
same.

**Decisions:**

1. Move the body of `run` in `cmd/server/main.go` into the new package with
   this shape:

   ```go
   func Build(ctx context.Context, cfg config.Config, opts Options) (*Server, error)
   ```

   - `Options` carries `Logger`, `ASABaseURL`, `ASAHTTPClient`,
     `StartScheduler bool` and an optional scheduler `Now func() time.Time`.
   - `*Server` exposes `Handler() http.Handler`, `Start()`, `Stop()`,
     `Wait()` and `CheckNow(ctx) error`.
   - `CheckNow` runs one scheduler check with a manual trigger, then waits for
     its jobs and the follow-up calculations before returning.

   `main` keeps signal handling, telemetry setup and `ListenAndServe`, and
   calls `Build`.
2. Add `NWSL_ASA_BASE_URL` to `config`. It defaults to `asa.DefaultBaseURL`
   and must be an absolute `http` or `https` URL; anything else is a
   configuration error. Wire it into the server's `asa.Client`. `cmd/sync`
   keeps its `-base-url` flag, but the flag's default comes from the
   configured value.
3. Export a way to inject the scheduler's clock (it already uses an internal
   `now`) and a synchronous check entry point. Reuse `checkWithTrigger`; don't
   duplicate planning logic.
4. Fake ASA data will be generated relative to the real current time (see T5),
   so `internal/cache`'s own `time.Now` calls stay as they are. Don't thread a
   clock through the cache.

**Tests:**

- `Build` with `StartScheduler: false` serves `/healthz` and makes no ASA
  request. Use a counting `httptest` server as the base URL.
- `CheckNow` against a counting server makes requests only to that server and
  returns after they finish.
- Invalid `NWSL_ASA_BASE_URL` values are rejected.
- `Stop` followed by `Wait` returns, and the race detector stays clean.

**Acceptance criteria:**

- Everything the old `run` did still happens, in the same order: source-scope
  seeding, forecast pre-cache, scheduler start, shutdown sequence. Show this in
  the handoff with a before/after list.
- `go test -race ./cmd/server ./internal/server ./internal/scheduler` passes.

**Stop if** moving the code requires changing scheduler planning behavior or
the sync lease semantics.

## T5 — Shared fake ASA (`internal/asatest`)

- **Model:** Sonnet 5.5. **Review:** none. Its own tests and T6 exercise it.
  **Depends on:** nothing (it can run alongside T4). **Size:** M.
- **Files:** new package `internal/asatest`.

**Goal:** a fake ASA server for tests, backed by `httptest`, that can be set
up per scenario.

**Decisions:**

1. Serve `/nwsl/teams`, `/nwsl/games` and `/nwsl/games/xgoals` with the same
   query-parameter filtering the real API applies. Read `internal/asa/client.go`
   for the parameters the client sends and `docs/asa_openapi.json` for the
   response shapes. Encode responses with the `asa` wire types so the fake
   can't drift from the client.
2. State can be changed while the server runs and is safe for concurrent use:
   - `SetTeams`, `SetGames`, `SetXGoals`, `UpsertGame` to change data;
   - `FailNext(path, status, n)` and `SetDown(bool)` to simulate failures;
   - `Requests() []Request` (path and query) and `ResetRequests()` to inspect
     traffic.
3. Builders:
   - `Season(teams int, base time.Time)` returns a double round robin with
     kickoffs spaced from `base`;
   - `PlayThrough(t time.Time, score func(game) (h, a int))` completes every
     game that kicks off before `t`;
   - `WithXG(fraction float64)` adds xG to that share of completed games.

   Scenarios pass `time.Now()`-relative times, for example a base of 60 days
   ago.
4. Unknown paths return 404 and are recorded, so a test can assert that none
   were requested.

**Tests:** drive the real `asa.Client` against the fake for each endpoint,
including filtering, error status codes and `SetDown`. Check that the decoded
results match the recorded files in `internal/asa/testdata` when the fake is
loaded with the same data.

**Not in scope:** moving existing `syncer` or `cmd/sync` tests onto the fake.

## T6 — playwright-go harness and the cache-only journey

- **Model:** Sonnet 5.5. **Review:** Opus. Every later browser task copies its
  patterns. **Depends on:** T1 (it edits the same Makefile, workflow and lint
  files), T4, T5. **Size:** L.
- **Files:** new `e2e/` directory with `//go:build e2e` on every file,
  `go.mod` and `go.sum` (add `github.com/mxschmitt/playwright-go`),
  `Makefile` (`test-e2e` target), `.github/workflows/test.yml` (new `e2e`
  job), `.golangci.yml` (add `build-tags: [e2e]` under `run`).
  The upstream module moved back to `github.com/mxschmitt/playwright-go`; the
  playwright-community path redirects to it and recent tags no longer resolve
  under it.

**Decisions:**

1. `make test-e2e` runs `go test -tags e2e -count=1 ./e2e/...`. A plain
   `go test ./...` never needs a browser.
2. Browser setup in `TestMain`:
   - Never download anything from tests. Use `playwright.Run` (driver only),
     not `playwright.Install`, and launch Chromium from the browser cache
     that the install step below filled.
   - If the browser is missing, fail with the message "Chromium is not
     installed; run `make e2e-install`". If `NWSL_E2E_CHROMIUM` is set, use
     it as `ExecutablePath` instead (for environments with a preinstalled
     browser).
   - Add a Makefile target `e2e-install` that runs
     `go run github.com/mxschmitt/playwright-go/cmd/playwright install chromium`.
     Run from the module root, this uses the `playwright-go` version in
     `go.mod`, so the browser always matches the library. Verify it works
     from a clean cache (`PLAYWRIGHT_BROWSERS_PATH=$TMPDIR/pw make e2e-install`
     and `make test-e2e` with the same variable), and add whatever import or
     `go mod tidy` step it needs. Add `e2e-install` to `.PHONY` and mention it
     in the `test-e2e` Makefile comment.
   - CI runs the same target with `--with-deps` added
     (`go run github.com/mxschmitt/playwright-go/cmd/playwright install --with-deps chromium`),
     with no browser cache: Playwright's CI guide advises against caching
     browser binaries because restoring a cache takes about as long as
     downloading them.
   - Use one browser per process and a new context for each test.
3. Fixture per test:
   - a temporary `NWSL_DATA_DIR`;
   - a T5 fake loaded with a 4-team season where half the games are played;
   - `server.Build(..., StartScheduler: false)` from T4, then `CheckNow` once
     to fill the cache;
   - the handler served behind `httptest.NewServer` under the `/nwsl-season/`
     prefix, using `http.StripPrefix` the way production does.
4. Helpers that later tasks reuse:
   - `newPage(t, viewport)` with `Desktop` (1280×800) and `Mobile`
     (390×844). It fails the test on any console error, `pageerror`, failed
     same-origin request, or CSP violation (listen for
     `securitypolicyviolation`).
   - `assertNoHorizontalOverflow`.
   - `assertNoASARequests(fake)`.
5. No `time.Sleep`. Use Playwright's waiting assertions
   (`playwright.NewPlaywrightAssertions`).

**Journey J1, cache-only pages:** after `CheckNow`, call
`fake.ResetRequests()`. Visit at both viewports:

- `/`, `/seasons`, `/history`, `/explore`;
- the current season's standings, fixtures, schedule-difficulty, forecast,
  model-evaluation and clinching pages.

Assert that each page loads with no errors and no overflow, and that the fake
received zero requests. Then call `fake.SetDown(true)`, visit the pages again,
and assert they still render.

**CI:** a separate `e2e` job that installs Chromium as above, runs
`make test-e2e`, and uploads Playwright traces when it fails.

**Acceptance criteria:** J1 passes 10 times in a row locally
(`-count=10`).

**Stop if** the playwright-go driver can't be downloaded in the environment
(report the network error), or if J1 shows that a page contacts ASA. That
would be a product bug; report it.

## T7 — Seeded-cache scenarios and the preview tool

- **Model:** Sonnet 5.5. **Review:** Opus (diff only; it moves test
  fixtures). **Depends on:** T6. **Size:** M.
- **Files:** new package `internal/apptest` (normal Go files, not tests, so
  the `e2e` package can import it), `internal/app/history_preview_test.go`
  (delete), any `internal/app/*_test.go` whose fixture helpers move, a new
  `cmd/preview/main.go`, `docs/history-logic-guide.md` (the preview
  instructions only).

**Goal:** the `TestHistoryPreview` scenarios become reusable seed functions
for the cache, plus a development tool.

**Decisions:**

1. Move the scenario builders (`historyArchive` and the default, `teams`,
   `team-history` and `season-trend` scenarios) into
   `apptest.Seed(t testing.TB, db *cache.DB, scenario string)`. Keep any
   `internal/app` tests that use the same helpers working by importing
   `apptest`. Nothing else in `internal/app` tests should change.
2. `go run ./cmd/preview -scenario teams` seeds a temporary database, serves
   the app on a loopback port and prints the URL. It writes nothing outside
   its temporary directory and doesn't start the scheduler.
3. Delete `TestHistoryPreview` and update the guide's instructions to use
   `cmd/preview`.

**Acceptance criteria:** `make test` passes; each scenario starts with
`cmd/preview`; `grep -rn /private/tmp` finds nothing.

## T8 — Explore browser tests and chart-geometry unit tests

- **Model:** Sonnet 5.5. **Review:** Opus. **Depends on:** T6, T7.
  **Size:** L.
- **Files:** `e2e/explore_test.go`, `e2e/geometry_test.go`,
  `internal/app/static/explore.js`, new
  `internal/app/static/explore-geometry.js`, `internal/app/templates/explore.html`
  (script tag only), `internal/app/security_headers_test.go` (only if the new
  script needs listing).

**Steps:**

1. **Write the behavior tests first, against the current `explore.js`.**
   Seed with `apptest` scenarios. Read chart state with
   `page.Evaluate("Chart.getChart(canvas)…")` rather than comparing pixels.
   Cover the behavioral items in the History guide's browser checklist:
   - for each view (`team-quadrants`, `team-rankings`, `team-history`,
     `season-trend`), the season, measure, units and display options round-trip
     through the URL, and Back/Forward restores them;
   - switching analyses makes no new network request;
   - sorting works in both directions, and with JavaScript disabled the native
     table and GET form still work;
   - keyboard inspection, dismissal and clearing a pinned point;
   - the Outlier plot has equal axis ranges and shows the parity diagonal;
     Scored vs allowed reverses the allowed axis;
   - `xG incomplete` and `xPoints incomplete` labels and the empty state
     appear;
   - no overflow at 390px.
2. **Extract the pure geometry from `explore.js`** (tight/tied/zero bounds,
   uniform point sizing, logo collision passes, viewport-capped Outlier size)
   into `explore-geometry.js`:
   - a classic script that defines one global, `window.NWSLGeometry`;
   - no build step;
   - it must comply with the existing CSP (no inline scripts, no `eval`).

   `explore.js` calls these functions instead of its inline versions. The
   step 1 tests must still pass without changes.
3. **Write table-driven geometry tests** (`geometry_test.go`): load a page,
   then call `NWSLGeometry.*` through `page.Evaluate` for each case. Include
   tied, all-zero, single-point, negative-gap and very large ranges, and
   coincident points.

**Acceptance criteria:**

- Step 1 tests pass both before and after step 2. Show this with two
  commits.
- Running all of `e2e` takes under 4 min in CI.
- For each checklist item, the handoff says "automated by <test>" or "still
  manual: <reason>". Only visual judgment calls should stay manual.

**Stop if** extracting the geometry would require changing what the charts
show.

## T9 — Browser tests for the other pages

- **Model:** Sonnet 5.5. **Review:** Opus (diff only). **Depends on:** T6, T7.
  It runs alongside T8; the files don't overlap. **Size:** M.
- **Files:** `e2e/pages_test.go`. `internal/apptest` only to add seed
  scenarios.

**Cover:**

- **Standings (`standings.js`):** local-time rendering in the browser's time
  zone (set `TimezoneId` on the context), sorting, and the no-script
  fallback.
- **Forecast:** the assumption builder (filter by team, choose fixture, add a
  result, apply the scenario), "Copy scenario link" round-trips the URL, and
  comparing another model.
- **Clinching:** grouped summaries expand and collapse with the keyboard.
- **Fixtures, schedule difficulty, bracket, model evaluation:** each renders
  at both viewports with no overflow.
- **Proxy prefix:** every link and asset on each page resolves under
  `/nwsl-season/` (no 404 network responses).

**Acceptance criteria:** the tests pass 10 times in a row; the handoff lists
which assertions in `internal/app/handler_test.go` these tests now duplicate,
as input for T11.

## T10 — Fake-ASA journeys

- **Model:** Sonnet 5.5. **Review:** Opus. These journeys encode architecture
  invariants. **Depends on:** T6. **Size:** L.
- **Files:** `e2e/journeys_test.go`. `internal/asatest` only to add builders.

Read first: `docs/sync-logic-guide.md` and `docs/clinching-logic-guide.md`.
Each journey changes the fake's state, calls `CheckNow`, and then checks
pages in the browser.

| Journey | What the fake does | What the browser must show |
| --- | --- | --- |
| J2 First sync | 4-team season, half played | Standings match the expected table computed in the test |
| J3 Matchday | A new result is added through `UpsertGame` after the first sync | Standings change; a team whose clinch the test arranged shows the clinched indicator on the clinching page |
| J4 Incomplete inventory | After a complete sync, one fixture disappears from `/nwsl/games` | The earlier fixture list is kept (count unchanged); no new indicator is published from the incomplete data |
| J5 Missing xG | `WithXG(0.5)` | Explore shows the xG-incomplete labels; pages don't error |
| J6 ASA errors | `FailNext("/nwsl/games", 503, n)` during a later sync | Pages keep the last good data; `/cache/status` reports the failed attempt |
| J7 Scheduler path | `StartScheduler: true` with a short check interval and an injected `Now` | A result that becomes due is picked up without calling `CheckNow` |

Arrange the J3 clinch scenario so that it is correct by construction, and
explain the arithmetic in a comment. Verify it against
`clinching.NewEvaluator` inside the test before asserting what the page
shows.

**Acceptance criteria:** each journey passes 10 times in a row, and J7 shows
no data races under `-race`.

**Stop if** J4 or J6 shows that an invariant is violated. That is a product
bug; report it.

## T11 — Scoped HTML assertions in Go tests

- **Model:** Sonnet 5.5. **Review:** Opus. It deletes assertions.
  **Depends on:** T8, T9. **Size:** M.
- **Files:** new `internal/app/htmlassert_test.go` (helper), and
  `internal/app/*_test.go`. `go.mod` changes only to promote `golang.org/x/net`
  from indirect to direct.

**Decisions:**

1. Write a helper based on `golang.org/x/net/html`:
   `find(t, body, selector)`, where the selector supports tag, `#id`,
   `.class` and `[attr=value]`, plus `text(node)`. Don't add a CSS selector
   library.
2. Convert the longest substring-list assertions, starting with the forecast
   page test (the list of about 30 strings in `handler_test.go`), so each
   expectation is checked inside its own page section.
3. Delete an assertion only when it is listed as duplicated in the T9
   handoff, or when a T8 test clearly covers it, and only if it checks feature
   presence. Keep every assertion about escaping, relative links, headers,
   no-script fallbacks, status codes and the cache-only fallback.
4. List each deleted assertion in the PR description, with the browser test
   that covers it.

**Acceptance criteria:**

- The count of `strings.Contains` calls in `internal/app` tests drops by at
  least half.
- Cross-package coverage of `internal/app` stays at or above its level before
  the task.
- `govulncheck` is clean after promoting the dependency.

## T12 — Documentation and agent guidance

- **Model:** Sonnet 5.5. **Review:** none (you read it). **Depends on:**
  T1–T11. **Size:** S.
- **Files:** `AGENTS.md`, `internal/app/AGENTS.md`, `README.md` (testing
  section), `docs/history-logic-guide.md` (browser verification section),
  this plan (set Status to Complete and add a short outcomes section).

**Steps:**

1. Describe the test layers from §2 and the layer-choice rule in one short
   README section.
2. In `AGENTS.md`:
   - require `make test-e2e` for template, JS, CSS and `internal/app`
     changes;
   - point to `internal/asatest` and `internal/apptest` for new tests;
   - state that a test must never call the real ASA.
3. In `internal/app/AGENTS.md`, replace "visually verify desktop and a 390px
   viewport" with: "run `make test-e2e`; still check visual design by eye at
   desktop and 390px."
4. Cut the History guide's browser checklist down to the items T8 reported as
   still manual.

**Acceptance criteria:** every command the docs mention exists and passes.
