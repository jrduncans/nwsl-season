#!/bin/sh

set -eu

repository_root=$(
  unset CDPATH
  cd -- "$(dirname -- "$0")/.."
  pwd
)

cd "$repository_root"

production_globs="--glob=*.go --glob=!**/*_test.go --glob=!internal/telemetry/nwslconv/**"

# shellcheck disable=SC2086 # The glob arguments intentionally expand as words.
if matches=$(rg $production_globs \
  'attribute\.(String|Int|Int64|Bool|Float64|StringSlice|Key)\("nwsl\.' \
  cmd internal); then
  echo "raw nwsl.* attribute constructors are prohibited outside the generated package:"
  printf '%s\n' "$matches"
  exit 1
fi

# Domain packages may retain attribute.KeyValue as a transport type, but all
# constructors belong in nwslconv. telemetry.go is the sole exception for
# standard OTel resource and exception attributes.
# shellcheck disable=SC2086 # The glob arguments intentionally expand as words.
if matches=$(rg $production_globs --glob=!internal/telemetry/telemetry.go \
  'attribute\.(String|Int|Int64|Bool|Float64|StringSlice|Key)\(' \
  cmd internal); then
  echo "OpenTelemetry attribute constructors outside telemetry plumbing and generated conventions are prohibited:"
  printf '%s\n' "$matches"
  exit 1
fi

# Signal names are generated alongside attributes. Keep production call sites
# on those constants so renames cannot drift from the registry.
# shellcheck disable=SC2086 # The glob arguments intentionally expand as words.
if matches=$(rg $production_globs \
  '"(cache\.season\.load|forecast\.(run|precache)|qualification\.(refresh|status_proof|no_help_batch)|scenario\.(refresh|generate_team)|scheduler\.(tick|job|decision)|sync\.(run|recalculate|source_operation|venue_history|asa_response|game_freshness|xg_freshness))"' \
  cmd internal); then
  echo "raw application span or event names are prohibited outside the generated package:"
  printf '%s\n' "$matches"
  exit 1
fi

echo "production Go instrumentation uses generated telemetry conventions"
