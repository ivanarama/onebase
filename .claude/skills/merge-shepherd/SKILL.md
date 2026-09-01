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
Единственное расширение по state — уже merged PR с незавершённым валидным
`pp:merge-cleanup-intent`: на нём разрешены только идемпотентные cleanup-фазы из
п. 1, а не update, push или повторный merge; до done те же `ship`/stop-метки
обязаны сохраняться.

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

1. **Сначала восстановление post-merge cleanup, затем обычная очередь.** Merge
   необратим, а снятие `in-work` и служебных меток — отдельные запросы. Поэтому
   до списка открытых PR получи **все** repository issue comments пагинированным
   REST и локально найди точные отдельные строки доверенного автора
   `ivanarama`:

   ```
   gh api --paginate "repos/ivanarama/onebase/issues/comments?per_page=100&sort=created&direction=asc" \
     --jq '.[] | {id,node_id,issue_url,created_at,updated_at,author:.user.login,body}'
   ```

   Транзакция cleanup хранится двумя неизменяемыми комментариями на PR:

   ```
   <!-- pp:merge-cleanup-intent head=<40hex> review-comment=<id> claim=<id> completion=<id> ship-event=<GraphQL node id> body-sha256=<64hex> issues=<sorted unique decimal csv|none> -->
   <!-- pp:merge-cleanup-done intent=<id> head=<40hex> merge=<40hex> -->
   ```

   `body-sha256` — lowercase SHA-256 raw UTF-8 точного тела PR в момент intent.
   `issues` — отсортированный уникальный список только из английских closing
   keywords `Fixes`/`Closes`/`Resolves`, либо `none`. Свободный русский текст и
   похожие строки в чужих комментариях не входят в список. Полный глобальный
   поток comments нужен именно для recovery: merged PR уже исчез из очереди
   открытых `ship`-PR, а Search API eventually consistent и доказательством
   отсутствия intent не считается.

   Для каждого найденного intent получи родительский PR, все его комментарии и
   полный server-ordered GraphQL timeline. Сопоставь REST `node_id` с GraphQL
   `IssueComment.id`, decimal REST id — со строковым `fullDatabaseId`. Intent
   валиден только если он от `ivanarama`, является точной отдельной строкой,
   `lastEditedAt == null`, адресует существующую каноничную claim-bound пару и
   точный trusted `ship-event`, а record `head`/proof/body/issues пересчитан и
   совпал. Для одинакового record каноничен самый ранний intent по GraphQL edge;
   более поздние точные копии — equivalent diagnostics. Другой intent-record
   для того же merged HEAD, edit или `CommentDeletedEvent` после каноничного
   intent закрывает recovery человеку. Done валиден только после своего intent,
   с теми же `head` и фактическим merge commit; при дублях каноничен самый ранний.

   Перед **каждой** cleanup-мутацией выполни два полных последовательных
   GraphQL-прохода от `cursor=null` до `hasNextPage=false` и принимай их только
   при побайтовом совпадении state, base, merge fields и всей последовательности
   `(edge cursor, __typename, payload)`. Одновременно потребуй REST
   `state == "closed"`, `.merged == true`, непустые `merged_at` и
   `merge_commit_sha`, GraphQL `state == MERGED`, `baseRefName == "main"`,
   наличие `ship` и отсутствие `hold`/`needs-decision` до done,
   точный intent `head == .head.sha`, ровно один `MergedEvent` после intent и
   совпадение его commit OID с `merge_commit_sha`. После merge допустим только
   обычный конечный `HeadRefDeletedEvent`; restore, новый commit/force-push,
   edit/delete protocol comment или неоднозначная timeline закрывают gate.
   Уже merged PR **никогда не отправляй в merge API повторно**: любой ответ
   прошлого PUT восстанавливается только по server state и intent.

   Незавершённый валидный intent доведи идемпотентными фазами:

   1. Для каждого номера из `issues` перечитай issue. Продолжай только если она
      закрыта. Если `in-work` есть — удали через REST и сверь отсутствие; `404`
      допустим только после повторного GET, доказавшего, что метки уже нет.
      Открытая заново issue — человеческий ход: метку не снимай и остановись.
   2. Когда все связанные issues закрыты и уже без `in-work`, опубликуй exact
      `pp:merge-cleanup-done`, сохрани возвращённый id, затем получи `.body`
      через jq `@base64`, декодируй UTF-8 и сравни байт-в-байт. До доказанного
      совпадения следующей мутации нет.
   3. Только после валидного done идемпотентно сними `ship` с уже merged PR и
      сверь отсутствие. Crash после done, но до DELETE, восстанавливает только
      эту фазу; отсутствие `ship` уже считается завершением.

   Сначала заверши **все** найденные незавершённые cleanup-транзакции по номеру
   PR. Пока хотя бы одна закрыта human/state gate, не мержи новый PR и закончи
   `НУЖЕН ЧЕЛОВЕК`. Recovery с валидным done и уже отсутствующим `ship` действий
   не требует. Если `ship` исчез до done или появился stop, это человеческий
   ход: не восстанавливай метку и останови cleanup.

   Затем очередь: получи **все** открытые PR пагинированным REST, затем локально
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

   Перед обычной сортировкой примени глобальный single-flight-барьер. Среди всех
   `ship`-PR найди самый ранний по номеру доказанный незавершённый handoff:
   открытый `pp:base-sync-intent`, текущий `to` валидного done либо legacy
   re-ship текущего HEAD — как без нового proof, так и с уже готовым
   интеграционным proof. Только этот PR является
   владельцем барьера. Если он ждёт REVIEW, не меняй **ни один** PR и закончи
   весь MERGE. Если его текущий `to` уже получил каноничный proof `reviewed`,
   обрабатывай владельца раньше всей обычной очереди: proof передаёт ход MERGE,
   но не освобождает барьер; его освобождает только merge/закрытие владельца.
   При нескольких legacy
   re-ship выбери только минимальный номер; остальные сохраняют `ship`, но ждут
   своей очереди. Нельзя заранее обновлять или мержить следующий PR: это изменит
   `main` и заставит повторно ревьюить владельца барьера.

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
   `pp:review-again`.

   Текущее наличие `ship` недостаточно. В полном стабильном GraphQL epoch выбери
   **последний переход именно метки `ship`** (`LabeledEvent` или
   `UnlabeledEvent`) строго по позиции server-ordered edge/cursor; учитывай
   события всех actors. Он обязан быть `LabeledEvent` от `ivanarama`. Старый
   trusted `LabeledEvent`, после которого человек снял метку, не оживает от
   повторной постановки другим actor или app. Никогда не сравнивай числовые REST
   ids комментариев и label events: они принадлежат разным таблицам и не задают
   общий порядок.

   Разрешены ровно три способа связать этот ship-transition с текущим proof:

   - **обычный:** edge `ship` расположен после edges review-комментария, claim и
     completion текущего SHA;
   - **carried:** тот же непрерывно присутствующий `ship` был поставлен после
     proof исходного SHA, а текущий HEAD достигнут только валидной цепочкой
     автоматических base-sync и после последнего звена уже получил новый
     каноничный proof с outcome `reviewed`;
   - **legacy reauthorized:** текущий HEAD — точный legacy base-sync, сделанный
     до intent/done-протокола, а человек заново поставил `ship` после anchor
     этого HEAD и интеграционное REVIEW уже создало для него каноничный proof с
     outcome `reviewed`.

   Legacy reauthorization валидна только если текущий HEAD `to` — merge-коммит
   ровно с двумя parents `[from, base]`; `from` имеет каноничный proof `reviewed`
   и trusted `ship` после него; между этим ship и `to` есть ровно один
   `PullRequestCommit` и нет иных HEAD/base lifecycle events; `base` — предок
   текущего `main`; а последний ship-transition — новый trusted `LabeledEvent`
   от `ivanarama` после anchor `to`. Старое снятие `ship` между `to` и новым
   label допустимо. Докажи условия двумя стабильными полными GraphQL snapshot и
   REST parents; похожий merge message доказательством не считается. Новый push
   после re-ship отменяет разрешение.

   Base-sync carry состоит из точных отдельных строк:

   ```
   <!-- pp:base-sync-intent from=<40hex> base=<40hex> review-comment=<id> claim=<id> completion=<id> ship-event=<GraphQL node id> previous=<done id|none> -->
   <!-- pp:base-sync-done intent=<id> from=<40hex> to=<40hex> base=<40hex> previous=<done id|none> ship-event=<GraphQL node id> -->
   ```

   Оба комментария должны быть от `ivanarama`, не редактированы и server-ordered
   в указанном порядке. `done` ссылается на самый ранний валидный intent для
   данного `from`; их `from`, `previous` и `ship-event` совпадают. Для первого
   звена `previous=none`, intent адресует каноничные review/claim/completion
   `from`, а `ship-event` либо идёт после них обычным способом, либо является
   доказанным legacy reauthorization после anchor `from`. Для следующего
   `previous` указывает на
   предыдущий done, его `to` равен новому `from`, а новый intent адресует
   каноничный proof этого `from`. Каждый переход после intent — ровно один
   `PullRequestCommit` без force-push/delete/restore/base-change; commit `to`
   имеет ровно двух родителей в порядке `[from, base]`, а `base` — предок
   текущего `main`. Проверяй parents через
   `repos/ivanarama/onebase/commits/<to>` и адресуй все intent/done по node id в
   обоих полных GraphQL snapshot. Последний переход `ship` во всей цепочке обязан
   оставаться исходным trusted `LabeledEvent`: снятие/повторная постановка,
   edit/delete marker, чужой head event или разрыв `previous` отменяют carry.

   Если текущий HEAD равен `to` валидного последнего done либо является точным
   legacy base-sync с re-ship после его anchor, но каноничного proof текущего
   HEAD ещё нет, это **не stale ship**: ничего не меняй, зафиксируй
   «ожидает интеграционное REVIEW» и переходи к следующему PR. Во всех остальных
   случаях отсутствие актуальной завершённой пары, более поздний override либо
   отсутствие обычного/carried/legacy-reauthorized разрешения — stale `ship`. Выполни атомарную
   передачу в REVIEW: сними `ship` через REST → сверь удаление → оставь
   комментарий «ship снят: текущий HEAD ещё не прошёл ревью» → прекрати обработку
   PR. После снятия `ship` комментарий является разрешённым завершающим шагом
   **этой же транзакции**, а не новой независимой мутацией. Если комментарий не
   удался, безопасное состояние уже достигнуто. Никакие update/push/merge до
   успешного SHA+authorization-гейта недопустимы.

2. Очередь при `strict: true` строго последовательна — работай с одним PR до
   конца, потом следующий. Состояние: `gh pr view <N> --json
   mergeStateStatus,mergeable,statusCheckRollup,body` (тело нужно в п. 5 —
   по нему снимается `in-work`).

3. По состоянию:
   - **BEHIND** → после полного label+SHA+authorization-гейта (включая допустимое
     legacy reauthorization) сохрани проверенный
     SHA, текущий `baseRefOid`, ids proof, node id исходного ship-transition и id
     предыдущего done (`none` для первого sync). Сначала опубликуй exact intent:

     ```
     <!-- pp:base-sync-intent from=<SHA> base=<baseRefOid> review-comment=<id> claim=<id> completion=<id> ship-event=<node id> previous=<done id|none> -->
     ```

     Перечитай полный timeline. Продолжает только самый ранний валидный intent
     для этой пары `from` + authorization; параллельный worker, создавший более
     поздний intent, останавливается. Затем ещё раз выполни полный гейт и вызови
     compare-and-update:

     ```
     echo '{"expected_head_sha":"<проверенный SHA>"}' | \
       gh api -X PUT repos/ivanarama/onebase/pulls/<N>/update-branch --input -
     ```

     При `422` сначала снова прочитай HEAD. Если он равен `from`, это
     validation/rate-limit отказ: `ship` не снимай, intent оставь для recovery,
     зафиксируй диагноз и закончи `НЕ СМОГ` либо `НУЖЕН ЧЕЛОВЕК`. Если HEAD уже
     другой, не объявляй гонку вслепую: восстанови самый ранний intent. Он
     допускает ровно новый merge-коммит с двумя parents `[from, base]` и ровно
     один соответствующий `PullRequestCommit`; любой другой переход — обычная
     stale-ship передача.

     После успешного update или доказанного recovery прочитай новый HEAD и его
     parents, повтори стабильный timeline gate и опубликуй exact done:

     ```
     <!-- pp:base-sync-done intent=<id> from=<SHA> to=<новый SHA> base=<второй parent> previous=<done id|none> ship-event=<node id> -->
     ```

     Если процесс упал после intent, следующий MERGE восстанавливает **самый
     ранний** intent: при `HEAD == from` повторяет CAS update; при доказанном
     `[from, base]` публикует отсутствующий done. Поэтому crash не превращается
     во второй ручной `ship`. После подтверждённого done метку `ship` **не
     снимай**; прекрати **весь запуск MERGE** со статусом «ожидает интеграционное REVIEW».
     REVIEW проверит новый HEAD, а при зелёном результате MERGE продолжит без
     второго клика человека. (`gh pr update-branch` в этой версии gh не работает.)
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
     label+SHA-гейт, создай и выбери самый ранний `pp:base-sync-intent` по тем же
     правилам, что для BEHIND, затем повтори гейт: механический merge тоже меняет
     HEAD и обязан быть восстанавливаемым handoff. REST-сверка перед push не
     атомарна, поэтому используй точный refspec вместе с compare-and-swap lease:
     `git push --force-with-lease=refs/heads/<ветка-PR>:<сохранённый SHA> origin HEAD:refs/heads/<ветка-PR>`.
     Lease failure означает гонку: ничего не перезаписывай и `ship` не снимай.
     После успешного push перечитай PR через REST и проверь,
     что новый `.head.sha` равен локальному `git rev-parse HEAD`; иначе не
     снимай `ship`, зафиксируй ошибку доставки и закончи `НУЖЕН ЧЕЛОВЕК`.
     Подтверждённый push меняет HEAD: проверь два parents `[from, base]`, единственный
     `PullRequestCommit`, опубликуй `pp:base-sync-done`, убери worktree и прекрати
     **весь запуск MERGE**, сохранив `ship`. Ждать CI и мержить новый SHA без
     интеграционного REVIEW нельзя; повторный человеческий `ship` при валидной
     carry-цепочке не нужен.
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
   конкретных review, claim и completion, а также epoch anchor node/cursor.
   Из точного тела PR вычисли raw UTF-8 `body-sha256`, извлеки
   `Fixes`/`Closes`/`Resolves`, нормализуй номера в sorted unique `issues` и
   собери exact `pp:merge-cleanup-intent` из п. 1. Непосредственно перед его
   POST снова выполни полный label+SHA+authorization-гейт. После POST сохрани
   **собственный возвращённый id**, получи `.body` через jq `@base64`, декодируй
   UTF-8 и сравни байт-в-байт. Затем перечитай полный timeline: продолжает только
   самый ранний валидный intent для точного head/proof/ship-event/body/issues.
   Если собственный id проиграл более раннему concurrent intent, этот worker
   останавливается; recovery использует каноничный intent. Timeout POST не
   повторяй вслепую — сначала найди exact marker прямым REST.

   Сохрани `node_id` каноничного cleanup-intent и его числовой `id` как новый
   comment watermark. Непосредственно перед PUT выполни **два последовательных
   одинаковых raw GraphQL-запроса**: каждый получает `headRefOid`, все текущие
   labels, четыре адресованных `node(id: ...)` (review, claim, completion и
   cleanup-intent), адресованный epoch anchor и все epoch events после anchor
   (не более 100; иначе fail closed). Принимай только побайтово одинаковые
   выбранные значения обоих полных ответов; любое отличие требует начать пару
   заново.

   В одном серверном снимке должны одновременно выполняться условия: HEAD равен
   проверенному SHA; `state == OPEN`; `baseRefName == "main"`; сохранённый
   `baseRefOid` принадлежит тому же снимку;
   есть `ship`; нет `hold` и актуального `needs-decision`;
   `labels.pageInfo.hasNextPage == false`; адресованный epoch anchor существует
   и точно совпадает с сохранёнными node id/type/payload (для override-
   `IssueComment` также `lastEditedAt == null`, автор `ivanarama` и отдельная
   строка `pp:review-again`); адресованные review/claim/completion и
   cleanup-intent
   не удалены, имеют `lastEditedAt == null`, а их `fullDatabaseId`, автор, SHA,
   Outcome-Label, tail/body, claim и epoch-sha256 всё ещё образуют тот же
   claim-bound proof; cleanup-intent точной отдельной строкой адресует эти ids,
   текущий body hash, closing issues и ship-event и остаётся самым ранним
   валидным intent этого record; после сохранённого anchor нет ни одного нового
   `PullRequestCommit`/`HeadRefForcePushedEvent`/`HeadRefDeletedEvent`/
   `HeadRefRestoredEvent`/`BaseRefChangedEvent`/`BaseRefForcePushedEvent`/
   `BaseRefDeletedEvent` (даже если после ABA-перехода
   `H → X → H` текущий `headRefOid` снова равен проверенному SHA), нет
   `CommentDeletedEvent`, а claim остаётся earliest; **последний** ship-transition среди возвращённых
   `LabeledEvent`/`UnlabeledEvent` — `LabeledEvent` от `ivanarama`. Для обычного
   разрешения его edge расположен после edges всех трёх адресованных комментариев
   в том же server-ordered timeline. Для carried-разрешения оба снимка дополнительно
   адресуют каждый intent/done и исходный ship-event по node id, воспроизводят
   непрерывную цепочку до текущего HEAD и доказывают отсутствие любого более
   позднего ship-transition; после
   completion нет `pp:review-again`. Предыдущий comment-watermark обязан
   присутствовать среди `comments(last:100)`: если его вытеснили 100+ новых
   комментариев, snapshot не доказывает отсутствие override — требуется новый
   аудит/completion. Если ни одного ship-transition нет в epoch timeline,
   snapshot не доказывает владельца текущей метки и закрывается. Только
   отсутствие свежего trusted последнего `labeled ship` лечится снятием и
   повторной постановкой `ship` после актуального заключения.

   Используй raw GraphQL, а не `gh pr view`, чтобы labels, HEAD и окно timeline
   принадлежали одному snapshot (глобальные `node_id`
   review/claim/completion/cleanup-intent и прочие
   переменные передай через `-F`). Числовые REST comment ids уже превышают
   32-битный диапазон GraphQL `databaseId`, поэтому во всех четырёх местах используй
   только `fullDatabaseId: BigInt` и сравнивай его строковое значение с REST id:

   ```graphql
   query($owner:String!,$name:String!,$number:Int!,$reviewNode:ID!,$claimNode:ID!,$completionNode:ID!,$cleanupIntentNode:ID!,$epochAnchorNode:ID!,$epochCursor:String!){
     review:node(id:$reviewNode){... on IssueComment{fullDatabaseId createdAt lastEditedAt author{login} body}}
     claim:node(id:$claimNode){... on IssueComment{fullDatabaseId createdAt lastEditedAt author{login} body}}
     completion:node(id:$completionNode){... on IssueComment{fullDatabaseId createdAt lastEditedAt author{login} body}}
     cleanupIntent:node(id:$cleanupIntentNode){... on IssueComment{fullDatabaseId createdAt lastEditedAt author{login} body}}
     epochAnchor:node(id:$epochAnchorNode){__typename
       ... on PullRequestCommit{id commit{oid}}
       ... on HeadRefForcePushedEvent{id createdAt afterCommit{oid}}
       ... on HeadRefRestoredEvent{id createdAt}
       ... on IssueComment{id fullDatabaseId createdAt lastEditedAt author{login} body}
     }
     repository(owner:$owner,name:$name){pullRequest(number:$number){
       headRefOid baseRefOid baseRefName state labels(first:100){nodes{name} pageInfo{hasNextPage}}
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
           ... on LabeledEvent{id createdAt actor{login} label{name}}
           ... on UnlabeledEvent{id createdAt actor{login} label{name}}
         }}
       }
     }}
   }
   ```

   Для carried-разрешения расширь **оба** одинаковых запроса переменными и
   `node(id: ...)` для исходного ship-event и каждого intent/done. У каждого
   `IssueComment` сравни `fullDatabaseId`, полный body, автора и
   `lastEditedAt == null`; у ship-node сравни тип, actor, label и позицию edge.
   Если вся цепочка не помещается в один полный ответ или любой node исчез,
   fail closed: автоматический мерж запрещён.

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

   Успех — только ответ с `merged: true`. При `409`, timeout, обрыве ответа или
   любом другом отказе **сначала** перечитай REST PR и полный GraphQL timeline.
   Если PR уже `merged`, `MergedEvent` расположен после каноничного intent и его
   commit совпадает с `merge_commit_sha`, merge состоялся: второй PUT запрещён,
   переходи прямо к recovery cleanup из п. 1. Только доказанный `open` с тем же
   HEAD означает, что merge не произошёл и ошибку надо разбирать по п. 3. Но
   после timeout/обрыва ответа даже такой GET не разрешает повторный PUT в этом
   запуске: закончи `НЕ СМОГ`, а следующий запуск заново выполнит два стабильных
   прохода и восстановит каноничный intent. Новый
   произвольный HEAD требует stale-ship передачи, а валидный base-sync intent —
   его recovery. Так CLI/REST не образуют два конкурирующих merge path, а
   потерянный успешный ответ не превращается во второй merge. Ветка после
   безопасного REST-мержа может остаться в origin; удаление ветки не важнее
   атомарной привязки SHA. Ишью закроется сам по `Fixes #N` из тела PR.

   **Сразу после мержа восстанови транзакцию cleanup из п. 1 и сними `in-work`
   с закрытых заявок этого PR.** Метку
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

   Метки нет — ответ 404 допустим только после повторного GET, подтвердившего её
   отсутствие. После всех номеров опубликуй и побайтово сверь
   `pp:merge-cleanup-done`, затем сними и сверь `ship` на merged PR. До done
   `ship` остаётся видимым recovery-сигналом; новый запуск всё равно находит
   intent глобальным пагинированным REST и никогда не повторяет merge.

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
