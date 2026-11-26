#!/usr/bin/env bash
# Build ghn-tele. Mặc định: EXE Windows (amd64). "local" = bản chạy trên máy này.
set -euo pipefail
cd "$(dirname "$0")"

VER=$(sed -n 's/^[[:space:]]*version[[:space:]]*=[[:space:]]*"\(.*\)"/\1/p' main.go | head -1)
mkdir -p dist

if [ "${1:-windows}" = "local" ]; then
  OUT="dist/ghn-tele-$VER-$(go env GOOS)-$(go env GOARCH)"
  CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$OUT" .
else
  OUT="dist/ghn-tele-$VER.exe"
  GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$OUT" .
fi

echo "OK  $OUT  ($(du -h "$OUT" | cut -f1))"
