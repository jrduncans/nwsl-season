#!/bin/sh

set -eu

collector_image="otel/opentelemetry-collector:0.159.0@sha256:7725a7a10c87d8853208bdd4bb3439ad3c0d7b32b4292b9300ac07c8daba14a2"
weaver_port="${WEAVER_LIVE_CHECK_OTLP_PORT:-14317}"
collector_port="${WEAVER_LIVE_CHECK_HTTP_PORT:-14318}"
admin_port="${WEAVER_LIVE_CHECK_ADMIN_PORT:-14320}"

validate_port() {
	name=$1
	value=$2
	case "$value" in
	'' | *[!0-9]*)
		echo "$name must be an integer from 1 to 65535; got '$value'" >&2
		exit 1
		;;
	esac
	if [ "$value" -lt 1 ] || [ "$value" -gt 65535 ]; then
		echo "$name must be an integer from 1 to 65535; got '$value'" >&2
		exit 1
	fi
}

validate_port WEAVER_LIVE_CHECK_OTLP_PORT "$weaver_port"
validate_port WEAVER_LIVE_CHECK_HTTP_PORT "$collector_port"
validate_port WEAVER_LIVE_CHECK_ADMIN_PORT "$admin_port"
if [ "$weaver_port" = "$collector_port" ] ||
	[ "$weaver_port" = "$admin_port" ] ||
	[ "$collector_port" = "$admin_port" ]; then
	echo "WEAVER_LIVE_CHECK_OTLP_PORT, WEAVER_LIVE_CHECK_HTTP_PORT, and WEAVER_LIVE_CHECK_ADMIN_PORT must be distinct" >&2
	exit 1
fi

collector_name="nwsl-telemetry-live-check-$$"
repository_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
state_dir=$(mktemp -d)
weaver_pid=""
collector_started=false

cleanup() {
	status=$?
	if [ "$collector_started" = true ]; then
		docker stop "$collector_name" >/dev/null 2>&1 || true
	fi
	if [ -n "$weaver_pid" ] && kill -0 "$weaver_pid" 2>/dev/null; then
		kill "$weaver_pid" 2>/dev/null || true
	fi
	if [ "$status" -ne 0 ]; then
		if [ -f "$state_dir/weaver.log" ]; then
			echo "Weaver live-check log:" >&2
			tail -n 200 "$state_dir/weaver.log" >&2 || true
		fi
		if [ -f "$state_dir/collector.log" ]; then
			echo "Collector log:" >&2
			tail -n 200 "$state_dir/collector.log" >&2 || true
		fi
		if [ -s "$state_dir/report.json" ]; then
			echo "Weaver live-check violations:" >&2
			if command -v jq >/dev/null 2>&1; then
				jq -r '[.. | objects | select(.level? == "violation") | {id, message, signal_type, signal_name}] | unique | .[] | "[\(.id)] \(.signal_type // "unknown") \(.signal_name // ""): \(.message)"' "$state_dir/report.json" >&2
			else
				grep -B 8 -A 4 '"level": "violation"' "$state_dir/report.json" >&2 || true
			fi
		fi
	fi
	rm -r "$state_dir"
	exit "$status"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

command -v docker >/dev/null 2>&1 || {
	echo "docker is required for the development-only Collector bridge" >&2
	exit 1
}

cd "$repository_dir"

mise exec -- weaver registry live-check \
	--registry ./telemetry/registry \
	--input-source otlp \
	--otlp-grpc-address 127.0.0.1 \
	--otlp-grpc-port "$weaver_port" \
	--admin-port "$admin_port" \
	--inactivity-timeout 120 \
	--format json \
	--output http \
	--fail-on violation \
	>"$state_dir/weaver.log" 2>&1 &
weaver_pid=$!

ready=false
attempt=0
while [ "$attempt" -lt 120 ]; do
	if curl -fsS "http://127.0.0.1:$admin_port/health" >/dev/null 2>&1; then
		ready=true
		break
	fi
	if ! kill -0 "$weaver_pid" 2>/dev/null; then
		echo "Weaver exited before becoming ready" >&2
		exit 1
	fi
	attempt=$((attempt + 1))
	sleep 1
done
if [ "$ready" != true ]; then
	echo "Weaver did not become ready" >&2
	exit 1
fi

docker run --rm --detach \
	--name "$collector_name" \
	--add-host host.docker.internal:host-gateway \
	--publish "127.0.0.1:$collector_port:4318" \
	--env "WEAVER_OTLP_ENDPOINT=host.docker.internal:$weaver_port" \
	--volume "$repository_dir/telemetry/live-check-collector.yaml:/etc/otelcol/config.yaml:ro" \
	"$collector_image" \
	--config /etc/otelcol/config.yaml \
	>"$state_dir/collector.id"
collector_started=true
docker logs --follow "$collector_name" >"$state_dir/collector.log" 2>&1 &

ready=false
attempt=0
while [ "$attempt" -lt 60 ]; do
	if curl -sS --connect-timeout 1 "http://127.0.0.1:$collector_port/v1/traces" >/dev/null 2>&1; then
		ready=true
		break
	fi
	if ! docker inspect "$collector_name" >/dev/null 2>&1; then
		echo "Collector exited before becoming ready" >&2
		exit 1
	fi
	attempt=$((attempt + 1))
	sleep 1
done
if [ "$ready" != true ]; then
	echo "Collector did not become ready" >&2
	exit 1
fi

NWSL_CONFIG_FILE=/dev/null \
NWSL_TELEMETRY_LIVE_CHECK=1 \
OTEL_EXPORTER_OTLP_ENDPOINT="http://127.0.0.1:$collector_port" \
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf \
go test ./internal/telemetrycontract -run '^TestRuntimeTelemetryMatchesRegistry$' -count=1

docker stop "$collector_name" >/dev/null
collector_started=false

curl -fsS -X POST "http://127.0.0.1:$admin_port/stop" -o "$state_dir/report.json"
if ! wait "$weaver_pid"; then
	weaver_pid=""
	exit 1
fi
weaver_pid=""

if ! grep -Eq '"span"[[:space:]]*:[[:space:]]*[1-9][0-9]*' "$state_dir/report.json" ||
	! grep -Eq '"metric"[[:space:]]*:[[:space:]]*[1-9][0-9]*' "$state_dir/report.json"; then
	echo "Weaver did not report both trace and metric samples" >&2
	exit 1
fi

echo "Weaver runtime telemetry contract check passed"
