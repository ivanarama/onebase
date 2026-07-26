# Пользователи, ограничения и нагрузочное тестирование

Дата анализа: 2026-06-23.
Обновлено по runtime-лимитам, метрикам, bounded reference/folder options и
background export UX: 2026-07-07.

Документ фиксирует текущие практические ограничения onebase для базы с несколькими
пользователями и способ проверить это нагрузочным тестом.

## Короткий вывод

Жесткого лимита на количество пользователей в коде нет. В базе может быть 10,
100 и больше учетных записей.

Практический лимит задается не числом записей в `_users`, а одновременной
активностью: тип базы, пул соединений PostgreSQL, тяжелые отчеты/проведения,
размер справочников, индексы, роли и пользовательские DSL-обработчики.

Для 10 пользователей система выглядит нормальной при PostgreSQL и умеренной
нагрузке. SQLite лучше оставлять для локального режима, демо и разработки:
в коде для SQLite намеренно стоит один открытый connection.

Для 100 зарегистрированных пользователей проблем само по себе не видно. Для
100 одновременно активных пользователей нужен PostgreSQL, настройка пула
соединений, индексы под реальные списки/фильтры и нагрузочный прогон на
проектной конфигурации.

## Что происходит при 10 пользователях

Ожидаемый режим:

- PostgreSQL: рабочий сценарий для небольшой команды.
- SQLite: возможны очереди на запись и заметные задержки под параллельной UI/API
  активностью.
- Каждая защищенная HTTP-операция проверяет наличие пользователей, сессию и роли.
  На 10 пользователях это не должно быть главным узким местом.
- Главные риски: тяжелые формы, отчеты, проведения документов, большие списки без
  индексов, неверные размеры пулов и пользовательские DSL-обработчики.

## Что происходит при 100 пользователях

100 пользователей как записи в базе не являются проблемой. Важный вопрос:
сколько из них одновременно нажимают списки, проводят документы, строят отчеты
или вызывают HTTP-сервисы.

Для 100 активных пользователей надо считать систему уже настоящей
многопользовательской инсталляцией:

- использовать PostgreSQL, не SQLite;
- явно подобрать `pool_max_conns` в DSN pgx, потому что приложение не задает
  размер пула само;
- смотреть насыщение БД и пула соединений;
- индексировать поля, по которым реально фильтруют, ищут и сортируют;
- прогонять k6-сценарии на копии настоящей конфигурации и данных.

## Текущие ограничения

### Аутентификация и сессии

- Жесткого лимита пользователей нет.
- `login` уникален.
- Один пользователь может иметь несколько активных сессий. В админке активные
  сессии показываются отдельными строками; можно завершить конкретную сессию
  или все сессии пользователя.
- `auth.max_sessions_per_user` задаёт лимит enterprise-сессий на пользователя.
  При достижении лимита вытесняется старейшая enterprise-сессия того же
  пользователя; сессии конфигуратора лимит не трогает.
- Сессия живет 24 часа на backend.
- Если в базе нет пользователей, защищенные маршруты считаются открытыми.
- Если сервер поднят наружу без пользователей, CLI только предупреждает, но не
  запрещает запуск.
- Сессионный токен принимается только из cookie `onebase_session`. Старый способ
  через query `?_tk=...` больше не работает.

### Роли и доступ

- Администратор имеет полный доступ.
- Обычный пользователь без ролей фактически не имеет прав на объекты/отчеты/
  обработки.
- UI в основном проверяет RBAC на сервере.
- REST API проверяет роли на операции list/get/create/update/delete/post:
  `read`, `write`, `delete`, `post`. Если пользователей в базе нет, маршруты
  остаются открытыми так же, как UI.
- Общего row-level security по данным пока нет. Проект платформенной модели
  зафиксирован в `Plans/79-row-level-access.md`; серверные события форм остаются
  дополнительным hook, но не заменяют общий контроль списков, REST, picker,
  отчетов и AI-запросов.

### База данных и конкуренция

- SQLite работает через один open connection. Это нормально для локального
  single-user/small-team режима, но плохо для 10-100 активных пользователей.
- PostgreSQL подключается через pgxpool без явной настройки пула в коде. Размер
  пула надо задавать DSN-параметрами, например `pool_max_conns=20`.
- Автонумерация сделана атомарно через `INSERT ... ON CONFLICT DO UPDATE ...
  RETURNING`.
- Оптимистическая блокировка для UI/REST редактирования использует
  `UpsertVersioned`: один `UPDATE ... WHERE id=? AND _version=?` с инкрементом
  `_version`. Если клиент не передал ожидаемую версию, сохраняется старый путь
  совместимости без проверки.
- DSL `LockManager` остаётся process-local для SQLite/single-process режима.
  В Save-хуках `БлокировкаДанных` на PostgreSQL дополнительно берёт
  transaction-scoped advisory locks внутри транзакции записи/проведения.

### Производительность UI и данных

- UI-списки пагинируются: обычный размер страницы 100, максимум 1000.
- REST list пагинируется: default `limit=100`, максимум `1000`, есть `offset`,
  `sort`, `dir` и заголовки `X-Total-Count` / `X-Limit` / `X-Offset`.
- Поиск через `LIKE "%..."` и сортировки по произвольным полям требуют индексов
  и аккуратного дизайна списков.
- Reference-options в UI грузятся через bounded initial options и server-side
  picker `/ui/_ref-options/{entity}`. Выбранные значения вне первой страницы
  добавляются точечно.
- Деревья иерархических справочников грузят детей узла лениво. Select
  родительской группы тоже ограничен первой страницей папок и добавляет текущего
  родителя отдельно.
- Аудит индексирован по record/user/time, но журнал будет расти, если включены
  create/update/delete/post события.

### Тяжелые операции

- У интерпретатора есть лимит циклов и глубины рекурсии.
- Для тяжелых runtime-контуров можно задать `limits:` в `config/app.yaml`:
  `request_timeout_sec`, `report_timeout_sec`, `report_max_rows`,
  `report_concurrency`, `export_timeout_sec`, `export_max_rows`,
  `export_concurrency`, `processor_timeout_sec`, `processor_concurrency`,
  `http_service_timeout_sec`, `http_service_concurrency`,
  `slow_operation_ms`.
- Отчеты и экспорт выполняются с контекстными timeout. Если задан
  `report_max_rows`/`export_max_rows`, SQL без явного верхнеуровневого `LIMIT`
  получает серверный `LIMIT max+1`; дополнительно чтение результата обрезается в
  Go. На экране отчета показывается предупреждение об усечении, экспорт при
  превышении лимита возвращает понятную ошибку.
- `report_concurrency`, `export_concurrency`, `processor_concurrency` и
  `http_service_concurrency` включают backpressure. Обычные тяжелые операции
  сверх лимита получают 429; фоновые Excel/PDF выгрузки отчётов ждут свободный
  `export_concurrency`-слот в статусе `queued`.
- UI-кнопки Excel/PDF отчётов запускают in-process background job и ведут на
  страницу статуса `/ui/export-jobs/{id}`. Готовый файл доступен по download URL
  30 минут; прямые `/ui/report/{name}/excel` и `/pdf` сохранены для совместимости.
- HTTP server имеет `ReadHeaderTimeout` и `IdleTimeout`, но без общего
  `ReadTimeout/WriteTimeout`, что сделано ради длинных операций вроде restore,
  SSE и download.

### Файлы, AI и прочее

- UI upload/body attachments ограничены примерно 50 MB.
- AI chat имеет limiter 10 сообщений в минуту на пользователя и optional дневной
  token cap.
- AI tools возвращают максимум 100 строк.
- Горизонтальное масштабирование требует отдельной работы: часть состояния
  процесс-локальная, файлы по умолчанию локальные, background export jobs
  in-process, realtime hub внутрипроцессный.

## Как запустить нагрузочное тестирование

Нагрузочный стенд лежит в `loadtest/`:

- `loadtest/docker-compose.yml` поднимает PostgreSQL, onebase, Prometheus,
  Grafana и k6 runner;
- `loadtest/run-postgres-validation.sh` запускает smoke/validation профиль
  поверх docker-compose;
- `loadtest/seed/main.go` наполняет базу через REST;
- `loadtest/k6/scenarios/post_document.js` создает и проводит документы;
- `loadtest/k6/scenarios/catalog_crud.js` проверяет справочник;
- `loadtest/k6/scenarios/list_query.js` проверяет чтение списков.

Короткая PostgreSQL-проверка:

```bash
./loadtest/run-postgres-validation.sh smoke
```

Более длинный профиль с дефолтными целями сценариев:

```bash
./loadtest/run-postgres-validation.sh validation
```

Runner оставляет стенд поднятым, чтобы открыть Prometheus/Grafana и HTML-отчёты.
Для автоматической очистки:

```bash
CLEANUP=1 ./loadtest/run-postgres-validation.sh smoke
```

Ручной запуск через Docker:

```bash
docker compose -f loadtest/docker-compose.yml up -d --build

go run ./loadtest/seed \
  -url http://localhost:8080 \
  -counterparties 200 \
  -documents 500 \
  -out loadtest/seed/counterparties.json
```

Главный сценарий с красивым web dashboard и HTML-отчетом:

```bash
mkdir -p loadtest/reports

docker compose -f loadtest/docker-compose.yml run --rm --service-ports \
  -e K6_WEB_DASHBOARD=true \
  -e K6_WEB_DASHBOARD_HOST=0.0.0.0 \
  -e K6_WEB_DASHBOARD_EXPORT=/reports/post_document.html \
  k6 run /scripts/scenarios/post_document.js
```

Во время прогона открыть:

- k6 dashboard: http://localhost:5665
- Prometheus с метриками onebase: http://localhost:9090
- Grafana: http://localhost:3000, логин `admin`, пароль `admin`

После завершения прогона HTML-отчет будет в:

```text
loadtest/reports/post_document.html
```

Остановить стенд:

```bash
docker compose -f loadtest/docker-compose.yml down -v
```

## Другие сценарии

CRUD справочника:

```bash
docker compose -f loadtest/docker-compose.yml run --rm --service-ports \
  -e K6_WEB_DASHBOARD=true \
  -e K6_WEB_DASHBOARD_HOST=0.0.0.0 \
  -e K6_WEB_DASHBOARD_EXPORT=/reports/catalog_crud.html \
  k6 run /scripts/scenarios/catalog_crud.js
```

Read-heavy списки:

```bash
docker compose -f loadtest/docker-compose.yml run --rm --service-ports \
  -e K6_WEB_DASHBOARD=true \
  -e K6_WEB_DASHBOARD_HOST=0.0.0.0 \
  -e K6_WEB_DASHBOARD_EXPORT=/reports/list_query.html \
  k6 run /scripts/scenarios/list_query.js
```

## Как читать результат

Сначала смотреть:

- `http_req_failed`: должен быть ниже порога сценария, обычно меньше 1%;
- `http_req_duration p(95)`: основная пользовательская задержка;
- `http_req_duration p(99)`: хвосты, которые пользователи будут замечать как
  случайные подвисания;
- количество dropped/failed iterations, если сценарий arrival-rate;
- в Prometheus: длительность HTTP-запросов onebase и насыщение пула БД;
- runtime-метрики: `onebase_active_sessions`, `onebase_sse_subscribers`,
  `onebase_active_scheduled_jobs`, `onebase_active_operations`,
  `onebase_operation_duration_seconds`, `onebase_slow_operation_total`,
  `onebase_limited_operation_total`, `onebase_webhook_inflight`,
  `onebase_webhook_retry_total`, `onebase_webhook_dropped_total`.
- PostgreSQL pool: `onebase_db_pool_acquired_conns`,
  `onebase_db_pool_max_conns`, `onebase_db_pool_empty_acquire_total`,
  `onebase_db_pool_canceled_acquire_total`.

Ориентир:

- если p95 растет, а CPU приложения высокое, смотреть DSL/отчеты/сериализацию;
- если p95 растет вместе с ожиданием connections, увеличивать и настраивать
  PostgreSQL pool/БД;
- если растет `onebase_limited_operation_total`, расширять соответствующий
  concurrency-лимит только после проверки БД/CPU или менять пользовательский
  сценарий;
- если растет `onebase_slow_operation_total`, смотреть структурные логи
  компонента `runtime_ops`: там есть kind/name/status/duration/rows/route/user и
  `sql_hash` для медленных отчетов;
- если только list-сценарии плохие, смотреть индексы, пагинацию и выбранные
  лимиты picker/tree/export.

## Если в базе есть пользователи

Самый простой нагрузочный режим сейчас: запускать стенд без пользователей, тогда
onebase открывает маршруты анонимно.

Если тестируется база с пользователями, нужен cookie `onebase_session`. Получить
его можно так:

```bash
curl -sS -c /tmp/onebase.cookies \
  -H 'Content-Type: application/json' \
  -d '{"login":"admin","password":"secret"}' \
  http://localhost:8080/auth/login

export OB_SESSION_COOKIE="$(awk '$6=="onebase_session"{print $7}' /tmp/onebase.cookies)"
```

Потом передать cookie в k6:

```bash
docker compose -f loadtest/docker-compose.yml run --rm --service-ports \
  -e OB_SESSION_COOKIE="$OB_SESSION_COOKIE" \
  -e K6_WEB_DASHBOARD=true \
  -e K6_WEB_DASHBOARD_HOST=0.0.0.0 \
  -e K6_WEB_DASHBOARD_EXPORT=/reports/post_document-auth.html \
  k6 run /scripts/scenarios/post_document.js
```

Важно: повторный login тем же пользователем больше не инвалидирует предыдущую
сессию автоматически. Для k6-прогонов всё равно лучше использовать отдельного
нагрузочного пользователя, чтобы не смешивать ручные действия и нагрузочные
метрики.

## Что исправить перед серьезной многопользовательской эксплуатацией

Минимальный список:

1. Реализовать `Plans/79-row-level-access.md`, если пользователи не должны
   видеть чужие строки.
2. Настроить PostgreSQL pool и индексы под реальные списки.
3. Обновить k6-профили под реальные пользовательские сценарии проекта.
4. Для горизонтального режима вынести background export jobs/realtime/files во
   внешние хранилища или явно остаться на single-process.
5. За reverse proxy/HTTPS выставлять cookie только по защищенному каналу.

Развёрнутые планы работ зафиксированы в
`Plans/76-multi-user-scale-readiness.md` и `Plans/79-row-level-access.md`.

## Источники в коде

- `internal/auth/users.go`
- `internal/auth/middleware.go`
- `internal/auth/handlers.go`
- `internal/auth/roles.go`
- `internal/storage/sqlite.go`
- `internal/storage/pg.go`
- `internal/storage/crud.go`
- `internal/storage/optimistic_lock.go`
- `internal/storage/query.go`
- `internal/runtime/locks.go`
- `internal/ui/handlers.go`
- `internal/ui/ops.go`
- `internal/metrics/metrics.go`
- `internal/webhook/webhook.go`
- `internal/api/handlers.go`
- `loadtest/README.md`
- `loadtest/docker-compose.yml`
- `loadtest/k6/lib/common.js`
