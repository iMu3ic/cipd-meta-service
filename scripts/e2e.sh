#!/usr/bin/env bash
# End-to-end check against the real cipd client.
# Usage: CIPD=/path/to/cipd scripts/e2e.sh
set -euo pipefail

CIPD="${CIPD:-cipd}"
work="$(mktemp -d)"
trap 'kill "${svc:-0}" 2>/dev/null || true; rm -rf "$work"' EXIT

mkdir -p "$work/store" "$work/payload"
echo hello > "$work/payload/hello.txt"
"$CIPD" pkg-build -in "$work/payload" -name test/pkg -out "$work/store/test.cipd"
go run ./cmd/cipd-meta-indexer -root "$work/store" -file "$work/store/test.cipd" \
  -index "$work/store/index.json" -name test/pkg -refs latest -tags version:1.0

go build -o "$work/cipd-meta-service" ./cmd/cipd-meta-service
"$work/cipd-meta-service" -root "$work/store" -addr 127.0.0.1:8099 &
svc=$!
for _ in $(seq 1 50); do
  curl -s -o /dev/null --max-time 1 http://127.0.0.1:8099/ && break
  sleep 0.2
done

check() { # version, expect ok|notfound
  rm -rf "$work/out"
    echo "test/pkg $1" > "$work/ensure.txt"
  if out="$("$CIPD" ensure -ensure-file "$work/ensure.txt" -root "$work/out" \
      -service-url http://127.0.0.1:8099 2>&1)"; then got=ok
  elif grep -q "not found" <<<"$out"; then got=notfound
  else got=error; fi
  [ "$got" = "$2" ] || { echo "FAIL: $1 expected $2, got $got"; echo "$out"; exit 1; }
  echo "ok: $1 -> $got"
}

check latest ok
check version:1.0 ok
check version:9.9 notfound