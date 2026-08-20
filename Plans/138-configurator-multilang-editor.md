# Этап 138 — Редактирование переводов синонимов в конфигураторе (фаза 4e)

> Номер сменён с 31 на 138 (issue #1035): под 31 жили разные планы, и ссылка «план 31» перестала быть однозначной. Номер 31 остался за планом «Стартовая страница с виджетами» (31-home-page-widgets). Ссылки «план 31» на эту тему читать как «план 138».

**Статус:** ✅ Реализовано

**Охват:** справочники/документы (поля, ТЧ), отчёты (параметры), обработки
(параметры), константы, подсистемы, регистры накопления (изм/рес), инфорегистры
(изм/рес), регистр бухгалтерии (ресурсы), страницы, главная страница.

**Реализовано в feature/enum-value-titles (T1–T6):**
- Переводы **значений** перечислений (`enum`) — метамодель расширена (`ValueTitles`,
  `ValueTitle(value, lang)`), YAML поддерживает оба формата, редактор переводов
  значений в конфигураторе, переводы в dropdown/списках/плитках/SlickGrid/DOM-ТЧ.

**Вынесено в follow-up (вне рамок):**
- Перевод **заголовка самого объекта** перечисления (имя enum-а) — нет структурированного
  редактора в конфигураторе (сырой YAML редактируется вручную).
- Виджеты/печатные формы/журналы/регламентные задания — нет структурированного
  редактора метаданных в конфигураторе (сырой YAML).

## Контекст

После этапа 30 (локализация интерфейса, фазы 4a/4b/4d) метаданные
поддерживают многоязычные синонимы: `Entity.Titles`, `Field.Titles`,
`Report.Titles`, `Param.Labels` и т.д. Рантайм-UI корректно переключает
эти заголовки по выбранному языку.

Однако **редакторы метаданных в конфигураторе** (формы
`/configurator/catalog`, `/reports`, `/processors`, `/subsystems`,
`/widgets`, `/account-register` в `internal/launcher/configurator_tmpl.go`
и обработчики в `configurator.go`) пока умеют редактировать только
**дефолтный** `title:` — блок `titles:` с переводами приходится
вписывать руками в YAML.

Нужно добавить ввод/правку переводов прямо в формы конфигуратора.

## Архитектура

### Переиспользуемый partial-шаблон

В `cfgTmpl.Parse(...)` подключить новый `{{define "titles-block"}}`:

```html
{{define "titles-block"}}
<div class="fg" style="margin-bottom:10px">
  <label style="font-size:12px;color:#666">
    {{t .Lang "Переводы заголовка по языкам"}}
  </label>
  {{range .Langs}}
    <div style="display:flex;gap:6px;margin-bottom:3px;align-items:center">
      <span style="width:60px;color:#666;font-size:12px">{{.Native}}</span>
      <input type="text" name="{{$.Prefix}}.{{.Code}}"
             value="{{index $.Values .Code}}"
             placeholder="{{$.Placeholder}}">
    </div>
  {{end}}
</div>
{{end}}
```

Вызов в формах:
```html
{{template "titles-block" (dict
    "Lang" $.Lang
    "Langs" $.AvailableLangs
    "Prefix" "titles"
    "Values" $obj.Titles
    "Placeholder" $obj.Name)}}
```

`dict` нужно добавить в `FuncMap` конфигуратора (стандартный helper).

### Helper парсинга формы

В `internal/launcher/configurator.go`:

```go
// parseMapForm reads form values matching "<prefix>.<lang>" into a
// map[lang]value, skipping empty entries. Returns nil when nothing set.
func parseMapForm(r *http.Request, prefix string) map[string]string {
    out := map[string]string{}
    for key, vals := range r.Form {
        if !strings.HasPrefix(key, prefix+".") || len(vals) == 0 {
            continue
        }
        if v := strings.TrimSpace(vals[0]); v != "" {
            out[strings.TrimPrefix(key, prefix+".")] = v
        }
    }
    if len(out) == 0 {
        return nil
    }
    return out
}
```

## Точки приложения

| Save-handler | Объект | Что добавляем |
|---|---|---|
| `catalogs/save` + `documents/save` | `Entity` | `Titles` объекта + у каждого поля `Titles`, у каждой ТЧ `Titles` |
| `reports/save` | `Report` | `Titles` + у каждого `Param.Labels` |
| `processors/save` | `Processor` | `Titles` + у каждого `Param.Labels` |
| `subsystems/save` | `Subsystem` | `Titles` |
| `widgets/save` | `Widget` | `Titles` + у каждой колонки `Labels` |
| `account-register/save` | `AccountRegister` | `Titles` + у каждого ресурса `Titles` |

## Изменения в коде

**`internal/launcher/configurator_tmpl.go`**:
- partial `titles-block` (и при необходимости `labels-block` с другим
  префиксом — формат одинаковый).
- `dict` helper в `cfgTmpl.Funcs`.
- В каждой форме редактирования объекта — вставить вызов partial
  рядом с существующим инпутом `name="title"`.
- Для полей/параметров/колонок — partial внутри `range` с префиксом
  `field.<i>.titles`/`param.<i>.labels`/`col.<i>.labels`.

**`internal/launcher/configurator.go`**:
- общий `parseMapForm(r, prefix)`.
- каждая save-функция:
  - `obj.Titles = parseMapForm(r, "titles")` → YAML-структура получает
    поле `Titles map[string]string \`yaml:"titles,omitempty"\``.
  - для полей — итерация `field.<i>.titles.<lang>` (можно
    `parseMapForm(r, fmt.Sprintf("field.%d.titles", i))`).
- расширить локальные YAML-структуры (`saveReport`, `saveCatalog`,
  `saveProcessor`, `saveSubsystem`, ...) полями `Titles`/`Labels`.

## Тесты

- Юнит на `parseMapForm`: trim, отбрасывание пустых, nil при пустом
  результате, разные префиксы не пересекаются.
- Integration: POST `catalogs/save` с `titles.en=Counterparty&titles.de=Geschäftspartner`
  → файл `catalogs/Контрагент.yaml` содержит блок `titles:` ровно с
  этими двумя строками; рантайм-UI при `lang: en` показывает
  «Counterparty» в навигации и заголовках.
- Регрессия: сохранение без переводов оставляет YAML без блока
  `titles:` (`omitempty`).

## Verification

1. Открыть конфигуратор → редактор справочника «Контрагент».
2. Ввести: `en: Counterparty`, `de: Geschäftspartner`, `sr: Партнер`.
3. Сохранить → проверить `catalogs/контрагент.yaml`:
   ```yaml
   name: Контрагент
   title: Контрагент
   titles:
     de: Geschäftspartner
     en: Counterparty
     sr: Партнер
   ```
4. Открыть рантайм-UI, переключить язык на `en` → имя справочника
   стало «Counterparty» в навигации, в H2 списка и в заголовке формы.
5. То же для отчёта (с параметрами), обработки, подсистемы, регистра.
6. Удалить значение перевода в форме и сохранить → соответствующая
   строка пропадает из YAML.

## Эстимейт

- Partial-шаблон + helper + `dict` в FuncMap: 0.5 дня.
- 6 форм × ~30 минут (Titles объекта): ~3 часа.
- Поля/параметры/колонки (вторая итерация): +0.5 дня.
- Тесты и verification: 2–3 часа.

**Итого:** ≈ 1–1.5 дня для базовой версии (Titles только у самих
объектов), +0.5 дня для полей/параметров/колонок.

## Зависимости

- Этап 30 (этот PR) — фундамент i18n.
- Фазы 4a, 4b, 4d (этот PR) — Titles в моделях метаданных.

## Связанные

- Фаза 4c (импорт многоязычных синонимов из 1С `xmlLang`) — можно
  делать независимо.
- Виджеты — для `WidgetColumn.Labels` рантайм пока не использует
  переведённые подписи в `ColumnSpec`; точечная доработка
  `internal/widget/runner.go`.
