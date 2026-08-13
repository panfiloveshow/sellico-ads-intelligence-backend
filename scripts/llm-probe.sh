#!/usr/bin/env bash
# llm-probe.sh — проверяет модели LLM на пригодность для ИИ-автопилота Ozon.
#
# Выбирать модель по названию нельзя: автопилот ФОРСИРУЕТ вызов функции
# (tool_choice: submit_proposals). Модель, которая этого не умеет или
# игнорирует форс, отвечает обычным текстом — решений не появляется вообще, и
# со стороны это неотличимо от «ИИ ничего не нашёл».
#
# Скрипт шлёт каждому кандидату тот же тип запроса, что и продакшен, и
# сообщает: вернулся ли вызов функции и за сколько секунд.
#
# Ключ читается из .env и НИКОГДА не печатается.
#
# Использование:
#   ./scripts/llm-probe.sh                      # список доступных моделей
#   ./scripts/llm-probe.sh model-a model-b ...  # проверить кандидатов
set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

if [ -f .env ]; then
  LLM_BASE_URL="$(grep -E '^LLM_BASE_URL=' .env | tail -1 | cut -d= -f2-)"
  LLM_API_KEY="$(grep -E '^LLM_API_KEY=' .env | tail -1 | cut -d= -f2-)"
fi
BASE="${LLM_BASE_URL:-https://integrate.api.nvidia.com/v1}"

if [ -z "${LLM_API_KEY:-}" ]; then
  echo "LLM_API_KEY не найден (ни в окружении, ни в .env)" >&2
  exit 1
fi

# Без аргументов — просто перечислить, что доступно аккаунту.
if [ $# -eq 0 ]; then
  echo "Доступные модели на $BASE:"
  curl -fsS "$BASE/models" -H "Authorization: Bearer $LLM_API_KEY" \
    | tr ',' '\n' | grep -oE '"id":"[^"]+"' | cut -d'"' -f4 | sort
  exit 0
fi

# Тот же контур, что в проде: одна функция + принудительный её вызов.
read -r -d '' TOOLS <<'JSON'
[{"type":"function","function":{
  "name":"submit_proposals",
  "description":"Вернуть список предложений по рекламе и краткое резюме",
  "parameters":{"type":"object","properties":{
    "summary":{"type":"string","description":"Резюме по кабинету на русском"},
    "proposals":{"type":"array","items":{"type":"object","properties":{
      "action_type":{"type":"string"},
      "new_value":{"type":"number"},
      "rationale":{"type":"string"}
    },"required":["action_type"]}}
  },"required":["summary","proposals"]}}}]
JSON

printf '%-45s %-10s %-8s %s\n' "МОДЕЛЬ" "ВЫЗОВ" "СЕК" "ПРИМЕЧАНИЕ"
for model in "$@"; do
  body=$(cat <<JSON
{"model":"$model",
 "messages":[
   {"role":"system","content":"Ты менеджер рекламы Ozon. Отвечай по-русски."},
   {"role":"user","content":"Кампания «Общ»: расход 31000 руб, выручка 116000 руб, ДРР 26.7%, цель 15%. Ставка 38 руб. Предложи действие."}],
 "tools":$TOOLS,
 "tool_choice":{"type":"function","function":{"name":"submit_proposals"}},
 "max_tokens":600}
JSON
)
  start=$(date +%s)
  resp=$(curl -sS --max-time 180 "$BASE/chat/completions" \
    -H "Authorization: Bearer $LLM_API_KEY" \
    -H "Content-Type: application/json" \
    -d "$body" 2>&1)
  elapsed=$(( $(date +%s) - start ))

  if echo "$resp" | grep -q '"tool_calls"'; then
    verdict="ДА"
    note="годится"
  elif echo "$resp" | grep -qi '"error"\|"detail"'; then
    verdict="ОШИБКА"
    # Текст ошибки печатаем, ключ в нём не фигурирует.
    note=$(echo "$resp" | tr ',' '\n' | grep -oE '"(message|detail|title)":"[^"]*"' | head -1 | cut -d'"' -f4)
  else
    verdict="НЕТ"
    note="ответила текстом, форс вызова функции проигнорирован"
  fi
  printf '%-45s %-10s %-8s %s\n' "$model" "$verdict" "$elapsed" "${note:-—}"
done
