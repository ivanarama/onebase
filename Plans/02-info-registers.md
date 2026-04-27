# Этап 2 — Регистры сведений

## Контекст

Сейчас в onebase только **регистры накопления** — они суммируют значения (приход/расход → остаток). Это не подходит для информации, которая просто **хранится по ключу и перезаписывается**:
- Курсы валют по дате
- Цены номенклатуры
- Назначение сотрудников на должности
- Параметры объектов (срок годности, страна происхождения)

Эта фича добавляет **регистры сведений** — второй из двух базовых типов регистров 1С. Может быть периодическим (с историей) или непериодическим.

---

## YAML формат

### Непериодический

`inforegs/ценыноменклатуры.yaml`:
```yaml
name: ЦеныНоменклатуры
periodic: false
dimensions:
  - name: Номенклатура
    type: reference:Номенклатура
  - name: ВидЦены
    type: enum:ВидЦены     # из этапа 1
resources:
  - name: Цена
    type: number
  - name: Валюта
    type: string
```

### Периодический

`inforegs/курсывалют.yaml`:
```yaml
name: КурсыВалют
periodic: true
dimensions:
  - name: Валюта
    type: string
resources:
  - name: Курс
    type: number
  - name: Кратность
    type: number
```

---

## Использование в DSL

### Чтение последнего значения (периодический)

```
курс = РегистрыСведений.КурсыВалют.ПолучитьПоследнее(this.Дата, "USD");
Если курс.Курс > 0 Тогда
  суммаВРублях = this.Сумма * курс.Курс;
КонецЕсли;
```

### Чтение по ключу (непериодический)

```
цена = РегистрыСведений.ЦеныНоменклатуры.Получить(Строка.Номенклатура, "Розница");
Строка.Цена = цена.Цена;
```

### Запись из документа

```
Запись = РегистрыСведений.КурсыВалют.СоздатьЗапись();
Запись.Период = this.Дата;
Запись.Валюта = this.Валюта;
Запись.Курс = this.Курс;
Запись.Кратность = 1;
Запись.Записать();
```

---

## Хранилище

### Непериодический регистр

```sql
CREATE TABLE info_<имя> (
    <dim1>, <dim2>, ...,           -- измерения
    <res1>, <res2>, ...,           -- ресурсы
    updated_at TIMESTAMPTZ,
    PRIMARY KEY (<dim1>, <dim2>, ...)
);
```

### Периодический

```sql
CREATE TABLE info_<имя> (
    period TIMESTAMPTZ NOT NULL,
    <dim1>, <dim2>, ...,
    <res1>, <res2>, ...,
    PRIMARY KEY (period, <dim1>, <dim2>, ...)
);
CREATE INDEX idx_<имя>_dims ON info_<имя> (<dim1>, <dim2>);  -- для ПолучитьПоследнее
```

`ПолучитьПоследнее(дата, ...измерения)` → `SELECT * FROM info_<имя> WHERE <dim1>=$1 AND ... AND period <= $дата ORDER BY period DESC LIMIT 1`.

---

## Изменения в коде

### Метаданные

**`internal/metadata/types.go`**
```go
type InfoRegister struct {
    Name       string
    Periodic   bool
    Dimensions []Field
    Resources  []Field
}
```

**`internal/metadata/yaml.go`**
- `LoadInfoRegisterFile(path) (*InfoRegister, error)` (аналогично существующей `LoadRegisterFile`)

### Загрузка проекта

**`internal/project/loader.go`**
```go
type Project struct {
    ...
    InfoRegisters []*metadata.InfoRegister
}

func (p *Project) loadInfoRegisters() error {
    dir := filepath.Join(p.Dir, "inforegs")
    items, err := os.ReadDir(dir)
    if os.IsNotExist(err) { return nil }
    for _, item := range items {
        if !strings.HasSuffix(item.Name(), ".yaml") { continue }
        ir, err := metadata.LoadInfoRegisterFile(filepath.Join(dir, item.Name()))
        if err != nil { return err }
        p.InfoRegisters = append(p.InfoRegisters, ir)
    }
    return nil
}
```

### Хранилище

**`internal/storage/inforeg.go`** (новый файл)
```go
// MigrateInfoRegisters создаёт таблицы для всех info-регистров
func (db *DB) MigrateInfoRegisters(ctx, regs []*metadata.InfoRegister) error

// Set записывает значение по ключу (для непериодических)
// или добавляет/обновляет запись на дату (для периодических)
func (db *DB) InfoRegSet(ctx, name string, key map[string]any, value map[string]any, period *time.Time) error

// Get возвращает значение по ключу (непериодический)
func (db *DB) InfoRegGet(ctx, name string, key map[string]any) (map[string]any, error)

// GetLast — последнее значение на дату для периодического
func (db *DB) InfoRegGetLast(ctx, name string, key map[string]any, onDate time.Time) (map[string]any, error)

// List — все записи (для UI просмотра)
func (db *DB) InfoRegList(ctx, name string, filter map[string]any, params ListParams) ([]map[string]any, error)

// Delete — удалить запись (только для непериодических, для периодических — через период)
func (db *DB) InfoRegDelete(ctx, name string, key map[string]any) error
```

### Runtime

**`internal/runtime/registry.go`**
```go
type Registry struct {
    ...
    inforegs map[string]*metadata.InfoRegister
}
func (r *Registry) GetInfoRegister(name string) *metadata.InfoRegister
func (r *Registry) InfoRegisters() []*metadata.InfoRegister
```

### DSL

**`internal/dsl/interpreter/env.go`**
- Добавить глобальный объект `РегистрыСведений`
- Каждое свойство — это регистр с методами `Получить`, `ПолучитьПоследнее`, `СоздатьЗапись`

**`internal/dsl/interpreter/builtins.go`**
- Реализация методов через интерфейс `MethodCallable`:
  ```go
  type infoRegProxy struct {
      name     string
      def      *metadata.InfoRegister
      store    *storage.DB
      ctx      context.Context
  }
  func (p *infoRegProxy) CallMethod(method string, args []any) any {
      switch method {
      case "Получить", "Get":
          // args соответствуют dimensions, возвращает map
          return p.store.InfoRegGet(p.ctx, p.name, key)
      case "ПолучитьПоследнее", "GetLast":
          // первый arg — дата, далее dimensions
          ...
      case "СоздатьЗапись", "NewRecord":
          return &infoRegRecord{...}
      }
  }
  ```

**Где runtime получает доступ к storage**: интерпретатор уже принимает `extraVars`, можно передать через них объект `Сервис` или прокинуть напрямую через интерфейс.

### UI

**`internal/ui/server.go`** (Mount)
```go
r.Get("/ui/inforeg/{name}", s.infoRegList)
r.Get("/ui/inforeg/{name}/new", s.infoRegForm)
r.Post("/ui/inforeg/{name}/new", s.infoRegSubmit)
r.Get("/ui/inforeg/{name}/{key}", s.infoRegEdit)
r.Post("/ui/inforeg/{name}/delete", s.infoRegDelete)
```

**`internal/ui/handlers.go`**
- Страница списка с фильтрами по измерениям
- Форма редактирования с полями для всех dimensions + resources + (Период для периодических)

**`internal/ui/server.go` (buildNav)**
- Добавить секцию «Регистры сведений» в nav (отдельно от регистров накопления)

### REST API

**`internal/api/server.go` + `handlers.go`**
- `GET /inforegs/{name}` — список (с фильтрами)
- `GET /inforegs/{name}/{key}` — получить запись
- `PUT /inforegs/{name}` — set (body: dimensions + resources + period)
- `DELETE /inforegs/{name}` — delete by key

### Конфигуратор

**`internal/launcher/configurator.go`**
- В `cfgData` добавить `InfoRegisters []cfgInfoRegister`
- Загрузка через `proj.InfoRegisters`
- Отображение в дереве в отдельной секции

**`internal/launcher/configurator_tmpl.go`**
- Иконка 📄 для непериодических, ⏱ для периодических
- В правой панели — измерения, ресурсы, флаг periodic

---

## Тесты

### `internal/storage/inforeg_test.go` (integration)

```go
func TestInfoReg_NonPeriodic(t *testing.T) {
    // Set value
    db.InfoRegSet(ctx, "ЦеныНоменклатуры", 
        map[string]any{"Номенклатура": uuid1, "ВидЦены": "Розница"},
        map[string]any{"Цена": 100, "Валюта": "RUB"},
        nil)
    
    // Get value
    rec, _ := db.InfoRegGet(ctx, "ЦеныНоменклатуры", 
        map[string]any{"Номенклатура": uuid1, "ВидЦены": "Розница"})
    require.Equal(t, 100.0, rec["Цена"])
    
    // Update
    db.InfoRegSet(ctx, ..., map[string]any{"Цена": 150}, nil)
    rec, _ := db.InfoRegGet(ctx, ...)
    require.Equal(t, 150.0, rec["Цена"])
}

func TestInfoReg_Periodic_GetLast(t *testing.T) {
    // 3 записи на разные даты
    db.InfoRegSet(ctx, "КурсыВалют", {"Валюта": "USD"}, {"Курс": 90}, &date1)
    db.InfoRegSet(ctx, "КурсыВалют", {"Валюта": "USD"}, {"Курс": 95}, &date2)
    db.InfoRegSet(ctx, "КурсыВалют", {"Валюта": "USD"}, {"Курс": 100}, &date3)
    
    // GetLast по date2.5
    rec, _ := db.InfoRegGetLast(ctx, "КурсыВалют", 
        map[string]any{"Валюта": "USD"}, date_between_2_and_3)
    require.Equal(t, 95.0, rec["Курс"])
}
```

### Расширить `examples/trade/`

- `inforegs/курсывалют.yaml` (периодический)
- `inforegs/ценыноменклатуры.yaml` (непериодический)
- В `documents/реализациятоваров.yaml` — добавить поле `Валюта` (string)
- В `src/реализациятоваров.os`:
  ```
  Если this.Валюта <> "RUB" Тогда
    курс = РегистрыСведений.КурсыВалют.ПолучитьПоследнее(this.Дата, this.Валюта);
    Если курс = Неопределено Тогда
      Error("Курс на " + Формат(this.Дата) + " не задан для " + this.Валюта);
    КонецЕсли;
    Для Каждого Строка Из this.Товары Цикл
      Строка.СуммаРуб = Строка.Сумма * курс.Курс;
    КонецЦикла;
  КонецЕсли;
  ```
- В UI должна появиться возможность открыть «Курсы валют» через nav и руками вбить курс

---

## Verification

1. `go test ./...` — юнит-тесты проходят, integration-тесты с TEST_DATABASE_URL
2. Открыть `examples/trade` в лаунчере:
   - В nav появились пункты «Курсы валют», «Цены номенклатуры»
   - Можно создать запись (для периодического — с датой)
   - При создании Реализации в валюте USD без курса — ошибка с понятным текстом
3. В конфигураторе → Регистры сведений видны оба регистра, видно структуру
4. `QUICKSTART.md` — добавить раздел «Регистры сведений»

## Эстимейт

- Метаданные + загрузка: 0.3 дня
- Хранилище (миграция, CRUD, GetLast): 1 день
- DSL (proxy объекты, методы): 1 день
- UI (список, форма, фильтры): 1 день
- REST API: 0.3 дня
- Конфигуратор: 0.2 дня
- Тесты + пример trade: 1 день

**Итого: 4.8 дня (~ 1 неделя).**
