#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

PROFILE_ARG=""
if [[ $# -gt 0 ]]; then
  case "$1" in
    smoke|quick|full)
      PROFILE_ARG="$1"
      shift
      ;;
  esac
fi

PROFILE="${PROFILE_ARG:-${MIXNET_BENCH_PROFILE:-full}}"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
OUTPUT_DIR="${MIXNET_BENCH_OUTPUT_DIR:-mixnet/benchmarks/output/${TIMESTAMP}}"
STREAM_TIMEOUT="${MIXNET_STREAM_TIMEOUT:-5m}"
MAX_ENCRYPTED_PAYLOAD="${MIXNET_MAX_ENCRYPTED_PAYLOAD:-134217728}"
GOCACHE_DIR="${GOCACHE:-/tmp/mixnet-go-build-cache}"

export MIXNET_STREAM_TIMEOUT="$STREAM_TIMEOUT"
export MIXNET_MAX_ENCRYPTED_PAYLOAD="$MAX_ENCRYPTED_PAYLOAD"
export GOCACHE="$GOCACHE_DIR"

mkdir -p "$GOCACHE_DIR"

exec go run ./mixnet/benchmarks/cmd/mixnet-bench \
  --profile "$PROFILE" \
  --output-dir "$OUTPUT_DIR" \
  "$@"
