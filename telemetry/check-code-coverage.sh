#!/bin/sh

set -eu

repository_root=$(
  unset CDPATH
  cd -- "$(dirname -- "$0")/.."
  pwd
)
inventory_dir=$(mktemp -d)
trap 'rm -r "$inventory_dir"' EXIT

cd "$repository_root"

rg --glob '*.go' --glob '!**/*_test.go' --no-filename --only-matching \
  --replace '$1' \
  'attribute\.(?:String|Int|Int64|Bool|Float64|StringSlice)\("(nwsl\.[^"]+)"' \
  cmd internal \
  | sed '/\.$/d' \
  | sort -u >"$inventory_dir/code-attributes"

rg --glob '*.yaml' --no-filename --only-matching \
  --replace '$1' \
  '^[[:space:]]+- (?:id|ref): (nwsl\.[^[:space:]]+)$' \
  telemetry/registry \
  | sort -u >"$inventory_dir/registry-attributes"

comm -23 "$inventory_dir/code-attributes" "$inventory_dir/registry-attributes" \
  >"$inventory_dir/missing-attributes"

{
  rg --glob '*.go' --glob '!**/*_test.go' --no-filename --only-matching \
    --replace '$1' \
    'telemetry\.Tracer\(\)\.Start\([^"\n]*"([a-z][a-z0-9_.]+)"' \
    cmd internal
  rg --glob '*.go' --glob '!**/*_test.go' --no-filename --only-matching \
    --replace '$1' \
    'RecordCompleted(?:Warning)?Span\([^"\n]*"([a-z][a-z0-9_.]+)"' \
    cmd internal
  rg --glob '*.go' --glob '!**/*_test.go' --no-filename --only-matching \
    --replace '$1' \
    'AddEvent\("([a-z][a-z0-9_.]+)"' \
    cmd internal
} | sort -u >"$inventory_dir/code-signals"

rg --glob '*.yaml' --no-filename --only-matching \
  --replace '$1' \
  '^[[:space:]]+name: ((?:cache|forecast|qualification|scenario|scheduler|sync)\.[^[:space:]]+)$' \
  telemetry/registry \
  | sort -u >"$inventory_dir/registry-signals"

comm -23 "$inventory_dir/code-signals" "$inventory_dir/registry-signals" \
  >"$inventory_dir/missing-signals"

if [ -s "$inventory_dir/missing-attributes" ] || [ -s "$inventory_dir/missing-signals" ]; then
  if [ -s "$inventory_dir/missing-attributes" ]; then
    echo "telemetry attributes emitted by Go but missing from the Weaver registry:"
    sed 's/^/  /' "$inventory_dir/missing-attributes"
  fi
  if [ -s "$inventory_dir/missing-signals" ]; then
    echo "telemetry spans or events emitted by Go but missing from the Weaver registry:"
    sed 's/^/  /' "$inventory_dir/missing-signals"
  fi
  echo "update telemetry/registry and regenerate docs/telemetry/catalog"
  exit 1
fi

echo "Go telemetry literals are covered by the Weaver registry"
