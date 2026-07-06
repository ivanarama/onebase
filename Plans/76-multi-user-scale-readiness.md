# План 76: готовность к многопользовательской нагрузке

**Статус:** в работе (аудит 2026-06-25; A/B/C/D1/E/F core реализован до 2026-07-07).

## Контекст

OneBase уже хорошо закрывает режим локальной базы, небольшой команды и
прикладной разработки: есть PostgreSQL, RBAC в UI, транзакционное сохранение
объектов через `entityservice`, audit, backup, `/metrics`, k6-стенд, CI/race,
безопасный bind по умолчанию, HTTP-сервисы с auth/rate-limit/CORS.

Следующий качественный порог — не "highload", а **100+ активных пользователей в
одной PostgreSQL-базе** и несколько реальных интеграций. В этом режиме критичны
не новые бизнес-фичи, а предсказуемость: нельзя отдавать данные мимо RBAC, нельзя
читать справочники целиком для каждого выбора, нельзя позволять одному отчёту или
экспорту съесть память/connection pool, нельзя полагаться на внутрипроцессные
блокировки, если появится второй экземпляр сервера.

## Факты аудита

- UI CRUD проверяет объектные права через `requirePerm`; REST API под
  `/catalogs/*` и `/documents/*` проверяет `read/write/delete/post` на текущих
  маршрутах. Отдельного REST `unpost` пока нет.
- UI-списки пагинируются; REST list тоже имеет default `limit=100` и максимум
  `1000`, плюс `offset` и заголовки `X-Total-Count` / `X-Limit` / `X-Offset`.
- `storage.List` умеет `Limit/Offset`; metadata содержит декларативные
  `indexes:`, миграция создаёт их для SQLite/PostgreSQL, а `onebase lint`
  предупреждает о `list_form` полях без leading index.
- Табличные части читаются и перезаписываются по `parent_id`; миграция создаёт
  индекс `(parent_id, строка)`.
- Массовая загрузка reference-options заменена на bounded initial options +
  server-side picker `/ui/_ref-options/{entity}`. Формы, табличные части,
  фильтры, отчёты, обработки, регистры и журналы не должны держать весь
  справочник в HTML; parent folder select иерархических справочников тоже
  ограничен первой страницей с добавлением текущего выбранного родителя.
- Отчёты и экспорт используют `RunQueryLimit`: SQL без верхнеуровневого `LIMIT`
  получает `LIMIT max+1`, а чтение результата имеет Go-side cap. Background
  export/polling остаётся UX-расширением поверх этих runtime limits.
- `UpsertVersioned` уже делает единый conditional update
  `UPDATE ... WHERE id=? AND _version=?` и возвращает `ErrVersionConflict` при
  `RowsAffected == 0`; без ожидаемой версии путь совместимости остаётся обычным
  `Upsert`.
- `БлокировкаДанных` для Save-хуков собирает ключи DSL-блокировок и на
  PostgreSQL берёт `pg_advisory_xact_lock` внутри транзакции записи/проведения.
  Realtime hub пока внутрипроцессный; для нескольких экземпляров нужен внешний
  pub-sub.
- Сессии хранятся в БД, но `CreateSession` удаляет старые сессии пользователя:
  фактически один активный вход на логин.
- Файлы/attachments по умолчанию живут на локальном диске процесса. Это нормально
  для одного сервера, но мешает горизонтальному масштабированию.

## Цель

Сделать onebase безопасной и предсказуемой для PostgreSQL-инсталляции с
примерно 100 активными пользователями:

- REST и UI соблюдают одинаковую модель RBAC.
- Большие списки, справочники выбора, отчёты и экспорт имеют лимиты и понятное
  поведение.
- Конкурентная запись не теряет изменения.
- Админ видит насыщение пула, медленные маршруты и тяжёлые операции.
- Есть понятный путь к двум экземплярам приложения, даже если MVP остаётся
  однопроцессным.

Не цель этого плана: строить SaaS на миллионы пользователей, полноценную
мультиарендность в одной БД или сложную row-level security модель для всех
конфигураций. Это отдельные продуктовые решения.

## Приоритетные этапы

### Этап A — REST/API guardrails (1-2 дня)

Статус на 2026-07-06: реализовано для текущего REST API; этот этап закрыт
кодом и security-тестами.

1. Вынести общую проверку прав из UI в переиспользуемый helper
   (`authz.Can(ctx, kind, name, op)` или тонкий метод рядом с api handler).
2. В текущем REST API добавить проверки:
   - `GET list/get` -> `read`;
   - `POST/PUT` -> `write`;
   - `DELETE` -> `delete`;
   - `POST /documents/{entity}/{id}/post` -> `post`;
   - будущий `unpost` -> `unpost`.
3. REST list: дефолтный `limit` 100, максимум 1000, `offset`, `sort`, `dir`,
   фильтры как сейчас. Ответ временно можно оставить массивом для совместимости,
   но добавить заголовки `X-Total-Count` / `X-Limit` / `X-Offset`.
4. Ограничить размер JSON body для REST тем же `MaxBytesReader`, что у upload/http
   services.
5. k6: сценарий с пользователями и ролями, который проверяет 403 и read-heavy
   list с лимитами.

Acceptance:
- не-админ с ролью только на один объект не может читать/писать чужой объект через REST;
- REST list без `limit` не возвращает весь справочник;
- старые тесты API проходят, новые security-тесты фиксируют 401/403/409.

### Этап B — индексы и большие списки (3-5 дней)

1. Добавить декларативные индексы в metadata:
   ```yaml
   indexes:
     - fields: [Контрагент, Дата]
     - fields: [ИНН]
       unique: true
   ```
   Минимальная модель: `fields`, `unique`, `where` не нужен в MVP.
2. Миграция создаёт индексы для PostgreSQL и SQLite с безопасными именами.
3. Автоиндексы:
   - табличные части: `(parent_id, строка)`;
   - reference-поля, участвующие в `ListForm`, фильтрах или часто используемые
     как сортировка, можно рекомендовать через `onebase lint`, но не создавать
     молча без декларации;
   - регистры накопления: базовый индекс `(period)` и, для измерений, составной
     `(dim..., period)` по декларации или conservative default для первых 2-3
     измерений.
4. `onebase lint`: предупреждения "поле используется в list/search/filter/sort,
   но индекса нет" и "таблица растёт без настройки page size".

Acceptance:
- миграция идемпотентна;
- индекс создаётся при первом запуске и не ломает существующие базы;
- тесты на PostgreSQL/SQLite проверяют имя индекса, unique и tablepart-index.

Реализация 2026-07-07:
- добавлены metadata `indexes:` с `fields`/`unique`, валидацией и YAML lint;
- миграция создаёт entity indexes и автоиндекс табличных частей
  `(parent_id, строка)`;
- для регистров создаются period/dimension indexes;
- `onebase lint` предупреждает `metadata.list-field-without-index` для
  `list_form` полей без leading index;
- CI PR #263 закрепляет нулевой lint по shipped `examples/*` и `templates/*`.

### Этап C — UI для больших справочников (2-4 дня)

1. Заменить массовую загрузку reference-options на серверный picker:
   `/ui/_ref-options/{entity}?q=&limit=&offset=`.
2. Поля ссылок в формах, табличных частях, параметрах отчётов, фильтрах и журналах
   должны искать по серверу, а не держать весь справочник в HTML/JS.
3. Tree-view и hierarchical catalogs: lazy-load детей узла; режим "всё дерево"
   оставить только до лимита или под явным admin-действием.
4. Для экспорта списков: явный лимит/подтверждение, сообщение "выгружается N из
   M", не молчаливый полный дамп.

Acceptance:
- форма с reference на справочник 100k записей открывается без загрузки всех rows;
- picker возвращает первые N совпадений и умеет открыть карточку выбранного элемента;
- tree-view не делает `ListParams{}` на весь каталог по умолчанию.

Реализация 2026-07-07:
- `/ui/_ref-options/{entity}?q=&limit=&offset=` возвращает capped JSON и total;
- initial options для обычных форм, табличных частей, фильтров, отчётов,
  обработок, регистров и журналов ограничены `refPickerDefaultLimit` и
  добавляют выбранные значения точечным `GetByID`;
- tree-view и `_tree-children` грузят детей узла лениво с лимитом;
- parent folder select в иерархических справочниках больше не делает полный
  `store.List`: грузятся только папки первой страницы, а текущий `parent_id`
  добавляется отдельно, если он вне первой страницы;
- отдельным UX-расширением остаётся background export/polling для очень больших
  Excel/PDF выгрузок поверх уже существующих runtime limits.

### Этап D — конкурентная запись и блокировки (2-4 дня)

Статус D1 на 2026-07-06: атомарный `UpsertVersioned` реализован и покрыт
последовательным и конкурентным regression-тестом. Статус D2 на 2026-07-07:
`БлокировкаДанных` в Save-хуках дополнительно берёт PostgreSQL
`pg_advisory_xact_lock` внутри транзакции записи/проведения; SQLite и
single-process остаются на текущем `LockManager`.

1. `UpsertVersioned` для PostgreSQL перевести на атомарный conditional update:
   `UPDATE ... SET ..., _version=_version+1 WHERE id=$id AND _version=$expected`.
   Если `RowsAffected == 0` -> `ErrVersionConflict`.
2. Для новых записей оставить insert/upsert-путь, но для редактирования UI/REST
   не делать split `SELECT` -> `Upsert`.
3. `БлокировкаДанных`:
   - SQLite и single-process режим оставляют текущий `LockManager`;
   - PostgreSQL получает `pg_advisory_xact_lock(hash(key))` внутри транзакции
     проведения/записи, чтобы несколько процессов сериализовались одинаково.
4. Проверить проведение одного документа конкурентно: один поток выигрывает,
   второй получает 409/конфликт или корректно ждёт lock.

Acceptance:
- race/integration-тест на два параллельных PUT одного объекта;
- integration-тест на PostgreSQL advisory lock с двумя соединениями;
- UI-конфликт остаётся понятным пользователю.

Реализация 2026-07-07:
- `LockCollector` собирает ключи, запрошенные DSL `БлокировкаДанных`, и
  освобождает забытые process-local locks в конце `entityservice.Save`;
- storage helper `AdvisoryXactLock` берёт PostgreSQL transaction-scoped advisory
  locks по стабильному FNV64 namespace hash; SQLite path — no-op;
- `entityservice.Save` вызывает advisory locks первым шагом внутри `WithTx`,
  до upsert, табличных частей, движений и `posted=true`;
- добавлен integration-тест PostgreSQL с двумя соединениями под тегом
  `integration`.

### Этап E — тяжёлые операции, таймауты и backpressure (3-5 дней)

1. Ввести runtime-настройки `limits:`:
   ```yaml
   limits:
     request_timeout_sec: 60
     report_timeout_sec: 120
     report_max_rows: 50000
     report_concurrency: 4
     export_timeout_sec: 180
     export_max_rows: 100000
     export_concurrency: 2
     processor_timeout_sec: 120
     processor_concurrency: 4
     http_service_timeout_sec: 30
     http_service_concurrency: 16
     slow_operation_ms: 2000
   ```
2. Отчёты:
   - SQL-обёртка с `LIMIT max+1`, если запрос сам не содержит явного лимита;
   - понятное предупреждение о truncation;
   - долгий Excel/PDF export перевести в background job с polling/SSE прогрессом.
3. Глобальные семафоры для тяжёлых контуров: отчёты, экспорт, processor run,
   HTTP-сервис с DSL.
4. Контекстные таймауты для отчётов/процессоров/HTTP-сервисов; scheduler timeout
   уже есть, его использовать как эталон.

Acceptance:
- тяжёлый отчёт отменяется по deadline и освобождает DB connection;
- при превышении concurrency пользователь получает 429/503 с понятным retry;
- экспорт 200k строк не держит HTTP handler до бесконечности.

Реализация 2026-07-06:
- добавлены runtime timeout/concurrency/max rows guardrails для отчётов,
  экспорта, обработок и HTTP-сервисов;
- `RunQueryLimit` добавляет SQL `LIMIT max+1`, если верхнеуровневого `LIMIT` нет,
  и оставляет Go-side read cap вторым рубежом;
- экран отчёта показывает truncation warning, экспорт сверх лимита возвращает
  413; отдельный background export с polling/SSE остаётся UX-расширением поверх
  уже ограниченного выполнения.

### Этап F — наблюдаемость и эксплуатация (2-3 дня)

1. Дополнить `/metrics`:
   - активные сессии;
   - активные SSE subscribers;
   - активные scheduled/processor/report jobs;
   - slow operations counter;
   - webhook queue/retry metrics.
2. `slog`-контур из плана 56/43.3: JSON-логи, request_id, user/login, route,
   duration, redaction секретов.
3. Slow query / slow report log: SQL hash, duration, rows, route/user.
4. Обновить `docs/users-load-limits.md`: SLO, команда запуска k6, как читать pool
   saturation и какие параметры подкрутить.

Acceptance:
- k6-прогон даёт p95/p99 + DB pool metrics;
- админ может отличить CPU/DSL bottleneck от ожидания connection pool.

Реализация 2026-07-06:
- `/metrics` дополнен активными сессиями, SSE subscribers, активными
  scheduler runs, operation counters/gauges/histograms, slow/limited operation
  counters и webhook inflight/retry/failure counters;
- request log пишет route/request_id/duration/user с существующей redaction и
  JSON-форматом через `ONEBASE_LOG_FORMAT=json`;
- slow operation log для отчётов пишет `sql_hash`, duration, rows, route/user;
- обновлён `docs/users-load-limits.md`.

### Этап G — путь к горизонтальному масштабированию (future)

Это не нужно для MVP 100 активных пользователей, но важно для архитектурного
выбора:

1. Shared file storage:
   - вариант A: `file_storage=db` для простоты;
   - вариант B: S3/minio backend для blobs/attachments.
2. Realtime hub через Redis/NATS pub-sub вместо внутрипроцессного hub.
3. Scheduler leader election: один активный scheduler на базу через advisory lock.
4. Sticky sessions не обязательны, потому что session lookup в БД, но нужно
   разрешить несколько активных сессий на логин или явно оставить текущую
   single-session политику.
5. Config reload/version broadcast между процессами.

## Отдельный продуктовый вопрос: row-level security

Сейчас RBAC объектный: право на справочник/документ целиком. Если сценарий
требует "менеджер видит только свои сделки" или "исполнитель видит только свои
задачи", нужен отдельный план:

- декларативные row filters в ролях или формах;
- серверное применение фильтра в UI/REST/AI tools/reports;
- тесты на невозможность обойти фильтр через API или report query;
- UX для администратора, чтобы он понимал, почему запись не видна.

Не стоит делать это скрыто через PostgreSQL RLS без платформенной модели: OneBase
должна одинаково работать на SQLite/dev и PostgreSQL/prod.

## Рекомендуемый порядок после текущего `onebase lint`

1. **E UX:** background export/polling/SSE для долгих Excel/PDF выгрузок.
   Runtime limits и truncation уже есть; нужен удобный пользовательский контур.
2. **Load validation:** k6 PostgreSQL прогон на демо/реальной конфигурации с
   проверкой p95/p99, pool saturation и runtime operation metrics.
3. **G future:** shared file storage, external pub-sub и scheduler leader
   election только когда один процесс с PostgreSQL и настроенным пулом перестанет
   хватать.

## Связанные планы

- План 26 — REST API v2. Его лучше делать после этапа A, чтобы v2 сразу
  наследовал правильный RBAC, лимиты и токены.
- План 56 — `onebase lint`, CI и наблюдаемость. Этапы B/F используют lint и slog.
- План 43.3 — точечный `slog`-контур.
- План 74 realtime — уже реализован как однопроцессный hub; этап G описывает
  внешний broker.
- `docs/users-load-limits.md` — текущая эксплуатационная справка и k6-стенд.

## Verification

Минимальная проверка плана после реализации этапов A-D:

```bash
go test ./internal/api ./internal/auth ./internal/storage ./internal/ui
go test -tags=integration ./internal/storage
docker compose -f loadtest/docker-compose.yml up -d --build
docker compose -f loadtest/docker-compose.yml run --rm k6 run /scripts/scenarios/list_query.js
docker compose -f loadtest/docker-compose.yml run --rm k6 run /scripts/scenarios/post_document.js
```

Целевой ориентир для демо-конфигурации на PostgreSQL: ошибки <1%, p95 списков
<500 мс, p95 записи/проведения <800 мс на согласованном профиле нагрузки. Для
реальной конфигурации SLA фиксируется отдельно.
