# План 128 — Сжатие ответов и заголовки безопасности для HTTP-сервисов

Дата проектирования: 2026-08-17.
Статус: ✅ **Реализовано** 2026-08-17 (PR #972).
Ветка: `feature/128-service-compress-headers`.
Часть группы «веб-примитивы» (124–128). Самостоятельная ценность вне CMS:
выгрузка обмена на 10 МБ JSON едет в 3–4 раза быстрее; публичный API-эндпоинт
получает управляемую политику заголовков вместо общей для админки.

## Контекст

**Сжатие.** Ответы `/hs/*` уходят без компрессии — `gzip` в кодовой базе
встречается только в бэкапе (`internal/backup/backup.go`), конфигурации в БД
(`internal/configdb/versions.go`) и автообновлении (`internal/selfupdate/stage.go`);
HTTP-слой не сжимает ничего (проверено грепом по `internal/` на `3350679f`).
Для JSON-выгрузок и HTML-страниц это троекратный проигрыш по трафику.

**Заголовки.** `websec.SecurityHeaders` (`internal/api/server.go:86`) ставит
единый набор на весь роутер: `nosniff`, `Referrer-Policy: same-origin`, CSP
только с директивой `frame-ancestors`. Набор подобран под админку: CSP
намеренно не ограничивает источники скриптов, потому что UI грузит свои
скрипты и инлайн-обработчики. Для **публичного** сервиса, который отдаёт HTML
постороннему посетителю, этого мало: нужен настоящий `default-src`, нужен
`X-Frame-Options`/`frame-ancestors 'none'` для страниц с формами, нужен HSTS
за TLS-терминатором. Задать их сегодня нельзя — в `httpservice.Service`
(`internal/httpservice/service.go:35`) полей нет.

## Что мешает в текущем коде (проверено по `3350679f`)

1. **Нет middleware сжатия.** Ответ пишется прямо в `http.ResponseWriter`
   (`writeServiceResult`, `internal/ui/services.go:418`).
2. **Заголовки уровня сервиса негде объявить** — структура `Service` знает
   только auth/secret/rate_limit/roles/cors/templates.
3. **Общий CSP уже стоит** на всех ответах роутера API (`api/server.go:86`).
   Значит политика уровня сервиса должна **перекрывать** значение, а не
   добавляться вторым заголовком (два `Content-Security-Policy` браузер
   применяет как пересечение — получится строже, чем задумано, и отладка этого
   занимает часы).
4. **CORS уже реализован** уровнем сервиса (`setCORSHeaders`,
   `services.go:167`) — новый блок заголовков обязан с ним уживаться, а не
   переписывать `Access-Control-*`.

## Синтаксис (YAML)

```yaml
name: Nortena
root_url: nortena
auth: none

compress: true              # gzip; по умолчанию true для auth: none, false для остальных

security_headers:
  csp: "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'"
  frame_options: DENY       # DENY | SAMEORIGIN | «» (не ставить)
  hsts: 15552000            # секунды; 0 = не ставить. Только для https-запросов
  referrer_policy: no-referrer
  extra:                    # произвольные дополнительные заголовки
    Permissions-Policy: "geolocation=(), camera=()"
```

Все ключи необязательны. `X-Content-Type-Options: nosniff` ставится всегда и не
настраивается — отключаемый nosniff нужен только для того, чтобы выстрелить
себе в ногу.

## Принятые решения

1. **Сжатие включено по умолчанию только для `auth: none`.** Причина —
   BREACH: сжатие ответа, содержащего секрет (CSRF-токен, персональные данные)
   вместе с отражённым вводом атакующего, позволяет угадывать секрет по длине
   ответа. Публичный анонимный сервис секретов не содержит по определению;
   авторизованный — содержит, и включать ему сжатие владелец должен осознанно
   (`compress: true`). Обратное умолчание (сжимать всё) — тихое ослабление
   защиты у всех существующих интеграций.
2. **Только gzip, без brotli.** `compress/gzip` в stdlib и уже слинкован;
   brotli — внешняя зависимость ради нескольких процентов. Клиент, не
   приславший `Accept-Encoding: gzip`, получает несжатый ответ.
3. **Порог 1 КиБ.** Ответы меньше — не сжимаем: на коротком JSON gzip даёт
   отрицательную экономию и лишний CPU.
4. **Сжимаем только «сжимаемые» типы**: `text/*`, `application/json`,
   `application/xml`, `application/javascript`, `image/svg+xml`. Уже сжатые
   (`image/png`, `application/pdf`, `application/zip`) не трогаем.
5. **`Vary: Accept-Encoding` обязателен** на всех сжимаемых ответах, иначе кэш
   плана 126 (и любой промежуточный прокси) отдаст gzip-тело клиенту, который
   gzip не принимает.
6. **Порядок с кэшем (план 126): кэшируем НЕсжатое тело, сжимаем на выдаче.**
   Иначе кэш пришлось бы держать в двух вариантах на ключ, а `304`-ответы
   считать по ETag сжатого тела (ETag обязан относиться к представлению —
   получилось бы два разных ETag на один ресурс).
7. **CSP уровня сервиса заменяет глобальный**, а не дополняет (см. пункт 3
   «что мешает»): устанавливаем через `Header().Set`, не `Add`.
8. **HSTS ставится только при HTTPS-запросе** (`r.TLS != nil` или
   `X-Forwarded-Proto: https` от доверенного прокси — берём тот же способ
   определения, что уже используется в платформе; если его нет, ограничиваемся
   `r.TLS`). HSTS на http-ответе игнорируется браузером, но в тестовой среде на
   localhost может заблокировать доступ по http к тому же хосту — это дорогая
   ошибка для разработчика.
9. **`extra` не может переопределить `nosniff`** и `Access-Control-*`
   (CORS — отдельный механизм сервиса): попытка задать их в `extra` — ошибка
   `onebase check`, а не молчаливое игнорирование.

## Изменения в коде

| Файл | Что делаем |
|---|---|
| `internal/httpservice/service.go` | Поля `Compress *bool \`yaml:"compress"\`` (nil = умолчание по auth) и `SecurityHeaders *SecurityHeadersConfig \`yaml:"security_headers"\``; тип `SecurityHeadersConfig{CSP, FrameOptions, ReferrerPolicy string; HSTS int; Extra map[string]string}`; нормализация в `Normalize()` |
| `internal/ui/services_compress.go` (новый) | `gzipWriter` поверх `http.ResponseWriter`: буферизация до порога, решение по `Content-Type` и `Accept-Encoding`, установка `Content-Encoding`/`Vary`, удаление `Content-Length`; пул `gzip.Writer` (`sync.Pool`) |
| `internal/ui/services_headers.go` (новый) | `applyServiceSecurityHeaders(w, r, svc)` |
| `internal/ui/services.go` | Вызов `applyServiceSecurityHeaders` сразу после определения сервиса (до исполнения обработчика — чтобы заголовки были и на ошибочных ответах); обёртка writer'а сжатием на выдаче, после кэша (решение 6) |
| `internal/configcheck/check.go` | Валидация: неизвестный `frame_options`, отрицательный `hsts`, запрещённые ключи в `extra` (решение 9) |
| `internal/httpservice/service_test.go` | Разбор YAML и умолчания `compress` по `auth` |
| `internal/ui/services_compress_test.go`, `services_headers_test.go` (новые) | Тесты (см. ниже) |
| `docs/features.md` | Секция «Сжатие и заголовки безопасности HTTP-сервисов», `status: testing` |

## Тесты

Через реальный HTTP (`httptest`), как `services_test.go`:

1. `TestCompress_GzipWhenAccepted` — ответ > 1 КиБ с `Accept-Encoding: gzip`
   приходит сжатым, распаковывается в исходное тело, есть
   `Vary: Accept-Encoding`.
2. `TestCompress_NoAcceptEncoding` — без заголовка тело несжато и корректно.
3. `TestCompress_BelowThreshold` — ответ 100 байт не сжимается (решение 3).
4. `TestCompress_BinaryTypeSkipped` — `image/png` не сжимается (решение 4).
5. `TestCompress_DefaultByAuth` — сервис `auth: none` сжимает без явного флага;
   сервис `auth: basic` — не сжимает; `auth: basic` + `compress: true` —
   сжимает (решение 1).
6. `TestCompress_ContentLengthRemoved` — на сжатом ответе нет неверного
   `Content-Length`.
7. `TestSecurityHeaders_ServiceOverridesGlobal` — CSP сервиса приходит **один
   раз** и содержит значение сервиса, а не глобальное (решение 7).
8. `TestSecurityHeaders_NosniffAlways` — `nosniff` есть даже без блока
   `security_headers`; попытка переопределить его через `extra` не проходит
   `check`.
9. `TestSecurityHeaders_HSTSOnlyOverTLS` — на http-запросе `Strict-Transport-Security`
   отсутствует (решение 8).
10. `TestSecurityHeaders_OnErrorResponses` — заголовки стоят и на 404/500 от
    сервиса (иначе страница ошибки — дыра в политике).
11. `TestSecurityHeaders_CORSUntouched` — блок `security_headers` не ломает
    существующие `Access-Control-*` (решение 4 в «что мешает»).
12. `TestCompress_WithCache` (если план 126 уже влит) — кэш хранит несжатое
    тело: клиент без gzip и клиент с gzip получают корректные ответы с одним и
    тем же ETag (решение 6).

## Verification

```powershell
taskkill /IM onebase.exe /F
go build ./... ; go test ./internal/ui/ ./internal/httpservice/ ./internal/configcheck/
```

```powershell
curl -s -H "Accept-Encoding: gzip" -D - -o body.gz http://localhost:8080/hs/demo/big
# ожидаем: Content-Encoding: gzip, Vary: Accept-Encoding, размер body.gz заметно меньше
curl -s -D - -o /dev/null http://localhost:8080/hs/demo/page | Select-String "Content-Security|X-Content-Type|Frame"
```

## Границы (чего не делаем)

- Не включаем сжатие для админки/REST v2 в этом плане: там браузерная сессия и
  CSRF-токены — вопрос BREACH требует отдельного разбора и своего плана.
- Не добавляем brotli (решение 2).
- Не делаем глобальный `security_headers` в `app.yaml` — политика привязана к
  конкретной публичной поверхности; общий набор уже даёт `websec`.
- Не трогаем `websec.SecurityHeaders` — он остаётся политикой по умолчанию.

## Эстимейт

| Работа | Оценка |
|---|---|
| Метаданные (`compress`, `security_headers`) + нормализация + `check` | 0.5 дня |
| gzip-обёртка с порогом, типами, пулом | 0.5 дня |
| Применение заголовков + порядок с CORS/кэшем | 0.25 дня |
| Тесты (12 сценариев) | 0.75 дня |
| Документация | 0.25 дня |
| **Итого** | **~2–2.25 дня** |
