---
name: framework-fix
description: Codex-адаптер правки каркаса OneBase для закрытия пробелов наполнения базы СППР: регистровые записей под ролью, обработка результатов целевого среза, канал форм и плана обмена, роль-scoped headless. Использовать по явной просьбе пользователя или при вызове $framework-fix.
---

# Правка каркаса (Codex)

Ты — разработчик каркаса. Полностью прочитай канон
`.claude/skills/framework-fix/SKILL.md` перед работой и следуй ему.

## Адаптация

1. Маршрут `CONTRIBUTING.md`, задачи, гейты и ИТОГ — канон.
2. Worktree: `git worktree add -b fix-<n>-framework /tmp/pr<n>
   origin/master`; один коммит на задачу, `gofmt -w . && go test ./...`
   перед коммитом.
3. Codex: `bash` для `go test ./internal/<пакет> -run <тест>`,
   `go test -run <префикс> -count=3`, `onebase check/test --project
   examples/sppr`; `apply_patch` для кода, тестов, `docs/`, `README.md`.
4. Права — REST (`internal/api/v2.go`), гейт этапов — слой хранения
   (`internal/storage/stage.go`); DSL прав не проверяет, headless `procrun`
   работает без контекста роли.
5. Каждое утверждение документации подтверди прогоном; неподтверждённое —
   дефект каркаса, правь в той же задаче. Тест обязан ловить смену формата.
6. Ограничения, которые не чинятся, — в `TRIAGE` и документацию.
   Draft PR: `gh pr create --draft --base master --head fix-<n>-framework
   --title "fix: ..." --body "..."`; трейлер `Generated-with: Codex`.
   Merge без метки `ship` и push в `master` запрещены.

## ИТОГ

Формат — канон. Адаптер добавляет: номер PR, состояние CI.
