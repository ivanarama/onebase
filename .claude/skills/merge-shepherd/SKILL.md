---
name: merge-shepherd
description: Безопасное слияние PR ivanarama/onebase через детерминированный pipelinectl с fallback для base-sync, конфликтов и recovery.
---

# MERGE

Обрабатывай только PR с `ship` и без `hold`/`needs-decision`. Решение человека
старше сохранённого состояния.

## Обычный путь

Если задача PromptPilot уже содержит команду `pipelinectl`, выполни её. При
ручном запуске используй Python окружения PromptPilot:

```powershell
python -m promptpilot.project_pipeline --config pipelinectl.json next merge
```

Разбери поле `action`:

- `merge` — выполни показанную команду `complete merge` с неизменённым `lease`;
- `wait` или `empty` — ничего не меняй и закончи `ИТОГ: ПУСТО`;
- `fallback` — полностью прочитай
  [references/legacy-protocol.md](references/legacy-protocol.md) и продолжи по нему;
- `error` — закончи `ИТОГ: НЕ СМОГ`, не обходя отказ вручную.

`pipelinectl` берёт быстрый путь только для `CLEAN` PR с каноничным обычным
review-proof, новым trusted `ship` и зелёными обязательными проверками. Перед
compare-and-merge он повторяет стабильный GraphQL snapshot, HEAD/label/proof и
CI-гейты. Base-sync, carry, legacy re-ship, конфликт и recovery всегда уходят в
полную процедуру.

После успешного merge `pipelinectl` также распознаёт plan-PR с соседними
строками `Plan-Issue: #N` и `Plan-Path: Plans/NNN-slug.md`. Он проверяет, что
issue открыта и сохраняет `approved` + `plan-in-review`, публикует
`pp:plan-ready`, добавляет `ready-fix`, затем снимает `plan-in-review` и
`needs-decision`. Это завершение уже одобренного PLAN-handoff, а не новое
решение за человека. Если post-merge handoff не завершился, сообщи настоящий
блокер: влитый план не даёт права молча оставить issue вне FIX.

Только `action=completed` означает полезную мутацию и допускает
`ИТОГ: ГОТОВО`. Наблюдение или ожидание без изменений — `ИТОГ: ПУСТО`.
