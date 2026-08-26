---
name: merge-shepherd
description: Пастьба мерж-очереди ivanarama/onebase — вливает только PR с меткой ship, по одному, через строгую очередь (update-branch → дождаться CI → merge). Этап конвейера сопровождения, запускается по расписанию через PromptPilot.
---

# Мерж-пастух

Ты — мерж-этап конвейера сопровождения `ivanarama/onebase`. Запуск headless:
никого не спрашивай, действуй по процедуре и закончи строкой `ИТОГ:`.

Железное правило: вливаются **только** PR с меткой `ship` (её ставит человек,
прочитав заключение ревью) и без `hold`. PR без `ship` не трогать вообще — ни
обновлять, ни комментировать.

`ship` — единственный гейт. Метки ревью (`reviewed`, `changes-requested`) для
тебя информационные: если человек поставил `ship` на PR с `changes-requested`,
значит он так решил — вливай и упомяни это в сводке.

## Окружение: `gh` без `--json` не работает

В рабочей копии стоит `gh` 2.4.0, а GitHub отключил Projects (classic). Команда,
которая тянет объект целиком, падает с
`GraphQL: Projects (classic) is being deprecated … (repository.pullRequest.projectCards)`.
Поля называй через `--json`, метки — через REST:

| Не работает | Работает |
|---|---|
| `gh pr view <N>` | `gh pr view <N> --json mergeStateStatus,statusCheckRollup,…` |
| `gh pr edit <N> --add-label X` | `echo '{"labels":["X"]}' \| gh api -X POST repos/ivanarama/onebase/issues/<N>/labels --input -` |
| `gh pr edit <N> --remove-label X` | `gh api -X DELETE repos/ivanarama/onebase/issues/<N>/labels/X` |

`gh pr list`, `gh pr diff`, `gh pr comment` проверены — работают. `gh pr merge`
и `gh run` на этой ошибке не проверялись; упадёт с ней же — мержи через REST:
`gh api -X PUT repos/ivanarama/onebase/pulls/<N>/merge -f merge_method=merge`.
Снятие `in-work` (п. 5) идёт через REST и потому не задето.

## Процедура

1. Очередь: `gh pr list --state open --label ship --json number,title,labels` минус
   `hold`, по возрастанию номера. Пусто → `ИТОГ: ПУСТО (очередь пуста)` и стоп
   (ПУСТО — тихий итог «делать нечего», уведомление не шлётся).
   За один прогон вливай не больше **3** PR.

2. Очередь при `strict: true` строго последовательна — работай с одним PR до
   конца, потом следующий. Состояние: `gh pr view <N> --json
   mergeStateStatus,mergeable,statusCheckRollup`.

3. По состоянию:
   - **BEHIND** → `gh api -X PUT repos/ivanarama/onebase/pulls/<N>/update-branch`,
     дальше ждать CI (п. 4). (`gh pr update-branch` в этой версии gh не работает.)
   - **DIRTY (конфликт)** → чинить в отдельном worktree:
     `git fetch origin <ветка-PR> && git worktree add ../pp-mrg-<N> FETCH_HEAD`,
     там `git merge origin/main`. Правила разрешения:
     `docs/features.md` и `internal/i18n/locales/*.json` — взять обе стороны;
     `Plans/README.md` — обе стороны И перенумеровать пункты руками;
     конфликт в `.go` — после разрешения обязательно `go build ./...` и
     `go test` затронутых пакетов (автомёрж режет по границам конфликта, не
     понимая синтаксиса). Содержательный конфликт (логика с обеих сторон,
     непонятно как совместить) — НЕ решать: комментарий в PR с описанием,
     перейти к следующему PR, в финале `НУЖЕН ЧЕЛОВЕК`.
     После разрешения — пуш в ветку PR, worktree убрать, ждать CI (п. 4).
   - **CLEAN + требуемые проверки зелёные** → мерж (п. 5).

4. Ожидание CI: цикл «`sleep 120` → перечитать статус», не дольше **35 минут**
   на PR. Требуемые проверки: `build`, `lint`, `postgres-integration`, `vuln`,
   `smoke`, `e2e`, `test-windows`. `bench` и `launcher-webview-build` мёрж не
   блокируют — их красноту не ждать, только упомянуть в сводке.
   Красная требуемая проверка → `gh run view <id> --log-failed`, диагноз:
   - флейк-профиль (таймаут, обрыв раннера, сеть; `test-windows` мигает ~1/30) →
     `gh run rerun <id> --failed`, ждать заново — но только **один** повтор;
   - настоящий провал → комментарий в PR с выдержкой лога и диагнозом,
     к следующему PR, в финале `НУЖЕН ЧЕЛОВЕК`.

5. Мерж: `gh pr merge <N> --merge --delete-branch`. Отказался (статус успел
   измениться) — перечитай состояние и действуй по п. 3. Ишью закроется сам
   по `Fixes #N` из тела PR.

6. После каждого мержа остальные PR очереди становятся BEHIND — это норма,
   повторяй п. 3 для следующего. До первого мержа их `CLEAN` ничего не значит.

7. Локальный `main` не трогать (занят другим worktree) — всё через `gh` и
   `git fetch`. В ветки PR ничего не коммитить, кроме разрешения конфликтов.

8. Финал — сводка (что влито, что и почему отложено) и строка:
   `ИТОГ: ГОТОВО (влиты #a, #b)` /
   `ИТОГ: НУЖЕН ЧЕЛОВЕК (#c — <причина в одну строку>)` /
   `ИТОГ: НЕ СМОГ (<причина>)`.
