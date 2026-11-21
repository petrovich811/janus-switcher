#!/usr/bin/env bash
# Устанавливает Janus в домашний каталог пользователя.
# Права root не нужны: демон работает от вашего имени.
set -euo pipefail

SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${HOME}/.local/bin"
BIN="${BIN_DIR}/janus"

echo "== сборка =="
cd "${SRC_DIR}"
if ! command -v go >/dev/null 2>&1; then
  echo "нужен Go 1.26 или новее: https://go.dev/dl/" >&2
  exit 1
fi
mkdir -p "${BIN_DIR}"
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "${BIN}" .
echo "собрано: ${BIN}"

echo "== горячие клавиши и автозапуск =="
"${BIN}" install

echo "== запуск демона =="
pkill -x janus 2>/dev/null || true
for _ in $(seq 1 25); do
  pgrep -x janus >/dev/null 2>&1 || break
  sleep 0.2
done
nohup "${BIN}" daemon >"${HOME}/.config/janus/daemon.out" 2>&1 &
disown

ok=""
for _ in $(seq 1 50); do
  out="$("${BIN}" status 2>/dev/null || true)"
  case "${out}" in
    *работает*) ok=1; break ;;
  esac
  sleep 0.2
done
if [ -n "${ok}" ]; then
  echo "демон запущен"
else
  echo "демон не ответил, смотрите ${HOME}/.config/janus/daemon.out" >&2
fi

echo
echo "Готово. Настройки: http://127.0.0.1:8791/"
echo "Открыть: ${BIN} ui"
if ! command -v janus >/dev/null 2>&1; then
  echo
  echo "Замечание: ${BIN_DIR} не в PATH. Добавьте в ~/.profile:"
  echo '  export PATH="$HOME/.local/bin:$PATH"'
fi
