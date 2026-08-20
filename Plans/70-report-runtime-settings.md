# Рантайм-настройки отчёта (СКД C3) · план реализации (план 70)

> **Для агентов-исполнителей:** РЕКОМЕНДУЕМЫЙ СУБ-НАВЫК: используйте
> `superpowers:subagent-driven-development` (свежий субагент на задачу) или
> `superpowers:executing-plans`. Шаги размечены чекбоксами `- [ ]`.
> Каждая правка движка — по TDD: сначала падающий тест, затем минимальная реализация.

**Цель:** дать пользователю менять структуру отчёта (группировки, показатели, колонки
кросс-таблицы, сортировку, отборы) прямо на форме отчёта — без правки конфигурации — и
сохранять эти настройки per-user/per-report. Это завершает «полноценную СКД» поверх
лайт-СКД (план 59) и вариантов компоновки (план 68, задача C2).

**Архитектура:** пользовательские настройки — это полноценный `report.Composition`,
построенный на базе активной компоновки (варианта) и применённый вместо
конфигурационного. Источник правок — панель «Настройки» на форме отчёта. Эффективная
компоновка вычисляется в `runReport`: `пользовательский override > выбранный вариант >
основной composition`. Сборщик `Composition` из полей формы выносится из конфигуратора
(`internal/launcher`) в нейтральный пакет и переиспользуется рантаймом. Per-user
хранение — JSON в служебной таблице `_settings` (как `llm.config`).

**Технологии:** Go (без CGo), `gopkg.in/yaml.v3` (конфигурация), `encoding/json`
(настройки в `_settings`), `shopspring/decimal` (деньги). Тесты — `go test`
(на Windows запускать через инструмент PowerShell). Хранилище — SQLite через
`ConnectSQLite` (БД-тесты без PostgreSQL).

---

## Контекст: что уже есть (читать перед началом)

**Готовый фундамент (плана 59 + 68):**
- `internal/report/report.go` — `Report` (поля `Composition *Composition`,
  `Variants []ReportVariant`), `Composition` (`Groupings`, `Columns`, `Measures`,
  `Totals`, `Detail`, `Sort`, `Conditional`, `Chart`, `DetailLink`, `DetailEntity`),
  `Measure` (`Field`, `Agg`, `Title`, `Align`, `Format`, `Expr`), метод
  `(*Report).ActiveComposition(name string) *Composition` (выбор варианта, fallback
  на основной).
- `internal/report/compose/compose.go` — `Compose(rows []Row, spec Composition, ev Evaluator) (*Result, error)`,
  `compose.go:cross.go` — `ComposeCross(...) (*CrossResult, error)`. `Row = map[string]any`.
- `internal/ui/handlers_reports.go` — `reportForm`, `reportRun`, `runReport`
  (вычисляет активную компоновку через `rep.ActiveComposition(r.FormValue("__variant"))`,
  ветка кросс при `len(comp.Columns)>0`), `reportExcel`. Текущий поток: `query.Compile`
  → `store.RunQuery` → `resolveUUIDsInReport` → `compose.Compose`/`ComposeCross` →
  `render "page-report"`.
- `internal/launcher/report_composition_form.go` — `parseCompositionForm(f url.Values) (*report.Composition, bool)`
  (собирает `Composition` из полей `comp.*`) и `applyReportComposition`/`setYAMLMapField`
  (точечная запись в YAML без потери прочих полей).
- `internal/storage/settings.go` — служебная key-value таблица `_settings`
  (`EnsureSettingsSchema`), паттерн upsert `INSERT ... ON CONFLICT (key) DO UPDATE`
  с `dialect.Placeholder(n)`; пример хранения целого JSON под ключом —
  `GetLLMConfig`/`SaveLLMConfig` (ключ `llm.config`).
- Текущий пользователь в обработчиках: `auth.UserFromContext(r.Context())` → `*auth.User`
  (поле `.Login`); `nil` для анонимной/одно­пользовательской сессии.
- `internal/ui/templates.go` — шаблон `page-report` (форма параметров + селектор
  вариантов `__variant`; блоки графика/данных), FuncMap (`reportParamQuery`,
  `variantQuery`, `t`, `lower`).
- `internal/configcheck/check.go` — `CheckReportComposition` (замыкание `checkComp`
  валидирует одну `Composition`; вызывается для основного и каждого варианта).

**Команды:**
- Тесты: `go test ./internal/report/... ./internal/ui/... ./internal/storage/... ./internal/launcher/... ./internal/configcheck/...`
- Сборка (остановив сервер): `taskkill /IM onebase.exe /F` → `go build -o onebase.exe ./cmd/onebase`
- Проверка конфигурации: `./onebase.exe check --project <dir>`
- Коммиты — `тип(scope): описание` по-русски, в конце `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.

**Дисциплина:** каждая задача — отдельный коммит. Ядро (`compose`, `report`, `storage`)
— строго TDD (тест без БД, кроме storage — там SQLite через `ConnectSQLite`). После
каждого этапа — сборка + `check` + ручная проверка.

---

## File Structure (что создаём/трогаем)

- `internal/report/compform/compform.go` — **создать.** Нейтральный сборщик
  `Composition` из `url.Values` (вынос `parseCompositionForm`). Зависит только от
  `internal/report` + stdlib. Импортируется и `launcher`, и `ui`.
- `internal/report/compform/compform_test.go` — **создать.** Юнит-тесты сборщика.
- `internal/launcher/report_composition_form.go` — **изменить.** `parseCompositionForm`
  становится тонкой обёрткой над `compform.Parse` (обратная совместимость, тесты не трогаем).
- `internal/report/settings.go` — **создать.** Тип `UserReportSettings` + JSON
  (де)сериализация.
- `internal/report/settings_test.go` — **создать.**
- `internal/storage/settings.go` — **изменить.** `GetReportUserSettings` /
  `SaveReportUserSettings` / `DeleteReportUserSettings` (JSON в `_settings`).
- `internal/storage/settings_report_test.go` — **создать.** SQLite round-trip.
- `internal/report/compose/filter.go` — **создать.** Чистая пост-запросная фильтрация
  строк по отборам (`ApplyFilters`).
- `internal/report/compose/filter_test.go` — **создать.**
- `internal/ui/handlers_reports.go` — **изменить.** Эффективная компоновка
  (override > вариант > основной), фильтрация строк, проброс в Excel.
- `internal/ui/report_settings.go` — **создать.** Хелперы: чтение настроек из формы
  (`compform.Parse`), сборка списка доступных полей из колонок результата,
  загрузка/сохранение per-user, рендер-данные панели.
- `internal/ui/report_settings_test.go` — **создать.**
- `internal/ui/templates.go` — **изменить.** Панель «Настройки» в `page-report`
  (поля, группировки/показатели/колонки/сортировка/отборы, кнопки
  Применить/Сохранить/Стандартные).
- `internal/i18n/locales/en.json` — **изменить.** Переводы новых t-ключей.

---

# Этап A — Фундамент: общий сборщик + хранение

## Task A1: Вынос сборщика `Composition` из формы в `internal/report/compform`

Цель — один сборщик `Composition` из `url.Values`, используемый и конфигуратором, и
рантаймом. Поведение идентично текущему `parseCompositionForm` (контракт: нет
`comp.present` → `(nil,false)`; есть, но пусто → `(nil,true)`; иначе `(c,true)`).

**Files:**
- Create: `internal/report/compform/compform.go`
- Create: `internal/report/compform/compform_test.go`
- Modify: `internal/launcher/report_composition_form.go`

- [ ] **Step 1 (тест):** Скопировать кейсы из
  `internal/launcher/report_composition_form_test.go::TestParseCompositionForm` в
  `compform_test.go`, заменив вызов на `compform.Parse(f)`:
```go
func TestParse(t *testing.T) {
    f := url.Values{}
    f.Set("comp.present", "1")
    f.Set("comp.grouping.0", "Менеджер")
    f.Set("comp.measure.0.field", "Сумма")
    f.Set("comp.measure.0.agg", "sum")
    c, present := compform.Parse(f)
    if !present || c == nil || len(c.Groupings) != 1 || c.Measures[0].Field != "Сумма" {
        t.Fatalf("parse: present=%v c=%+v", present, c)
    }
}
```
- [ ] **Step 2:** Запустить — FAIL (пакета нет). `go test ./internal/report/compform/ -run TestParse -v`
- [ ] **Step 3:** Создать `compform.go`: `package compform`, функция
  `Parse(f url.Values) (*report.Composition, bool)` — перенести тело
  `parseCompositionForm` дословно (поля `comp.*`, очистка `#000000`/`#ffffff`,
  правило «пусто всё → (nil,true)»). Импорт `net/url`, `strconv`, `strings`,
  `internal/report`.
- [ ] **Step 4:** Запустить — PASS.
- [ ] **Step 5:** В `internal/launcher/report_composition_form.go` заменить тело
  `parseCompositionForm` на `return compform.Parse(f)` (обёртка ради существующих
  вызовов/тестов). Удалить осиротевшие импорты.
- [ ] **Step 6:** Прогнать `go test ./internal/launcher/ ./internal/report/...` — PASS
  (старые тесты конфигуратора зелёные через обёртку).
- [ ] **Step 7:** Commit: `refactor(report): вынос сборщика Composition из формы в compform`

## Task A2: Тип `UserReportSettings` + JSON-сериализация

Пользовательская настройка — это выбранный вариант (база) + эффективный `Composition`
+ список отборов. Сохраняется как один JSON.

**Files:**
- Create: `internal/report/settings.go`
- Create: `internal/report/settings_test.go`

**Дизайн типов (`settings.go`):**
```go
// Filter — пользовательский отбор по полю результата запроса (применяется к строкам
// до компоновки). Op: eq|ne|gt|ge|lt|le|contains.
type Filter struct {
    Field string `json:"field"`
    Op    string `json:"op"`
    Value string `json:"value"`
}

// UserReportSettings — рантайм-настройки отчёта конкретного пользователя.
// Variant — имя варианта-базы (пусто = основной). Composition — эффективная
// компоновка (полная, не дельта). Filters — пользовательские отборы строк.
type UserReportSettings struct {
    Variant     string       `json:"variant"`
    Composition *Composition `json:"composition,omitempty"`
    Filters     []Filter     `json:"filters,omitempty"`
}

func (s *UserReportSettings) JSON() (string, error) { … }      // json.Marshal
func ParseUserSettings(raw string) (*UserReportSettings, error) // json.Unmarshal; "" → (nil,nil)
```

- [ ] **Step 1 (тест):** В `settings_test.go`: собрать `UserReportSettings` с вариантом,
  composition (1 группировка, 1 показатель) и одним фильтром → `JSON()` → `ParseUserSettings`
  → поля совпадают. Отдельный кейс: `ParseUserSettings("")` → `(nil, nil)` без ошибки.
- [ ] **Step 2:** Запустить — FAIL. `go test ./internal/report/ -run TestUserSettings -v`
- [ ] **Step 3:** Реализовать типы + `JSON()`/`ParseUserSettings` в `settings.go`.
- [ ] **Step 4:** Запустить — PASS.
- [ ] **Step 5:** Commit: `feat(report): тип UserReportSettings (рантайм-настройки + отборы)`

## Task A3: Хранение настроек per-user в `_settings`

Ключ — `report.settings.<reportName>.<userLogin>` (для анонима `userLogin` = `""`).
Значение — JSON `UserReportSettings`. Паттерн — как `GetLLMConfig`/`SaveLLMConfig`.

**Files:**
- Modify: `internal/storage/settings.go`
- Create: `internal/storage/settings_report_test.go`

**Сигнатуры:**
```go
func reportSettingsKey(report, user string) string { return "report.settings." + report + "." + user }
func (db *DB) GetReportUserSettings(ctx context.Context, report, user string) (string, error) // raw JSON; нет ключа → ("",nil)
func (db *DB) SaveReportUserSettings(ctx context.Context, report, user, raw string) error      // upsert
func (db *DB) DeleteReportUserSettings(ctx context.Context, report, user string) error          // сброс к стандартным
```

- [ ] **Step 1 (тест):** В `settings_report_test.go`: `ConnectSQLite(ctx, tmpfile)` →
  `EnsureSettingsSchema` → `Get` пусто (`""`) → `Save(report,user,`{"variant":"X"}`)` →
  `Get` возвращает тот же JSON → `Delete` → `Get` снова пусто. Проверить, что настройки
  разных user не пересекаются (ключи `report.settings.R.alice` vs `…bob`).
- [ ] **Step 2:** Запустить — FAIL. `go test ./internal/storage/ -run TestReportUserSettings -v`
- [ ] **Step 3:** Реализовать три метода + `reportSettingsKey`. `Get`: `SELECT value …`,
  ошибку «нет строк» трактовать как `("",nil)`. `Save`: `EnsureSettingsSchema` + upsert.
  `Delete`: `DELETE FROM _settings WHERE key = <ph>`.
- [ ] **Step 4:** Запустить — PASS.
- [ ] **Step 5:** Commit: `feat(storage): per-user хранение настроек отчёта в _settings`

### Проверка этапа A
- [ ] `go test ./internal/report/... ./internal/storage/... ./internal/launcher/...` — зелёное.
- [ ] `go build ./...` чист.

---

# Этап B — Рантайм-применение

## Task B1: Эффективная компоновка в `runReport`

Источник правок — поле формы `__settings` (JSON `UserReportSettings`, пишется панелью).
Приоритет: `override.Composition` (если есть) → `rep.ActiveComposition(variant)`.
Базой панели служит активный вариант, поэтому override уже учитывает выбор варианта.

**Files:**
- Modify: `internal/ui/handlers_reports.go`
- Create: `internal/ui/report_settings.go` (хелпер чтения настроек из запроса)
- Create: `internal/ui/report_settings_test.go`

- [ ] **Step 1 (тест):** В `report_settings_test.go`: `effectiveComposition(rep, settings)`
  — чистый хелпер: при `settings.Composition != nil` возвращает его; иначе
  `rep.ActiveComposition(settings.Variant)`; `settings==nil` → `rep.Composition`.
  Три кейса.
- [ ] **Step 2:** Запустить — FAIL. `go test ./internal/ui/ -run TestEffectiveComposition -v`
- [ ] **Step 3:** В `report_settings.go`:
```go
func readReportSettings(r *http.Request) *reportpkg.UserReportSettings {
    raw := r.FormValue("__settings")
    if raw == "" { return nil }
    s, err := reportpkg.ParseUserSettings(raw)
    if err != nil { return nil }
    return s
}
func effectiveComposition(rep *reportpkg.Report, s *reportpkg.UserReportSettings) *reportpkg.Composition {
    if s != nil && s.Composition != nil { return s.Composition }
    if s != nil { return rep.ActiveComposition(s.Variant) }
    return rep.ActiveComposition("")
}
```
- [ ] **Step 4:** Запустить — PASS.
- [ ] **Step 5:** В `runReport`: заменить `comp := rep.ActiveComposition(variant)` на
  `settings := readReportSettings(r)`, `comp := effectiveComposition(rep, settings)`.
  `variant` для шаблона взять как `r.FormValue("__variant")` (как сейчас). Передать в
  шаблон `"UserSettings": settings` (для предзаполнения панели).
- [ ] **Step 6:** Прогнать `./internal/ui/...` — PASS (существующие тесты не сломаны:
  без `__settings` поведение прежнее).
- [ ] **Step 7:** Commit: `feat(report): эффективная компоновка из рантайм-настроек пользователя`

## Task B2: Пост-запросная фильтрация строк по отборам

Отборы применяются к строкам результата ДО компоновки (не трогаем SQL — безопаснее и
не зависит от диалекта). Сравнение строковое/числовое по типу значения.

**Files:**
- Create: `internal/report/compose/filter.go`
- Create: `internal/report/compose/filter_test.go`
- Modify: `internal/ui/handlers_reports.go` (вызов перед `Compose`/`ComposeCross`)

**Контракт:**
```go
// ApplyFilters возвращает подмножество rows, удовлетворяющих всем f (AND).
// Имена полей сравниваются регистронезависимо (как alignRowKeys). Числа сравниваются
// через decimal, строки — по содержимому; contains — подстрока без регистра.
func ApplyFilters(rows []Row, f []report.Filter) []Row
```

- [ ] **Step 1 (тест):** В `filter_test.go`: 3 строки `{Товар, Сумма}`; фильтр
  `{Field:"Сумма", Op:"gt", Value:"100"}` → остаются строки с суммой > 100;
  `{Field:"Товар", Op:"contains", Value:"ябл"}` → подстрока без регистра; пустой
  список фильтров → все строки.
- [ ] **Step 2:** Запустить — FAIL. `go test ./internal/report/compose/ -run TestApplyFilters -v`
- [ ] **Step 3:** Реализовать `ApplyFilters` + хелпер `matchFilter(row, f)`:
  достать значение по полю (регистронезависимо), привести к `decimal` при числовом
  сравнении (`toDecimal` уже есть в пакете), иначе строковое сравнение; `contains` —
  `strings.Contains(strings.ToLower(...), …)`.
- [ ] **Step 4:** Запустить — PASS. Добавить кейсы: неизвестное поле (строка не проходит
  числовой фильтр → исключить), неизвестный `Op` (игнорировать фильтр — пропускать строку).
- [ ] **Step 5:** В `runReport`/`reportExcel`: после `resolveUUIDsInReport`, если
  `settings != nil && len(settings.Filters) > 0` → `rows = compose.ApplyFilters(rows, settings.Filters)`.
- [ ] **Step 6:** Прогнать `./internal/report/compose/ ./internal/ui/...` — PASS.
- [ ] **Step 7:** Commit: `feat(report): пост-запросные отборы строк в рантайм-настройках`

## Task B3: Excel учитывает рантайм-настройки

**Files:**
- Modify: `internal/ui/handlers_reports.go` (`reportExcel`)

- [ ] **Step 1:** В `reportExcel` заменить `comp := rep.ActiveComposition(r.URL.Query().Get("__variant"))`
  на чтение настроек из query (`readReportSettings` уже использует `r.FormValue`, который
  читает и GET-query) + `effectiveComposition`. Применить `ApplyFilters` к строкам.
- [ ] **Step 2:** Ручная проверка: выгрузка отражает применённые настройки/отборы.
- [ ] **Step 3:** Commit: `feat(report): экспорт в Excel учитывает рантайм-настройки`

### Проверка этапа B
- [ ] Все тесты зелёные; `go build ./...`.
- [ ] С `__settings`-параметром (вручную в URL) отчёт строится по override; без него —
  как раньше.

---

# Этап C — UI: панель «Настройки» на форме отчёта

> UI поэтапно. Доступные поля для группировок/показателей/отборов берём из колонок
> результата запроса (`cols` из `RunQuery`) — их и передаём в шаблон. Панель пишет
> скрытое поле `__settings` (JSON) и сабмитит форму отчёта.

## Task C1: Панель полей — группировки и показатели

**Files:**
- Modify: `internal/ui/report_settings.go` (сбор доступных полей, рендер-данные)
- Modify: `internal/ui/handlers_reports.go` (передать `cols` и текущие настройки в шаблон)
- Modify: `internal/ui/templates.go` (блок-`<details>` «Настройки» в `page-report`)
- Modify: `internal/i18n/locales/en.json`
- Create/Modify: `internal/ui/report_settings_test.go` (рендер панели через `tmpl.ExecuteTemplate`)

- [ ] **Step 1 (тест):** Рендер `page-report` с `"ReportCols": []string{"Товар","Сумма"}`,
  `"UserSettings": &reportpkg.UserReportSettings{Composition: &reportpkg.Composition{
  Groupings:[]string{"Товар"}, Measures:[]reportpkg.Measure{{Field:"Сумма",Agg:"sum"}}}}`
  → HTML содержит блок настроек (маркер `data-block="settings"`), чекбоксы полей
  `Товар`/`Сумма`, гидден `name="__settings"`. (Паттерн теста — `TestReportVariantsSelect`.)
- [ ] **Step 2:** Запустить — FAIL.
- [ ] **Step 3:** В `templates.go` добавить `<details class="card report-block"
  data-block="settings"><summary>{{t .Lang "Настройка отчёта"}}</summary>…</details>`:
  для каждой колонки — строка с чекбоксом «группировка» и «показатель»; скрытое
  `<input type="hidden" name="__settings">`; JS собирает JSON из чекбоксов в это поле
  перед сабмитом. Показывать только при `{{if .ReportCols}}`.
- [ ] **Step 4:** В `handlers_reports.go` передавать `"ReportCols": cols` (из `RunQuery`)
  и `"UserSettings": settings` во все ветки рендера данных. В `en.json` добавить
  `Настройка отчёта`, `Группировка`, `Показатель`, `Отбор`, `Применить`, `Сохранить`,
  `Стандартные настройки`.
- [ ] **Step 5:** Запустить — PASS; прогнать `./internal/ui/...`.
- [ ] **Step 6:** Commit: `feat(report): панель рантайм-настроек — группировки и показатели`

## Task C2: Отборы в панели

**Files:**
- Modify: `internal/ui/templates.go` (строки отбора: поле/оператор/значение + «добавить»)
- Modify: `internal/ui/report_settings_test.go`

- [ ] **Step 1 (тест):** Рендер с `UserSettings.Filters=[{Сумма,gt,100}]` → HTML
  содержит select поля, select оператора (`>`/`<`/`содержит`…) и значение `100`.
- [ ] **Step 2:** FAIL → добавить разметку строк отбора (повторяемые, кнопка «+ отбор»);
  JS включает их в JSON `__settings.filters` → PASS.
- [ ] **Step 3:** Commit: `feat(report): отборы в панели рантайм-настроек`

## Task C3: Кнопки «Применить» / «Сохранить» / «Стандартные»

- «Применить» — сабмит формы (строит отчёт по `__settings`, без записи в БД).
- «Сохранить» — POST на `/ui/report/<name>/settings/save` (записать per-user).
- «Стандартные» — POST на `/ui/report/<name>/settings/reset` (удалить per-user).

**Files:**
- Modify: `internal/ui/templates.go` (3 кнопки в панели)
- Modify: `internal/ui/handlers_reports.go` (+ маршруты save/reset)
- Modify: `internal/ui/server.go` (регистрация маршрутов, рядом с `report/{name}` )
- Modify: `internal/ui/report_settings_test.go`

- [ ] **Step 1 (тест):** `saveReportSettings`/`resetReportSettings` — юнит на хелпере:
  для пользователя `alice` сохранить `__settings`, прочитать обратно через
  `GetReportUserSettings`; reset → пусто. (БД — SQLite; поднять минимальный `Server`
  с `store` либо тестировать через `store` напрямую, без HTTP.)
- [ ] **Step 2:** FAIL → реализовать обработчики: `user := ""; if u := auth.UserFromContext(r.Context()); u != nil { user = u.Login }`;
  `store.SaveReportUserSettings(ctx, rep.Name, user, r.FormValue("__settings"))` /
  `DeleteReportUserSettings`. Редирект обратно на форму отчёта.
- [ ] **Step 3:** Зарегистрировать маршруты в `server.go`. PASS.
- [ ] **Step 4:** Commit: `feat(report): сохранение и сброс рантайм-настроек отчёта`

### Проверка этапа C
- [ ] Панель «Настройки» рендерится на форме отчёта; чекбоксы/отборы пишут `__settings`.
- [ ] «Применить» перестраивает отчёт; «Сохранить»/«Стандартные» не падают.

---

# Этап D — Персистентность per-user

## Task D1: Загрузка сохранённых настроек при открытии

При GET-открытии отчёта без явного `__settings` подставить сохранённые настройки
текущего пользователя (если есть), чтобы отчёт открывался в привычном пользователю виде.

**Files:**
- Modify: `internal/ui/handlers_reports.go` (`reportForm`, `runReport`)
- Modify: `internal/ui/report_settings_test.go`

- [ ] **Step 1 (тест):** хелпер `loadUserSettings(ctx, store, report, user) *UserReportSettings`
  — для `alice` с сохранёнными настройками возвращает их; для `bob` без настроек — `nil`.
- [ ] **Step 2:** FAIL → реализовать (через `GetReportUserSettings` + `ParseUserSettings`).
- [ ] **Step 3:** В `reportForm`/`runReport`: если в запросе нет `__settings`, но есть
  сохранённые для пользователя — использовать их (`settings = loadUserSettings(...)`).
  Явный `__settings` из формы имеет приоритет (текущее редактирование).
- [ ] **Step 4:** PASS; прогнать `./internal/ui/...`.
- [ ] **Step 5:** Commit: `feat(report): автозагрузка сохранённых рантайм-настроек отчёта`

## Task D2: Индикатор и сброс «изменённого» состояния

- [ ] **Step 1:** В панели показывать пометку «настройки изменены/сохранены» и держать
  кнопку «Стандартные настройки» активной только при наличии сохранённых/применённых
  настроек (флаг в рендер-данные из `loadUserSettings != nil` / `settings != nil`).
- [ ] **Step 2:** Ручная проверка цикла: применить → сохранить → переоткрыть (настройки
  применились) → стандартные (вернулось к конфигурации).
- [ ] **Step 3:** Commit: `feat(report): индикатор и сброс пользовательских настроек отчёта`

### Проверка этапа D
- [ ] Сохранённые настройки переживают переоткрытие отчёта и привязаны к пользователю.
- [ ] «Стандартные настройки» возвращают конфигурационный вид.
- [ ] Анонимная сессия (`user==""`) работает (настройки под пустым ключом, не мешают
  именованным пользователям).

---

## Совместимость и принципы (на всех этапах)
- Без `__settings` и без сохранённых настроек поведение отчёта прежнее (полная обратная
  совместимость с планами 59/68).
- Пользовательские настройки **не** меняют конфигурацию (YAML) — только строку в
  `_settings`. Конфигурация остаётся источником стандартного вида.
- Ядро (`compose.ApplyFilters`, `report.UserReportSettings`, `compform.Parse`) — чистые
  функции/типы с тестами без БД; storage — SQLite-тест.
- Деньги/числа в отборах — `decimal`, не `float64`.
- Имена полей в отборах/настройках сравниваются регистронезависимо (колонки результата
  — нижний регистр); переиспользовать подход `alignRowKeys`.

## Каталог возможностей (`docs/features.md`)
- [ ] После этапа D добавить секцию «Настройка отчёта пользователем» со `status: testing`,
  `since: build-NNN`, `date`, кратким «что это и как попробовать» (панель «Настройки» на
  форме отчёта: группировки/показатели/отборы, Применить/Сохранить/Стандартные).

## Self-review (выполнено при составлении)
- **Покрытие спецификации (Task C3 плана 68):** хранение per-user (`_settings`, A3/D);
  UI-панель на форме отчёта (C1–C3); переиспользование сборщика `Composition` из формы
  (A1 — вынос `parseCompositionForm` в `compform`, используется рантаймом). Добавлены
  отборы (B2/C2) и автозагрузка (D1) — естественная часть «полноценной СКД».
- **Типы согласованы между задачами:** `UserReportSettings{Variant, Composition, Filters}`
  (A2) используется в B1/B3/C/D; `report.Filter` (A2) — в `compose.ApplyFilters` (B2) и
  панели (C2); `compform.Parse` (A1) — в `report_settings.go`. Методы storage
  (`Get/Save/DeleteReportUserSettings`) — в A3 и переиспользуются в C3/D1.
- **Без плейсхолдеров:** каждый шаг ядра содержит сигнатуры/тестовые утверждения; UI-шаги
  указывают точный маркёр (`data-block="settings"`, `name="__settings"`) и точки
  интеграции (`runReport`, `reportExcel`, `server.go`).
- **Зависимости/порядок:** A (фундамент) → B (применение) → C (UI) → D (персистентность).
  B1 зависит от A2; C от B; D от A3+C3.
