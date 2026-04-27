# Этап 1 — Перечисления и Константы

## Контекст

В onebase сейчас 3 типа метаданных (справочники, документы, регистры накопления). Перечислений и констант нет. На практике:
- Поле «Вид контрагента» (Поставщик/Покупатель) приходится делать строкой — нет валидации, опечатки.
- Глобальные параметры (валюта учёта, основная организация) хранить негде — дублируются в каждом документе.

Эта фича добавляет два простых, но фундаментальных типа объектов 1С.

---

## Часть А — Перечисления (Enumerations)

### YAML формат

`enums/видконтрагента.yaml`:
```yaml
name: ВидКонтрагента
values:
  - Поставщик
  - Покупатель
  - Прочее
```

### Использование в полях

`catalogs/контрагент.yaml`:
```yaml
name: Контрагент
fields:
  - name: Наименование
    type: string
  - name: Вид
    type: enum:ВидКонтрагента   # ← новый тип поля
```

### DSL

```
Если this.Вид = "Поставщик" Тогда
  Error("Покупателю нельзя выписывать счёт-фактуру");
КонецЕсли;
```

Доступ к перечислению глобально: `Перечисления.ВидКонтрагента.Поставщик` (вернёт строку «Поставщик»).

### Хранилище

Тип `enum:Имя` хранится в БД как `TEXT` с `CHECK (col IN ('значение1','значение2',...))`. При удалении значения из enum — `ALTER TABLE DROP CONSTRAINT` + `ADD CONSTRAINT` с новым списком.

### Изменения в коде

**`internal/metadata/types.go`**
```go
type Enum struct {
    Name   string
    Values []string
}

const FieldTypeEnum FieldType = "enum"

type Field struct {
    Name      string
    Type      FieldType
    RefEntity string  // для reference
    EnumName  string  // для enum:Имя
}
```

**`internal/metadata/yaml.go`**
- `LoadEnumFile(path) (*Enum, error)` — парсинг YAML
- В `parseField` распознавать префикс `enum:` → ставить `EnumName`

**`internal/project/loader.go`** (после `loadMetadata`)
```go
type Project struct {
    ...
    Enums []*metadata.Enum
}

func (p *Project) loadEnums() error {
    dir := filepath.Join(p.Dir, "enums")
    items, err := os.ReadDir(dir)
    if os.IsNotExist(err) { return nil }
    if err != nil { return err }
    for _, item := range items {
        if !strings.HasSuffix(item.Name(), ".yaml") { continue }
        e, err := metadata.LoadEnumFile(filepath.Join(dir, item.Name()))
        if err != nil { return err }
        p.Enums = append(p.Enums, e)
    }
    return nil
}
```

**`internal/metadata/validate.go`**
- При `enum:` проверять что имя существует в списке Enums проекта
- Передать список enums в `Validate(entities, enums)`

**`internal/runtime/registry.go`**
```go
type Registry struct {
    ...
    enums map[string]*metadata.Enum
}

func (r *Registry) Load(entities, programs, registers, reports, enums) {
    r.enums = make(map[string]*metadata.Enum)
    for _, e := range enums { r.enums[e.Name] = e }
}

func (r *Registry) GetEnum(name string) *metadata.Enum {
    r.mu.RLock(); defer r.mu.RUnlock()
    return r.enums[name]
}
```

**`internal/storage/ddl.go`**
- В `CreateTableSQL` для поля типа enum: `name TEXT CHECK (name IN ('v1','v2'))`
- В миграции `AlterTableSQL` обрабатывать изменение списка значений (DROP + ADD constraint)

**`internal/dsl/interpreter/env.go`**
- Добавить глобальный объект `Перечисления` со свойствами по именам enums
- `Перечисления.ВидКонтрагента.Поставщик` → возвращает строку

**`internal/ui/handlers.go`**
- В `formField` для типа enum рендерить `<select>` с `<option>` для каждого значения
- В `getEntity` ничего менять не надо — enum это поле

**`internal/ui/handlers.go`** (loadRefOptions)
- Добавить `loadEnumOptions` — собрать значения для каждого enum-поля

**`internal/api/handlers.go`** (createObject, updateObject)
- Перед `Upsert` валидировать что значение enum-поля в списке (или CHECK constraint в БД сделает это)

**`internal/launcher/configurator.go`**
- В `loadCfgData` загружать `enums` из `proj.Enums`
- В UI добавить секцию «Перечисления» со списком и значениями

**`internal/converter/parser1c/types.go`**
- Сейчас Enums помечаются как `SkippedItem`. Реализовать парсинг и запись в `enums/<имя>.yaml`

### Тесты

**`internal/metadata/yaml_test.go`** (новый)
```go
func TestLoadEnumFile(t *testing.T) {
    e, err := LoadEnumFile("testdata/enum.yaml")
    require.NoError(t, err)
    assert.Equal(t, "ВидКонтрагента", e.Name)
    assert.Equal(t, []string{"Поставщик", "Покупатель"}, e.Values)
}
```

**`internal/storage/ddl_test.go`**
```go
func TestCreateTableSQL_WithEnum(t *testing.T) {
    e := &Entity{Fields: []Field{{Name: "Вид", Type: "enum", EnumName: "ВидКонтрагента"}}}
    enums := map[string]*Enum{"ВидКонтрагента": {Values: []string{"Поставщик", "Покупатель"}}}
    sql := CreateTableSQL(e, enums)
    assert.Contains(t, sql, `вид TEXT CHECK (вид IN ('Поставщик','Покупатель'))`)
}
```

---

## Часть Б — Константы

### YAML формат

Один файл со всеми константами `constants/константы.yaml`:
```yaml
constants:
  - name: ОсновнаяВалюта
    type: string
    default: "RUB"
    label: Основная валюта учёта
  - name: ОсновнаяОрганизация
    type: reference:Контрагент
    label: Наша организация
  - name: НачалоУчёта
    type: date
    default: "2026-01-01"
```

### Доступ из DSL

```
Если this.Контрагент = Константы.ОсновнаяОрганизация Тогда
  Error("Нельзя выписывать счёт самой себе");
КонецЕсли;

Сообщить(Константы.ОсновнаяВалюта);  // "RUB"
```

### Хранилище

```sql
CREATE TABLE _constants (
    name TEXT PRIMARY KEY,
    value JSONB,
    updated_at TIMESTAMPTZ DEFAULT now()
);
```

JSONB позволяет хранить любой тип (строка, число, дата, UUID для reference).

### Изменения в коде

**`internal/metadata/types.go`**
```go
type Constant struct {
    Name      string
    Type      FieldType
    RefEntity string   // для reference
    Default   any
    Label     string
}
```

**`internal/metadata/yaml.go`**
- `LoadConstantsFile(path) ([]*Constant, error)`

**`internal/project/loader.go`**
```go
type Project struct {
    ...
    Constants []*metadata.Constant
}

func (p *Project) loadConstants() error {
    path := filepath.Join(p.Dir, "constants", "константы.yaml")
    if _, err := os.Stat(path); os.IsNotExist(err) { return nil }
    consts, err := metadata.LoadConstantsFile(path)
    if err != nil { return err }
    p.Constants = consts
    return nil
}
```

**`internal/storage/migrate.go`**
- При миграции создать таблицу `_constants`
- Для каждой константы из `proj.Constants`:
  - Если записи нет — INSERT с default
  - Если есть — оставить как есть (не перетирать пользовательское значение)

**`internal/storage/constants.go`** (новый)
```go
func GetConstant(ctx, pool, name) (any, error) { /* SELECT value FROM _constants */ }
func SetConstant(ctx, pool, name, value any) error { /* INSERT ... ON CONFLICT */ }
func ListConstants(ctx, pool) (map[string]any, error)
```

**`internal/runtime/registry.go`**
```go
type Registry struct {
    ...
    constants map[string]*metadata.Constant
}

func (r *Registry) GetConstant(name string) *metadata.Constant
```

**`internal/dsl/interpreter/env.go` + `interpreter.go`**
- Добавить глобальный объект `Константы`
- При обращении `Константы.Имя` — runtime читает из БД (через `storage.GetConstant`)
- Кеш в interpreter на время выполнения процедуры

**`internal/ui/handlers.go`**
- Страница `GET /ui/constants` — список + форма редактирования
- `POST /ui/constants` — сохранить значения

**`internal/ui/templates.go`**
- Добавить пункт «Константы» в nav (внизу)
- Шаблон `page-constants` с формой

**`internal/launcher/configurator.go`**
- Секция «Константы» в дереве — показывает имя, тип, default, текущее значение

### Тесты

**`internal/storage/constants_test.go`**
```go
func TestConstants_GetSet(t *testing.T) {
    db := connectTestDB(t)
    SetConstant(ctx, db.Pool(), "test", "value1")
    v, err := GetConstant(ctx, db.Pool(), "test")
    assert.Equal(t, "value1", v)
}
```

**Расширить `examples/trade/`**
- Добавить `constants/константы.yaml`:
  ```yaml
  constants:
    - name: НашаОрганизация
      type: reference:Контрагент
      label: Наша организация
    - name: ОсновнаяВалюта
      type: string
      default: "RUB"
  ```
- Добавить enum `enums/видконтрагента.yaml`
- В `catalogs/контрагент.yaml` добавить поле `Вид` типа `enum:ВидКонтрагента`
- В `src/реализациятоваров.os` использовать:
  ```
  Если this.Покупатель = Константы.НашаОрганизация Тогда
    Error("Покупатель — наша организация, недопустимо");
  КонецЕсли;
  ```

---

## Verification

1. `go test ./...` — все тесты проходят
2. Запустить `examples/trade`, открыть в браузере:
   - В форме Контрагента поле «Вид» — выпадающий список с 3 значениями
   - В nav появилась ссылка «Константы», на странице — форма с 2 полями
   - При сохранении документа Реализация с покупателем = НашаОрганизация — ошибка
3. В конфигураторе видны секции «Перечисления» и «Константы»
4. `QUICKSTART.md` — добавить разделы «Перечисления» и «Константы»

## Эстимейт

- **Перечисления**: 1.5 дня
  - Метаданные + загрузка: 0.3
  - Хранилище + миграция: 0.3
  - DSL доступ: 0.2
  - UI (форма select + конфигуратор): 0.4
  - Тесты + пример: 0.3
- **Константы**: 1 день
  - Метаданные + загрузка: 0.2
  - Таблица + миграция: 0.2
  - DSL доступ: 0.2
  - UI редактирование: 0.3
  - Тесты + пример: 0.1

**Итого: 2.5 дня.**
