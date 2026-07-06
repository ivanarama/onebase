# План 76: готовность к многопользовательской нагрузке

**Статус:** запланировано (аудит 2026-06-25).

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

- UI CRUD проверяет объектные права через `requirePerm`, но текущий REST API под
  `/catalogs/*` и `/documents/*` находится только под session middleware: сами
  REST-хендлеры не проверяют `read/write/delete/post/unpost`.
- UI-списки пагинируются, но REST list вызывает `storage.List` без лимита и
  возвращает весь набор.
- `storage.List` умеет `Limit/Offset`, но metadata не содержит декларативных
  индексов под прикладные списки, сортировки и фильтры. Автоиндексы есть только в
  отдельных местах: иерархия, predefined, scheduled/audit, бухрегистр.
- Табличные части читаются и перезаписываются по `parent_id`, но миграция не
  создаёт индекс `(parent_id, строка)`.
- Несколько UI-путей грузят reference-options целиком: формы, параметры отчётов,
  фильтры списков, журналы и деревья.
- Отчёты и экспорт вызывают `RunQuery`, который считывает весь SQL-результат в
  память; компоновщик потом режет до 50k строк, но уже после загрузки.
- `UpsertVersioned` делает `SELECT _version`, затем обычный `Upsert`. Для
  настоящей PostgreSQL-конкуренции надёжнее единый conditional update
  `UPDATE ... WHERE id=? AND _version=?`.
- `БлокировкаДанных` и realtime hub внутрипроцессные. Для нескольких экземпляров
  нужны PostgreSQL advisory locks / внешний pub-sub.
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

### Этап D — конкурентная запись и блокировки (2-4 дня)

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

### Этап E — тяжёлые операции, таймауты и backpressure (3-5 дней)

Детальный implementation plan: [`79-runtime-limits-backpressure-observability.md`](79-runtime-limits-backpressure-observability.md).

1. Ввести runtime-настройки `limits:`:
   ```yaml
   limits:
     request_timeout_sec: 60
     report_timeout_sec: 120
     report_max_rows: 50000
     export_max_rows: 100000
     processor_concurrency: 4
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

### Этап F — наблюдаемость и эксплуатация (2-3 дня)

Детальный implementation plan: [`79-runtime-limits-backpressure-observability.md`](79-runtime-limits-backpressure-observability.md).

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

1. **A:** REST RBAC + REST list caps. Это самый короткий и самый важный security
   win для многопользовательской эксплуатации.
2. **D1:** атомарная optimistic locking запись. Это маленькая по объёму, но
   принципиальная корректность.
3. **B/C:** индексы + server-side reference picker. Это даст реальный выигрыш
   на больших справочниках.
4. **F:** slog/request_id/slow operations, чтобы следующие оптимизации делались
   по данным, а не на глаз.
5. **E:** лимиты и background export для тяжёлых отчётов.
6. **G:** горизонтальное масштабирование только когда один процесс с PostgreSQL
   и настроенным пулом перестанет хватать.

## Связанные планы

- План 26 — REST API v2. Его лучше делать после этапа A, чтобы v2 сразу
  наследовал правильный RBAC, лимиты и токены.
- План 79 — детализация этапов E/F: runtime-лимиты, backpressure, slow logs и
  operation metrics.
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
