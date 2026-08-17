# План 126 — Кэш ответов HTTP-сервисов

Дата проектирования: 2026-08-17.
Статус: ⬜ **Проект** (не начато).
Ветка: `feature/126-http-service-cache`.
Часть группы «веб-примитивы» (124–128). Самостоятельная ценность вне CMS:
любой публичный сервис-справочник (прайс для мобильного приложения, курсы,
остатки для витрины партнёра) сегодня исполняет DSL и ходит в БД на каждый
запрос, включая одинаковые.

## Контекст

`serviceDispatch` (`internal/ui/services.go:132`) на каждый запрос: находит
сервис → проверяет auth → читает тело → строит DSL-переменные → исполняет
обработчик → сериализует ответ. Никакого повторного использования результата
нет — проверено по структуре `httpservice.Service`
(`internal/httpservice/service.go:35`): поля `auth`, `secret`, `rate_limit`,
`roles`, `cors`, `templates`, кэша нет.

Для учётной интеграции это терпимо (запросы редкие и разные), для публичной
витрины — нет: один и тот же HTML/JSON пересобирается на каждый заход, включая
заходы поисковых роботов.

## Что мешает в текущем коде (проверено по `3350679f`)

1. **Ответ пишется прямо в `http.ResponseWriter`** — `writeServiceResult`
   (`internal/ui/services.go:418`) сразу выставляет заголовки и пишет тело.
   Чтобы ответ можно было сохранить, запись должна идти через буфер
   (`httptest.ResponseRecorder`-подобный или собственный writer).
2. **Реестр сервисов читается «вживую»** (`MountServices`,
   `internal/ui/services.go:90`) — `--watch` подхватывает изменения без
   рестарта. Значит кэш обязан сбрасываться при перезагрузке конфигурации,
   иначе после правки модуля сайт продолжит отдавать старую страницу.
3. **Аутентификация разрешается ДО исполнения** (`resolveServiceAuth`,
   `services.go:297`) и кладёт пользователя в контекст. Любой кэш ответа для
   сервиса с auth ≠ none — это выдача чужого ответа другому пользователю
   (см. решение 2).
4. **Метрики** — `internal/metrics`, счётчики регистрируются через
   `RegisterCounterFunc(name, help, func() float64)` (`metrics.go:138`).
5. **`golang.org/x/sync` уже в `go.mod`** (строка 52, `// indirect`) — для
   single-flight не нужна новая зависимость, только промоут в прямые.

## Синтаксис (YAML)

```yaml
name: Nortena
root_url: nortena
auth: none
cache:
  ttl: 60            # секунды; 0 или отсутствие блока = кэш выключен
  vary: [query]      # что входит в ключ, помимо пути: query, host, lang
  public: true       # отдавать наружу Cache-Control: public, max-age=<ttl>
  max_body: 1048576  # байт; ответы крупнее не кэшируются (по умолчанию 1 МиБ)
templates:
  - template: /{*путь}
    methods:
      GET: Роутер
```

Сброс из DSL (уточнено при реализации: функции плоские, как остальные
инжектируемые builtin'ы платформы, а не методы объекта-глобала):

```
СброситьКэшСервисов();               // весь кэш процесса
СброситьКэшСервисов("Nortena");      // один сервис (имя или root_url)
Всего = РазмерКэшаСервисов();        // байт в кэше — для диагностики
```

Английские имена: `ResetServiceCache([name])`, `ServiceCacheSize()`.

## Принятые решения

1. **Кэшируются только GET и HEAD.** POST/PUT/DELETE — мутации; кэш на них
   даёт «повтор заявки не сохранился», а это молчаливая потеря данных.
2. **Кэш разрешён ТОЛЬКО при `auth: none`.** Иначе ответ, собранный под правами
   одного пользователя (RLS, маскирование ПДн, роли), достанется другому —
   немедленная утечка. Гейт двойной, fail-closed:
   - `onebase check` — ошибка конфигурации «кэш допустим только при auth: none»;
   - рантайм — при `auth != none` блок `cache` игнорируется и **пишется
     предупреждение в лог при загрузке** (не молча: молчаливое игнорирование
     оставит владельца в уверенности, что кэш работает).
3. **Кэшируется только успешный ответ `200`** без `Set-Cookie`. 3xx/4xx/5xx не
   кэшируем: «404 залип на час» — типовой инцидент CMS; `Set-Cookie` в кэше
   раздаёт одну сессию нескольким клиентам.
4. **Хранение — в памяти процесса, LRU по суммарному размеру.** Лимит по
   умолчанию 64 МиБ на весь процесс (не на сервис), настраивается через
   `app.yaml` (`http_cache_max_bytes`). БД как хранилище не берём: кэш в БД
   меняет одну проблему (нагрузка) на другую (запись при каждом ответе) и
   тянет вопросы конкурентной инвалидации.
   **Кэш процессный**: при нескольких процессах/репликах каждый греет свой —
   задокументировать, это не дефект, а свойство.
5. **ETag + условные запросы.** На кэшируемом ответе считаем `sha256` тела →
   `ETag: W/"<hex16>"`. Запрос с `If-None-Match`, совпавшим с текущим ETag,
   получает `304 Not Modified` без тела (и без исполнения DSL при попадании в
   кэш). Это даёт экономию трафика даже при `ttl: 0`… но ETag считаем только
   при включённом кэше — иначе пришлось бы буферизовать каждый ответ каждого
   сервиса ради заголовка, который никто не спросил.
6. **`vary` управляет ключом.** По умолчанию `[query]`. Ключ:
   `root_url | METHOD | path | ?отсортированный query (если vary: query) |
   host (если vary: host) | lang (если vary: lang)`. Порядок параметров в URL
   на ключ не влияет (сортируем), иначе `?a=1&b=2` и `?b=2&a=1` — две записи с
   одинаковым содержимым.
   `vary: []` (пустой список) — ключ только по пути: осознанный режим «одна
   страница для всех», query игнорируется.
7. **Промахи по ключу сериализуются.** Пять одновременных запросов на
   «холодную» популярную страницу не должны запускать пять исполнений DSL (это
   ровно тот момент, когда сайт и падает). Реализовано мьютексом на ключ
   (`serviceCache.lockKey`) с повторной проверкой кэша после ожидания, а не
   `singleflight`: результат тот же (обработчик исполняется один раз, ждущие
   берут готовый ответ), но не требует новой прямой зависимости и вписывается
   в диспетчер без его разбора на части. Ошибки не шарятся между ждущими:
   ждущий, не найдя ответа в кэше, выполняет запрос сам.
8. **Сброс кэша при перезагрузке реестра** (`--watch`, hot-reload, план из
   `dev-workflow`) — обязательно, иначе правка модуля не видна на сайте, и это
   выглядит как «платформа не перезагрузила конфигурацию».
9. **Инвалидация по данным — задача конфигурации, не платформы.** Автосброс
   «при записи любой сущности, участвовавшей в ответе» требует трассировки
   зависимостей запроса — это отдельный большой механизм. Конфигурация зовёт
   `КэшСервисов.Очистить(...)` из `OnWrite` контентных справочников (хук уже
   существует: `internal/entityservice/service.go:445`). В плане 129 это
   прописано как обязанность конфигурации.
10. **Метрики** `onebase_http_cache_hits_total`, `..._misses_total`,
    `..._evictions_total`, `..._bytes` — через `RegisterCounterFunc`/
    `RegisterGaugeFunc`. Без метрик нельзя ответить «кэш вообще работает?».

## Изменения в коде

| Файл | Что делаем |
|---|---|
| `internal/httpservice/service.go` | Тип `CacheConfig{TTL int, Vary []string, Public bool, MaxBody int64}`; поле `Cache *CacheConfig \`yaml:"cache"\``; нормализация в `Normalize()` (нижний регистр `vary`, дефолт `MaxBody`) |
| `internal/httpservice/service_test.go` | Разбор и нормализация блока `cache` |
| `internal/ui/services_cache.go` (новый) | `type serviceCache struct` — LRU (`container/list` + `map[string]*list.Element`), TTL, учёт суммарного размера, `Get(key) (*cachedResponse, bool)`, `Put(key, resp, ttl)`, `Clear(serviceRoot string)`, `Size() int64`; `singleflight.Group`; счётчики метрик |
| `internal/ui/services.go` | В `serviceDispatch` после `resolveServiceAuth`/rate-limit и до `beginOperation`: вычисление ключа, попытка отдачи из кэша (+`304` по `If-None-Match`), при промахе — исполнение через single-flight с буферизацией ответа и записью в кэш. Буфер: `cachingWriter` поверх `http.ResponseWriter` |
| `internal/ui/server.go` | Поле `svcCache *serviceCache` в `Server`, инициализация из `app.yaml`; сброс при перезагрузке реестра |
| `internal/ui/dsl_service_cache.go` (новый) | Функции `СброситьКэшСервисов`/`ResetServiceCache`, `РазмерКэшаСервисов`/`ServiceCacheSize`; имя сервиса принимается и как `name`, и как `root_url` |
| `internal/ui/handlers_dsl.go` | Регистрация функций рядом с вложениями |
| `internal/api/metrics.go`, `internal/ui/server.go` | Счётчики hits/misses/evictions/bytes в `/metrics` через `ServiceCacheStats()` |
| `internal/cli/project_runtime.go`, `internal/api/server.go` | Сброс кэша при горячей перезагрузке (`--watch`) |
| `internal/configcheck/check.go` | Гейт: `cache` при `auth != none` → ошибка; `ttl < 0` → ошибка; неизвестное значение в `vary` → ошибка |
| `internal/project/*` (загрузка `app.yaml`) | Ключ `http_cache_max_bytes` (по умолчанию 64 МиБ) |
| `docs/features.md` | Секция «Кэш ответов HTTP-сервисов», `status: testing` |
| `internal/cli/aiguide.go` | Кратко: блок `cache:` и `КэшСервисов.Очистить()` в `OnWrite` |

Ключевые сигнатуры:

```go
type cachedResponse struct {
    Status  int
    Header  http.Header   // без hop-by-hop и без Set-Cookie
    Body    []byte
    ETag    string
    Expires time.Time
}

func (c *serviceCache) Get(key string) (*cachedResponse, bool)
func (c *serviceCache) Put(key string, resp *cachedResponse, ttl time.Duration)
func (c *serviceCache) Clear(serviceRoot string) int   // "" = всё; возвращает число выброшенных записей
func cacheKey(svc *httpservice.Service, r *http.Request, lang string) string
```

## Тесты

`internal/ui/services_cache_test.go` — через реальный HTTP (`httptest.NewServer`
поверх смонтированного роутера), как существующие `services_test.go`.

1. `TestServiceCache_HitSkipsHandler` — обработчик, считающий вызовы; два
   одинаковых GET → счётчик 1, тела совпадают.
2. `TestServiceCache_TTLExpires` — после истечения TTL обработчик вызывается
   снова (время подменяется через инъекцию `now func() time.Time`, а не
   `time.Sleep` в тесте).
3. `TestServiceCache_VaryQuery` — `?a=1` и `?a=2` кэшируются раздельно;
   `?a=1&b=2` и `?b=2&a=1` — одна запись (решение 6).
4. `TestServiceCache_VaryHost` — при `vary: [host]` разные `Host` дают разные
   записи; без `host` в `vary` — одну.
5. `TestServiceCache_OnlyGET` — POST не кэшируется (обработчик вызывается
   каждый раз).
6. `TestServiceCache_AuthNotNone` — сервис с `auth: basic` и блоком `cache`:
   ответы **не** кэшируются, в логе предупреждение (решение 2). Тест —
   регрессионный на утечку между пользователями: два запроса под разными
   учётками получают разные тела.
7. `TestServiceCache_NoCacheForNon200AndSetCookie` — 404 и ответ с `Set-Cookie`
   не попадают в кэш.
8. `TestServiceCache_ETag304` — второй запрос с `If-None-Match` даёт 304 без
   тела; неверный ETag — 200 с телом.
9. `TestServiceCache_MaxBody` — ответ больше `max_body` не кэшируется
   (обработчик зовётся дважды), но отдаётся корректно.
10. `TestServiceCache_SingleFlight` — N параллельных запросов на холодный ключ
    → обработчик вызван один раз, все получают одинаковый ответ.
11. `TestServiceCache_ClearFromDSL` — `КэшСервисов.Очистить("Nortena")`
    выбрасывает записи только этого сервиса.
12. `TestServiceCache_ClearOnReload` — перезагрузка реестра сбрасывает кэш
    (решение 8).
13. `TestServiceCache_LRUEviction` — при переполнении лимита старые записи
    выбрасываются, процесс не растёт неограниченно.
14. `internal/configcheck` — тест гейта: конфигурация с `cache` при
    `auth: session` не проходит `check`.

## Verification

```powershell
taskkill /IM onebase.exe /F
go build ./... ; go test ./internal/ui/ ./internal/httpservice/ ./internal/configcheck/
go build -o onebase.exe ./cmd/onebase
```

Ручная проверка (конфигурация с сервисом, отдающим текущее время):

```powershell
.\onebase.exe run --project <конф> --sqlite test.db --port 8080
curl -i http://localhost:8080/hs/demo/time    # запомнить время и ETag
curl -i http://localhost:8080/hs/demo/time    # время ТО ЖЕ (кэш), ETag тот же
curl -i -H 'If-None-Match: "<etag>"' http://localhost:8080/hs/demo/time   # 304
# подождать ttl → время обновилось
curl -s http://localhost:8080/metrics | Select-String http_cache
```

Нагрузочная проверка (критерий приёмки для CMS, план 129): страница каталога
на SQLite держит ≥ 100 req/s с кэшем; без кэша — базовая линия для сравнения
(`loadtest/`, k6 — см. `project_load_testing` в заметках).

## Границы (чего не делаем)

- Не делаем распределённый кэш (Redis/общий стор) — это P3-1 из плана 111.
- Не делаем автоинвалидацию по зависимостям данных (решение 9).
- Не кэшируем ответы авторизованных сервисов даже «по согласию владельца»:
  правильный способ — вынести публичную часть в отдельный `auth: none` сервис.
- Не трогаем кэширование статики UI (`internal/ui/static.go` — там свой ETag).

## Эстимейт

| Работа | Оценка |
|---|---|
| `CacheConfig` + нормализация + гейт в `check` | 0.5 дня |
| LRU-кэш с TTL, размером и метриками | 1 день |
| Интеграция в `serviceDispatch` (буферизация, ETag/304, single-flight) | 1 день |
| Глобал `КэшСервисов` + инжекция | 0.25 дня |
| Тесты (14 сценариев) | 1.25 дня |
| Документация | 0.25 дня |
| **Итого** | **~4–4.25 дня** |
