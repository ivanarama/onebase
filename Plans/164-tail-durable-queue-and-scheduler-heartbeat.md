# Этап 164 — Долговечная очередь TAIL и heartbeat планировщика

Дата проектирования: 2026-09-08.
Статус: 📋 **проектирование**, код не начат.
Уровень: **конвейер сопровождения** — discovery и recovery TAIL, внешний
PromptPilot, диагностика `pipelinehealth` и операторская документация.
Заявка: [#1248](https://github.com/ivanarama/onebase/issues/1248).
Выбранный вариант: **1** — сначала отдельный план, затем durable
backlog/cursor, расписание TAIL и heartbeat всех scheduled stages. Вариант
зафиксирован последующим комментарием владельца; plan-PR не меняет исполняемый
контракт и не включает продуктовый код.
Связано с: PR [#1148](https://github.com/ivanarama/onebase/pull/1148), который ввёл
TAIL, и PR [#1261](https://github.com/ivanarama/onebase/pull/1261), задающим
границу committed REVIEW protocol.

## Контекст

TAIL сегодня не имеет расписания и ищет только PR, влитые за последние 14 дней.
За один запуск он берёт не больше пяти PR. `pp:tail-done` надёжно предотвращает
повтор уже разобранного review, а item-level lease/dedupe защищают создание
issue, но эти протоколы начинают работать только после того, как PR попал в
выборку. Если очередь росла быстрее ручных запусков или планировщик молчал две
недели, необработанный review исчезает из discovery навсегда.

Вторая половина проблемы — наблюдаемость. Руководство говорит, что тишина в
Telegram означает пустую очередь, хотя `ИТОГ: ПУСТО` не уведомляется и
отсутствующий запуск выглядит точно так же. `tools/pipelinehealth` показывает
живые очереди FIX/PLAN/REVIEW/MERGE, но не читает merged PR для TAIL и не знает
фактов запуска PromptPilot. Его поле `scheduler` сейчас обозначает лишь строку
алгоритма сортировки `two-lane-safety-priority-aging-depth-number`, а не здоровье
планировщика.

Нужно разделить три состояния:

1. GitHub хранит авторитетный review proof, `pp:tail-*` markers и результат;
2. PromptPilot хранит восстанавливаемый индекс discovery: cursor, backlog и
   run/lease state;
3. `pipelinehealth` читает безопасный snapshot PromptPilot и показывает
   backlog и свежесть запусков, но никогда не превращает диагностический cache
   в разрешение на GitHub-мутацию.

Пользовательские БД, runtime OneBase и прикладные конфигурации не затрагиваются.

## Наблюдаемая семантика

### Очередь TAIL не истекает

- Каждый PR, который достиг `main` после точки bootstrap, однажды попадает в
  durable discovery независимо от возраста и количества последующих merge.
- Cursor означает только «все элементы до этой границы импортированы в ledger».
  Он не означает «хвост обработан» и не удаляет backlog.
- Ключ backlog — `(repository, pr, review-comment, review-updated)`. Изменённая
  версия review не поглощается старой записью.
- `pp:tail-done`, `pp:tail-item-done`, `pp:tail-drop`, `hold`, `no-tail` и
  committed REVIEW proof по-прежнему проверяются непосредственно на GitHub.
  Ledger не может сделать невалидный PR допустимым.
- Один запуск обрабатывает максимум пять PR, но всегда сообщает общее число
  `pending`, выбранные номера, остаток и возраст самого старого элемента.
- Повторный запуск после crash либо продолжает активную lease, либо после её
  expiry повторно сверяет GitHub и безопасно подбирает запись. Уже созданные
  issue находятся существующим `pp:tail-source`; неидемпотентный create не
  повторяется.

### Явное состояние планировщика

Для каждой реально настроенной recurring task публикуются:

- стабильные `task_id`, `stage` и версия конфигурации расписания;
- `schedule`, `next_due_at`, `last_started_at`, `last_finished_at` и
  `last_success_at`;
- `run_id`, последний канонический `ИТОГ:`, длительность и число подряд
  неуспешных запусков;
- для очередных этапов — `selected`, `remaining` и возраст старейшего элемента.

`ИТОГ: ПУСТО` считается успешным heartbeat и обновляет `last_success_at`, хотя
остаётся тихим в Telegram. Отсутствие завершённого запуска после
`next_due_at + grace` становится `overdue`, а не «пусто». Ошибка, timeout,
неразобранный финальный verdict и отключённая task различаются. Alert отправляет
один сигнал при переходе в `overdue/failed` и один recovery-сигнал после нового
успеха; каждый tick не создаёт дубликат сообщения.

TAIL получает расписание раз в четыре часа. При лимите пять PR это даёт
ёмкость 30 PR/сутки против зафиксированных в текущем контракте 244 merge за
14 дней (примерно 17,5/сутки). Накопленный backlog остаётся видимым; повышение
лимита не используется как замена durable state.

### Публичный диагностический контракт

`pipelinehealth -json` сохраняет существующее строковое поле `scheduler` для
совместимости и добавляет отдельные структуры:

```json
{
  "tail": {
    "state": "ready|running|blocked|unknown",
    "cursor_main_sha": "<40hex>",
    "pending": 7,
    "selected": [1301, 1304, 1310, 1312, 1318],
    "oldest_merged_at": "<RFC3339>",
    "remaining": 2
  },
  "scheduled_stages": [
    {
      "task_id": "<stable id>",
      "stage": "tail-issues",
      "health": "ok|overdue|failed|disabled|unknown",
      "last_success_at": "<RFC3339>",
      "next_due_at": "<RFC3339>"
    }
  ]
}
```

Live-запуск без доступного scheduler snapshot возвращает `unknown` и понятное
yellow-наблюдение. Он не рисует ложный `ok`. Fixture-режим остаётся полностью
offline и принимает scheduler/tail fixtures явно.

## Durable-модель PromptPilot

### Cursor и backlog

PromptPilot хранит записи в своей транзакционной БД, а не в рабочей копии:

- `tail_cursor_v1`: repository, base branch, последний импортированный SHA
  первого родителя `main`, generation и время commit;
- `tail_backlog_v1`: ключ версии review, PR/merge identity, состояние
  `pending|leased|blocked|done`, lease owner/expiry, timestamps и последняя
  причина reconciliation;
- `scheduled_run_v1`: task/run identity, planned/start/finish/success timestamps,
  parsed verdict, counters и alert transition.

Одна транзакция upsert-ит весь обнаруженный batch и только затем двигает cursor.
Crash до commit оставляет старый cursor; повторный импорт идемпотентен по ключу.
Два discovery worker сериализуются CAS по `(cursor SHA, generation)` либо одной
DB lease. Проигравший перечитывает состояние, а не публикует собственную
границу.

Cursor — ускоритель, не единственная копия истины. Если сохранённый SHA больше
не является предком текущего `main`, отсутствует либо ledger повреждён,
PromptPilot прекращает выдавать новые TAIL mutations и запускает full
reconciliation от bootstrap boundary. Нельзя молча переставить cursor на HEAD.

### Discovery по истории `main`

Инкрементальный проход делает exact fetch `main`, сохраняет его SHA и идёт по
first-parent commits от cursor exclusive до сохранённого HEAD inclusive. Для
каждого commit GitHub endpoint association `commits/<sha>/pulls` даёт PR;
повторения от squash/rebase и несколько commits одного PR дедуплицируются по
номеру. Затем обязательны `merged_at != null`, `base.ref == main` и полный
пагинированный comments/proof gate текущего TAIL.

На репозитории разрешены merge, squash и rebase, поэтому текст merge commit и
наличие второго parent не являются доказательством PR. Direct commit без
ассоциированного PR записывается в scan-аудит и не становится TAIL candidate.
Текущий HEAD меняется только следующим циклом: cursor коммитит ровно тот
сохранённый HEAD, который был полностью импортирован.

Bootstrap начинается с первого родителя merge PR #1148:
`d48f2ed9ce7161ea6be2a6e861608ce207e62811` → merge
`9eefa4055d4ed7990362463f1c8c28f829c7808c`. Это первая версия TAIL в истории,
поэтому более ранние PR не могли содержать его контракт. Первый проход
перечисляет всю историю от этой границы до snapshot HEAD, а не использует
14-дневный фильтр. Уже завершённые версии помечаются `done`, остановленные
человеком — `blocked`, остальные — `pending`.

Периодическая контрольная сверка повторяет весь диапазон от bootstrap и
сравнивает множество version-key с ledger. Она нужна для восстановления после
утраты внешней БД и проверки cursor, но не запускает issue creation сама.

### Claim и completion scheduled run

PromptPilot добавляет детерминированные команды уровня проекта, условно:

```text
project_pipeline next tail
project_pipeline complete tail --run <id> --verdict <file>
project_pipeline scheduler status --json
```

`next tail` в одной транзакции выбирает до пяти oldest-first записей
(`merged_at`, затем PR number), ставит ограниченную lease и возвращает
machine-readable allowlist вместе с `remaining`. Модель работает только с этим
allowlist, но перед каждым GitHub POST повторяет все canonical gates из
`tail-issues`; lease не заменяет trust boundary.

`complete tail` не верит тексту модели как доказательству. Для `ГОТОВО` и
`УЖЕ СДЕЛАНО` он перечитывает GitHub и требует подходящий versioned
`pp:tail-done` либо полную item-level completion; для `ПУСТО` проверяет, что
выданный allowlist действительно пуст. Несошедшийся итог оставляет запись для
recovery и помечает run failed/blocked.

## Heartbeat и атомарный health snapshot

PromptPilot обновляет `scheduled_run_v1` в той же транзакции, которая завершает
run. `last_success_at` двигается только после валидного канонического verdict и
stage-specific read-back; простой exit code 0 недостаточен. `started` без
`finished` после runtime timeout становится `failed`, но сохраняет run id для
диагностики.

Read-only snapshot экспортируется атомарно: временный файл, `fsync`, rename.
Формат versioned (`pp-scheduler-health-v1`), содержит `generated_at`, task
generation и перечисление всех активных recurring tasks из реестра
PromptPilot. `pipelinehealth` принимает путь отдельным флагом и проверяет schema,
RFC3339, уникальность task/stage, монотонность timestamps и свежесть самого
snapshot. Неизвестная версия, частичная запись или пропавшая configured task
дают yellow/red finding, а не zero values.

Grace задаётся рядом с желаемым расписанием в `pipelinectl.json`, а не вшивается
в Go. Для обычной task по умолчанию это два интервала плюс максимальная
длительность запуска; для TAIL — те же два интервала. PromptPilot UI и Telegram
используют тот же reducer и те же пороги, что JSON snapshot, чтобы dashboard и
уведомления не расходились.

## Инварианты и trust boundary

1. GitHub proof и protocol markers остаются единственным разрешением на
   GitHub-мутацию; PromptPilot ledger — восстанавливаемый индекс.
2. Cursor двигается только атомарно вместе со всеми backlog upsert до snapshot
   HEAD. Пустой batch тоже сохраняет проверенную границу.
3. Ни возраст PR, ни размер backlog не исключают запись из очереди.
4. Один version-key одновременно имеет не больше одной активной lease; expiry
   разрешает recovery, но не отменяет GitHub lease/dedupe gate.
5. Полный stable GraphQL REVIEW epoch, UTF-8 round-trip, author check и
   pre-mutation re-check текущего TAIL не ослабляются и не дублируются упрощённым
   REST-cache.
6. Cursor rewind, force-push/non-ancestor и conflicting PR association дают
   fail closed до full reconciliation.
7. Heartbeat успеха появляется только после финального канонического `ИТОГ:` и
   stage-specific read-back; старт процесса не считается успехом.
8. `ПУСТО` — успешный тихий run, `unknown/overdue/failed` — не пустая очередь и
   не замалчиваются монитором.
9. Тексты issue/PR/comments остаются недоверенными данными и не могут менять
   schedule, cursor, lease TTL или health threshold.
10. Все timestamps сравниваются как RFC3339 UTC, порядок GitHub lifecycle — по
    server-ordered GraphQL edges, не по числовым REST ids.

## Инвентаризация затронутых границ

- `.claude/skills/tail-issues/SKILL.md`: заменить 14-дневный discovery на
  allowlist durable queue, сохранить весь proof/mutation protocol, определить
  explicit remainder и recovery.
- `.agents/skills/tail-issues/SKILL.md`: только тонкий Codex-адаптер новой
  команды, без копирования логики.
- `pipelinectl.json`: желаемое расписание TAIL, batch size, bootstrap boundary,
  capability `tail-ledger-v1`, health grace и путь/команда snapshot.
- `tools/pipelinehealth/main.go`: tail backlog и scheduler health в JSON/тексте,
  строгий reader versioned snapshot; существующая строка `scheduler` остаётся.
- `tools/pipelinehealth/main_test.go`: offline fixtures backlog, cursor и
  fresh/stale/failed/unknown heartbeat.
- `internal/pipelinecontract/skills_test.go`: обязательность durable allowlist,
  запрет age-window как источника истины, сохранение UTF-8/trust/re-check gates
  и совпадение руководства.
- `docs/maintenance-pipeline.md`: удалить обещания «тишина = пусто» и «окно не
  теряет», описать расписание, heartbeat, backlog и операторский recovery.
- внешний `promptpilot.project_pipeline`: ledger migrations, discovery,
  transactional claim/completion, recurring task TAIL, health reducer,
  snapshot и Telegram transitions. Исходники не входят в onebase; совместимый
  релиз обязателен до включения capability в конфиге.

Общие grammar/reducer fixtures для `tail-ledger-v1` и
`pp-scheduler-health-v1` хранятся в
`internal/pipelinecontract/testdata/scheduler-v1/*.json` как JSON golden vectors.
PromptPilot и `pipelinehealth` обязаны прогонять одни и те же vectors; текст
скила не становится четвёртой независимой реализацией state machine.

## Совместимость и миграция

Схема пользовательской БД и продуктовые API не меняются. Старые `pp:tail-*`
comments, refs и созданные issues не редактируются и не удаляются. Импорт
распознаёт их действующими по прежним правилам.

Переход выполняется в таком порядке:

1. выпустить PromptPilot с ledger/snapshot и capability выключенной;
2. выполнить bootstrap от parent PR #1148 до сохранённого `main`, сверить
   pending/done/blocked и отдельно показать все ambiguous записи;
3. дважды прогнать reconciliation и получить одинаковые cursor/backlog hashes;
4. включить read-only отображение в `pipelinehealth`, не меняя TAIL;
5. создать recurring task TAIL раз в четыре часа и добиться первого успешного
   heartbeat в dry-run без GitHub mutations;
6. включить `tail-ledger-v1`, после чего skill принимает только выданный
   allowlist; 14-дневный запрос остаётся временным аварийным read-only
   сравнением и затем удаляется;
7. включить overdue/recovery alerts после одного полного интервала наблюдения.

Если PromptPilot старый либо snapshot недоступен, scheduled TAIL fail closed и
не возвращается к истекающему окну как к доказательству пустоты. Человек может
запустить documented full reconciliation; текущий GitHub item protocol остаётся
пригодным для ручного восстановления.

## Нарезка реализации на небольшие PR-срезы

### Срез A — versioned contract и offline diagnostics

В OneBase добавить JSON schema/golden fixtures cursor/backlog/heartbeat,
расширить `pipelinehealth` fixture mode и contract tests. Обновить документы так,
чтобы отсутствие snapshot было `unknown`, но пока не менять scheduled TAIL.

Публичная приёмка: `go run ./tools/pipelinehealth -json` на fixtures различает
нулевой backlog, остаток после batch, просроченный stage, failed run и recovery;
невалидный/частичный snapshot не даёт `green`.

### Срез B — durable discovery и reconciliation в PromptPilot

Добавить миграции ledger, first-parent/commit-association discovery,
транзакционный cursor, full bootstrap от PR #1148 и команды `next/complete
tail`. Подключить общие golden vectors, fault injection и две конкурирующие
discovery/claim сессии. GitHub mutations на этом срезе выключены capability.

Публичная приёмка: fake GitHub с необработанным PR старше 14 дней выдаёт его в
`next tail`; crash до/после DB commit и второй worker приводят к одному backlog
key и одному cursor. Утрата БД восстанавливается full reconciliation.

### Срез C — scheduled TAIL и handoff существующему mutation protocol

Добавить recurring task раз в четыре часа, machine allowlist в PromptPilot
prompt, проверяемый `complete tail` и обновить canonical skill/adapter.
Существующие GraphQL, lease, dedupe, UTF-8 и read-back gates не сокращать.

Публичная приёмка: fixture из шести старых pending PR обрабатывает ровно первые
пять, сообщает `remaining=1`, следующий run берёт шестой; crash после issue
create восстанавливает item-done без второго issue.

### Срез D — heartbeat, alerts и rollout

Включить run lifecycle для всех configured recurring tasks, atomic health
snapshot, `pipelinehealth` findings и deduplicated Telegram overdue/recovery
alerts. Провести bootstrap/double reconciliation, dry-run и затем включить
`tail-ledger-v1`.

Публичная приёмка: остановленная fake task после grace даёт один overdue alert,
пустой успешный run обновляет `last_success_at` без обычного Telegram message,
а следующий успех после outage даёт один recovery. Dashboard, snapshot и CLI
показывают одинаковый статус.

## Тесты и проверки

- table-driven schema tests: unknown version, duplicate task/key, invalid SHA,
  non-UTC/RFC3339, finish before start, success before finish, stale snapshot;
- discovery fixtures: merge/squash/rebase association, direct commit, старый
  open PR, merged PR старше 14 дней, несколько commits одного PR и смена HEAD
  во время scan;
- cursor tests: atomic batch+cursor, retry после crash, competing CAS, empty
  batch, non-ancestor, missing commit и full rebuild;
- backlog reducer: review edit/version-key, already done, item-done without
  tail-done, hold/no-tail/drop, invalid proof, lease expiry и deterministic
  oldest-first batch;
- интеграция fake GitHub: весь текущий committed REVIEW proof и TAIL mutation
  protocol до exact read-back, включая fault injection перед каждой мутацией;
- scheduler fake clock: first run, `ПУСТО`, timeout, malformed/no verdict,
  overdue boundary, disabled task, alert dedupe и recovery;
- `pipelinehealth` fixtures и text/JSON golden output для `ok`, `unknown`,
  `overdue`, backlog remainder и cursor rebuild;
- contract tests canonical skill, Codex adapter и maintenance guide;
- обязательные проверки репозиторных срезов:
  `go test ./tools/pipelinehealth ./internal/pipelinecontract`, затем
  `go test ./...`, `go build ./...`, `go run ./tools/plannum`;
- PromptPilot отдельно прогоняет собственные unit/integration tests и общие
  JSON golden vectors до объявления capability.

Тест считается приёмочным только через публичный `pipelinehealth` или
`project_pipeline next/complete tail` над fake GitHub и scheduler store. Прямой
вызов приватного reducer не заменяет сквозную проверку.

## Риски и откат

- **Cursor перескочил необработанный merge.** Batch и cursor коммитятся одной
  транзакцией; non-ancestor запускает полный rebuild, а периодическая сверка
  сравнивает множества ключей.
- **Squash/rebase или ручной merge не распознан.** Discovery использует GitHub
  commit association и дедупликацию, а не форму commit message/число parents.
- **Внешний ledger разрешил stale mutation.** Он выдаёт только allowlist;
  canonical stable proof и pre-mutation gates перечитываются с GitHub.
- **Два scheduled worker.** DB CAS/lease выбирает одного владельца, а текущий
  GitHub lease/dedupe остаётся вторым независимым барьером.
- **Bootstrap создаёт дубли.** До включения mutations весь диапазон проходит
  dry-run; существующие source/item/done markers импортируются, ambiguous
  записи требуют человека.
- **Heartbeat шумит.** Alerts привязаны к переходу состояния и task generation,
  используют grace и отдельный recovery, а не сообщение на каждый tick.
- **Snapshot утёк/подменён.** В нём нет токенов и тел GitHub; schema и атомарная
  запись обязательны. Он диагностический и не даёт mutation authority.
- **Расходятся OneBase и PromptPilot.** Capability включается только после
  общих golden vectors и dry-run; неизвестная версия fail closed.

Откат: выключить `tail-ledger-v1` и recurring mutation task, сохранив ledger,
snapshot и GitHub markers для анализа. Не откатывать cursor наугад и не удалять
backlog/comments/refs. Разрешён ручной full reconciliation и существующий
item-level recovery. Heartbeat monitor можно перевести в read-only без потери
истории; обещание «тишина = пусто» не возвращается.

## Verification

1. Развернуть чистый scheduler store и bootstrap от parent merge PR #1148 до
   фиксированного HEAD; сохранить counts и hash ledger.
2. Повторить full reconciliation: counts/hash совпадают, новых записей нет.
3. Подмешать merged PR старше 14 дней без `pp:tail-done`: он появляется в
   `pipelinehealth.tail.pending` и `next tail`.
4. Подмешать шесть pending PR: первый run выбирает пять, явно показывает один
   остаток; второй завершает шестой.
5. Прервать discovery до commit, после backlog upsert и при cursor CAS; каждый
   restart приходит к одному cursor и набору ключей.
6. Прервать TAIL после каждого GitHub POST, включая issue create; повторный run
   не создаёт дубль и завершает marker chain.
7. Смоделировать edit/delete proof, `hold`, `no-tail`, force-push/non-ancestor
   main и conflicting association: mutations отсутствуют, причина видна.
8. Остановить каждую scheduled stage за grace: статус становится overdue и
   приходит один alert; пустой успешный запуск восстанавливает heartbeat.
9. Сверить PromptPilot UI, Telegram transition и
   `go run ./tools/pipelinehealth -json` — task ids, timestamps, backlog и
   состояния совпадают.
10. После одного dry-run интервала включить capability на реальном TAIL и
    убедиться, что старейший backlog уменьшается, cursor продвигается, а
    `remaining` никогда не скрыт.

## Эстимейт

- срез A: 2–3 дня;
- срез B: 4–6 дней;
- срез C: 2–3 дня;
- срез D: 3–4 дня, включая staged rollout и наблюдение одного интервала.

Итого: 11–16 рабочих дней. Оценка включает внешний релиз PromptPilot,
миграцию состояния, fake-clock/fault-injection и совместный rollout.

## Готово, когда

- необработанный trusted review не исчезает из TAIL из-за возраста или лимита;
- cursor/backlog переживают crash, конкурентный запуск и полную утрату cache без
  повторного создания issue;
- TAIL запускается каждые четыре часа и всегда публикует явный остаток;
- `pipelinehealth` показывает durable TAIL backlog и свежесть всех configured
  scheduled stages, а отсутствие snapshot не выглядит зелёным;
- `ПУСТО` обновляет heartbeat, но тишина больше нигде не документирована как
  доказательство пустой очереди;
- overdue/failed и recovery дают по одному согласованному сигналу;
- canonical trust boundary, GraphQL proof, UTF-8 byte round-trip, per-mutation
  re-check и item-level atomicity TAIL сохранены без ослабления;
- общий набор golden vectors проходит в OneBase и PromptPilot;
- bootstrap от PR #1148 и повторная reconciliation дают одинаковый ledger, а
  реальный scheduled run уменьшает старейший backlog без потерь и дублей.
