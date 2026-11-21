#!/usr/bin/env bash
# Убирает демон, горячие клавиши и автозапуск. Настройки остаются.
set -euo pipefail
BIN="${HOME}/.local/bin/janus"

if [ -x "${BIN}" ]; then
  "${BIN}" uninstall || true
fi
pkill -x janus 2>/dev/null || true
rm -f "${BIN}"
echo "демон остановлен, ${BIN} удалён"
echo "конфиг оставлен: ${HOME}/.config/janus/"
