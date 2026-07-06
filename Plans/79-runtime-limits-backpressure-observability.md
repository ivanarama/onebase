# План 79: runtime-лимиты, backpressure и slow operations для плана 76 E/F

**Статус:** запланировано, детализация этапов E/F плана 76.

## Контекст

Первый срез плана 76 уже закрыл основные guardrails для многопользовательской
работы: REST RBAC, caps для REST list, атомарный optimistic lock,
декларативные `indexes:`, индексы табличных частей и server-side reference
picker.

Остаток E/F - это не новая бизнес-фича, а эксплуатационный слой вокруг тяжёлых
операций. Сейчас отчёт, Excel/PDF export, обработка или HTTP-сервис на DSL могут
долго занимать goroutine, CPU и connection из пула. `/metrics` уже есть, но
админу всё ещё не хватает видимости по активным тяжёлым операциям и slow logs.

## Цель

Сделать тяжёлые контуры предсказуемыми под нагрузкой, не ломая уже работающие
функции:

- отчёты и экспорт получают настраиваемые timeout/max rows;
- обработки и HTTP-сервисы получают настраиваемые timeout/concurrency;
- при перегрузке пользователь получает понятный 429/503, а не подвисание;
- slow operations видны в логах и `/metrics`;
- существующие конфигурации по умолчанию не получают жёстких новых ограничений.

## Не цели

- Не менять семантику запросов отчётов без явной настройки лимитов.
- Не применять общий timeout к restore, backup download, SSE, file download и
  другим потоковым/долгим маршрутам, где длинный HTTP-запрос является нормой.
- Не заменять существующие HTTP-сервисы и не менять их DSL API.
- Не строить полноценную очередь фоновых задач для всего runtime в первом PR.
- Не решать горизонтальное масштабирование из этапа G.

## Совместимость

Все новые ограничения должны быть additive:

- `limits:` отсутствует в `app.yaml` - поведение максимально близко к текущему.
- `0` в числовом лимите означает "выключено".
- Семафоры включаются только при `*_concurrency > 0`.
- Row caps для отчётов и export включаются только при `report_max_rows` /
  `export_max_rows > 0`.
- Для включённых лимитов ошибка должна быть явной: 429/503/504 или сообщение
  "результат усечён", но не молчаливое усечение.

## Конфиг

Добавить в `config/app.yaml`:

```yaml
limits:
  request_timeout_sec: 0       # общий дефолт для DSL-контуров, 0 = выключено
  report_timeout_sec: 0
  report_max_rows: 0
  export_timeout_sec: 0
  export_max_rows: 0
  processor_timeout_sec: 0
  processor_concurrency: 0
  http_service_timeout_sec: 0
  http_service_concurrency: 0
  slow_operation_ms: 1000
```

Изменения в коде:

- `internal/project/loader.go` - `LimitsConfig` внутри `AppConfig`.
- `internal/ui/server.go` - прокинуть limits в `ui.Config`/`Server`.
- `internal/cli/run.go`, `internal/cli/dev.go`, launcher runtime - передавать
  limits из загруженной конфигурации.
- `docs/users-load-limits.md` - описать параметры и пример для 100 активных
  пользователей.

## Runtime helper

Добавить небольшой внутренний helper, например `internal/runtime/ops`:

- `Limiter` - named semaphore по типам операций;
- `WithTimeout(ctx, kind, name, timeout)` - создаёт context с deadline только
  когда timeout > 0;
- `Observe(kind, name, started, status, rows)` - пишет metrics и slow log;
- `TryAcquire(kind)` - возвращает release function или ошибку перегрузки.

Типы операций:

- `report.run`;
- `report.export`;
- `processor.run`;
- `http_service.run`;
- позже: `ai.tool`, `backup.restore`, если потребуется.

## Этап E1 - отчёты и export без смены поведения

1. Обернуть `runReport` в `report_timeout_sec`, если он задан.
2. Добавить `storage.RunQueryLimit(ctx, sql, args, maxRows)`:
   - если `maxRows <= 0`, работает как `RunQuery`;
   - если `maxRows > 0`, читает максимум `maxRows + 1` строк;
   - возвращает флаг `truncated`, не меняя SQL текст.
3. В UI отчёта при `truncated=true` показывать предупреждение.
4. В Excel/PDF export использовать `export_timeout_sec` и `export_max_rows`.
5. Не переводить все export в background job сразу. Первый безопасный шаг:
   - если лимит выключен, старое поведение;
   - если лимит включён и превышен, понятная ошибка/предупреждение;
   - background export оставить этапом E3.

Acceptance:

- отчёт без `limits:` работает как раньше;
- отчёт с `report_max_rows: 1000` читает максимум 1001 строку и показывает
  предупреждение;
- timeout отменяет query через context и освобождает DB connection;
- Excel/PDF export не молча режет результат.

## Этап E2 - обработки и HTTP-сервисы

1. Обернуть запуск обработок:
   - `internal/ui/handlers_processors.go`;
   - `internal/ui/offline.go`, если CLI/offline режим должен соблюдать limits.
2. Обернуть `serviceDispatch` вокруг `s.interp.Call(...)`:
   - `http_service_timeout_sec`;
   - `http_service_concurrency`;
   - 429 при занятых слотах, 504 при timeout.
3. Сохранить существующие per-service `rate_limit`, auth, CORS и `net.enabled`.
   Новый concurrency limiter работает дополнительно к ним.
4. Для HTTP-сервисов не применять лимит к `/hs`, `/hs/openapi.json`, `/hs/docs`,
   только к вызовам DSL-обработчиков.

Acceptance:

- существующий HTTP-сервис без limits отвечает как раньше;
- при `http_service_concurrency: 1` второй параллельный вызов получает 429/503;
- при timeout обработчик завершается с понятной ошибкой;
- per-service `rate_limit` продолжает работать.

## Этап E3 - долгий export как job

Делать после E1/E2, если синхронный export остаётся проблемой.

Минимальный вариант:

- sync export остаётся для результатов до `export_inline_max_rows`;
- большой export создаёт job и возвращает страницу с progress/polling;
- результат хранится временно и скачивается отдельным URL;
- cleanup старых файлов по TTL.

Не включать в первый PR, если E1/E2 уже дают достаточный guardrail.

## Этап F1 - metrics

Расширить `internal/metrics` без тяжёлой зависимости на Prometheus client:

- counters:
  - `onebase_operation_total{kind,status}`;
  - `onebase_slow_operation_total{kind}`;
  - `onebase_limited_operation_total{kind,reason}`;
- histograms:
  - `onebase_operation_duration_seconds{kind}`;
- gauges:
  - `onebase_active_operations{kind}`;
  - `onebase_active_sessions`;
  - `onebase_sse_subscribers`, если realtime hub уже может отдать count;
  - `onebase_scheduler_running_jobs`, если доступно из scheduler.

DB pool metrics уже есть в `/metrics`, не дублировать.

Acceptance:

- `/metrics` показывает активные report/export/processor/http_service операции;
- slow counter растёт при операции дольше `slow_operation_ms`;
- labels имеют низкую кардинальность: `kind`, `status`, без id документа или
  произвольного URL.

## Этап F2 - slow logs

Писать `slog` warning для операций дольше `slow_operation_ms`:

Поля:

- `component=runtime_ops`;
- `kind`;
- `name`;
- `duration_ms`;
- `status`;
- `rows`;
- `truncated`;
- `user_login`, если есть;
- `route` и `request_id`, если операция пришла из HTTP;
- `sql_hash`, но не полный SQL с параметрами.

Секреты и DSN должны проходить через существующий redaction.

Acceptance:

- slow report виден в JSON/text логах;
- SQL не попадает в лог полностью;
- request_id позволяет связать HTTP-запрос и slow operation.

## Тесты

- `internal/project`: парсинг `limits:` из app.yaml.
- `internal/storage`: `RunQueryLimit` возвращает `truncated` и не читает весь
  результат.
- `internal/ui`: report max rows, report timeout, export max rows.
- `internal/ui`: HTTP-сервис с concurrency 1 и timeout.
- `internal/metrics`: counters/gauges/histograms для operations.
- Интеграционный тест на PostgreSQL: timeout отменяет долгий query и не держит
  connection.

## Verification

```bash
go test ./internal/project ./internal/storage ./internal/metrics ./internal/ui
go test -tags=integration ./internal/storage ./internal/ui
docker compose -f loadtest/docker-compose.yml up -d --build
docker compose -f loadtest/docker-compose.yml run --rm k6 run /scripts/scenarios/list_query.js
```

Ручная проверка:

1. Запустить отчёт без `limits:` - поведение как раньше.
2. Включить `report_max_rows: 10` - увидеть предупреждение об усечении.
3. Включить `http_service_concurrency: 1` и вызвать HTTP-сервис двумя curl
   параллельно - второй получает 429/503.
4. Открыть `/metrics` и увидеть active/slow operation metrics.
5. Посмотреть лог slow report с `request_id`.

## Эстимейт

- E1: отчёты/export limits и timeout - 1.5-2 дня.
- E2: обработки и HTTP-сервисы, semaphores - 1-1.5 дня.
- F1/F2: metrics и slow logs - 1.5-2 дня.
- Документация и k6 notes - 0.5 дня.

Итого: 4.5-6 дней без background export. С background export - плюс 3-5 дней.
