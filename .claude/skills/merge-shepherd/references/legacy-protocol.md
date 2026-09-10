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

## GitHub CLI: проверяй возможность, а не номер версии

Рабочая версия `gh` меняется независимо от репозитория, поэтому скилл не
приписывает ей заранее известные поломки. В preflight выполни `gh --version` и
`gh api user`; ненулевой exit code — ошибка, а не «пустой ответ». Используй
точные `--json`-поля и REST-команды из самой процедуры: они одновременно
задают минимальный контракт данных и не зависят от лишних полей CLI.

После изменения метки всегда сверь ответ API или повторный GET. Если текущая
версия отвергла использованный флаг либо поле, остановись до следующей мутации и
сообщи точную ошибку; не переключайся молча на непроверенный обход.

## Процедура

Перед ручной очередью проверь, не вернул ли `pipelinectl next merge`
`action=cleanup`: такой lease всегда заверши через `complete merge-cleanup`, не
вызывая merge API повторно. Если быстрый путь вернул `fallback`, отдельно найди
доверенные неизменённые пары
`pp:merge-cleanup-intent`/`pp:merge-cleanup-done` во всём пагинированном потоке
repository issue comments. Незавершённый intent старше обычной очереди: уже
merged PR можно только передать обратно в `complete merge-cleanup`, а открытый
PR с изменившимся HEAD/body/proof/ship или неоднозначной timeline требует
человека. Не обходи такой intent выбором следующего PR. Intent содержит exact
HEAD, SHA-256 review-proof и raw UTF-8 body, а также sorted same-repository
closing issues; qualified-ссылка на другой repository локальной issue не
считается. Done адресует exact intent, HEAD и подтверждённый merge commit.

1. Очередь: получи **все** открытые PR пагинированным REST, затем локально
   оставь метку `ship`, исключи `hold` и `needs-decision`. После
   single-flight/recovery упорядочь обычные PR по effective priority, затем
   номеру: manual `queue:p0`…`queue:p3` старше `queue:auto:p0`…`queue:auto:p3`;
   без них critical/security → P0, `bug` → P1,
   `enhancement`/`documentation`/default → P2, `question` → P3; каждые полные
   168 часов ожидания поднимают на уровень вплоть до P1; P0 остаётся полосой срочной работы:

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

   Разрешены ровно четыре способа связать этот ship-transition с текущим proof:

   - **обычный sticky:** edge `ship` расположен после anchor текущего SHA;
     каноничный proof `reviewed` того же SHA может завершиться позже. Так
     разрешение «сливать после успешного ревью» не гоняется с финальным
     служебным completion;
   - **carried:** тот же непрерывно присутствующий `ship` был поставлен в эпохе
     исходного SHA после его anchor, а текущий HEAD достигнут только валидной цепочкой
     автоматических base-sync и после последнего звена уже получил новый
     каноничный proof с outcome `reviewed`;
   - **legacy reauthorized:** текущий HEAD — точный legacy base-sync, сделанный
     до intent/done-протокола, а человек заново поставил `ship` после anchor
     этого HEAD и интеграционное REVIEW уже создало для него каноничный proof с
     outcome `reviewed`.
   - **protocol-recovery reauthorized:** текущий HEAD — точный `to` trusted
     intent/done handoff, исходный carry которого невалиден; человек заново
     поставил `ship` после edge самого done, а интеграционное REVIEW уже создало
     каноничный proof текущего HEAD с outcome `reviewed`.

   Legacy reauthorization валидна только если текущий HEAD `to` — merge-коммит
   ровно с двумя parents `[from, base]`; `from` имеет каноничный proof `reviewed`
   и trusted `ship` после него; между этим ship и `to` есть ровно один
   `PullRequestCommit` и нет иных HEAD/base lifecycle events; `base` — предок
   текущего `main`; а последний ship-transition — новый trusted `LabeledEvent`
   от `ivanarama` после anchor `to`. Старое снятие `ship` между `to` и новым
   label допустимо. Докажи условия двумя стабильными полными GraphQL snapshot и
   REST parents; похожий merge message доказательством не считается. Новый push
   после re-ship отменяет разрешение.

   Protocol-recovery reauthorization требует trusted не редактированные
   intent/done, exact parents `[from, base]`, каноничный source proof `from`,
   ровно один `PullRequestCommit` между intent и done, `base` как предка
   текущего `main` и последний trusted `ship` от `ivanarama` после edge done.
   После done не допускаются HEAD/base lifecycle events. Исходный stale
   `ship-event` не оживает: новый label разрешает только точный текущий HEAD и
   отменяется следующим push. Условия доказываются двумя стабильными GraphQL
   snapshot и REST parents.

   Base-sync carry состоит из точных отдельных строк:

   ```
   <!-- pp:base-sync-intent from=<40hex> base=<40hex> review-comment=<id> claim=<id> completion=<id> ship-event=<GraphQL node id> previous=<done id|none> -->
   <!-- pp:base-sync-done intent=<id> from=<40hex> to=<40hex> base=<40hex> previous=<done id|none> ship-event=<GraphQL node id> -->
   ```

   Оба комментария должны быть от `ivanarama`, не редактированы и server-ordered
   в указанном порядке. `done` ссылается на самый ранний валидный intent для
   данного `from`; их `from`, `previous` и `ship-event` совпадают. Для первого
   звена `previous=none`, intent адресует каноничные review/claim/completion
   `from`, а `ship-event` идёт после anchor `from` (до или после completion)
   либо является
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

   Если это первый аудит текущего HEAD и последний переход `ship` — trusted
   `LabeledEvent` после его anchor, разрешение просто ждёт обычного REVIEW:
   метку не снимай и PR не обновляй. То же правило действует, если текущий HEAD равен `to` валидного последнего done, является точным
   legacy base-sync с re-ship после его anchor либо имеет доказанный
   protocol-recovery re-ship после своего done, но каноничного proof текущего
   HEAD ещё нет, это **не stale ship**: ничего не меняй, зафиксируй
   «ожидает интеграционное REVIEW» и переходи к следующему PR. Во всех остальных
   случаях отсутствие актуальной завершённой пары, более поздний override либо
   отсутствие обычного/carried/legacy/protocol-recovery разрешения — stale `ship`. Выполни атомарную
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
     legacy либо protocol-recovery reauthorization) сохрани проверенный
     SHA, ids proof, node id исходного ship-transition и id
     предыдущего done (`none` для первого sync). Для protocol-recovery всегда
     начни новую исправленную цепочку с `previous=none`; malformed historical
     done остаётся только доказанным anchor reauthorization. Authoritative tip
     целевой ветки получи только прямым REST-чтением
     `gh api repos/ivanarama/onebase/git/ref/heads/main --jq .object.sha`;
     `PullRequest.baseRefOid` не используй как tip `main`. Сначала опубликуй exact intent:

     ```
     <!-- pp:base-sync-intent from=<SHA> base=<authoritative main ref SHA> review-comment=<id> claim=<id> completion=<id> ship-event=<node id> previous=<done id|none> -->
     ```

     Перечитай полный timeline. Продолжает только самый ранний валидный intent
     для этой пары `from` + authorization; параллельный worker, создавший более
     поздний intent, останавливается. Непосредственно перед update ещё раз
     прочитай `refs/heads/main`; затем ещё раз выполни полный гейт и вызови
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
     parents. `done.base` всегда равен фактическому второму parent, а не слепой
     копии `intent.base`. Если `main` сдвинулся между intent и update, расхождение
     допустимо только когда `intent.base` — предок фактического parent,
     фактический parent — предок текущего `main`, а остальные timeline/HEAD
     условия сохранились; диагностика обязана показать
     `base_sync_base_advanced`. Повтори стабильный timeline gate и опубликуй exact done:

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
     (перезапускай весь выбранный workflow run, не угадывая поддержку дополнительных флагов);
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
   проверенному SHA; `state == OPEN`; `baseRefName == "main"`; сохранённый
   `baseRefOid` принадлежит тому же снимку;
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
   `LabeledEvent`/`UnlabeledEvent` — `LabeledEvent` от `ivanarama`. Для обычного
   sticky-разрешения его edge расположен после anchor текущего HEAD; каноничный
   proof может завершиться позже в той же неизменной epoch. Для carried-разрешения оба снимка дополнительно
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

   Успех — только ответ с `merged: true`. `409` означает, что HEAD успел
   измениться между гейтом и merge: ничего не влилось, перечитай состояние.
   Сначала проверь recovery самого раннего валидного `pp:base-sync-intent`;
   только недоказанный новый HEAD требует stale-ship передачи из п. 1. Другой отказ — перечитай
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

   **Plan-PR возвращает исходную issue в FIX.** Если в теле влитого PR есть
   точные соседние строки `Plan-Issue: #<N>` и
   `Plan-Path: Plans/<NNN>-<slug>.md`, это не closing keywords. Перечитай issue
   `<N>` и потребуй `state=open`, `approved` и `plan-in-review`. Опубликуй
   комментарий «План `<path>` влит через PR #<PR>; заявка возвращена в FIX.» с
   точной строкой
   `<!-- pp:plan-ready issue=<N> pr=<PR> path=<path> -->`; проверь body через
   `@base64` байт-в-байт. Затем добавь и сверь `ready-fix`, сними и сверь
   `plan-in-review`, после него `needs-decision`. `approved`, `decision:*` и
   `queue:p*` не меняй. Повторный recovery с уже существующим точным
   `pp:plan-ready` идемпотентно доводит только незавершённые label-фазы.

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
