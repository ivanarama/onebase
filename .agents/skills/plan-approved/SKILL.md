---
name: plan-approved
description: "Создание отдельного технического plan-PR для одобренных issues ivanarama/onebase с меткой plan-needed. Использовать для очереди PLAN или вызова $plan-approved, при необходимости с номером issue."
---

# Codex-адаптер этапа PLAN

1. До действий полностью прочитать каноническую процедуру [`.claude/skills/plan-approved/SKILL.md`](../../../.claude/skills/plan-approved/SKILL.md).
2. Сначала применять `AGENTS.md`, затем совместимые соглашения `CLAUDE.md` и `Plans/README.md`.
3. `$plan-approved [N]` — эквивалент `/plan-approved <N>`. Номер не обходит eligibility-гейт.
4. Перед GitHub-мутациями проверить `gh` и логин `ivanarama`; непосредственно перед записью перечитать issue/PR.
5. Создавать только plan-PR в изолированном worktree. Не писать продуктовый код, не ставить `ship`, не сливать PR и не запускать FIX.
6. На Windows сохранять UTF-8-процедуру, `Get-Content -Encoding UTF8 -Raw`, проверку через `@base64` и сравнение байт-в-байт.
7. В коммите использовать `Generated-with: Codex`. Сохранять приоритеты, маркеры, переходы меток и точный формат `ИТОГ:` канонической процедуры.

Логика конвейера живёт в канонической процедуре; этот файл — только адаптер Codex.
