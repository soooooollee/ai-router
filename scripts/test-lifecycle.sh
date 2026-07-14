#!/bin/sh

set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d 2>/dev/null || mktemp -d -t airoute-lifecycle-test)
runtime_dir="$tmp/runtime"
binary="$tmp/air"
config="$tmp/airoute.yaml"
gateway_port=$((22000 + ($$ % 10000)))
admin_port=$((gateway_port + 1))

cleanup() {
  AIROUTE_RUNTIME_DIR="$runtime_dir" "$binary" stop >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

sed \
  -e "s/127.0.0.1:12666/127.0.0.1:$gateway_port/" \
  -e "s/127.0.0.1:12667/127.0.0.1:$admin_port/" \
  "$root/examples/airoute.minimal.yaml" >"$config"

(cd "$root" && go build -trimpath -o "$binary" ./cmd/airoute)

AIROUTE_RUNTIME_DIR="$runtime_dir" "$binary" start --config "$config" | grep -q "started in the background"
AIROUTE_RUNTIME_DIR="$runtime_dir" "$binary" status | grep -q "status=running"
AIROUTE_RUNTIME_DIR="$runtime_dir" "$binary" logs --lines 20 | grep -q "gateway listening"
AIROUTE_RUNTIME_DIR="$runtime_dir" "$binary" restart | grep -q "started in the background"
AIROUTE_RUNTIME_DIR="$runtime_dir" "$binary" status | grep -q "status=running"
AIROUTE_RUNTIME_DIR="$runtime_dir" "$binary" stop | grep -q "stopped"

if curl -fsS "http://127.0.0.1:$gateway_port/healthz" >/dev/null 2>&1; then
  printf 'gateway is still running after air stop\n' >&2
  exit 1
fi

printf 'background lifecycle test passed\n'
