# Этап 163 — Crash-safe handoff между последовательными PR-срезами одной issue

## Контекст

План связан с issue #1379. Она зафиксировала воспроизводимый разрыв на первой
реальной многошаговой реализации: PR #1374 выполнил срез A плана
`Plans/159-undefined-and-typed-empty-values.md`, был влит с `Part of #1274` и
намеренно без `Fixes #1274`. После merge issue #1274 осталась открытой с
`approved`, `ready-fix`, `in-work` и `queue:p0`; удалённая ветка `fix/1274`
осталась на HEAD уже влитого PR, а открытого PR для неё больше нет.

Текущие границы расходятся именно в этом состоянии:

- `.claude/skills/fix-approved/SKILL.md` исключает `in-work` из новой
  FIX-очереди и считает существующий `fix/<N>` без открытого PR проигранным
  branch-claim;
- `tools/pipelinehealth/main.go` при `in-work` или ссылке из открытого PR просто
  пропускает issue, поэтому потерянная работа не попадает ни в FIX, ни в
  `human_waiting`;
- MERGE в `.claude/skills/merge-shepherd/SKILL.md` и
  `references/legacy-protocol.md` снимает `in-work` только с issues, названных
  closing keywords, а отдельный post-merge handoff знает только plan-PR;
- быстрый путь `promptpilot.project_pipeline` выполняет merge и post-merge
  действия вне этого репозитория, поэтому новый контракт должен одинаково
  работать в quick path, legacy fallback и recovery;
- план 159 уже задаёт срезы A–D и требует закрыть #1274 только срезом D, но его
  заголовки — человекочитаемый текст, а не достаточное машинное доказательство
  того, какой срез завершён и какой разрешён следующим.

Выбран вариант 1 из triage issue #1379, подтверждённый последующим комментарием
владельца: сохранить одну ведущую issue и ввести явный committed next-slice
handoff между MERGE, FIX и `pipelinehealth`. Вариант с отдельной дочерней issue
на каждый срез не используется.

Цель этапа — сделать каждый переход между срезами отдельной восстанавливаемой
транзакцией. Само наличие `Part of #N`, ветки или редактируемого тела PR не
считается доказательством перехода.

## Наблюдаемая семантика

### Декларация срезов в плане

План, который допускает несколько продуктовых PR, объявляет линейную цепочку
точными отдельными строками рядом с заголовками срезов:

```markdown
<!-- pp:plan-slice key=A next=B -->
### Срез A — ...

<!-- pp:plan-slice key=D next=done -->
### Срез D — ...
```

`key` и `next` — ASCII-токены `[A-Za-z0-9][A-Za-z0-9._-]{0,31}`; каждый `key`
уникален, ровно один ключ начальный, ссылки `next` образуют одну цепочку без
циклов и пропусков, `done` встречается ровно один раз. Валидатор работает по
байтам UTF-8/LF и сохраняет SHA-256 всего файла плана. Произвольный Markdown
между маркерами остаётся содержательной спецификацией для человека.

Для одноразового PR ничего не меняется: `Fixes #N` и ветка `fix/N` остаются
обычным путём. Новый протокол включается только для issue, у которой выбранный
влитый план содержит не меньше двух валидных `pp:plan-slice`.

### Доказательство активного среза

До передачи PR в REVIEW FIX публикует на PR один доверенный, не редактированный
record, привязанный к точному HEAD и версии плана:

```text
pp-slice-declaration-v1
issue=<N>
plan-path=Plans/<NNN>-<slug>.md
plan-sha256=<64 lowercase hex>
slice=<key>
next=<key|done>
previous-handoff=<REST comment id|none>
head=<40 lowercase hex>
body-sha256=<64 lowercase hex>
<!-- pp:slice-declaration -->
```

Тело PR содержит отдельные строки `Slice-Issue: #N`, `Slice-Plan: <path>`,
`Slice-Key: <key>` и `Next-Slice: <key|done>`. Record фиксирует их точное
прочтение, но proof строится не по одному редактируемому телу: REVIEW проверяет
record через два полных одинаковых GraphQL snapshot, требует
`lastEditedAt == null`, отсутствие последующего delete, совпадение HEAD,
SHA-256 тела PR и SHA-256 плана, а committed `pp:head-reviewed` для такого PR
несёт `slice-declaration=<id>`. Изменение HEAD, тела PR или плана требует нового
review epoch и новой декларации.

FIX одновременно оставляет на ведущей issue расширенный `pp:in-work` record с
номером PR, HEAD, id декларации, ключом среза и предыдущим handoff. Поэтому
исчезновение открытого PR после merge наблюдаемо по самой issue, даже если
post-merge процесс упал до первого нового комментария.

Для `next != done` обязательны `Part of #N` и отсутствие `Fixes`, `Closes` или
`Resolves` для этой issue. Для `next=done` обязательно ровно одно closing
упоминание ведущей issue. MERGE отказывает при смешанном, неоднозначном или
несогласованном объявлении; текст другой issue не может расширить область
перехода.

### Committed next-slice handoff

После успешного compare-and-merge доказательством завершения становится ровно
один `MergedEvent` с проверенным HEAD и merge SHA. По нему и проверенной
`slice-declaration` MERGE создаёт на ведущей issue root-record:

```text
pp-next-slice-v1
issue=<N>
plan-path=Plans/<NNN>-<slug>.md
plan-sha256=<64 lowercase hex>
completed-slice=<key>
next-slice=<key>
pr=<PR>
head=<40 lowercase hex>
merge=<40 lowercase hex>
declaration=<REST comment id>
previous-handoff=<REST comment id|none>
<!-- pp:next-slice-claim fingerprint-sha256=<64hex> owner=<uuid> -->
```

Fingerprint — точная ASCII/LF запись всех полей выше плюс сохранённый
issue-decision fingerprint; JSON, CRLF и Unicode-нормализация в хеш не входят.
Каноничен самый ранний доверенный неотредактированный root с тем же
fingerprint. Одновременные побайтово эквивалентные roots после одного
`MergedEvent` — диагностические losers; конфликтующие roots закрывают маршрут
человеку.

Root — начальная 30-минутная lease. Renewal/takeover строится точными
`pp:next-slice-lease` с `previous`, как в остальных committed-протоколах.
Владелец перед каждой фазой дважды получает идентичный server-ordered GraphQL
timeline и заново проверяет REST issue, PR, labels, canonical triage/decision,
declaration, `MergedEvent`, отсутствие edit/delete и свою активную lease.
`hold`, закрытие issue, новая версия решения или непротокольное вмешательство
останавливают транзакцию.

Фазы handoff неизменяемы и восстанавливаемы:

1. root фиксирует завершённый и следующий срез до изменения labels;
2. MERGE снимает `in-work`, только если timeline доказывает, что это та метка,
   которую поставил завершённый срез, сохраняет `approved`, `ready-fix` и
   ручную `queue:p*`, затем публикует `pp:next-slice-labels` с event watermark и
   хешем итоговых labels;
3. `pp:next-slice-done claim=<root-id> fingerprint-sha256=<hash>` коммитит
   доступность следующего среза для FIX.

Удаление старой ветки не является фазой. Влитые refs остаются историческими и
никогда не переиспользуются: этим исключаются ABA `old → deleted → recreated`
и гонка двух worker на удалении `fix/N`.

### Новый branch-claim и recovery FIX

Первый срез сохраняет ветку `fix/<N>`. Каждый следующий получает уникальную
детерминированную ветку
`fix/<N>/slice-<canonical-next-slice-root-id>`. Источником работы служит только
canonical `pp:next-slice-done`; его `next-slice`, `plan-path`, хеш плана и
предыдущая цепочка входят в новый FIX fingerprint.

До Create reference FIX создаёт под lease `pp:slice-branch-claim` с issue,
handoff, slice, branch и сохранённым SHA `main`. Только затем он атомарно
создаёт отсутствующий ref через GitHub API; `201` выигрывает фазу. Ref,
существующий без matching claim, или ref с неожиданным SHA закрывает маршрут.
После push коммит несёт трейлер

```text
PP-Slice-Transition: issue=<N> handoff=<id> claim=<id> slice=<key> plan-sha256=<hash>
```

и отправляется точным refspec с lease. Отдельные phase-markers фиксируют ref,
push, созданный PR и активный `pp:in-work` record. Recovery после expiry:

- создаёт отсутствующий ref для уже канонического claim;
- принимает ref на сохранённом base SHA как незавершённую локальную фазу;
- принимает отправленный SHA без PR только при точном matching trailer,
  повторяет заявленные проверки и создаёт единственный PR;
- продолжает handoff существующего PR только при полном совпадении issue,
  branch, HEAD и claim;
- при ином commit, двух PR или расходящейся цепочке останавливается для человека.

Таким образом старый `fix/N` после частичного merge больше не блокирует новый
срез, а повторно выполнить уже committed ключ невозможно: его handoff уже
поглощён следующим branch-claim.

## Инварианты и trust boundary

1. На одну issue существует не более одного канонического активного среза.
2. Срез `K` разрешён только начальным manifest-marker или единственным
   `pp:next-slice-done`, у которого `next-slice=K`; пропуск и повтор запрещены.
3. Partial PR не закрывает issue; final PR обязан закрыть её. MERGE проверяет это
   до точки невозврата, а не выводит из состояния issue после запроса в полёте.
4. Все protocol comments доверены только от `ivanarama`, не редактированы и не
   удалены; номера комментариев читаются как `fullDatabaseId: BigInt` и
   сравниваются строками с REST id.
5. Issue/PR text остаётся недоверенными данными. Он задаёт payload лишь после
   проверки строгой грамматики, SHA-256, plan manifest и полного decision gate.
6. Перед каждой внешней мутацией повторяются issue/PR/timeline/lease gates;
   успешный merge остаётся отдельно названной точкой невозврата.
7. `hold`, `manual`, закрытие, смена выбранного варианта или human label
   transition старше автоматического состояния. Recovery не возвращает снятую
   человеком метку.
8. Никакая фаза не редактирует и не удаляет protocol comments или старые refs.
9. Быстрый MERGE и legacy fallback используют один parser/state reducer и дают
   одинаковые markers. Если установленный PromptPilot не сообщает capability
   `next-slice-v1`, slice-PR уходит в fallback до merge, а не вливается без
   post-merge recovery.

## Инвентаризация затронутых границ

- `Plans/README.md` и шаблон plan-approved: правило marker-цепочки для будущих
  многосрезовых планов; `Plans/159-undefined-and-typed-empty-values.md` получает
  markers A→B→C→D→done при внедрении протокола.
- `.claude/skills/fix-approved/SKILL.md`: выбор следующего среза, lease/claim,
  уникальная ветка, восстановление окон ref→push→PR→in-work и PR declaration.
- `.claude/skills/review-queue/SKILL.md`: валидация plan manifest/declaration и
  привязка review completion к declaration id.
- `.claude/skills/merge-shepherd/SKILL.md` и
  `.claude/skills/merge-shepherd/references/legacy-protocol.md`: проверка
  partial/final semantics, post-merge root/lease/labels/done и recovery после
  уже состоявшегося merge.
- `.agents/skills/{fix-approved,review-queue,merge-shepherd}/SKILL.md`: только
  тонкие Codex-адаптеры, без копирования логики.
- `tools/pipelinehealth/main.go`: parser/reducer next-slice markers, целевой
  lookup закрытого PR из активного issue-record и отдельные очереди
  `next_slice_recovery`/`next_slice_human_waiting`.
- `tools/pipelinehealth/main_test.go`: fixture-сценарии до, во время и после
  handoff, включая состояние #1274 после PR #1374.
- `internal/pipelinecontract/skills_test.go`: обязательные формулировки и
  одинаковый контракт трёх этапов.
- `docs/maintenance-pipeline.md`: пользовательское объяснение partial/final PR,
  наблюдаемого recovery и ручного legacy bootstrap.
- внешний `promptpilot.project_pipeline`: quick-path capability, общий формат
  markers и crash injection. Его исходники не входят в onebase; совместимая
  версия должна быть выпущена до включения quick path в конфиге планировщика.

Общий parser и reducer не следует реализовывать трижды в текстах и утилите.
Репозиторная Go-реализация задаёт fixtures/grammar; PromptPilot обязан прогнать
те же JSON golden vectors перед объявлением capability.

## Совместимость и миграция

Схема пользовательской БД, продуктовый runtime и публичный OneBase API не
меняются. Новые сущности живут только в GitHub comments, refs и инструментах
сопровождения; новые labels не требуются.

Односрезовые issues и уже открытые обычные PR продолжают текущий маршрут.
Новые многосрезовые планы получают manifest до первого продуктового PR. План
159 обновляется markers без изменения содержательной нарезки.

Состояние #1274/PR #1374 возникло до появления декларации, поэтому его нельзя
автоматически объявить доказанным по редактируемому post-merge телу PR. Для
него и других legacy-orphans разрешён ровно один ручной, доверенный и
неотредактированный bootstrap-record с точными `issue`, `plan-path`,
`plan-sha256`, `completed-slice`, `next-slice`, `pr`, `head` и `merge`. Он
проверяется против единственного `MergedEvent`, существующего плана и
отсутствия closing keyword, затем входит в тот же root/lease/labels/done
reducer. Неоднозначный PR, изменённый план или два возможных следующих среза
остаются в `next_slice_human_waiting`; автоматика не угадывает.

До выпуска PromptPilot с `next-slice-v1` quick path обязан fail closed для
slice-PR и передавать их legacy процедуре. После выпуска capability включается
сначала на fixture dry-run, затем на одном реальном переходе #1274 B→C.

## Нарезка реализации на небольшие PR

### Срез A — грамматика, manifest и наблюдаемость

Добавить строгий parser/reducer и golden vectors, markers в план 159, правила в
plan-approved и новые состояния `pipelinehealth`. На этом шаге мутации
next-slice ещё запрещены: найденный merged-orphan виден как recovery/human, но
FIX его не берёт.

Публичная приёмка: fixture точного состояния #1274 после PR #1374 больше не
исчезает из отчёта; она классифицируется как legacy bootstrap required.

### Срез B — FIX claim и восстановление до PR

Ввести canonical handoff consumption, уникальные refs, branch lease/phase
markers, commit trailer, PR declaration и расширенный `pp:in-work`. Покрыть все
окна от claim до созданного PR. Пока MERGE не умеет выпускать новый handoff,
этот срез работает на синтетическом committed fixture.

Публичная приёмка: два параллельных FIX worker на одном `pp:next-slice-done`
создают один ref и один PR; crash после ref и после push восстанавливается без
повторного выполнения предыдущего среза.

### Срез C — REVIEW/MERGE и PromptPilot quick path

Привязать review proof к declaration, реализовать partial/final gate,
post-merge root/lease/labels/done в legacy fallback и PromptPilot, добавить
capability negotiation. Проверить recovery после merge до root, после root и
между каждой label-фазой.

Публичная приёмка: partial PR с `next=B` остаётся открытой issue и выдаёт один
handoff B; final PR с `next=done` закрывает issue и не возвращает её в FIX;
quick path и fallback оставляют побайтово одинаковую логическую цепочку.

### Срез D — legacy bootstrap и сквозной сценарий

Задокументировать и применить явный bootstrap #1274 A→B, затем прогнать два
последовательных тестовых partial PR (A→B и B→C) и final-вариант. Обновить
maintenance docs и удалить только временный feature gate, но не исторические
markers/refs.

Публичная приёмка: после merge первого partial PR `pipelinehealth` показывает
короткую recovery-фазу, затем следующий FIX candidate; второй PR получает
другую ветку и previous-handoff, а повторный запуск A отвергается. Рестарт в
каждой точке оставляет тот же итог.

## Тесты и проверки

- table-driven grammar tests: duplicate/missing keys, cycle, unknown next,
  non-ASCII token, CRLF/BOM, path traversal, несовпадающие body/plan hashes;
- reducer tests: equivalent roots, conflicting roots, lease renewal/takeover,
  edit/delete, same-second events, BigInt comment ids и human relabel;
- `pipelinehealth` fixtures: открытый active PR, merged до root, root без labels,
  labels без done, done→FIX, closed-unmerged→human, final merge и legacy #1274;
- contract tests трёх canonical skills и тонких адаптеров;
- PromptPilot fake-GitHub integration с fault injection до и после каждого
  POST/DELETE/merge и повторным запуском recovery;
- concurrency-тест двух FIX и двух MERGE workers на одной цепочке;
- end-to-end две последовательные ветки и PR одной issue с проверкой, что
  `Part of` не закрывает, `Fixes` final закрывает, а старый ref не используется;
- обязательные репозиторные проверки каждого среза:
  `go test ./tools/pipelinehealth ./internal/pipelinecontract`, затем
  `go test ./...` и `go build ./...`; golden vectors PromptPilot запускаются
  отдельно в его окружении.

Тест считается приёмочным только через публичный запуск `pipelinehealth` или
CLI PromptPilot над fake GitHub API. Прямой вызов приватного parser без
сквозного состояния не заменяет регрессию.

## Риски и откат

- **Двойной следующий PR.** Предохранители: один canonical done, уникальный ref
  от root id, persistent branch claim и точный PR lookup перед каждой мутацией.
- **Повтор уже влитого среза.** Предохранители: linear manifest,
  previous-handoff, declaration в review proof и immutable merge boundary.
- **Преждевременное закрытие.** Partial/final gate проверяет closing keyword до
  merge; несовпадение `next` останавливает PR.
- **Crash после merge.** Расширенный issue active-record позволяет
  `pipelinehealth` найти закрытый PR; MergedEvent + declaration позволяют
  восстановить root без доверия к изменяемому телу.
- **Расхождение quick path и fallback.** Один набор golden vectors и обязательная
  capability; неизвестная версия только отказывает до merge.
- **Человеческое вмешательство принято за recovery.** Полные event/timeline
  gates и правило «последний human transition старше» запрещают re-add/delete.
- **Рост GitHub API-нагрузки.** Targeted lookup закрытого PR делается только для
  issue с точным active marker; остальная очередь остаётся пагинированным REST.

Откат выполняется feature gate: запретить новые slice declarations и вернуть
такие issues в диагностическое `next_slice_human_waiting`. Уже созданные
comments и refs не удаляются; они остаются аудитом и не влияют на обычные
issues без валидного manifest. Откат PromptPilot делается до версии, которая
fail closed отправляет slice-PR в fallback. Label-фазы возвращаются только по
сохранённому event watermark; слепое добавление `in-work` запрещено.

## Verification

1. Создать fixture issue с валидным plan manifest A→B→done и первым PR A.
2. Доказать REVIEW declaration, влить A, остановив процесс в каждой фазе
   post-merge, и каждый раз запустить `pipelinehealth` и recovery.
3. Убедиться, что итоговые labels — `approved`, `ready-fix` и исходная
   `queue:p*`, а `in-work` отсутствует.
4. Дважды параллельно запустить FIX: существует только
   `fix/N/slice-<root-id>` и один PR B; ветка `fix/N` не меняется.
5. Влить B как final с `Fixes #N`; issue закрыта, `in-work` снята, нового
   next-slice handoff нет.
6. Повторить с edit/delete declaration, поздним `hold`, снятием/re-add labels,
   закрытым без merge PR и conflicting bootstrap: во всех случаях fail closed и
   точная диагностика.
7. На реальном #1274 проверить ручной bootstrap PR #1374 A→B и один полный
   автоматический переход B→C до общего включения capability.

## Эстимейт

- срез A: 2–3 дня;
- срез B: 3–4 дня;
- срез C: 4–6 дней, включая синхронный релиз PromptPilot;
- срез D: 1–2 дня.

Итого: 10–15 рабочих дней. Оценка включает fault-injection и гонки; без них
протокол не считается crash-safe.

## Готово, когда

- состояние issue #1274 после PR #1374 наблюдаемо и имеет однозначный legacy
  recovery, а не исчезает из всех очередей;
- каждый partial merge коммитит ровно один следующий ключ, final merge не
  создаёт следующий handoff;
- FIX создаёт новую уникальную ветку и один PR для следующего среза, не меняя и
  не удаляя ref предыдущего;
- crash/retry на каждой внешней мутации сходится к одному состоянию;
- повтор, пропуск, edit/delete, late hold и человеческий relabel завершаются
  fail closed с диагностикой;
- quick path, fallback и `pipelinehealth` используют одну грамматику и проходят
  общие golden vectors;
- сквозной тест двух последовательных PR одной issue стабильно проходит;
- обычные одноразовые FIX/REVIEW/MERGE маршруты не изменили поведение.
