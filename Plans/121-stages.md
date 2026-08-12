# План 121 — Этапы: декларативные состояния, гейт переходов и «где застряло»

Дата проектирования: 2026-08-12.
Статус: ⬜ **Проект** (не начато). Вырос из разбора плана 85: см. «Отношение к
плану 85» ниже.

## Контекст

Состояние объекта («Черновик → На согласовании → Утверждён → Отгружен») есть
почти в каждой прикладной конфигурации, но платформа про него ничего не знает.
Она видит обычный реквизит-перечисление, а порядок этапов и допустимые переходы
живут в коде модуля — каждый раз заново и каждый раз только на том пути записи,
где автор конфигурации вспомнил их проверить.

Из-за этого не работают три вещи, которые пользователь считает само собой
разумеющимися: **нельзя перескочить этап** (гарантированно, а не «если вызвали
через форму»), **видно, кто и когда двигал объект**, и **видно, где всё
застряло** — сколько объектов на каждом этапе и сколько времени они там висят.

Состояние на 2026-08-12 (ссылки проверены на `main` `aae7b794`):

| Кусок | Где | Состояние |
|---|---|---|
| **Обычная точка записи №1** | `storage/crud.go:76` `db.upsert` | создание и все записи без версии: `Upsert`/`UpsertProvisional`/`UpsertPreserveVersion` (`crud.go:46-65`) |
| **Обычная точка записи №2** | `storage/optimistic_lock.go:36` `UpsertVersioned` | правки существующих объектов: собственный `UPDATE … WHERE id AND _version` **мимо** `upsert`. Через него идут форма UI, REST с If-Match и DSL (версия ставится при чтении объекта) — комментарий в файле фиксирует, что хук FTS однажды именно этот путь и пропустил |
| — из форм и проведения | `entityservice/service.go:301`, `:330-342` | `UpsertProvisional` / `Upsert` / `UpsertPreserveVersion` / `UpsertVersioned` |
| — из DSL (`Документы.X`) | `ui/dsl_documents.go:791`, `:795`, `:885` | отдельный путь, не через `entityservice` |
| — запись справочника из DSL | `storage/catalog_write.go:18` | вызывает обычный `Upsert`, поэтому сходится в точке №1 |
| **Доверенный writer: обмен** | `exchange/package.go:483`, `:644`, `:726-736` | `ApplyPackage` после разрешения конфликта сейчас вызывает обычный `Upsert`; ему нужен отдельный узкий storage-writer с replication-семантикой и provenance |
| **Доверенный writer: миграция** | `storage/migrate.go:558`, `predefined.go:103`, `:160-209` | `SyncAllPredefined` делает прямой `INSERT … ON CONFLICT DO UPDATE` произвольных полей сущности **мимо обеих** обычных точек; это третья обязательная точка stage-инварианта |
| **Raw restore** | `backup/universal.go:1202-1279`, `:1540-1553` | импортирует снимок таблиц внутри общей restore-транзакции; не проигрывает переходы заново, а восстанавливает `_stage_history` как данные |
| **Старое значение уже читается** | `storage/crud.go:79-85` и `optimistic_lock.go:43-46` (`oldRow` через `GetByID`) | в обеих точках, ради аудит-диффа, до всякой записи; сейчас чтение **best-effort** и ошибка отбрасывается — для инварианта переходов этот код переиспользовать как есть нельзя |
| **Доступ к значениям расходится** | `storage/crud.go:875-879`, `storage/audit.go:221-227`, `dsl/interpreter/catalogs_proxy.go:421-425`, `:598-600` | persistence читает имя поля и его lowercase-вариант, DSL справочника хранит lowercase, а `AuditDiff` читает только точное `f.Name`; `AuditDiff` **нельзя** переиспользовать как есть для stage-гейта |
| **Актор уже в контексте** | `audit.go:23` `WithAuditUser` (ставит auth-middleware), `:28` `auditUserFromCtx` | логин доезжает до слоя storage |
| Журнал изменений | `audit.go:262` `logCreate`, `:278` `logUpdate` | **условный и best-effort**: `!s.Enabled \|\| !s.Create/Update` молча пропускает запись, ошибка `db.Log` игнорируется |
| Декларация состояний | — | **нет**: `metadata/yaml.go:57-87` `rawEntity` не знает ни `stages`, ни `transitions` |
| Гейт переходов | — | **нет**: `grep transitions\|state_machine` по `internal/metadata` пуст |
| История переходов | `examples/tasks/inforegs/история_статусов.yaml` | прикладной обходной путь: регистр сведений, который пишет DSL руками |
| «Сколько висит на этапе» | — | нет; считается только вручную запросом по прикладному регистру |
| Схема маршрута | — | нет; типы диаграмм в виджетах — `pie`/`bar`/`line` (`widget/runner.go:93,106,111-121`) |
| **ECharts уже вендорен, graph-adapter отсутствует** | `internal/webassets/echarts/echarts.min.js`, отдача `ui/static.go:66`, подключения `ui/templates.go:1161`, `:2104`, `:3214`, хелпер `ui/widget_helpers.go:89` зарегистрирован в `templates.go:704` | новая JS-зависимость не нужна, но текущие `ChartData`/`EChartsOption` поддерживают только numeric `bar`/`line`/`pie` (`widget/runner.go:67-128`); для `nodes`/`links` нужен отдельный безопасный adapter |
| Системная таблица — прецедент | `storage/audit.go:72` `EnsureAuditSchema`, `ai_audit.go:36`, `fts.go:23` `_fts`, `exchange.go:77` `_exchange_changes` | образец: `Ensure*Schema` + явный вызов на всех путях подъёма базы |
| Транзакции | `storage/tx.go:237` `WithTxIfNeeded`, `:248` `WithTxScope`; `storage/tx_hooks.go:37` `DeferUntilTxCommit` | публичная запись может присоединиться к транзакции вызывающего или открыть свою. `WithTxScope` даёт nested savepoint. `DeferUntilTxCommit` годится для внешнего side effect, но **не** для `_stage_history`: история должна быть SQL-записью внутри той же транзакции, иначе между commit и hook остаётся crash-gap |
| `onebase check` не читает рабочие строки | `cli/check.go:63-70`, `configcheck/full.go:77-81`, `:148-179` | CLI передаёт только каталог проекта, а executable SQL проверяет на новой временной пустой SQLite; здесь возможна проверка схемы графа и predefined, но не записанных нарушений и текущих данных |

### Четыре ограничения задают реализацию

1. **Обычных точек перехвата две, но writers не исчерпываются ими.** Формы,
   REST и прикладной DSL сходятся в
   `storage/crud.go:76` `db.upsert` (создание и записи без версии) и
   `storage/optimistic_lock.go:36` `UpsertVersioned`, который делает
   собственный `UPDATE … WHERE id AND _version` **мимо** `upsert`. Прецедент
   зафиксирован комментарием в самом файле: хук FTS однажды не задел второй
   путь, и в поиске оставалось стёртое пользователем значение. Гейт и история
   обязаны стоять в обеих точках. Кроме них, `SyncPredefined` пишет поля
   напрямую, а exchange должен получить отдельный writer: оба маршрута обязаны
   сохранить валидность значения и синтетическую историю со своим source, хотя
   могут обходить adjacency. Raw restore — третий, ещё более низкий доверенный
   маршрут: он восстанавливает уже готовые строки объекта и истории и не должен
   изображать пользовательские переходы.

2. **Stage-accessor канонический, `AuditDiff` — не источник истины.** Общая
   `canonicalFieldValue` возвращает `(value, present)` с теми же правилами, что persistence:
   сначала `f.Name`, затем `strings.ToLower(f.Name)` при отсутствующем/`nil`
   exact-значении. Её используют `fieldValueDialect`, stage-accessor, predefined
   и exchange, поэтому write и check не расходятся; `present` отдельно отличает
   полностью отсутствующий ключ predefined от явного пустого значения.
   Stage-accessor поверх неё нормализует enum к строке. `AuditDiff` можно отдельно
   исправить и затем переиспользовать нормализацию, но его текущий exact-key
   lookup не годится. Актор берётся из audit-context только для `source=local`;
   ошибка записи истории, в отличие от best-effort audit, откатывает объект.

3. **Read → check → write → history — одна сериализованная DB-транзакция.**
   Сейчас ни `Upsert`, ни `UpsertVersioned` сами по себе не обещают открыть
   транзакцию, а unversioned-путь не блокирует строку между `GetByID` и
   `INSERT … ON CONFLICT DO UPDATE`. Если просто поставить проверку рядом с
   аудитом, два конкурентных запроса могут оба проверить переход из одного
   старого этапа, а при autocommit объект может сохраниться без истории.
   Для сущности со `stages` публичные точки идут через `WithTxScope` и
   внутренний `...InTx`: прямой вызов получает транзакцию, а внутри чужой
   транзакции операция получает savepoint и сама откатывает частичную запись,
   даже если вызывающий обработает ошибку и продолжит работу. Без `stages`
   остаётся прежний путь без новой stage-транзакции и блокировки.

   На PostgreSQL **до чтения** берётся существующий transaction advisory lock
   `DB.AdvisoryXactLock` (`storage/advisory_lock.go:20-47`) по стабильному ключу
   `(entity,id)`, затем существующая строка дополнительно читается `FOR UPDATE`.
   Advisory lock обязателен и для create: `FOR UPDATE` не блокирует отсутствующую
   строку. На SQLite staged unversioned update использует прочитанную `_version`
   как CAS, create — `INSERT … ON CONFLICT DO NOTHING` с проверкой
   `RowsAffected`; `SQLITE_BUSY(_SNAPSHOT)` нормализуется в concurrent/version
   conflict. Матрица открывает **два независимых `storage.DB` к одному файлу**,
   потому что один SQLite handle сам ограничен `SetMaxOpenConns(1)`
   (`storage/sqlite.go:108-126`). Любая ошибка чтения, кроме переносимого
   `storage.IsNotFound` (`tx.go:224-227`), является ошибкой операции, а не
   созданием нового объекта.

4. **Порядок истории причинный, timestamp — только часы.** `at DESC` не задаёт
   последнее событие: PostgreSQL `now()` фиксирован на старте транзакции
   (`storage/dialect.go:88-89`), SQLite `datetime('now')` имеет секундную
   точность (`:149-153`), а одна внешняя транзакция может выполнить несколько
   логических записей. Поэтому под той же record-lock вычисляется монотонный
   `event_no`; именно он определяет latest. `at` берётся после lock через новый
   dialect statement-time (`clock_timestamp()` / SQLite `strftime(...%f...)`)
   и нужен только для отображения и длительности.

## Цель

- **декларация**: у сущности объявляются упорядоченные этапы и допустимые
  переходы — на существующем поле-перечислении, **без нового вида объекта
  метаданных**;
- **гейт**: недопустимый локальный переход отвергается в слое storage — в обеих
  обычных точках записи (`upsert` и `UpsertVersioned`); migration/predefined,
  replication и raw restore имеют отдельную явно маркированную семантику, а не
  скрытый bypass;
- **история**: платформа сама пишет `_stage_history` (кто, когда, из какого в
  какой), безусловно и в той же транзакции;
- **отчёт «где застряло»**: сколько объектов на этапе, сколько времени висят,
  что просрочено;
- **схема**: этапы со стрелками и подсветкой текущего — на уже вендоренном
  ECharts.

### Чего этот план не делает

Ни экземпляров процессов, ни объекта «Задача», ни адресации, ни
автопродвижения, ни `bpm/*.yaml`, ни параллельных ветвей с кворумом. Всё это —
предмет плана 85, и он остаётся замороженным.

## Синтаксис / метаданные

Блок `stages` у сущности, рядом с `fulltext`/`tile_view`/`based_on`
(`metadata/yaml.go:57-87`):

```yaml
name: Заявка
fields:
  - { id: f_state, name: Состояние, type: enum:СостояниеЗаявки }
stages:
  field: Состояние                  # текущее имя поля; identity берётся из его id
  order: [Черновик, НаСогласовании, Утверждена, Отклонена]
  initial: Черновик                 # опц.; по умолчанию первый элемент order
  transitions:                      # допустимые переходы; всё, чего нет, — запрещено
    - { from: Черновик,       to: [НаСогласовании] }
    - { from: НаСогласовании, to: [Утверждена, Отклонена] }
    - { from: Отклонена,      to: [Черновик] }
  deadline_days: { НаСогласовании: 2 }   # опц.: сколько ждём на этапе → просрочка
  enforce: warn                          # warn (умолчание) | strict
```

`enforce: warn` — недопустимый локальный переход пишется в runtime-лог и в
историю с признаком нарушения, после чего виден в защищённом отчёте этапов, но
запись проходит. `strict` — запись отвергается. `onebase check` проверяет только
статическую конфигурацию графа и predefined: к рабочей БД он не подключается.
Умолчание `warn` выбрано осознанно: иначе включение блока в существующей
конфигурации ломает уже работающие данные и обработки (см. «Обратная
совместимость»).

Значения этапов не дублируются — они берутся из перечисления, объявленного в
`enums/`. `order` задаёт порядок для отчёта и схемы, `transitions` — правила.
Имена значений enum здесь являются хранимыми идентификаторами, как и сейчас в
`metadata.Enum.Values`; локализуемая подпись берётся через `ValueTitle`. Поэтому
смена подписи безопасна, а переименование самого enum-идентификатора остаётся
явной миграцией бизнес-данных и истории, не косметическим rename этого плана.
`field` остаётся удобной ссылкой по текущему имени, но при загрузке обязательно
разрешается в непустой устойчивый `Field.ID` (план 81). Модель `Stages` хранит и
текущее имя для доступа к колонке, и `FieldID` как identity истории. При
переименовании конфигуратор меняет `stages.field` вместе с `Field.Name`, сохраняя
`Field.ID`. Текущий web-конфигуратор имя существующего поля не редактирует
(`configurator_field_ids.go`), а его raw `yaml.Node` обязан лишь сохранить блок.
При ручном rename автор меняет и `stages.field`; будущий rename UI обязан найти
ссылку по прежнему `Field.ID` и переписать имя атомарно. Поэтому rename не
начинает новую последовательность событий и не прячет прежнюю историю.
При локальном создании допустим только `initial`; пустое или неизвестное значение
считается нарушением. Доверенные migration/replication writers могут
синтетически создать объект сразу на другом **известном** этапе и всегда
помечают источник. Запись с неизменившимся этапом не создаёт событие истории.

Статическая валидация до запуска проверяет весь контракт:

- `order` непуст, не содержит дублей после канонизации и покрывает **все**
  значения enum; `initial` входит в `order` (если отсутствует — это первый
  элемент);
- `field` существует, имеет тип `enum`, имеет непустой валидный `id` по правилам
  плана 81, у сущности ровно один блок `stages`;
- каждый `from` и `to` входит в `order`, одно ребро не объявлено дважды,
  self-edge запрещён как бессмысленный (неизменившийся stage вообще не является
  событием), все этапы достижимы из `initial`; циклы допустимы, терминальные
  вершины без исходящих рёбер допустимы;
- `deadline_days` ссылается только на `order` и содержит положительные целые
  числа; отсутствующий `enforce` нормализуется в `warn`, явно заданный — только
  `warn` или `strict`;
- значение stage во всех `predefined` при явном указании непусто и известно.
  Отсутствующее значение означает: при первом создании storage подставляет
  `initial`, при последующих sync существующий stage не перезаписывается.

## Хранилище / SQL

`_stage_history` — системная таблица, создаётся `EnsureStageHistorySchema` по
образцу `storage/audit.go:72`:

| Колонка | Смысл |
|---|---|
| `id` | PK |
| `entity_name`, `record_id` | объект |
| `field_id` | устойчивый `Field.ID` поля-этапа; имя намеренно не является identity и берётся из текущих метаданных |
| `event_no` | монотонный номер события внутри `(entity_name, field_id, record_id)`, назначенный под той же блокировкой; источник истины для latest |
| `from_stage`, `to_stage` | было / стало (`from_stage` пуст при создании) |
| `at` | wall-clock момент на принимающей БД, снятый после record-lock; не используется как tie-breaker |
| `user_id`, `user_login` | nullable-актор из `auditUserFromCtx` только для `source=local`; migration/exchange принудительно не подделывают пользователя |
| `source`, `source_ref` | `local` / `exchange` / `migration` и опциональный узел/пакет — происхождение синтетического перехода |
| `violation` | `true` для пропущенного в режиме `warn` недопустимого перехода |

Ограничение `UNIQUE (entity_name, field_id, record_id, event_no)` защищает
последовательность. Индексы:
`(entity_name, field_id, record_id, event_no DESC)` — последнее событие и история
объекта; `(entity_name, field_id, to_stage, at)` — фильтр истории по этапу. Миграция
сущности дополнительно создаёт индекс её **текущего stage-поля**, иначе отчёт
вынужден сканировать всю бизнес-таблицу независимо от индексов истории.

Отчёт группирует текущие, не помеченные на удаление строки таблицы сущности по
полю-этапу и присоединяет событие с максимальным `event_no` для каждого
`record_id`; один индекс истории сам по себе не доказывает текущий этап, потому
что в нём остаются прежние переходы. Помеченные на удаление объекты в агрегат v1
не входят, их история остаётся доступной из карточки при наличии прав.

Время на этапе считается как разница между текущим моментом и последней
записью по объекту. Арифметика дат в запросах уже есть (issue #707, PR #727).
Если история отсутствует или `latest.to_stage` не равен текущему значению,
длительность — «неизвестно», а runtime report показывает рассинхронизацию;
подставлять время чужого этапа и считать такую строку просроченной нельзя.
Wall-clock не участвует в выборе latest даже при одинаковых или сдвинутых часах.

## Изменения в коде

- **`internal/metadata/`** — `rawEntity.Stages`, модель `Stages` с разрешённым
  `FieldID` и полная статическая валидация из предыдущего раздела. Она же
  проверяет явные значения stage у predefined. Конфигурация без `stages`
  получает `nil` в модели и не включает stage-ветку записи.
- **Все поверхности метаданных** — `internal/cli/schema.go` описывает `stages`
  и его вложенные ключи в JSON Schema, а `internal/configcheck/lint.go` добавляет
  верхнеуровневый ключ и nested-schema, чтобы опечатка не проходила как
  неизвестное расширение. `internal/launcher/configurator_types.go` сохраняет
  блок сырым `yaml.Node`: обычная правка полей через `saveEntity` не вправе его
  стереть. `save_entity_keys_test.go` остаётся structural guard, а отдельный
  round-trip тест покрывает непустой `stages`, включая порядок transitions и
  сохранение `Field.ID`. Отдельный metadata/storage тест загружает конфигурацию
  до/после ручного rename (`Field.ID` тот же, `stages.field` обновлён) и доказывает
  продолжение истории. Когда в UI появится rename, его rewrite получит свой тест.
- **`internal/storage/stage.go`** (новый) — единый `canonicalFieldValue` с
  persistence-compatible exact/lowercase lookup и признаком presence;
  существующий `fieldValueDialect` переводится на него, а `stageFieldValue`
  добавляет enum-нормализацию. Здесь же живут
  `checkStageTransition` и диалектная сериализация. `AuditDiff` as-is сюда не
  вызывается. Неизменившийся
  stage не создаёт событие. Пустое/неизвестное значение и local create не в
  `initial` проходят обычную `warn`/`strict` семантику; trusted
  exchange/migration всё равно принимают только известные непустые значения.
- **`internal/storage/crud.go` И `internal/storage/optimistic_lock.go`** — гейт
  ставится в **обе** обычные точки записи. Только при `entity.Stages != nil`
  публичная функция оборачивает полный цикл в `WithTxScope`; внутренний
  `...InTx` требует tx-context, берёт PG advisory lock до чтения либо применяет
  SQLite CAS, затем делает read → check → write → history. В
  `UpsertVersioned` успешность остаётся связана с `UPDATE … WHERE _version`:
  stale version не пишет историю. Ошибка чтения не маскируется под создание.
  При `strict` возвращается локализованная ошибка, при `warn` — обязательное
  event с `violation=true`; внешний runtime warning ставится через
  `DeferUntilTxCommit`, чтобы rollback/savepoint не оставлял ложный лог. Только
  SQL-history пишется немедленно внутри транзакции.
- **Provisional create** — для staged entity новая callback-операция
  `WithProvisionalCreate(..., fields, hook)` владеет scope целиком: внутренне
  вставляет техническую строку без history, вызывает hook, затем всегда делает
  финальный `UpsertPreserveVersion` по мутированному `fields` и пишет ровно одно
  create-event. Прямой публичный `UpsertProvisional` для staged entity
  отвергается; без `stages` его контракт не меняется. Так нельзя случайно commit
  provisional row без history. Финальный stage проверяется относительно пустого
  состояния: если hook сменил initial на иной stage, это create-нарушение
  (`warn` пропускает и помечает, `strict` откатывает строку и side effects).
  Логический create-mode передаётся только внутренним вызовом callback; публичный
  `UpsertPreserveVersion` сам по имени create не предполагает и на существующей
  строке DSL write/post считается обычным переходом со следующим `event_no`.
  Audit по-прежнему видит одно финальное create (`crud.go:50-65`, `:174-183`).
- **`internal/storage/stage_history.go`** (новый) —
  `EnsureStageHistorySchema`, обязательный `LogStageChange`, чтение истории и
  агрегат. Следующий `event_no = COALESCE(MAX(event_no), 0) + 1` вычисляется под
  record-lock, insertion error возвращается наружу. Никакого
  `DeferUntilTxCommit`: rollback объекта или savepoint обязан тем же SQL rollback
  откатить history.
- **`internal/storage/predefined.go`** — третий writer не прячется под общими
  Upsert. Текущий `SyncPredefined` сначала preallocate-ит UUID **всему** списку,
  чтобы разрешить self/cross-reference, и лишь затем проходит items; поэтому
  блокировка внутри item уже опаздывает. Для staged entity один `WithTxScope`
  охватывает весь sync. До первого поиска/генерации UUID он валидирует имена и
  берёт все логические PG-lock `(entity, predefined_name)` в отсортированном
  порядке (эквивалентно допустим один стабильный entity-level sync-lock). Это
  запрещает двум migrate раздать разные UUID одному conflict-target и не создаёт
  deadlock при разном порядке YAML. SQLite использует ту же цельность scope и
  проверяемый conflict/CAS между двумя handles, а не снимок, начатый до
  сериализации. После разрешения всей `nameToUUID` для фактического ID берётся и
  обычный `(entity,id)` record-lock, чтобы sync сериализовался с пользовательской
  записью. Direct upserts, существующие FTS hooks и history входят в тот же
  scope; старое значение и фактический record ID читаются строго. Отсутствующий stage при
  INSERT получает `initial`, а при conflict не обновляет stage. Явно заданное
  известное значение может создать или переместить predefined мимо adjacency,
  но пишет ровно одно synthetic event `source=migration`,
  `source_ref=entity/predefined_name`, actor `NULL`; одинаковое значение события
  не пишет. Это сознательная trusted migration-семантика, а не скрытый bypass.
- **Ранний подъём схемы** — `EnsureStageHistorySchema` вызывается в самом начале
  `DB.Migrate`, **до** `SyncAllPredefined` (`migrate.go:558`), поэтому общий
  chokepoint покрывает `migrate`, `run`, `deploy`, dev, test и schema migration
  universal restore и `DemoReset.migrateSchema`. Headless `procrun`, который может писать через те же DSL
  writers без `Migrate`, вызывает Ensure явно рядом с `EnsureAuditSchema`
  (`cli/procrun.go:68-80`); `openExchangeBase` делает то же рядом с
  `EnsureExchangeSchema` (`cli/exchange.go:88-113`), потому что CLI
  `exchange load/sync` тоже не мигрирует базу. Universal restore и DemoReset
  получают таблицу через ранний `DB.Migrate` внутри своего общего restore scope,
  до clear/import. Поэтому новый archive импортируется и в target, где таблицы
  ещё не было; отдельный ad-hoc Ensure в DemoReset не нужен.
- **`internal/exchange/` + узкий storage-writer** — для staged entity весь
  per-object цикл `local queue/version/read → conflict decision/hook → apply →
  queue cleanup/history` выполняется в одном scope. На PostgreSQL `(entity,id)`
  advisory/row lock берётся **до** чтения local state и держится до конца; иначе
  обычный save между решением конфликта и `applyObject` будет молча перетёрт по
  устаревшему решению. На SQLite write-intent берётся до чтения (либо весь цикл
  защищён final CAS с безопасным полным retry); нельзя начинать snapshot до
  сериализации. Conflict hook исполняется внутри scope как обычный local writer:
  PG lock reentrant в той же tx, nested savepoint допустим, adjacency не
  обходится, его отдельный local event сохраняется. Только после решения
  «incoming wins» вызывается `ApplyReplicatedEntity(ctx, ..., plan, fromNode,
  messageNo)`. Метод фиксирует `source=exchange`, канонический
  `source_ref=plan/from_node/message_no`, actor `NULL`, использует уже удерживаемую
  record-lock/event sequence, обходит только adjacency, но отвергает
  пустой/неизвестный destination обычного живого объекта.
  Tombstone — отдельная узкая операция: существующая строка сохраняет stage и
  получает только replication version/deletion state, без stage-event;
  tombstone отсутствующей строки создаёт технический deleted placeholder raw-
  веткой и тоже не проходит create-stage gate. Последующее resurrection обязано
  принести известный непустой stage и пишет единственный synthetic
  `empty→known`; duplicate tombstone/resurrection идемпотентны.
  Replication capability **не кладётся** в context всей `ApplyPackage`, иначе
  DSL conflict hook (`dslvars/exchange_hook.go:39-60`) унаследует bypass для
  собственных локальных записей. Exported storage method не объявляется
  security boundary; проверяемая гарантия — у ordinary `Upsert` нет параметра
  bypass и только exchange chokepoint вызывает специальный writer.
- **Universal backup/restore и DemoReset** — добавить `_stage_history` в единый
  `backup.systemTables` (`universal.go:75`): это автоматически включает export,
  manifest allowlist, clear/import и проверку counts. `DemoReset` уже вызывает
  `migrateSchema` внутри restore-транзакции, а она — ранний `DB.Migrate`; затем
  clear обязан удалить возможные synthetic migration/predefined events, и import
  устанавливает ровно history из архива. Таблица не
  имеет FK `user_id → _users`, как `_audit` (`storage/audit.go:75-91`):
  удалённые либо legacy actor UUID могут отсутствовать, а переносимый архив не
  должен из-за этого ломаться. DemoReset импортирует users/roles из архива и
  сохраняет только sessions/scheduled-runs. Полный restore старого архива без
  `_stage_history` очищает прежнюю target-history и оставляет пустую. Restore-
  миграция может временно создать synthetic events, но ни одно из них не
  переживает commit: финальная history либо точно архивная, либо пустая для
  старого архива.
- **`internal/ui/`** — отдельный `stageGraphOption` строит ECharts `graph`
  (`nodes`/`links`, направление, current highlight); существующий numeric
  `widget.ChartData` не расширяется притворным `Kind=graph`. JSON проходит тот
  же `json.Marshal`-based safe helper, что `echartsJSON`
  (`ui/widget_helpers.go:84-98`), с тестом на `</script>`/кавычки. Общий partial
  подключается и к `page-form`, и к `page-managed-form`; обе формы условно
  добавляют vendored ECharts script только для существующего доступного staged-
  объекта. Идемпотентный initializer в `ui.js` работает при полном load и после
  HTMX replacement, не создавая повторные charts/listeners. Новая JS-зависимость
  не добавляется.
- **RBAC / row access / mask** — history handler до запроса проверяет право
  чтения сущности, доступ к конкретной строке и видимость stage-поля. Нельзя
  копировать текущий прямой `recordHistory → AuditByRecord`
  (`ui/admin.go:1092-1113`): вводится общий авторизованный loader и существующий
  endpoint также переводится на него. Loader применяет текущие scalar field
  policies и к обычному audit: `hide` удаляет field-event целиком, `mask_*`
  преобразует **оба** `OldValue`/`NewValue` через `access.MaskValue`, `full`
  оставляет их, а неизвестное/удалённое поле и неизвестная стратегия закрываются
  без выдачи raw value. Решение принимается до reference lookup; для разрешённых
  ссылок label получает маски целевой сущности, затем к итоговым двум scalar
  значениям применяется политика исходного поля непосредственно перед render /
  JSON. Так enrichment или его ошибка не превращают UUID/старое значение в
  обход маски. При `mask_admin` тот же redactor применяется к record history и
  к `enrichAuditEntriesGlobal`, до передачи в админский шаблон.
  Для stage-поля любая политика, кроме `full`, подавляет history, graph и report
  целиком: частично замаскированные enum labels всё равно раскрывали бы equality,
  counts и маршрут. Агрегат получает SQL predicate через
  `rowFilterFor` (`ui/row_access.go:170-182`) **до** `GROUP BY`; скрытые строки
  не попадают даже в counts. Его params несут `RowFilterEvaluated` и повторяют
  storage fail-closed guard из `List` (`storage/crud.go:383-390`), чтобы новый
  caller не мог забыть RLS. Если stage-field masked/hidden, history, graph и
  report не выполняют запрос и не показываются. Direct URL работает fail-closed.
- **`internal/configcheck/`** — `onebase check` проверяет модель графа,
  predefined и генерируемую SQL-схему на временной БД. Записанные warn-events,
  текущие пустые/неизвестные значения, несовпадение current/latest и неизвестная
  длительность показываются только runtime stage-report, уже подключённым к data
  DB. DB-aware режим `onebase check` — отдельная задача, а не неявное изменение
  его контракта.
- **`internal/cli/describe.go`, `impact.go`, `aiguide.go`** — новый блок в
  выдаче структуры и в `AGENTS.md`, чтобы ИИ-помощник его использовал.
- **`internal/i18n`** — новые строки в 16 локалей (гейт `i18ncheck` в джобе
  `build` не пропустит коммит без них).

## Обратная совместимость

Четыре места, где план может сломать работающее:

1. **Существующие конфигурации.** Блока `stages` у них нет → поведение
   бизнес-записей неизменно: stage-wrapper, savepoint, advisory lock и CAS не
   включаются, остаются прежние SQL/error semantics. Миграция платформы может
   создать пустую внутреннюю `_stage_history`, как создаёт другие system tables.
   Прямая проверка: `examples/*` проходят `onebase check` и свои тесты без
   правок, а storage regression сравнивает вызовы/ошибки пути без stages.
2. **Данные до объявления этапов.** История у них пуста; по одному текущему
   значению нельзя доказать, какими переходами объект к нему пришёл. Runtime
   report показывает текущее пустое/неизвестное значение отдельно и длительность
   «неизвестно», но не объявляет прошлое нарушение фактом; статический
   `onebase check` этих строк не видит. Отсюда умолчание `enforce: warn`: новые
   нарушения начинают фиксироваться, затем `strict` включается осознанно.
3. **Отчёт «сколько висит» для старых объектов.** Истории нет → показывать
   «неизвестно», а не считать от нуля. Дата первой записи в `_stage_history`
   становится точкой отсчёта.
4. **Техническое создание с hook.** Provisional row не считается бизнес-
   переходом и не оставляет самостоятельной истории. Callback-scope не может
   успешно завершиться без финального write; единственное create-event описывает
   состояние, которое станет видно после commit. Ошибка hook/final strict-гейта
   откатывает provisional row и транзакционные DB-side effects hook-а.

## Риски и решения

| Риск | Решение |
|---|---|
| Переименование stage-поля отрезает прежнюю историю | `stages.field` разрешается в обязательный устойчивый `Field.ID`; `_stage_history`, latest и индексы используют `field_id`, а текущее имя — только для доступа к бизнес-колонке. Rename/round-trip тест сохраняет одну последовательность `event_no` |
| Конфигуратор или schema/lint не знает новый YAML-блок | сырой `yaml.Node` в `saveEntity`, structural key guard и round-trip; JSON Schema и nested lint обновляются в том же изменении |
| Обмен данными (план 86): пакет может не содержать промежуточные переходы источника | per-object scope и record-lock охватывают local state, conflict hook/decision и apply. Только incoming-wins вызывает узкий replication-writer и пишет synthetic event при фактической смене stage: `source=exchange`, `source_ref=plan/from_node/message_no`, actor `NULL`; `at` означает время появления на приёмнике. Hook остаётся обычным local writer без bypass. Unknown live stage отвергается; tombstone сохраняет/не создаёт stage и не пишет event, resurrection требует known stage. Повторный `message_no`/version пропускается. Точный перенос акторов/времени — отдельное расширение формата |
| `SyncPredefined` обходит обычные Upsert и preallocate-ит весь список до item-loop | это явный третий writer: один scope на весь sync, отсортированные name-locks/entity-lock до всей preallocation, затем record-lock; known-value validation, atomic direct upsert + mandatory `source=migration` history; omitted field вставляет initial и не сбрасывает существующий stage |
| Restore / DemoReset должны воспроизводить историю, но не повторно применять переходы | `_stage_history` входит в `systemTables`/manifest и атомарно публикуется вместе с объектами. DemoReset migration/predefined может временно писать events до clear, но commit содержит точно архивную history; users/roles импортируются, sessions/scheduled-runs сохраняются. Без FK на `_users`. Старый архив без таблицы очищает target-history и даёт пустую |
| Два конкурентных перехода читают один исходный этап | PG advisory lock берётся до read и действует также для отсутствующей строки; SQLite staged CAS/create-conflict работает между двумя DB handles. Один запрос видит результат другого либо получает нормализованный version/busy conflict. Тест запрещает историю `A→B`, `A→C`, если фактическая цепочка `B→C` не разрешена |
| Несколько events имеют одинаковый `at` или commit идут не в порядке старта tx | latest определяется только монотонным `event_no`; `at` остаётся wall-clock атрибутом и не участвует в причинном порядке |
| Проведение и пометка удаления (план 50) меняют объект, не трогая этап | гейт срабатывает только при **изменении** поля-этапа — эти пути его не касаются |
| Маска/RLS раскрывает stage или хотя бы число скрытых объектов | entity-read + row predicate + field mask применяются до history query/aggregate; masked stage полностью подавляет history/graph/report, direct URL fail-closed |
| Кто вправе двигать этап | в первой версии — обычные права на запись объекта. Права «на переход» (роль X может только Согласование→Утверждена) — отдельный вопрос, сознательно вне плана |
| `_stage_history` растёт | v1 не удаляет строки автоматически. Будущая retention-policy обязана сохранить latest event каждого живого `(entity, field_id, record)` либо сначала материализовать `entered_at`; слепая audit-cleanup ломает duration и запрещена |
| Конфигурация без stages получила новые lock/error semantics | stage-wrapper условен по `entity.Stages != nil`; regression фиксирует прежний обычный write-path |

## Тесты

- **metadata/static check**: валидные графы, цикл, terminal node; пустой/duplicate
  `order`, enum value вне `order`, unknown/duplicate/self edge, unreachable node,
  invalid initial/enforce/deadline, stage-поле без устойчивого `id` и unknown
  predefined отвергаются; rename с тем же `Field.ID` продолжает прежнюю
  `(entity, field_id, record)` history/event sequence;
- JSON Schema принимает полный `stages` и отвергает лишние nested keys;
  `configcheck` сообщает точный путь опечатки; configurator save round-trip
  сохраняет блок и transitions (плюс structural `save_entity_keys_test` не
  требует exemption); before/after ручного rename с тем же `Field.ID` продолжает
  одну history, а будущий UI rename обязан получить rewrite-тест;
- **матричный** `dbtest.ForEachDialect`: local gate, обязательная история,
  `event_no`, latest и защищённый агрегат одинаковы на SQLite/PostgreSQL;
- публичные `entityservice`, `Документы.X.Записать` и запись справочника меняют
  **существующий** объект, чтобы покрыть `UpsertVersioned` и lowercase-map DSL
  справочника; отдельно покрыты обычный `Upsert`, create и неизменившийся stage;
- `warn` пропускает известный недопустимый переход, пишет runtime warning и
  `violation=true`; `strict` отвергает; история пишется при выключенном audit.
  Outer rollback убирает event и не выпускает отложенный warning;
- canonical accessor одинаково читает exact/lowercase ключи и не использует
  текущий `AuditDiff` как stage-истину;
- `Upsert` без внешней транзакции атомарен; fault injection между object write и
  history не оставляет ни одного отдельно. Во внешней транзакции ошибка stage-
  операции откатывает её savepoint; caller ловит ошибку, unrelated outer work
  успешно commit. Полный outer rollback откатывает object и history;
- ошибка чтения old row не трактуется как создание; version conflict и SQLite
  busy-snapshot не пишут history;
- concurrent update открывает два соединения. Для SQLite это **два DB handles к
  одному файлу**, не два goroutine на одноконнектном handle. Допустима только
  цепочка относительно реально зафиксированного stage;
- отдельный concurrent create одного UUID: PG absent-row защищён advisory lock,
  SQLite insert/CAS; ровно один event трактуется как create. Второй вызов после
  lock перечитывает committed row и либо проходит как проверенный update, либо
  получает conflict; ложного второго create-event нет;
- две логические stage-записи в одном outer tx получают `event_no=1,2` даже при
  одинаковом `at`; PG waiter, чья tx началась раньше, не становится latest из-за
  раннего `now()`;
- provisional callback + hook даёт только финальный create-event; прямой staged
  `UpsertProvisional` отвергается, hook error не оставляет строку, смена stage в
  hook проверяет `warn`/`strict`; write/post в одной tx даёт причинные номера;
- `SyncPredefined`: omitted stage при первом insert даёт initial и одно
  migration-event, повторный sync не сбрасывает текущий stage; явный
  create либо update с фактической сменой stage пишет ровно один migration-event,
  а update с тем же значением — ни одного; actor
  `NULL`; два параллельных PG sync списка с двумя взаимными/self references
  стартуют до preallocation, сериализуются отсортированными name-locks (или
  entity-lock), сохраняют одинаковые фактические UUID и ссылки и не путают
  record ID; SQLite-вариант открывает два handles к одному файлу и проверяет ту
  же гарантию/нормализованный conflict; history schema существует до вызова из
  `DB.Migrate` и в `procrun`;
- exchange: local-wins не пишет **exchange-event**, incoming-wins при фактической
  смене stage пишет ровно один с полным provenance и actor `NULL`; unknown live
  stage отклоняется, duplicate message/version идемпотентен. Paused concurrency-
  тест ставит обычный local save между старым местом чтения и apply: PG lock /
  SQLite write-intent не позволяет принять решение по stale state. Если conflict
  hook сам делает локальную запись, она проходит обычный гейт под reentrant lock
  и имеет отдельный `source=local`; tombstone на пустом receiver создаёт deleted
  placeholder и 0 events, затем resurrection с known stage даёт 1 event, оба
  duplicate-вызова ничего не добавляют; CLI load на базе без таблицы сначала
  выполняет Ensure;
- security integration: прямой URL без entity/row access не читает history,
  скрытые RLS-строки не попадают в counts, маска stage подавляет history, graph и
  report до SQL; обычная audit history удаляет `hide` field-event и одинаково
  маскирует `OldValue`/`NewValue` для `mask_tail`/`mask_city`/`mask_all`, включая
  reference/date, неизвестное поле и включённый `mask_admin`, не оставляя raw
  fallback; guarded aggregate без `RowFilterEvaluated` падает fail-closed;
  escaping graph payload не позволяет закрыть `<script>`;
- universal portable round-trip включает `_stage_history`, manifest allowlist её
  принимает, старый archive без файла очищает target-history и при full restore,
  и при DemoReset; новый DemoReset импортирует users/roles, сохраняет sessions /
  scheduled-runs и умеет стартовать на target без заранее созданной таблицы.
  Fault/test hook после migrate и до clear может увидеть временный migration-
  event, но после успешного commit остаётся точно history архива и ни одного
  synthetic restore-event;
- render/behavior tests обеих `page-form` и `page-managed-form`: существующий
  доступный staged-object получает partial, conditional ECharts и одну
  инициализацию; new, non-staged, masked и RLS-denied object не получают ни
  graph payload, ни conditional script; HTMX replacement не дублирует instance;
- без `stages` обычные writes не получают новый savepoint/lock/CAS, а
  `examples/*` проходят `onebase check` и свои тесты без правок;
- retention-contract test (когда появится cleanup) запрещает удалить latest
  event живой записи; до тех пор автоматической чистки нет.

## Verification

1. Объявить `stages` у «Заявки» в `examples/callcenter`; попытаться из
   «Черновик» прыгнуть сразу в «Утверждена» — при `strict` отказ, при `warn`
   запись проходит с runtime warning и появляется в защищённом stage-report.
   `onebase check` независимо ловит испорченный граф/predefined без подключения
   к рабочей БД.
2. Пройти маршрут нормально → в карточке заявки видна история: кто, когда,
   из какого этапа в какой, с причинным порядком `event_no`.
3. Отчёт «где застряло»: N заявок на «НаСогласовании», из них M висят дольше
   `deadline_days`; пользователь с row filter видит только свои N/M, а при
   маске stage не видит отчёт вовсе.
4. Схема этапов на форме заявки: текущий этап подсвечен отдельным graph-adapter,
   без новой JS-зависимости.
5. То же самое из DSL-обработки; пакет обмена применяет отдельную явно
   доверенную replication-семантику и оставляет синтетическую историю с
   `plan/from_node/message_no`, а обычный storage-вызов и запись из conflict hook
   перескочить этап не могут. Изменение predefined оставляет `source=migration`.

## Эстимейт

22–29 рабочих дней:

| Этап | Дней |
|---|---|
| декларация + полная валидация + статический `check` | 2–3 |
| транзакционный гейт, canonical accessor, PG lock/SQLite CAS, deterministic sequence | 5–6 |
| `_stage_history` + migration/predefined + ранний `Ensure*` + backup/DemoReset | 4–5 |
| exchange provenance, hook isolation и узкий replication-writer | 2–3 |
| защищённый отчёт «где застряло» (RBAC/RLS/mask) | 3–4 |
| схема на ECharts | 2 |
| матричные/concurrency/security тесты, i18n×16, tooling/docs | 4–6 |

**Минимальный срез — 14–19 дней**: декларация и static validation + обе обычные
точки гейта + canonical accessor/сериализация + deterministic history +
migration/predefined + ранний Ensure и backup/restore + exchange writer вместе с
tombstone/resurrection и concurrency-контрактом. Только такой срез даёт общую
гарантию переходов и «кто когда двигал» для уже поддерживаемых writers; runtime
report, graph и DB-aware diagnostic CLI не входят и добавляются поверх без смены
формата истории.

## Отношение к плану 85

План 85 (бизнес-процессы и задачи) предлагает движок карты маршрута с
`bpm/*.yaml`, экземплярами процессов, задачами и автопродвижением. Разбор
2026-08-12 показал: спроса на движок нет (208 issue от 18 авторов — ни одного
запроса на функциональность плана 85; сценарий документооборота при этом в
аудитории живёт — на OneBase строится СЭД DocFlow, но её автор просит
общеплатформенные фиксы, а не движок БП), сценарий согласования уже собирается
прикладным слоем (`examples/callcenter/src/Согласование.module.os`, 114 строк),
а цена движка занижена — по оценке, примерно вдвое — из-за сквозных подсистем.

Этот план забирает из 85 то, что даёт ценность **само по себе и для любого
документа**, а не только для того, под который нарисована карта маршрута:
гарантию переходов, историю и ответ на вопрос «где застряло». Новый вид объекта
метаданных не заводится, откат — удаление блока `stages` из YAML.

Следующий кирпич, если понадобится (отдельным планом): **предикат
существования по регистру в строковых политиках** — «вижу строку, если в
регистре есть запись, связывающая меня с её значением». Сейчас `RowValue`
знает только `user`/`user_attr`/`literal`/`list` (`auth/row_access.go:31-35`),
а предикат через ссылку — один шаг, связь один-к-одному
(`storage/predicate.go:128`). Это механизм ролевой адресации из 1С, сделанный
как общее правило видимости; он закрывает целый класс задач вне всякого BPM
(«менеджер видит клиентов своей группы», «кладовщик — документы своих складов»)
и является предусловием для платформенной «Задачи», если та когда-нибудь
понадобится.
