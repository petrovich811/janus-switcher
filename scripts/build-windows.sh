#!/usr/bin/env bash
# Собирает Windows-версию из Linux (кросс-компиляция, без cgo).
# Готовый файл: dist/janus.exe
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
mkdir -p dist
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build -trimpath -ldflags "-H=windowsgui -s -w" -o dist/janus.exe .
echo "готово: dist/janus.exe"
ls -lh dist/janus.exe | awk '{print "размер:", $5}'
