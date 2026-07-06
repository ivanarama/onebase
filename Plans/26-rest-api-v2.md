# Этап 26 — REST API v2 (фильтры, пагинация, OpenAPI)

**Статус:** 🟨 Частично реализовано

**Актуализация 2026-07-06:** первый PR v2 берёт стабильный HTTP-контракт
`/api/v2/catalog|document/...`: envelope `{data, meta}`, `page/limit`,
`filter[F]`, CRUD, `post/unpost` и `openapi.json`, переиспользуя текущие
session/RBAC guardrails и `entityservice.Save`. Bearer API-токены, admin UI
токенов и JSON-исполнение отчётов остаются отдельными PR, чтобы не смешивать
контракт API с новой моделью аутентификации и runtime отчётов.

**Актуализация 2026-06-25:** перед полноценным v2 нужно закрыть guardrails из
плана 76: RBAC в текущем REST API, дефолтные лимиты/пагинацию, caps на размер
ответа/запроса и атомарную optimistic locking запись. Иначе v2 закрепит старые
многопользовательские риски вместо того, чтобы стать безопасным публичным API.

## Контекст

Текущий REST API (`/api/...`) возвращает все записи без фильтрации и пагинации.
Интеграция с внешними системами, мобильными клиентами и Excel-надстройками требует полноценного API:
- Фильтрация и поиск (те же параметры, что в UI)
- Пагинация
- Аутентификация через API-токен
- OpenAPI 3.0 спецификация для автогенерации клиентов

## Синтаксис / UX

### CRUD-эндпоинты

```
GET    /api/v2/catalog/{name}          ?q=&filter[F]=&page=&limit=&sort=&dir=
GET    /api/v2/catalog/{name}/{id}
POST   /api/v2/catalog/{name}          Body: JSON объекта
PUT    /api/v2/catalog/{name}/{id}     Body: JSON объекта
DELETE /api/v2/catalog/{name}/{id}

GET    /api/v2/document/{name}         ?q=&filter[F]=&page=&limit=
GET    /api/v2/document/{name}/{id}
POST   /api/v2/document/{name}
PUT    /api/v2/document/{name}/{id}
DELETE /api/v2/document/{name}/{id}

POST   /api/v2/document/{name}/{id}/post    # Провести
POST   /api/v2/document/{name}/{id}/unpost  # Отменить проведение

GET    /api/v2/report/{name}?param1=&param2=   # Выполнить отчёт → JSON

GET    /api/v2/openapi.json            # OpenAPI 3.0 спецификация
```

### Формат ответа

```json
{
  "data": [...],
  "meta": {
    "total": 1250,
    "page": 2,
    "limit": 50,
    "total_pages": 25
  }
}
```

### Аутентификация

API-токен в заголовке: `Authorization: Bearer <token>`

Токены создаются в **Администрирование → API-токены**.

```sql
CREATE TABLE _api_tokens (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL,
    token_hash TEXT NOT NULL,
    user_id    UUID REFERENCES _users(id),
    created_at TIMESTAMPTZ DEFAULT now(),
    last_used  TIMESTAMPTZ,
    expires_at TIMESTAMPTZ
);
```

## Изменения в коде

**`internal/api/v2/`** (новый пакет):
- `router.go` — chi-роутер `/api/v2/...`
- `catalog.go` — CRUD-хэндлеры для справочников
- `document.go` — CRUD + post/unpost
- `report.go` — запуск отчёта → JSON
- `openapi.go` — генерация OpenAPI-спецификации из метаданных
- `auth.go` — middleware проверки Bearer-токена

**`internal/storage/tokens.go`**:
- `CreateToken`, `ValidateToken`, `RevokeToken`

**`internal/ui/server.go`**:
- Маунт `/api/v2/` → новый роутер

**Конфигуратор** (admin UI):
- Страница «API-токены»: список, создание, отзыв

## OpenAPI-генерация

```go
func GenerateOpenAPI(proj *project.Project) *openapi3.T {
    spec := openapi3.NewT()
    spec.Info = &openapi3.Info{Title: proj.Config.Name, Version: proj.Config.Version}
    for _, e := range proj.Entities {
        addCRUDPaths(spec, e)
    }
    // Схемы из метаданных полей
    return spec
}
```

Используем `github.com/getkin/kin-openapi/openapi3` для валидации и сериализации.

## Тесты

- `GET /api/v2/catalog/X?q=...` возвращает отфильтрованные записи
- POST → GET → PUT → DELETE round-trip
- Аутентификация: 401 без токена, 403 при недостатке прав
- `GET /api/v2/openapi.json` возвращает валидную OpenAPI 3.0 спецификацию

## Эстимейт

5–7 дней.
