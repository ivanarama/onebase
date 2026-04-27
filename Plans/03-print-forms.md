# Этап 3 — Печатные формы

## Контекст

Без печатных форм платформа не воспринимается как «настоящая 1С». Бухгалтер должен иметь возможность распечатать накладную, счёт-фактуру, акт. Сейчас в onebase нет этого функционала.

**Цель**: добавить простой механизм декларативных печатных форм (YAML-шаблон → HTML/PDF), привязанных к документам или справочникам.

**Подход**: начинаем с HTML-печати (через `window.print()` с CSS `@media print`). PDF-генерация — опциональный этап через subprocess `wkhtmltopdf` или `chromedp`.

---

## YAML формат шаблона

`printforms/счёт.yaml`:
```yaml
name: СчётНаОплату
document: Реализация            # привязка к документу
title: "Счёт на оплату № {{Номер}} от {{Дата | date}}"

# Шапка (markdown / простой HTML)
header: |
  ## ООО "{{Константы.НашаОрганизация.Наименование}}"
  ИНН/КПП: {{Константы.НашаОрганизация.ИНН}}
  
  **Покупатель**: {{Покупатель.Наименование}}, ИНН {{Покупатель.ИНН}}
  
  **Дата документа**: {{Дата | date}}

# Главная таблица — строки табличной части
table:
  source: Товары
  columns:
    - field: "@row"           # номер строки
      label: "№"
      width: 40px
    - field: Товар.Наименование
      label: Товар
    - field: Количество
      label: Кол-во
      align: right
    - field: Цена
      label: Цена
      align: right
      format: "number:2"
    - field: Сумма
      label: Сумма
      align: right
      format: "number:2"
  totals:
    - field: Количество
      sum: true
    - field: Сумма
      sum: true
      label: "Итого"

# Подвал
footer: |
  **Всего к оплате**: {{Итог.Сумма | number:2}} {{Константы.ОсновнаяВалюта}}
  
  ___________________________________
  
  Руководитель: __________________ /__________________/
  
  МП
```

### Поддерживаемые форматтеры

| Имя | Что делает |
|---|---|
| `date` | `02.01.2006` |
| `datetime` | `02.01.2006 15:04` |
| `number:N` | число с N знаками после запятой |
| `currency` | число + символ валюты |
| `upper` / `lower` | регистр |
| `default:VAL` | значение по умолчанию если пусто |

### Доступ к полям в `{{...}}`

- `Поле` — поле документа
- `Поле.ПодПоле` — для reference-полей (раскрытие справочника)
- `Константы.Имя` — константа из этапа 1
- `@row` — номер строки в `table.columns`
- `Итог.Сумма` — итог по колонке (рассчитывается из `totals`)

---

## Изменения в коде

### Новый пакет `internal/printform`

**`internal/printform/printform.go`**
```go
type PrintForm struct {
    Name     string
    Document string                 // имя документа/справочника
    Title    string
    Header   string                 // markdown / mini HTML
    Table    *TableSection
    Footer   string
}

type TableSection struct {
    Source  string                  // имя табличной части
    Columns []Column
    Totals  []TotalSpec
}

type Column struct {
    Field  string
    Label  string
    Width  string
    Align  string                   // left/center/right
    Format string                   // formatter spec
}

type TotalSpec struct {
    Field string
    Sum   bool
    Label string
}
```

**`internal/printform/loader.go`**
```go
func LoadFile(path string) (*PrintForm, error)
func LoadDir(dir string) ([]*PrintForm, error)
```

**`internal/printform/renderer.go`**
```go
type RenderContext struct {
    Document    map[string]any              // поля документа
    TableParts  map[string][]map[string]any // его табличные части
    Constants   map[string]any              // константы
    Refs        map[string]map[string]any   // развёрнутые ссылки
}

func Render(form *PrintForm, ctx *RenderContext) (template.HTML, error)
```

Внутренний пайплайн:
1. Заголовок: `Render` строки `Title` через `{{...}}` → result
2. Header: markdown → HTML (использовать `github.com/yuin/goldmark` или встроенный простой парсер для `**bold**` и `## heading`)
3. Таблица: пройти по строкам `Source` table-part, для каждой колонки извлечь значение и применить форматтер
4. Итоги: суммировать по колонкам где `Sum: true`
5. Footer: markdown → HTML

**`internal/printform/formatters.go`**
```go
func ApplyFormat(value any, spec string) string {
    parts := strings.SplitN(spec, ":", 2)
    switch parts[0] {
    case "date":     return formatDate(value)
    case "datetime": return formatDateTime(value)
    case "number":   n, _ := strconv.Atoi(parts[1]); return formatNumber(value, n)
    ...
    }
}
```

**`internal/printform/templates.go`**
- HTML-каркас обёртки печатной формы (с CSS `@media print`):
  ```css
  @page { margin: 1cm; }
  body { font-family: 'Times New Roman'; font-size: 11pt; }
  .pf-table { width: 100%; border-collapse: collapse; }
  .pf-table th, .pf-table td { border: 1px solid #000; padding: 4px 8px; }
  .pf-totals { font-weight: bold; }
  .pf-noprint { display: none; } @media screen { .pf-noprint { display: block; } }
  ```
- Кнопка «Печать» сверху (только на экране): `<button onclick="window.print()">Печать</button>`

### Загрузка проекта

**`internal/project/loader.go`**
```go
type Project struct {
    ...
    PrintForms []*printform.PrintForm
}

func (p *Project) loadPrintForms() error {
    dir := filepath.Join(p.Dir, "printforms")
    forms, err := printform.LoadDir(dir)
    if os.IsNotExist(err) { return nil }
    p.PrintForms = forms
    return nil
}
```

### Runtime

**`internal/runtime/registry.go`**
```go
type Registry struct {
    ...
    printForms map[string][]*printform.PrintForm  // entityName → формы
}

func (r *Registry) GetPrintForms(entity string) []*printform.PrintForm
```

### UI

**`internal/ui/server.go`** (Mount)
```go
r.Get("/ui/{kind}/{entity}/{id}/print/{form}", s.printDocument)
```

**`internal/ui/handlers.go`**
```go
func (s *Server) printDocument(w, r) {
    entity := s.getEntity(...)
    formName := chi.URLParam(r, "form")
    form := findForm(s.reg.GetPrintForms(entity.Name), formName)
    if form == nil { http.Error(...); return }
    
    id, _ := uuid.Parse(chi.URLParam(r, "id"))
    row, _ := s.store.GetByID(ctx, entity.Name, id, entity)
    tps, _ := s.store.GetTableParts(ctx, entity.Name, id, entity)
    
    // Развернуть ссылки
    refs := s.expandRefs(ctx, row, entity)
    
    // Загрузить константы
    constants, _ := storage.ListConstants(ctx, s.store.Pool())
    
    rctx := &printform.RenderContext{
        Document: row, TableParts: tps,
        Refs: refs, Constants: constants,
    }
    html, err := printform.Render(form, rctx)
    if err != nil { http.Error(...); return }
    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    w.Write([]byte(html))
}
```

**`internal/ui/templates.go`** (page-form для документа)
- На странице редактирования документа добавить выпадающий список «Печать»:
  ```html
  {{if .PrintForms}}
  <details class="print-menu">
    <summary>📄 Печать ▾</summary>
    {{range .PrintForms}}
    <a href="/ui/{{$kind}}/{{$entity}}/{{$id}}/print/{{.Name}}" target="_blank">{{.Name}}</a>
    {{end}}
  </details>
  {{end}}
  ```

### Конфигуратор

**`internal/launcher/configurator.go`**
- В `cfgData` добавить `PrintForms []cfgPrintForm`
- Отображение в дереве в секции «Печатные формы», при клике — содержимое YAML

---

## Опциональный этап: PDF

После того как HTML-печать работает, можно добавить кнопку «Скачать PDF».

### Вариант 1: Headless Chrome

```go
import "github.com/chromedp/chromedp"

func renderPDF(html string) ([]byte, error) {
    ctx, _ := chromedp.NewContext(...)
    var pdf []byte
    err := chromedp.Run(ctx, chromedp.Tasks{
        chromedp.Navigate("data:text/html;base64," + base64.StdEncoding.EncodeToString([]byte(html))),
        chromedp.ActionFunc(func(ctx context.Context) error {
            var err error
            pdf, _, err = page.PrintToPDF().Do(ctx)
            return err
        }),
    })
    return pdf, err
}
```

Требует Chrome/Chromium на машине.

### Вариант 2: wkhtmltopdf через subprocess

Если установлен `wkhtmltopdf` — pipe HTML через stdin, получить PDF на stdout. Без Go-зависимостей.

```go
cmd := exec.Command("wkhtmltopdf", "--quiet", "-", "-")
cmd.Stdin = strings.NewReader(html)
var out bytes.Buffer
cmd.Stdout = &out
err := cmd.Run()
return out.Bytes(), err
```

### Endpoint

```go
r.Get("/ui/{kind}/{entity}/{id}/print/{form}/pdf", s.printDocumentPDF)
```

---

## Тесты

**`internal/printform/loader_test.go`**
```go
func TestLoadFile(t *testing.T) {
    f, err := LoadFile("testdata/счёт.yaml")
    require.NoError(t, err)
    assert.Equal(t, "СчётНаОплату", f.Name)
    assert.Equal(t, "Реализация", f.Document)
    assert.Len(t, f.Table.Columns, 5)
}
```

**`internal/printform/renderer_test.go`**
```go
func TestRender_Simple(t *testing.T) {
    form := &PrintForm{
        Title: "Счёт № {{Номер}}",
        Header: "Покупатель: {{Контрагент.Наименование}}",
    }
    ctx := &RenderContext{
        Document: map[string]any{"Номер": "INV-001"},
        Refs: map[string]map[string]any{
            "Контрагент": {"Наименование": "ООО Ромашка"},
        },
    }
    html, _ := Render(form, ctx)
    assert.Contains(t, string(html), "Счёт № INV-001")
    assert.Contains(t, string(html), "Покупатель: ООО Ромашка")
}

func TestRender_TableTotals(t *testing.T) {
    form := &PrintForm{
        Table: &TableSection{
            Source: "Товары",
            Columns: []Column{{Field: "Сумма", Format: "number:2"}},
            Totals:  []TotalSpec{{Field: "Сумма", Sum: true, Label: "Итого"}},
        },
    }
    ctx := &RenderContext{
        TableParts: map[string][]map[string]any{
            "Товары": {{"Сумма": 100.0}, {"Сумма": 250.0}},
        },
    }
    html, _ := Render(form, ctx)
    assert.Contains(t, string(html), "350.00")  // итог
}
```

**Расширить `examples/trade/`**
- `printforms/счёт_на_оплату.yaml` для документа Реализация
- `printforms/накладная.yaml` для документа Реализация (другая форма — М-15)
- `printforms/приходный_ордер.yaml` для Поступления

---

## Verification

1. `go test ./internal/printform/...` — юниты на парсинг и рендер
2. В `examples/trade` создать документ Реализация с товарами, заполнить
3. На странице документа — выпадающий список «Печать», в нём 2 формы
4. Клик на «Счёт на оплату» — открывается новая вкладка с распечатываемой формой
5. `Ctrl+P` в браузере → корректно отображается на бумаге A4
6. `QUICKSTART.md` — раздел «Печатные формы»

## Эстимейт

- Парсер YAML + структуры: 0.3 дня
- Renderer (`{{...}}`, форматтеры, markdown header/footer): 1.5 дня
- Таблица + итоги: 0.5 дня
- UI (кнопка печати + страница рендера): 0.5 дня
- Конфигуратор: 0.2 дня
- Тесты + пример trade: 1 день
- *Опционально*: PDF через chromedp: +0.5 дня

**Итого: 4 дня (без PDF) или 4.5 дня (с PDF).**
