# Этап 162 — Неизменяемая идентичность PR и изоляция fork-проверок

## Контекст

План связан с issue #1245. Независимый аудит показал два класса риска в
конвейере REVIEW/MERGE:

- результат ревью и разрешение на merge должны относиться не просто к номеру PR
  и имени ветки, а к точной идентичности base-репозитория, head-репозитория,
  head-ref и HEAD SHA;
- код из fork-PR недоверен сильнее текста PR: его нельзя исполнять в процессе,
  где доступны GitHub-токен, пользовательские credentials и запись вне
  одноразовой рабочей области.

Выбран рекомендованный вариант 2 из доверенного triage: сначала закрепить
identity-bound протокол для same-repo PR, затем сохранить автоматическую работу
с чистыми fork-PR через отдельный изолированный путь. Быстрое постоянное
ограничение «fork запрещены» не выбирается.

После triage в `main` уже появились важные части защиты, и реализация не должна
их переписывать:

- REVIEW берёт материал через `git fetch origin pull/<N>/head`, сверяет
  `FETCH_HEAD` с сохранённым SHA и создаёт detached worktree;
- review-claim/completion и `pp:head-reviewed` привязаны к SHA и server-ordered
  epoch; HEAD/base ABA, edit/delete комментариев и stale `ship` закрываются
  GraphQL-гейтами;
- MERGE делает финальный compare-and-merge с полем `sha`;
- `pipelinectl` выдаёт lease и повторяет гейты обычного REVIEW/CLEAN MERGE, а
  сложные base-sync/conflict/recovery отправляет в legacy fallback.

Оставшийся разрыв подтверждается текущими границами:

- `tools/pipelinehealth.apiPull.Head` содержит только `sha`, `Base` — только
  `ref`, а выдаваемый `candidate` не несёт repository/ref identity;
- GraphQL snapshots в legacy REVIEW/MERGE получают `headRefOid` и
  `baseRefName`, но не `PullRequest.id`, `repository.id`, `headRepository.id` и
  `headRefName` как одну неизменяемую запись;
- конфликтный MERGE всё ещё делает `git fetch origin <ветка-PR>` и CAS-push в
  `origin refs/heads/<ветка-PR>`. Для fork это либо не находит ветку, либо
  адресует одноимённую upstream-ветку;
- отдельного контракта исполнения недоверенного fork-кода нет. Detached
  worktree изолирует файлы Git, но не секреты, сеть и права процесса.

## Выбранная модель идентичности

### Канонический снимок

Каждая цель REVIEW/MERGE получает `PRIdentityV1`, построенный только из полей
GitHub REST/GraphQL, а не из тела PR или имени локального remote:

```text
pp-pr-identity-v1
base-repository-id=<GraphQL Repository.id>
base-repository=<owner/name для диагностики>
base-ref=<baseRefName>
pr-id=<GraphQL PullRequest.id>
pr-number=<decimal>
head-repository-id=<GraphQL Repository.id>
head-repository=<owner/name для диагностики>
head-ref=<headRefName>
head-sha=<40 lowercase hex>
cross-repository=<true|false>
```

Запись кодируется UTF-8 без BOM, без Unicode-нормализации, с LF между строками
и обязательным финальным LF. В digest входят точные Unicode code points,
возвращённые GitHub: в частности, `head-ref=ветка/проверка` остаётся
представимым и одинаково сериализуется на Windows и Linux. Невалидная UTF-8
последовательность или невозможность получить точное исходное значение закрывает
гейт; lossy-перекодирование и замена символов запрещены. SHA-256 точных байтов
этой записи называется `identity-sha256`. Поля `*-id`, `pr-id`, `head-ref`, `head-sha` и
`cross-repository` участвуют в сравнении; человекочитаемые `owner/name` тоже
входят в digest, чтобы rename/transfer не проходил незаметно. Пустой
`headRepository` (удалённый fork), отсутствующий ref/SHA, неожиданный base repo
или `base-ref != main` — fail closed.

`baseRefOid` хранится рядом в интеграционном снимке, но не входит в
`PRIdentityV1`: движение `main` само по себе не отменяет законченный
содержательный аудит текущего HEAD. Его совместимость с новым `main` по-прежнему
доказывается существующим strict-CI/base-sync контуром.

### Versioned proof

Новый протокол пишет только v2-маркеры. Заключение REVIEW перед строками
`Reviewed-SHA:` и `Reviewed-Identity-SHA256:` содержит fenced `text` block с
полной канонической записью `PRIdentityV1`. Это не диагностический дубль:
неизменяемая запись нужна downstream-потребителям FIX и TAIL, чтобы после push,
merge или удаления head-ref пересчитать digest и доказать историческую
identity, а не пытаться восстановить её из текущих mutable-полей PR.

Маркеры REVIEW:

```text
<!-- pp:review-claim-v2 head=<SHA> identity-sha256=<64hex> review-comment=<id> epoch-sha256=<64hex> -->
<!-- pp:head-reviewed-v2 head=<SHA> identity-sha256=<64hex> review-comment=<id> claim=<id> epoch-sha256=<64hex> -->
```

В человекочитаемом заключении рядом с `Reviewed-SHA:` появляется
`Reviewed-Identity-SHA256:`. Base-sync intent/done также получают v2 и тот же
`identity-sha256`; `to` меняет HEAD, поэтому done фиксирует новый identity digest,
вычисленный после update и повторного снимка.

FIX не переносит v2 proof на новый HEAD неявно. Коммит доработки и итоговый
комментарий образуют отдельный identity-bound переход:

```text
PP-Fix-Transition-v2: from=<reviewed SHA> from-identity-sha256=<64hex> review-comment=<id> claim=<id> epoch-sha256=<64hex>
<!-- pp:fix-pushed-v2 from=<reviewed SHA> from-identity-sha256=<64hex> head=<new SHA> identity-sha256=<64hex> review-comment=<id> claim=<id> epoch-sha256=<64hex> -->
```

`from-identity-sha256` обязан совпасть с записью в каноничном v2-заключении и
claim-bound completion. Перед CAS-push FIX проверяет старую identity и proof.
После push он строит новую `PRIdentityV1`: все поля, кроме `head-sha`, обязаны
побайтно совпасть со старой, `head-sha` обязан равняться фактически
отправленному commit, а его первый parent — `from`. Только затем разрешены
`pp:fix-pushed-v2` и снятие `changes-requested`. Сам transition не считается
новым REVIEW proof: новый HEAD снова проходит REVIEW и получает собственные
claim/completion. Любая смена repo/ref/base, чужой commit, неоднозначный parent
или несовпавший digest оставляет метку и закрывает recovery.

TAIL принимает v2 proof только вместе с точной identity-записью из указанного
review-comment. Он пересчитывает digest, проверяет review/earliest claim/
completion в одной server-ordered epoch и требует
`completion < MergedEvent(head-sha)`. Между anchor и merge запрещены изменения
HEAD/base/identity; после merge сохраняется существующее узкое исключение для
единственного конечного удаления head-ref без restore. Поэтому TAIL не зависит
от текущего существования ветки, но не может приписать хвост другому PR,
репозиторию или восстановленному ref.

Метка `ship` остаётся человеческим действием и не пытается хранить SHA внутри
GitHub label. Для v2 merge-authority возникает только у доверенного
server-ordered `LabeledEvent(ship)`, который идёт **после** identity-bound
completion текущей identity. Ранний `ship`, даже поставленный на том же SHA, не
переносится через v2 review или bridge: автоматика снимает stale-метку и ждёт
новой постановки человеком. Поэтому `ship(A) → rename/transfer A→B при том же
SHA → REVIEW(B)` наблюдаемо отличается от разрешённого
`REVIEW(B) → ship(B)`. Свежий финальный snapshot повторно подтверждает ту же
identity; любое последующее изменение repository/ref/SHA снова обнуляет
authority.

## Наблюдаемая семантика и инварианты

1. Один lease адресует ровно `{base repository, PR, head repository, head ref,
   head SHA}`. Команда `complete` отвергает изменение любого поля цели.
2. Номер PR разрешается только внутри ожидаемого base repository ID. Подмена
   конфигурации `owner/name` или работа с другим репозиторием не переиспользует
   proof.
3. Материал всегда fetch-ится через синтетический ref base-репозитория
   `refs/pull/<N>/head`, затем `FETCH_HEAD == head-sha` проверяется до worktree и
   до любого исполнения. Имя head-ветки не является источником кода.
4. Same-repo push/base-sync допустим только при точном совпадении
   `head-repository-id == base-repository-id`, `head-ref` из lease и удалённого
   SHA. Целевой refspec строится структурно, не из текста PR.
5. Fork-PR не вызывает локальные build/test-команды в процессе, где доступны
   GitHub credentials. Содержательный аудит читает diff; исполнение подтверждают
   GitHub-hosted checks, относящиеся к тому же `head-sha` и запущенные событием
   `pull_request`, а не `pull_request_target`.
6. CLEAN fork с полным identity proof, зелёными обязательными checks и свежим
   `ship` может быть слит тем же compare-and-merge `sha=<head-sha>`.
7. BEHIND/DIRTY fork автоматика не fetch/push-ит через `origin` и не пишет в
   репозиторий автора. Она ставит `needs-decision` с точной причиной: автору
   нужно обновить ветку, либо человеку — явно разрешить отдельный механизм.
8. Если fork-checks отсутствуют, относятся к другому SHA, используют
   `pull_request_target`, требуют секретов или их происхождение нельзя доказать,
   REVIEW завершается `needs-decision`, а не зелёным verdict.
9. Все pre-mutation гейты повторно получают полный `PRIdentityV1`, labels,
   proof и epoch одним стабильным GraphQL snapshot. REST используется для
   инвентаризации, но не заменяет финальный proof.
10. Текст PR, имена веток и комментарии остаются недоверенными данными и не
    могут менять политику изоляции.
11. Ни v1 proof, ни `ship`, поставленный до v2 completion, не дают права на
    merge. После bridge человек заново ставит `ship`; только это событие связано
    наблюдаемым порядком с новой identity.
12. FIX принимает владение только от каноничного v2 completion и связывает
    старую и новую identity явным `PP-Fix-Transition-v2`; совпадение одного SHA
    или имени ветки недостаточно для push и post-push recovery.
13. TAIL извлекает хвост только из review-comment, на который ссылается
    каноничный v2 completion merged HEAD, и проверяет сохранённую identity до
    каждой pre-create мутации.

## Инвентаризация затронутых границ

### Репозиторий onebase

- `pipelinectl.json` — минимальная версия identity-протокола и политика fork:
  ожидаемый base repository, `base_branch`, разрешённый CLEAN fork merge и
  запрет локального исполнения/branch mutation для fork.
- `.claude/skills/review-queue/SKILL.md`,
  `.claude/skills/fix-approved/SKILL.md`,
  `.claude/skills/merge-shepherd/SKILL.md` и
  `.claude/skills/tail-issues/SKILL.md` — краткие обязательные инварианты
  быстрого пути/потребителей proof и условия fallback.
- `.claude/skills/*/references/legacy-protocol.md` — полный v2 snapshot,
  exact-fetch, same-repo/fork маршруты, FIX transition, TAIL reconstruction,
  recovery и предмутационные гейты.
- `.agents/skills/review-queue/SKILL.md`,
  `.agents/skills/fix-approved/SKILL.md`,
  `.agents/skills/merge-shepherd/SKILL.md` и
  `.agents/skills/tail-issues/SKILL.md` — только тонкие Codex-адаптеры, без
  копирования логики.
- `tools/pipelinehealth/main.go` — чтение head/base repository identity и выдача
  её в `review_candidates`, `content_review_candidates`, `merge_candidates` и
  `merge_executable`; диагностика missing/deleted head repository.
- `tools/pipelinehealth/main_test.go` — fixture-тесты маршрутизации и сериализации
  identity.
- `internal/pipelinecontract/skills_test.go` — исполняемые тесты текста
  канонических процедур, форматов v2 и запрета опасного fork push/exec.
- `docs/maintenance-pipeline.md` и `CLAUDE.md` — пользовательское описание
  identity lease, границы `ship` и fork-политики.

### PromptPilot

Реализация `python -m promptpilot.project_pipeline` находится вне этого
репозитория. Её релиз должен получить структуру `PRIdentityV1`, canonical digest,
v2 parser/writer, стабильные GraphQL snapshots и fork policy до повышения
минимальной версии в `pipelinectl.json`. Обновлять только документацию onebase
без выпуска совместимого CLI запрещено: top-level skill иначе обещает гейт,
которого исполняемый путь не выполняет.

## Синтаксис / конфигурация

Предлагаемое расширение `pipelinectl.json`:

```json
{
  "protocol": {
    "minimum_version": 2,
    "pr_identity": "pp-pr-identity-v1"
  },
  "fork_policy": {
    "review": "diff-and-trusted-ci",
    "local_execution": "deny",
    "clean_merge": true,
    "branch_update": "human"
  }
}
```

`next review` возвращает identity как отдельный объект и его digest. Поле
`complete` несёт непрозрачный lease; модель не собирает команду из `head-ref`.
`complete review|merge` принимает только неизменённый lease и повторно получает
identity с GitHub.

## Хранилище / SQL

Схема пользовательской БД и продуктовые данные не меняются. Постоянное состояние
протокола остаётся в доверенных не редактируемых GitHub comments и timeline
events. Новые таблицы, миграции и generated artifacts не нужны.

## Совместимость и миграция

Переход выполняется dual-read/single-write без переноса старого merge-authority:

- v2-инструмент и все четыре этапа REVIEW/FIX/MERGE/TAIL всегда пишут v2;
- v1 SHA-bound completion временно читается только как доказательство уже
  выполненного содержательного аудита точного SHA для текущего same-repo PR;
  из него нельзя доказать прежние repository/ref, поэтому старый `ship` не
  переносится;
- v1 никогда не разрешает fork merge, fork base-sync или push;
- открытый same-repo PR с валидным v1 proof не требует повторного содержательного
  аудита, но REVIEW пишет bridge-комментарий и identity-bound v2 completion со
  ссылками на исходные review/claim/completion и новым identity digest; после
  этого stale `ship` снимается и merge ждёт нового человеческого
  `LabeledEvent(ship)`, строго более позднего v2 completion;
- неоднозначный bridge, отсутствующий repository ID, смена repo/ref при том же
  SHA до завершения bridge или любой edit/delete ведёт к новому REVIEW, а не к
  догадке;
- FIX не начинает новую доработку от v1 completion: same-repo proof сначала
  получает REVIEW bridge в v2. Уже опубликованный до cutover v1
  `PP-Fix-Transition` не переименовывается и не достраивается выдуманным v2
  digest; его новый HEAD безопасно возвращается в REVIEW. Незавершённая v2
  post-push фаза восстанавливается только при точном совпадении обеих identity;
- TAIL в течение релизного окна продолжает читать уже влитые каноничные v1
  пары по прежнему строгому epoch-гейту. Для всех новых REVIEW он принимает
  только v2 и сохранённую `PRIdentityV1`; bridge после merge не синтезируется;
- после одного релизного окна и отсутствия v1-кандидатов fallback сохраняет
  parser только для диагностики, но не как authority FIX/MERGE/TAIL.

Старые `pp:base-sync-*` цепочки не переписываются и не являются authority для
v2 merge. Same-repo цепочка может использоваться как диагностическая история и
получить v2 bridge только после свежего identity snapshot; затем всё равно нужен
новый человеческий `ship` после v2 completion. Fork и неоднозначная цепочка
fail closed. Это сохраняет аудит, не редактирует исторические записи и не
приписывает старому событию identity, которой в нём не было.

## Последовательность небольших PR-срезов

### Срез A — Identity schema и read-only инвентаризация

1. Добавить `PRIdentityV1` и canonical serializer в PromptPilot.
2. Расширить GraphQL/REST чтение полями base/head repository ID/name,
   `PullRequest.id`, `headRefName`, `headRefOid`, `baseRefName` и
   `isCrossRepository`.
3. Расширить `pipelinehealth` и JSON candidates, не меняя пока мутации.
4. Fail closed для null/deleted head repository и non-main base.

Публичные тесты среза:

- два PR с одинаковым `headRefName` в upstream и fork получают разные digest;
- изменение только head repo/ref при прежнем SHA меняет digest;
- golden fixture с `head-ref=ветка/проверка` даёт один и тот же byte-identical
  UTF-8 digest на Windows и Linux;
- non-main, удалённый fork и пустой SHA отсутствуют в executable queues и дают
  явную finding;
- порядок очереди не меняется от добавления identity-полей.

### Срез B — v2 REVIEW lease/proof для same-repo

1. Включить identity в `next review` lease и оба mutation-time snapshots.
2. Перейти на v2 claim/completion и exact `refs/pull/<N>/head` во всех путях.
3. Разрешить локальный прогон только после `cross-repository=false` и повторной
   проверки identity.
4. Добавить dual-read v1 и безопасный bridge.

Публичные тесты среза:

- push между `next` и `complete`, branch rename, transfer и ABA HEAD отвергают
  старый lease;
- одноимённая ветка `origin` не используется как материал;
- v1 same-repo proof мостится один раз, повтор recovery идемпотентен;
- `ship(A) → rename/transfer A→B при том же SHA → bridge(B)` снимает stale
  `ship` и ждёт нового события после v2 completion; сценарий
  `bridge(B) → ship(B)` разрешает следующий MERGE;
- сменившийся identity не публикует review comment/label.

### Срез C — v2-потребители FIX и TAIL

1. Научить FIX читать каноничный v2 REVIEW proof, повторять полный identity/
   epoch gate перед каждым внешним изменением и писать только
   `PP-Fix-Transition-v2`/`pp:fix-pushed-v2`.
2. Проверять CAS-переход `reviewed identity → новый HEAD identity`: неизменны
   base/head repository, PR и ref, меняется только SHA, а первый parent нового
   commit равен reviewed SHA.
3. Научить TAIL реконструировать сохранённую `PRIdentityV1`, v2 completion и
   merge edge перед каждым claim/create/done; сохранить ограниченный v1 drain
   только для уже влитой истории.
4. Обновить канонические процедуры FIX/TAIL, их fallback, PromptPilot handlers
   и исполняемые contract fixtures одновременно с parser-ами v2.

Публичные тесты среза:

- сквозной fixture REVIEW(v2 changes-requested) → FIX transition v2 →
  REVIEW(new HEAD) доказывает обе identity и не принимает старый completion за
  аудит нового HEAD;
- смена head repo/ref при прежнем SHA до CAS-push или post-push recovery
  запрещает comment/label mutation;
- чужой commit и transition с неверным parent/digest не снимают
  `changes-requested`;
- сквозной fixture REVIEW(v2 reviewed + tail) → MERGE → TAIL выбирает точный
  review-comment и создаёт хвост, а другой PR/identity с тем же SHA — нет;
- допустимый post-merge head delete не теряет хвост, restore и pre-merge
  lifecycle event закрывают TAIL;
- v1 in-flight FIX возвращается в REVIEW, а v1 merged TAIL drain остаётся
  ограничен миграционным окном.

### Срез D — v2 MERGE и безопасные same-repo mutations

1. Включить identity digest в обычный merge proof и base-sync v2 chain.
2. Перед update/push проверять `head-repository-id == base-repository-id`, точный
   ref и SHA.
3. Для conflict push использовать только структурно построенный exact refspec с
   lease; финальный merge оставить compare-and-merge по SHA.
4. Синхронизировать legacy fallback и contract tests.

Публичные тесты среза:

- stale HEAD/identity не переиспользует чужой `ship`; stale-метка снимается
  только после подтверждённого v2 completion, чтобы человек мог поставить её
  заново уже для текущей identity;
- ранний `ship` до v2 completion никогда не даёт merge-authority, даже при том
  же SHA;
- same-repo CAS loser ничего не перезаписывает;
- fork никогда не достигает команд `git fetch origin <headRefName>` и
  `git push origin ...`;
- retarget `main → release → main` требует нового proof.

### Срез E — Изолированный fork-маршрут

1. Для fork запретить локальное исполнение и проверять обязательные GitHub-hosted
   checks ровно для `head-sha`.
2. Проверять источник workflow: допустим `pull_request` без repository secrets;
   `pull_request_target` не считается доказательством безопасного исполнения
   fork-кода.
3. Разрешить CLEAN merge через base repository API при полном v2 proof.
4. BEHIND/DIRTY и недоказуемый CI передавать человеку без push в fork.

Публичные тесты среза:

- fake command runner фиксирует ноль локальных exec-вызовов для fork;
- зелёные checks другого SHA не принимаются;
- CLEAN fork проходит REVIEW → human `ship` → compare-and-merge;
- BEHIND/DIRTY fork получает один идемпотентный handoff и не захватывает
  интеграционную полосу.

### Срез F — Включение и удаление переходного authority

1. Выпустить PromptPilot с v2, затем поднять `minimum_version` в onebase.
2. Обновить канонические skills, адаптеры и maintenance-документацию.
3. Наблюдать одно релизное окно; проверить открытые v1 proofs и recovery.
4. Убрать v1 как merge-authority, оставив понятную диагностику.

Публичные тесты среза:

- старый CLI отклоняется до любой GitHub-мутации;
- новый CLI и fallback дают одинаковое решение на общей таблице fixtures;
- golden fixtures подтверждают byte-identical identity digest на Windows/Linux;
- `go run ./tools/pipelinehealth -json` не показывает protocol drift.

## Общая матрица тестов

Обязательны сценарии:

- same-repo, fork, удалённый fork, fork transfer/rename;
- одинаковое имя head-ветки в base и fork;
- stale HEAD до fetch, после аудита, перед label и перед merge;
- тот же SHA при изменившемся repo/ref;
- base `release`, retarget и base ABA;
- v1 → v2 bridge, crash после claim, повтор completion, stale `ship` до bridge
  и свежий `ship` после completion;
- REVIEW→FIX→REVIEW с двумя identity, crash до/после `pp:fix-pushed-v2`, чужой
  push и смена repo/ref при неизменном SHA;
- REVIEW→MERGE→TAIL с сохранённой identity-записью, post-merge delete и
  запрещённым restore;
- CLEAN/BEHIND/DIRTY для same-repo и fork;
- CI success/failure/pending, другой SHA и `pull_request_target`;
- Windows/Linux byte-identical UTF-8 без BOM и LF canonical serialization,
  включая Unicode `headRefName`.

Проверки репозитория для каждого среза: `go test ./tools/pipelinehealth`,
`go test ./internal/pipelinecontract`, `go run ./tools/pipelinehealth -json` на
fixture без внешних мутаций и `go run ./tools/plannum` при изменении планов.

## Риски и меры

- **Ложное ощущение sandbox.** Worktree не считается sandbox; fork-код либо
  исполняется GitHub-hosted `pull_request` CI, либо не исполняется автоматически.
- **Расхождение tool/fallback.** Общая таблица golden fixtures и contract tests
  обязательна для обеих реализаций и всех потребителей REVIEW/FIX/MERGE/TAIL.
- **Поломка открытых PR.** Dual-read ограничен same-repo и имеет одноразовый v2
  bridge; fork v1 fail closed.
- **Rename/transfer создаёт лишний повтор REVIEW.** Это намеренная цена: меняется
  адрес будущей мутации, хотя содержимое SHA прежнее.
- **Недоступный fork CI.** Автоматика не ослабляет гейт, а делает точный human
  handoff; автор может обновить PR или владелец принять решение.
- **Изменение внешнего PromptPilot раньше репозитория.** Включение разделено на
  выпуск CLI и последующий bump минимальной версии.

## Откат

До включения `minimum_version=2` новый CLI можно отключить без изменения GitHub
состояния. После включения откат разрешён только в fail-closed режим: остановить
автоматические REVIEW/MERGE и оставить диагностику v2. Возврат к v1 authority
запрещён, потому что уже созданные v2 proof могут относиться к identity, которую
v1 не умеет проверить. Исторические comments и labels не удаляются.

## Критерии завершения

- В fast и fallback пути lease/proof содержит один и тот же `identity-sha256`.
- FIX и TAIL умеют независимо реконструировать тот же v2 proof: FIX связывает
  две identity через проверяемый transition, TAIL связывает reviewed identity с
  точным merged event.
- Ни один путь не получает код по одному `headRefName` и не push-ит fork через
  `origin`.
- Изменение repo/ref/SHA/base identity до любой мутации останавливает операцию.
- Fork-код не исполняется локально с credentials; CLEAN fork остаётся
  автоматически ревьюируемым и сливаемым после доверенного CI и `ship`.
- BEHIND/DIRTY fork получает детерминированный human handoff без изменения его
  ветки.
- Все перечисленные сценарные тесты зелёные на Windows и Linux, а старый CLI
  fail closed до GitHub POST/PUT/DELETE.
- Документация честно описывает границу `ship`: label остаётся человеческим
  разрешением, но merge-authority возникает только у нового события после
  свежего v2 proof той же identity.

## Эстимейт

- Срез A: 1–1,5 дня.
- Срез B: 2–3 дня.
- Срез C: 2–3 дня.
- Срез D: 2–3 дня.
- Срез E: 2–3 дня.
- Срез F и миграционное наблюдение: 1–2 дня плюс одно релизное окно.

Итого: 10–16 рабочих дней, без изменения продуктового кода и пользовательских
данных.
