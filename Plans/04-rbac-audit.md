# Этап 4 — RBAC и Audit log

## Контекст

Сейчас в onebase примитивная авторизация: флаг `is_admin bool` на пользователе. Для production-использования нужно:
1. **RBAC** (Role-Based Access Control) — роли с правами на сущности и операции
2. **Audit log** — журнал кто что когда изменил, для compliance и расследования инцидентов

---

## Часть А — RBAC

### Концепция

- **Роль** — именованный набор разрешений (например, «Менеджер», «Бухгалтер», «Кладовщик»)
- **Разрешение** — пара (объект, операция). Объект: `catalogs/Контрагент`, `documents/Реализация`, `registers/Остатки`, `reports/ОстаткиТоваров`. Операция: `read`, `write`, `delete`, `post` (для документов), `run` (для отчётов)
- **Пользователь** имеет 0 или несколько ролей
- **`is_admin`** — суперроль, минует проверки

### YAML формат

`roles/менеджер.yaml`:
```yaml
name: Менеджер
description: Менеджер по продажам
permissions:
  catalogs:
    Контрагент: [read, write]
    Номенклатура: [read]
    Склад: [read]
  documents:
    Реализация: [read, write, post, unpost]
    Поступление: [read]
  registers:
    Остатки: [read]
  reports:
    ОстаткиТоваров: [run]
```

`roles/кладовщик.yaml`:
```yaml
name: Кладовщик
description: Работа со складом
permissions:
  catalogs:
    Номенклатура: [read, write]
    Склад: [read]
  documents:
    Поступление: [read, write, post, unpost]
    Реализация: [read]      # видит, не редактирует
  registers:
    Остатки: [read]
```

### База данных

```sql
CREATE TABLE _roles (
    id UUID PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    description TEXT,
    permissions JSONB NOT NULL,    -- весь permissions блок из YAML
    updated_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE _user_roles (
    user_id UUID REFERENCES _users(id) ON DELETE CASCADE,
    role_id UUID REFERENCES _roles(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);
```

При запуске сервера: синк YAML-ролей → таблица `_roles` (upsert по имени).

### Изменения в коде

**`internal/auth/roles.go`** (новый)
```go
type Permission struct {
    Catalogs   map[string][]string  // entity → ops
    Documents  map[string][]string
    Registers  map[string][]string
    InfoRegs   map[string][]string  // из этапа 2
    Reports    map[string][]string
}

type Role struct {
    ID          string
    Name        string
    Description string
    Permissions Permission
}

func (r *Repo) LoadRolesYAML(dir string) ([]*Role, error)   // парсит YAML, синкает в _roles
func (r *Repo) GetRolesForUser(ctx, userID) ([]*Role, error)
func (r *Repo) AssignRole(ctx, userID, roleID) error
func (r *Repo) UnassignRole(ctx, userID, roleID) error

// Has — главная функция проверки
// kind: "catalog"|"document"|"register"|"inforeg"|"report"
// entity: имя сущности
// op: "read"|"write"|"delete"|"post"|"unpost"|"run"
func (u *User) Has(kind, entity, op string, roles []*Role) bool {
    if u.IsAdmin { return true }
    for _, r := range roles {
        var m map[string][]string
        switch kind {
        case "catalog":  m = r.Permissions.Catalogs
        case "document": m = r.Permissions.Documents
        case "register": m = r.Permissions.Registers
        case "inforeg":  m = r.Permissions.InfoRegs
        case "report":   m = r.Permissions.Reports
        }
        for _, allowed := range m[entity] {
            if allowed == op { return true }
        }
    }
    return false
}
```

**`internal/auth/users.go`** (расширить `User` структуру)
```go
type User struct {
    ID, Login, FullName string
    IsAdmin bool
    Roles []*Role         // загружаются вместе с пользователем
}
```

**`internal/auth/middleware.go`**
- В существующем `Middleware` после успешной аутентификации загружать роли пользователя и класть в context
- Новый middleware-фабрика:
```go
func (r *Repo) RequirePerm(kind, entity, op string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w, req) {
            user := UserFromContext(req.Context())
            if user == nil { http.Error(w, "unauthorized", 401); return }
            if !user.Has(kind, entity, op, user.Roles) {
                http.Error(w, "forbidden", 403)
                return
            }
            next.ServeHTTP(w, req)
        })
    }
}
```

**`internal/api/server.go`** (применить к роутам)
```go
// REST CRUD
r.With(authRepo.RequirePerm("catalog", entity, "read")).Get("/catalogs/{entity}/{id}", ...)
r.With(authRepo.RequirePerm("catalog", entity, "write")).Post("/catalogs/{entity}", ...)
...
```

Упростить: вместо явного wrapping каждого роута сделать один общий middleware который смотрит на URL `/{kind}/{entity}` и выводит права автоматически:
```go
r.Use(authRepo.AutoPermMiddleware(reg))  // умеет парсить URL и выбирать op по HTTP method
```

**`internal/ui/handlers.go`** (фильтрация nav)
- В `buildNav` фильтровать сущности по правам: пользователь видит только те, на которые есть `read`
- Скрывать кнопки Создать/Удалить/Провести если нет соответствующих прав

**`internal/ui/admin.go`**
- Страница `/ui/admin/roles` — список ролей
- Страница `/ui/admin/roles/{id}` — редактирование (имя, описание, permissions через чекбоксы для каждой сущности и операции)
- На странице `/ui/admin/users/{id}` — добавить блок «Роли» с чекбоксами

**`internal/project/loader.go`**
- Загрузка `roles/*.yaml` в `Project.Roles`

### Тесты

**`internal/auth/roles_test.go`**
```go
func TestUser_Has(t *testing.T) {
    role := &Role{Permissions: Permission{
        Documents: map[string][]string{"Реализация": {"read", "write"}},
    }}
    user := &User{Roles: []*Role{role}}
    
    assert.True(t,  user.Has("document", "Реализация", "read", user.Roles))
    assert.True(t,  user.Has("document", "Реализация", "write", user.Roles))
    assert.False(t, user.Has("document", "Реализация", "post", user.Roles))
    assert.False(t, user.Has("document", "Поступление", "read", user.Roles))
}

func TestUser_IsAdmin_OverridesAll(t *testing.T) {
    user := &User{IsAdmin: true}
    assert.True(t, user.Has("anything", "anywhere", "any-op", nil))
}
```

**Integration test** (требует БД):
- Создать роль через YAML
- Создать пользователя, назначить роль
- Запросить REST endpoint без прав → 403
- Назначить нужное право → 200

---

## Часть Б — Audit log

### Концепция

Каждое изменение в данных логируется:
- **Что**: `действие` + `сущность` + `id записи` + `поле` (для update) + `старое/новое значение`
- **Кто**: `user_id`
- **Когда**: `at`
- **Откуда**: `ip` (опционально), `user_agent` (опционально)

### Действия

| action | Когда |
|---|---|
| `create` | Создание новой записи в каталоге/документе |
| `update` | Изменение поля (по одной записи на каждое изменённое поле) |
| `delete` | Удаление записи |
| `post` | Проведение документа |
| `unpost` | Отмена проведения |
| `login` | Вход в систему |
| `logout` | Выход |

### База данных

```sql
CREATE TABLE _audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID,                    -- может быть NULL для системных действий
    user_login TEXT,                 -- денормализация на случай удаления пользователя
    action TEXT NOT NULL,            -- create|update|delete|post|unpost|login|logout
    entity_kind TEXT,                -- catalog|document|register|inforeg|constant|role|user
    entity_name TEXT,                -- ИмяСущности
    record_id UUID,                  -- ID изменённой записи
    field TEXT,                      -- название поля (для update)
    old_value JSONB,
    new_value JSONB,
    ip TEXT,
    at TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_audit_record ON _audit (entity_name, record_id);
CREATE INDEX idx_audit_user ON _audit (user_id, at DESC);
CREATE INDEX idx_audit_at ON _audit (at DESC);
```

### Изменения в коде

**`internal/storage/audit.go`** (новый)
```go
type AuditEntry struct {
    ID         uuid.UUID
    UserID     *uuid.UUID
    UserLogin  string
    Action     string
    EntityKind string
    EntityName string
    RecordID   *uuid.UUID
    Field      string
    OldValue   any
    NewValue   any
    IP         string
    At         time.Time
}

func (db *DB) AuditLog(ctx, e *AuditEntry) error
func (db *DB) AuditDiff(old, new map[string]any, entity *metadata.Entity) []FieldChange
func (db *DB) AuditByRecord(ctx, entityName string, recordID uuid.UUID) ([]*AuditEntry, error)
func (db *DB) AuditSearch(ctx, filter AuditFilter, params ListParams) ([]*AuditEntry, error)

type AuditFilter struct {
    UserID     *uuid.UUID
    Action     string
    EntityName string
    DateFrom   *time.Time
    DateTo     *time.Time
}
```

**`internal/storage/crud.go`** (хук в Upsert)
- При `Upsert` сначала прочитать старое значение (если запись существует)
- После успешного INSERT/UPDATE — для каждого изменённого поля вызвать `AuditLog(action: update, field: ...)` или `AuditLog(action: create)` если новая запись

**`internal/storage/posting.go`** (хук в Post/Unpost — уже есть в `internal/runtime/movements.go`?)
- Аналогично — логировать действие post/unpost

**`internal/auth/handlers.go`**
- В `LoginSubmit` после успешного логина — `AuditLog(action: login)`
- В `Logout` — `AuditLog(action: logout)`

**Контекст пользователя**
- В `crud.go` хуки нужно знать пользователя. Передавать через `context.Context`:
```go
type ctxKey int
const userCtxKey ctxKey = iota

func WithUser(ctx context.Context, userID, login string) context.Context
func GetUser(ctx context.Context) (uuid.UUID, string, bool)
```
- Middleware кладёт в context, storage читает оттуда

**`internal/ui/admin.go`**
- Страница `/ui/admin/audit` — список с фильтрами:
  - По пользователю (select из всех пользователей)
  - По сущности (select)
  - По действию
  - По диапазону дат
  - Пагинация
- Колонки: `at | user | action | entity | record_id | field | old → new`

**`internal/ui/handlers.go`**
- На странице редактирования любой записи добавить кнопку **«История»** → `/ui/{kind}/{entity}/{id}/history`
- Страница истории показывает все изменения этой конкретной записи в обратном хронологическом порядке

**`internal/api/handlers.go`**
- `GET /audit` — REST endpoint для получения логов (с фильтрами)
- `GET /audit/record/{entity}/{id}` — история конкретной записи

### Производительность

- Audit-таблица растёт быстро. Партиционирование по месяцам (через `pg_partman`) — отдельная задача.
- Для больших обновлений (массовое перепроведение) audit-вставки делать батчем.
- Опция в конфигурации: `audit.enabled: true/false`, `audit.exclude_fields: [updated_at, ...]`.

### Тесты

**`internal/storage/audit_test.go`** (integration)
```go
func TestAudit_OnUpdate(t *testing.T) {
    ctx := WithUser(context.Background(), userID, "admin")
    db.Upsert(ctx, "Контрагент", id, map[string]any{"Наименование": "Old"}, entity)
    db.Upsert(ctx, "Контрагент", id, map[string]any{"Наименование": "New"}, entity)
    
    history, _ := db.AuditByRecord(ctx, "Контрагент", id)
    require.Len(t, history, 2)
    assert.Equal(t, "create", history[0].Action)
    assert.Equal(t, "update", history[1].Action)
    assert.Equal(t, "Наименование", history[1].Field)
    assert.Equal(t, "Old", history[1].OldValue)
    assert.Equal(t, "New", history[1].NewValue)
}
```

---

## Verification

### RBAC

1. `go test ./internal/auth/...` — все тесты проходят
2. В `examples/trade` создать роли:
   - `roles/менеджер.yaml` (как в примере выше)
   - `roles/кладовщик.yaml`
3. Создать через UI пользователя `manager1`, назначить роль Менеджер
4. Войти как `manager1` → в nav видны только разрешённые сущности, нет кнопки «Удалить» для документов на которые нет права `delete`
5. Прямой переход по URL на запрещённую сущность → 403
6. Войти как admin → доступ ко всему

### Audit

1. `go test ./internal/storage/audit_test.go` — integration-тесты проходят
2. В `examples/trade`:
   - Войти как admin, отредактировать справочник
   - Открыть `/ui/admin/audit` → видна запись с user, action, field, old→new
   - На странице записи → кнопка «История» → видна вся история
3. Провести документ → в audit запись `post` с record_id
4. Войти как другой пользователь, отредактировать → audit показывает разные user

### Документация

`QUICKSTART.md` — добавить разделы:
- «Роли и права доступа»
- «Журнал изменений»

## Эстимейт

### RBAC
- Метаданные ролей + загрузка YAML: 0.5 дня
- Таблицы _roles, _user_roles + миграция: 0.3 дня
- `User.Has`, синк ролей: 0.5 дня
- Middleware (RequirePerm, AutoPerm): 0.5 дня
- UI (роли, назначение): 1 день
- Фильтрация nav и кнопок: 0.5 дня
- Тесты + примеры: 0.7 дня

**RBAC: 4 дня**

### Audit log
- Таблица _audit + миграция: 0.2 дня
- AuditLog + Diff: 0.5 дня
- Хуки в Upsert/Delete/Post/Unpost: 0.5 дня
- Контекст пользователя через ctx: 0.3 дня
- UI (общий лог + история записи): 1 день
- Тесты + пример: 0.5 дня

**Audit: 3 дня**

**Итого RBAC + Audit: 7 дней (~ 1.5 недели).**
