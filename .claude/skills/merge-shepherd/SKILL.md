---
name: merge-shepherd
description: Пастьба мерж-очереди ivanarama/onebase — вливает только PR с меткой ship, по одному, через строгую очередь (update-branch → дождаться CI → merge). Этап конвейера сопровождения, запускается по расписанию через PromptPilot.
---

# Мерж-пастух

Ты — мерж-этап конвейера сопровождения `ivanarama/onebase`. Запуск headless:
никого не спрашивай, действуй по процедуре и закончи строкой `ИТОГ:`.

Железное правило: обрабатываются **только** PR с меткой `ship` (её ставит
человек, прочитав заключение ревью) и без `hold`/`needs-decision`. PR без `ship`
или с любым стопом не трогать вообще — ни обновлять, ни комментировать.

`ship` — разрешение человека, но `hold` и `needs-decision` старше него. Метки
ревью (`reviewed`, `changes-requested`) для тебя информационные: если человек
поставил `ship` на PR с `changes-requested` и стопов нет, значит он так решил —
вливай и упомяни это в сводке.

## UTF-8 — инвариант до первой мутации

На Windows **до чтения любого файла** настрой PowerShell и только затем читай
`CLAUDE.md`, этот скил и данные, из которых строится человекочитаемый текст:

```powershell
$utf8 = [Text.UTF8Encoding]::new($false)
[Console]::InputEncoding = $utf8
[Console]::OutputEncoding = $utf8
$OutputEncoding = $utf8
Get-Content -LiteralPath <path> -Encoding UTF8 -Raw
```

Голый `Get-Content` запрещён: Windows PowerShell может принять UTF-8 без BOM за
Windows-1251 и превратить `Триаж` в `РўСЂРёР°Р¶`. Перед POST проверь видимый
текст обратным строгим преобразованием Windows-1251 → UTF-8; если оно даёт
другой валидный текст, это mojibake — остановись **до любой GitHub-мутации**.

После POST человекочитаемого комментария запроси его `.body` через jq `@base64`,
декодируй байты как UTF-8 и сравни байт-в-байт с отправленным телом. Пока точное
совпадение не доказано, не меняй метки и не публикуй следующий protocol marker.
Консольное отображение само по себе не считается проверкой.

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

`gh pr list`, `gh pr diff`, `gh pr comment`, `gh run view` и
`gh run rerun` в 2.4.0 используют явные поля либо REST и не затрагивают
`projectCards`; применяй команды из процедуры ниже. Проблема относится именно к
вызовам без явного набора полей (`gh pr view <N>`) и к `gh pr edit` для меток.
Снятие `in-work` (п. 5) также идёт через REST.

**Метку после постановки сверь с ответом.** `gh pr edit` ругался на неизвестное
имя метки, REST — нет: ответ POST содержит итоговый список меток объекта, и если
твоей в нём не оказалось, значит имя набрано с опечаткой. Не проверишь — узнаешь
об этом только тем, что следующий этап не увидит PR, а это ровно тот молчаливый
отказ, против которого написан весь этот раздел.

## Процедура

1. Очередь: получи **все** открытые PR пагинированным REST, затем локально
   оставь метку `ship`, исключи `hold` и `needs-decision`, отсортируй по номеру:

   ```
   gh api --paginate "repos/ivanarama/onebase/pulls?state=open&per_page=100" \
     --jq '.[] | {number,title,state,baseRefName:.base.ref,labels:[.labels[].name]}'
   ```

   Локально оставь только `state == "open"` и `baseRefName == "main"`: MERGE
   этого конвейера никогда
   не вливает PR в другую целевую ветку. `gh pr list` без явного лимита здесь
   запрещён: 30 припаркованных PR не должны
   скрыть 31-й допустимый и дать ложный `ПУСТО`. Пусто →
   `ИТОГ: ПУСТО (очередь пуста)` и стоп (ПУСТО — тихий итог «делать нечего»,
   уведомление не шлётся).
   За один прогон вливай не больше **3** PR.

   Фильтр списка не является достаточным гейтом: метки могут измениться, пока ты
   работаешь с PR или ждёшь CI. Перед **каждым внешним изменением PR**
   (`update-branch`, push разрешённого конфликта, комментарий, постановка метки,
   merge), на каждом опросе CI и непосредственно перед compare-and-merge REST
   перечитывай метки через REST:

   ```
   gh api repos/ivanarama/onebase/issues/<N> --jq '[.labels[].name]'
   ```

   Продолжать можно только если `ship` присутствует, а `hold` и
   `needs-decision` отсутствуют. Гейт закрылся — этот PR сразу прекрати обрабатывать
   без изменения меток и переходи к следующему; решение человека старше
   сохранённого состояния очереди.

   На тех же контрольных точках проверь, что `ship` относится именно к текущему
   отревьюенному HEAD:

   ```
     gh api repos/ivanarama/onebase/pulls/<N> --jq '{sha:.head.sha,state,baseRefName:.base.ref}'
   gh api --paginate "repos/ivanarama/onebase/issues/<N>/comments?per_page=100" \
     --jq '.[] | {id,node_id,created_at,updated_at,author:.user.login,body}'
   gh api --paginate "repos/ivanarama/onebase/issues/<N>/timeline?per_page=100" \
     --jq '.[] | {id,created_at,event,actor:(.actor.login // .user.login),label:(.label.name // null),body}'
   ```

   На каждой контрольной точке PR обязан оставаться `open`, а `baseRefName` —
   точным `main`. Close/merge или текущий иной base между выбором и мутацией
   закрывает gate без изменения PR. Любой `BaseRefChangedEvent`/
   `BaseRefForcePushedEvent`/`BaseRefDeletedEvent` после proof anchor инвалидирует
   аудит, включая ABA `main → другая → main`; если PR снова открыт в `main`,
   сними stale `ship` безопасной передачей в REVIEW. Среди server-ordered GraphQL
   timeline edges должен быть валидный
   `<!-- pp:head-reviewed <текущий SHA> review-comment=<числовой id>
   claim=<числовой id> epoch-sha256=<64hex> -->`, который ссылается на
   существующие более ранние доверенные не редактированные review и
   earliest-claim комментарии server-ordered REVIEW epoch. Перед каждым
   MERGE-гейтом реконструируй тот же стабильный пагинированный GraphQL epoch
   двумя полными идентичными проходами по ordered edges и node payload;
   любой `COMMENT_DELETED_EVENT`, `lastEditedAt != null`, несовпадение
   claim/epoch или claim-less legacy completion запрещает update/push/merge.
   Валидна только первая completion-ссылка
   на данный review-comment, и между ними не должно быть `pp:review-again`.
   Для одного SHA без разделяющего override канонична только самая ранняя
   валидная пара; параллельные дубликаты игнорируются.
   Этот committed-маркер доказывает, что его точная `Outcome-Label` была
   подтверждена перед публикацией; последующее изменение маршрутных меток не
   стирает аудит. После completion не должно быть отдельной строки
   `pp:review-again`. Нет актуальной завершённой пары либо есть более поздний
   override — это stale `ship`, а не разрешение на мерж нового кода. Выполни
   специальную атомарную передачу в REVIEW: сними `ship` через REST → сверь
   удаление → оставь комментарий «ship снят: текущий HEAD ещё не прошёл ревью» →
   прекрати обработку PR. После снятия `ship` обычный гейт закономерно закрыт,
   поэтому комментарий является разрешённым завершающим шагом **этой же
   транзакции**, а не новой независимой мутацией. Если комментарий не удался,
   безопасное состояние уже достигнуто: PR не сольётся и REVIEW подхватит SHA.
   Никакие update/push/merge до успешного SHA-гейта недопустимы.

   Текущее наличие `ship` недостаточно. В полном стабильном GraphQL epoch выбери
   **последний переход именно метки `ship`** (`LabeledEvent` или
   `UnlabeledEvent`) строго по позиции server-ordered edge/cursor; учитывай
   события всех actors. Он обязан быть `LabeledEvent` от `ivanarama` и его edge
   обязан располагаться после edges review-комментария, claim и completion
   текущего SHA. Все три proof-комментария не редактированы, поэтому отдельного
   межтипового сравнения с edit timestamp нет. Старый trusted `LabeledEvent`, после
   которого человек снял метку, не оживает от повторной постановки другим actor
   или app. Никогда не сравнивай числовые REST ids комментариев и label events:
   они принадлежат разным таблицам и не задают общий порядок. Если edge-order
   недоступен или переход отсутствует — stale `ship`, метку нужно снять и
   поставить заново.

2. Очередь при `strict: true` строго последовательна — работай с одним PR до
   конца, потом следующий. Состояние: `gh pr view <N> --json
   mergeStateStatus,mergeable,statusCheckRollup,body` (тело нужно в п. 5 —
   по нему снимается `in-work`).

3. По состоянию:
   - **BEHIND** → после полного label+SHA-гейта сохрани проверенный SHA и выполни
     compare-and-update с ним:

     ```
     echo '{"expected_head_sha":"<проверенный SHA>"}' | \
       gh api -X PUT repos/ivanarama/onebase/pulls/<N>/update-branch --input -
     ```

     При `422` сначала снова прочитай HEAD. Только несовпадение с сохранённым SHA
     означает гонку и требует stale-ship передачи в REVIEW. Если SHA тот же,
     это validation/rate-limit отказ: `ship` не снимай, зафиксируй диагноз и
     закончи `НЕ СМОГ` либо `НУЖЕН ЧЕЛОВЕК`. Успешная команда
     меняет HEAD, поэтому старое ревью больше недействительно: сразу выполни
     атомарную передачу из п. 1 (снять `ship`, сверить, прокомментировать) и
     прекрати обработку PR. CI и новый `ship` будут уже после повторного REVIEW.
     (`gh pr update-branch` в этой версии gh не работает.)
   - **DIRTY (конфликт)** → чинить в отдельном worktree, привязанном к SHA
     последнего успешного гейта. Сначала обнови main
     `git fetch origin main:refs/remotes/origin/main`, затем выполни
     `git fetch origin <ветка-PR>` и проверь,
     что `git rev-parse FETCH_HEAD` равен сохранённому SHA, и выполнить
     `git worktree add -B pp-mrg-<N> ../pp-mrg-<N> <сохранённый SHA>`; там
     `git merge origin/main`. Несовпадение FETCH_HEAD — stale-ship передача без
     worktree. Сохрани HEAD до merge и различай результат команды явно:
     - exit code 0 и HEAD изменился — merge создан, переходи к проверкам;
     - exit code 0 и HEAD не изменился — это настоящий no-op: ничего не пушь и
       `ship` не снимай, убери worktree, перечитай GitHub-состояние и
       диагностируй, почему `DIRTY` не воспроизвёлся;
     - ненулевой exit code и `git diff --name-only --diff-filter=U` непуст — это
       настоящий конфликт: разреши допустимые файлы, выполни `git add` и commit,
       затем обязательно проверь, что HEAD изменился;
     - ненулевой exit code без unmerged-файлов — это ошибка команды, а не
       конфликт: ничего не пушь и `ship` не снимай, зафиксируй диагноз.
     Не классифицируй результат только по неизменившемуся HEAD: при обычном
     конфликте HEAD остаётся прежним до commit. Правила разрешения:
     `docs/features.md` и `internal/i18n/locales/*.json` — взять обе стороны;
     `Plans/README.md` — обе стороны И перенумеровать пункты руками;
     конфликт в `.go` — после разрешения обязательно `go build ./...` и
     `go test` затронутых пакетов (автомёрж режет по границам конфликта, не
     понимая синтаксиса). Содержательный конфликт (логика с обеих сторон,
     непонятно как совместить) — НЕ решать: комментарий в PR с описанием,
     поставить через REST `needs-decision` и сверить ответ, перейти к следующему
     PR, в финале `НУЖЕН ЧЕЛОВЕК`. `ship` не снимай: решение человека о мерже
     остаётся, но `needs-decision` паркует попытки до разрешения конфликта.
     После механического разрешения перед push ещё раз выполни полный
     label+SHA-гейт. REST-сверка перед push не атомарна, поэтому используй
     точный refspec вместе с compare-and-swap lease:
     `git push --force-with-lease=refs/heads/<ветка-PR>:<сохранённый SHA> origin HEAD:refs/heads/<ветка-PR>`.
     Lease failure означает гонку: ничего не перезаписывай и `ship` не снимай.
     После успешного push перечитай PR через REST и проверь,
     что новый `.head.sha` равен локальному `git rev-parse HEAD`; иначе не
     снимай `ship`, зафиксируй ошибку доставки и закончи `НУЖЕН ЧЕЛОВЕК`.
     Подтверждённый push меняет HEAD: затем убери worktree, выполни атомарную
     передачу из п. 1 (снять `ship`, сверить, прокомментировать) и прекрати
     обработку PR. Ждать CI и мержить новый SHA без повторного REVIEW нельзя.
   - **CLEAN + требуемые проверки зелёные** → мерж (п. 5).

4. Ожидание CI: цикл «`sleep 120` → перечитать статус», не дольше **35 минут**
   на PR. Требуемые проверки: `build`, `lint`, `postgres-integration`, `vuln`,
   `smoke`, `e2e`, `test-windows`, `launcher-webview-build` — список тот же, что
   в `.github/branch-protection.json`, и сверяться надо с ним. Мёрж не блокирует
   только `bench` — его красноту не ждать, лишь упомянуть в сводке.
   Красная требуемая проверка → `gh run view <id> --log-failed`, диагноз:
   - флейк-профиль (таймаут, обрыв раннера, сеть; `test-windows` мигает ~1/30) →
     непосредственно перед мутацией ещё раз выполни полный label+SHA-гейт,
     затем `gh run rerun <id>` и ждать заново — но только **один** повтор
     (`--failed` в `gh` 2.4.0 ещё не поддерживается);
   - настоящий провал → комментарий в PR с выдержкой лога и диагнозом,
     поставить через REST `needs-decision`, сверить ответ, к следующему PR, в
     финале `НУЖЕН ЧЕЛОВЕК`.

   Истекли 35 минут после разрешённого перезапуска — так же оставь комментарий,
   поставь `needs-decision` и переходи дальше. Без парковочной метки следующий
   прогон снова потратит те же 35 минут на неизменившееся состояние.

   После решения человек снимает `needs-decision`; оставшийся `ship` возвращает
   PR в очередь без повторного одобрения мержа.

5. Мерж: выполни последний полный гейт из п. 1 и сохрани GraphQL `node_id`
   конкретных review, claim и completion, а также epoch anchor node/cursor и
   числовой `id` последнего комментария как watermark. Непосредственно перед PUT
   выполни **два последовательных одинаковых raw GraphQL-запроса**: каждый
   получает `headRefOid`, все текущие labels, три адресованных `node(id: ...)`,
   адресованный epoch anchor и все epoch events после anchor (не более 100;
   иначе fail closed). Принимай только побайтово одинаковые выбранные значения
   обоих полных ответов; любое отличие требует начать пару заново.

   В одном серверном снимке должны одновременно выполняться условия: HEAD равен
   проверенному SHA; `state == OPEN`; `baseRefName == "main"`;
   есть `ship`; нет `hold` и актуального `needs-decision`;
   `labels.pageInfo.hasNextPage == false`; адресованный epoch anchor существует
   и точно совпадает с сохранёнными node id/type/payload (для override-
   `IssueComment` также `lastEditedAt == null`, автор `ivanarama` и отдельная
   строка `pp:review-again`); адресованные review/claim/completion
   не удалены, имеют `lastEditedAt == null`, а их `fullDatabaseId`, автор, SHA,
   Outcome-Label, tail/body, claim и epoch-sha256 всё ещё образуют тот же
   claim-bound proof; после сохранённого anchor нет ни одного нового
   `PullRequestCommit`/`HeadRefForcePushedEvent`/`HeadRefDeletedEvent`/
   `HeadRefRestoredEvent`/`BaseRefChangedEvent`/`BaseRefForcePushedEvent`/
   `BaseRefDeletedEvent` (даже если после ABA-перехода
   `H → X → H` текущий `headRefOid` снова равен проверенному SHA), нет
   `CommentDeletedEvent`, а claim остаётся earliest; **последний** ship-transition среди возвращённых
   `LabeledEvent`/`UnlabeledEvent` — `LabeledEvent` от `ivanarama`, а его edge
   расположен после edges всех трёх адресованных комментариев в том же
   server-ordered timeline; после
   completion нет `pp:review-again`. Предыдущий comment-watermark обязан
   присутствовать среди `comments(last:100)`: если его вытеснили 100+ новых
   комментариев, snapshot не доказывает отсутствие override — требуется новый
   аудит/completion. Если ни одного ship-transition нет в epoch timeline,
   snapshot не доказывает владельца текущей метки и закрывается. Только
   отсутствие свежего trusted последнего `labeled ship` лечится снятием и
   повторной постановкой `ship` после актуального заключения.

   Используй raw GraphQL, а не `gh pr view`, чтобы labels, HEAD и окно timeline
   принадлежали одному snapshot (глобальные `node_id` review/claim/completion и прочие
   переменные передай через `-F`). Числовые REST comment ids уже превышают
   32-битный диапазон GraphQL `databaseId`, поэтому во всех трёх местах используй
   только `fullDatabaseId: BigInt` и сравнивай его строковое значение с REST id:

   ```graphql
   query($owner:String!,$name:String!,$number:Int!,$reviewNode:ID!,$claimNode:ID!,$completionNode:ID!,$epochAnchorNode:ID!,$epochCursor:String!){
     review:node(id:$reviewNode){... on IssueComment{fullDatabaseId createdAt lastEditedAt author{login} body}}
     claim:node(id:$claimNode){... on IssueComment{fullDatabaseId createdAt lastEditedAt author{login} body}}
     completion:node(id:$completionNode){... on IssueComment{fullDatabaseId createdAt lastEditedAt author{login} body}}
     epochAnchor:node(id:$epochAnchorNode){__typename
       ... on PullRequestCommit{id commit{oid}}
       ... on HeadRefForcePushedEvent{id createdAt afterCommit{oid}}
       ... on HeadRefRestoredEvent{id createdAt}
       ... on IssueComment{id fullDatabaseId createdAt lastEditedAt author{login} body}
     }
     repository(owner:$owner,name:$name){pullRequest(number:$number){
       headRefOid baseRefName state labels(first:100){nodes{name} pageInfo{hasNextPage}}
       comments(last:100){nodes{fullDatabaseId createdAt lastEditedAt author{login} body}}
       timelineItems(first:100,after:$epochCursor,itemTypes:[PULL_REQUEST_COMMIT,HEAD_REF_FORCE_PUSHED_EVENT,HEAD_REF_DELETED_EVENT,HEAD_REF_RESTORED_EVENT,BASE_REF_CHANGED_EVENT,BASE_REF_FORCE_PUSHED_EVENT,BASE_REF_DELETED_EVENT,MERGED_EVENT,ISSUE_COMMENT,COMMENT_DELETED_EVENT,LABELED_EVENT,UNLABELED_EVENT]){
         updatedAt
         pageInfo{hasNextPage}
         edges{cursor node{__typename
           ... on PullRequestCommit{id commit{oid}}
           ... on HeadRefForcePushedEvent{id createdAt afterCommit{oid}}
           ... on HeadRefDeletedEvent{id createdAt}
           ... on HeadRefRestoredEvent{id createdAt}
           ... on BaseRefChangedEvent{id createdAt previousRefName currentRefName}
           ... on BaseRefForcePushedEvent{id createdAt beforeCommit{oid} afterCommit{oid}}
           ... on BaseRefDeletedEvent{id createdAt baseRefName}
           ... on MergedEvent{id createdAt commit{oid}}
           ... on IssueComment{id fullDatabaseId createdAt lastEditedAt author{login} body}
           ... on CommentDeletedEvent{createdAt}
           ... on LabeledEvent{createdAt actor{login} label{name}}
           ... on UnlabeledEvent{createdAt actor{login} label{name}}
         }}
       }
     }}
   }
   ```

   Вторая идентичная проверка этой пары — **точка невозврата** операции merge. GitHub не
   предоставляет транзакцию, одновременно условную по labels и выполняющую
   merge: `sha` защищает только HEAD. Поэтому `hold`/`needs-decision`, поставленные
   уже после снимка и после отправки PUT, не могут отменить запрос в полёте;
   ставить отмену нужно до точки невозврата. Не утверждай в отчёте обратное.
   Сохрани SHA снимка и используй атомарный compare-and-merge REST:

   ```
   echo '{"merge_method":"merge","sha":"<проверенный SHA>"}' | \
     gh api -X PUT repos/ivanarama/onebase/pulls/<N>/merge --input -
   ```

   Успех — только ответ с `merged: true`. `409` означает, что HEAD успел
   измениться между гейтом и merge: ничего не влилось, перечитай состояние и
   выполни stale-ship передачу в REVIEW из п. 1. Другой отказ — перечитай
   состояние и действуй по п. 3. Ветка после безопасного REST-мержа может
   остаться в origin; удаление ветки не важнее атомарной привязки SHA. Ишью
   закроется сам по `Fixes #N` из тела PR.

   **Сразу после мержа сними `in-work` с закрытых заявок этого PR.** Метку
   вешает фиксер, когда открывает PR, и снять её больше некому: он к заявке уже
   не вернётся, а ты — последний, кто её касается. Иначе каждая проехавшая
   заявка уносит `in-work` в закрытые навсегда, и метка перестаёт означать
   «едет прямо сейчас» (так и случилось с #1136).

   Номера бери из тела PR по **всем** написаниям, какие понимает GitHub, —
   `Fixes`, `Closes`, `Resolves` (регистр не важен). Автоматика всегда пишет
   `Fixes`, но PR, написанный человеком руками, может нести `Closes #N`, и это
   ровно тот случай, ради которого шаг и вводится.

   ```
   gh api -X DELETE repos/ivanarama/onebase/issues/<N>/labels/in-work
   ```

   Метки нет — ответ 404, это не ошибка: заявка её и не носила.

   Отдельно поищи в теле русские «Закрывает/Исправляет/Решает #N»: GitHub их
   ключевыми словами не считает, такая заявка останется открытой. `in-work` с
   неё не снимай — она и правда ещё не доехала, — но назови её в сводке: закрыть
   придётся руками (сверка — `go run ./tools/issuetail`).

6. После каждого мержа остальные PR очереди становятся BEHIND — это норма,
   повторяй п. 3 для следующего. До первого мержа их `CLEAN` ничего не значит.

7. Локальный `main` не трогать (занят другим worktree) — всё через `gh` и
   `git fetch`. В ветки PR ничего не коммитить, кроме разрешения конфликтов.

8. Финал — сводка (что влито, что и почему отложено) и строка:
   `ИТОГ: ГОТОВО (влиты #a, #b)` /
   `ИТОГ: НУЖЕН ЧЕЛОВЕК (#c — <причина в одну строку>)` /
   `ИТОГ: НЕ СМОГ (<причина>)`.
