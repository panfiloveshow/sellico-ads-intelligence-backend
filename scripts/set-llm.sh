#!/usr/bin/env bash
# set-llm.sh — сменить ключ и модель LLM и выкатить, одной командой.
#
# Существует потому, что однострочники с вложенными кавычками через ssh
# ломаются: shell уходит в незакрытую кавычку, введённое туда не доезжает до
# сервера, а секрет остаётся в истории команд. Здесь ключ читается без эха и
# не попадает ни в историю, ни в вывод, ни в список процессов.
#
# Запускать НА СЕРВЕРЕ:
#   cd /opt/sellico && ./scripts/set-llm.sh
#
# Ключ можно не менять — нажмите Enter, и останется прежний.
set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

if [ ! -f .env ]; then
  echo "Файл .env не найден в $(pwd)" >&2
  exit 1
fi

DEFAULT_MODEL="minimaxai/minimax-m3"

read -rsp "Новый ключ NVIDIA (Enter — оставить текущий): " NEW_KEY
echo
read -rp "Модель [$DEFAULT_MODEL]: " NEW_MODEL
NEW_MODEL="${NEW_MODEL:-$DEFAULT_MODEL}"

# Правка через python из переменных окружения: значение не появляется в
# аргументах команды, поэтому его не видно в `ps`.
NEW_KEY="$NEW_KEY" NEW_MODEL="$NEW_MODEL" python3 - <<'PY'
import os, pathlib, re

path = pathlib.Path(".env")
text = path.read_text()

def upsert(text: str, key: str, value: str) -> str:
    pattern = re.compile(rf"^{re.escape(key)}=.*$", re.M)
    if pattern.search(text):
        return pattern.sub(lambda _: f"{key}={value}", text, count=1)
    return text.rstrip("\n") + f"\n{key}={value}\n"

key = os.environ.get("NEW_KEY", "")
if key:
    text = upsert(text, "LLM_API_KEY", key)
    print("ключ обновлён")
else:
    print("ключ оставлен прежним")

text = upsert(text, "LLM_MODEL", os.environ["NEW_MODEL"])
path.write_text(text)
print("модель:", os.environ["NEW_MODEL"])
PY

unset NEW_KEY

echo
echo "Проверяю модель до выката..."
if ./scripts/llm-probe.sh "$NEW_MODEL"; then
  echo
  echo "Выкатываю..."
  ./scripts/deploy.sh update
else
  echo "Пробник не отработал — выкат не запускаю, .env уже обновлён." >&2
  exit 1
fi
