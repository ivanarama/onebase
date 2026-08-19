# План 53 — Усиление web-безопасности

**Статус:** ✅ Реализовано (2026-06-10, ветка `fix/web-security-hardening`)

> **Как реализовано.** Этап 1: `auth.OneTimeCodes` (single-use, TTL 30 с) +
> `POST /auth/one-time-code`; `/auth/bootstrap` принимает только `code=`;
> middleware не читает `?_tk=`; сырой токен больше не вшивается в HTML
> конфигуратора; лаунчер проксирует выдачу кода (`POST /bases/{id}/one-time-code`);
> логгер `api/logging.go` редактирует `_tk/token/code/api_key/password/secret`.
> Этап 2: `auth.LoginLimiter` — 5 неудач (IP, login) в окне минуты → 429 +
> Retry-After; сброс при успехе. Этап 3: пакет `internal/websec` (база + лаунчер):
> nosniff, Referrer-Policy same-origin, CSP `frame-ancestors 'self' localhost:*`
> (вместо X-Frame-Options — конфигуратор на другом порту встраивает базу в
> iframe). **Отступление:** CSRF — проверка Origin/Sec-Fetch-Site вместо
> double-submit cookie: не требует правки всех форм/fetch и не ломает
> REST-клиенты без Origin; защита от браузерного CSRF эквивалентна.
> Этап 4: `--host` (по умолчанию `127.0.0.1`), предупреждение в stderr при
> не-loopback без пользователей; dev-сервер всегда loopback.
**Источник:** `docs/history/АнализПроекта-2026-06-10.md` §2.2, §2.4, §2.5, §2.7.
**Приоритет:** 🟠 Высокий — актуально при выходе из single-user desktop в сеть/мультипользователя.

---

## Контекст

Платформа исполняет бизнес-операции через POST (провести/удалить/изменить документ),
но web-слой не имеет нескольких базовых защит. Поиск по коду: `csrf`, `rate-limit`,
`x-frame-options`, `content-security-policy` — **0 совпадений**.

Отдельно: сессионный токен передаётся в URL и попадает в логи.

---

## Этап 1 — Токен сессии вне URL и логов (§2.2)

**Проблема.** Конфигуратор открывает пользовательский режим в iframe, передавая токен
в query: `/ui?_tk=<token>` (`launcher/configurator_tmpl.go:2690`, читается в
`auth/middleware.go:33`). `middleware.Logger` стоит на корневом роутере выше auth-группы
(`api/server.go:34`) и логирует полный RequestURI → **токен пишется в stdout**. Токены в
URL также утекают через `Referer` и историю браузера. TTL токена — 24 ч (`users.go:245`).

> Замечено ещё в плане 47 (HTTP-безопасность #4 «✅ верно»), но в реализацию не попало.

**Решение.**
1. Заменить передачу токена в URL на **одноразовый bootstrap-код**: конфигуратор
   запрашивает у лаунчера короткоживущий (TTL ~30 c, single-use) код, передаёт его в
   `/auth/bootstrap?code=...`; хендлер обменивает код на сессию и ставит cookie, затем
   редиректит без кода. `auth/handlers.go:170 Bootstrap` уже почти такой — перевести с
   `token` на `code`.
2. Пока не убран `_tk` полностью — добавить **редактирующий LogFormatter** для
   `middleware.Logger` (или свой logger middleware), вырезающий значения
   `_tk|token|code|api_key` из логируемого URI.

| Файл | Изменение |
|---|---|
| `internal/auth/oneTimeCode.go` (новый) | генерация/обмен/протухание одноразовых кодов (in-memory map + mutex + TTL) |
| `internal/auth/handlers.go` | `Bootstrap` принимает `code`, обменивает на сессию |
| `internal/auth/middleware.go` | удалить ветку `_tk` из query (оставить cookie) после миграции конфигуратора |
| `internal/launcher/configurator_tmpl.go:2690` | URL без `_tk`; сперва дёрнуть `/cfg/one-time-code`, открыть `/auth/bootstrap?code=` |
| `internal/api/server.go` | свой logger middleware с редактированием секретов |

---

## Этап 2 — Rate-limiting логина (§2.4)

**Проблема.** Брутфорс пароля ничем не ограничен (`auth/handlers.go:67 LoginSubmit`,
`:114 LoginJSON`).

**Решение.** Лёгкий in-memory лимитер попыток на `(IP, login)`: счётчик + окно +
экспоненциальная задержка/блокировка после N неудач. Без внешних зависимостей.

```go
// internal/auth/ratelimit.go
type LoginLimiter struct { mu sync.Mutex; attempts map[string]*bucket; maxFails int; window time.Duration }
func (l *LoginLimiter) Allow(key string) (ok bool, retryAfter time.Duration)
func (l *LoginLimiter) Reset(key string) // при успешном входе
```

Применить в `LoginSubmit`/`LoginJSON`: при `!Allow` → `429 Too Many Requests` +
`Retry-After`, не доходя до `Authenticate`. На успех — `Reset`. Учитывать `X-Forwarded-For`
осторожно (только если за доверенным прокси; иначе `RemoteAddr`).

---

## Этап 3 — Security-заголовки + CSRF (§2.5)

**Заголовки** (middleware на корневом роутере):
- `X-Frame-Options: SAMEORIGIN` (или `Content-Security-Policy: frame-ancestors 'self'`) —
  база встраивается в iframe конфигуратора того же origin, clickjacking извне закрыт.
- `X-Content-Type-Options: nosniff`.
- `Referrer-Policy: same-origin` — попутно режет утечку токена через Referer (этап 1).

**CSRF.** Токен по схеме double-submit cookie:
1. На GET-рендере формы выдаём cookie `ob_csrf=<rand>` (НЕ HttpOnly, чтобы JS читал) и
   кладём то же значение в скрытое поле/заголовок.
2. Middleware на mutating-методах (`POST/PUT/DELETE`) сверяет заголовок `X-CSRF-Token`
   (или поле формы) с cookie. Несовпадение → `403`.
3. Исключения: публичные `/login`, `/auth/*` (там нет сессии), `/health`, PWA-ассеты.
   Для form-POST добавить hidden input в шаблоны; для fetch-POST — заголовок (общий
   JS-хелпер уже шлёт JSON, добавить заголовок централизованно).

| Файл | Изменение |
|---|---|
| `internal/api/server.go` | `r.Use(securityHeaders)`, `r.Use(csrf.Protect)` внутри protected-группы |
| `internal/auth/csrf.go` (новый) | выдача/проверка double-submit токена |
| `internal/ui/templates*.go` | hidden `_csrf` в формах + заголовок в fetch-хелпере |

---

## Этап 4 — Bind на loopback по умолчанию (§2.7)

**Проблема.** Сервер слушает `:port` (все интерфейсы, `api/server.go:86`), а при
отсутствии пользователей auth отключён целиком (`admin.go:668`, `middleware.go:20`) —
включая консоль кода (произвольный DSL). Комбинация = footgun при пробросе порта.

**Решение.**
- Флаг `--host` (по умолчанию `127.0.0.1`); `--host 0.0.0.0` — явное согласие.
- При старте на не-loopback адресе **без настроенных пользователей** — заметное
  предупреждение в stderr (и опционально отказ без `--allow-insecure`).

| Файл | Изменение |
|---|---|
| `internal/cli/run.go` | флаг `--host`, проброс в `api.New` |
| `internal/api/server.go` | `Addr = host:port`; предупреждение при `0.0.0.0` + `!hasUsers` |

---

## Тесты

- `ratelimit_test.go` — блокировка после N попыток, сброс на успех, окно.
- `csrf_test.go` — POST без/с неверным токеном → 403; с верным → проходит; публичные пути исключены.
- `auth handlers` — bootstrap по одноразовому коду: повторное использование кода → отказ; протухание.
- security-заголовки присутствуют в ответе (`headers_test.go`).
- логи не содержат значения токена/кода (проверка LogFormatter).

## Verification

1. `curl` логина в цикле → после N попыток `429 + Retry-After`.
2. POST провести документ из стороннего origin без CSRF-токена → 403; из UI — работает.
3. В stdout-логе на заходе из конфигуратора нет `_tk`/`code` значения.
4. `onebase run` без `--host` слушает только `127.0.0.1` (проверить `netstat`).
5. Открытие пользовательского режима из конфигуратора по-прежнему работает (bootstrap-код).

## Связанные

- План 47 (этап 1) — там закрыли path traversal/zip-slip; `_tk` остался — закрывается здесь.
- План 54 — rate-limit для ИИ-чата (там cost-DoS), отдельно от логина.

## Эстимейт

- Этап 1 (токен/логи): **1.5 дня**.
- Этап 2 (rate-limit логина): **0.5 дня**.
- Этап 3 (заголовки + CSRF): **1.5–2 дня** (CSRF в form-POST требует правки шаблонов).
- Этап 4 (bind): **0.5 дня**.
- **Итого ≈ 4–4.5 дня.**
