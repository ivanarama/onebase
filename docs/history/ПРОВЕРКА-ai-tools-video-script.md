# Короткое видео: новые AI-инструменты OneBase

Сценарий для записи короткого видео о новых AI-инструментах OneBase.

## Идея

Показать за 2-3 минуты контраст:

- раньше AI был просто помощником, который угадывает YAML;
- теперь AI работает как разработчик: читает контракт проекта, проверяет запросы,
  видит impact, делает безопасный refactor preview, пишет через staging, показывает
  tool trace и имеет rollback/safety gates.

## Подготовка

Открыть терминал в корне репозитория:

```bash
cd /Users/ivantitov/projects/onebase
```

Опционально увеличить шрифт терминала. Для видео лучше использовать `examples/minimal`,
потому что команды быстрые и вывод короткий.

## Сценарий на 2-3 минуты

### 0:00-0:15 — хук

На экране: README/терминал.

Сказать:

> Раньше AI в конфигураторе мог в основном сгенерировать простой YAML-каркас.
> Сейчас у него появился нормальный developer toolbelt: контракт проекта,
> проверки, запросы, impact, refactor preview, MCP и история tool calls.

### 0:15-0:40 — AI больше не угадывает проект

Команда:

```bash
go run ./cmd/onebase describe --project examples/minimal --compact | head -60
```

Сказать:

> Вот компактный machine-readable контекст. AI видит объекты, поля, табличные
> части, отчёты, виджеты и DSL-сигнатуры. Это уже не промпт "угадай структуру",
> а контракт платформы.

Показать в выводе: `documents`, `registers`, `reports`, `widgets`, `fields`.

### 0:40-1:05 — быстрая проверка запроса без UI

Команда:

```bash
go run ./cmd/onebase report explain --project examples/minimal ОстаткиТоваров --json | head -60
```

Сказать:

> AI может проверить отчёт headless, без запуска UI. Он видит исходный запрос,
> скомпилированный SQL и sources. Для виджетов работает то же самое.

Опциональная вторая команда, если хочется показать widget path:

```bash
go run ./cmd/onebase widget explain --project examples/minimal СуммаСклада --json | head -50
```

### 1:05-1:35 — "что сломается?"

Команда:

```bash
go run ./cmd/onebase impact --project examples/minimal --object Поступление --json | head -80
```

Сказать:

> Перед переименованием или удалением AI теперь может спросить: что затронет
> изменение? Impact показывает ссылки в метаданных, формах, запросах, отчётах,
> виджетах и даёт migration notes.

Показать в выводе: `summary`, `migrationNotes`, `matches`.

### 1:35-2:05 — безопасный refactor preview

Команда:

```bash
go run ./cmd/onebase refactor rename-field \
  --project examples/minimal \
  --object Поступление \
  --from Номер \
  --to НомерДокумента \
  --json | head -100
```

Сказать:

> Это не blind replace. По умолчанию refactor ничего не пишет: он строит preview,
> показывает файлы, строки и impact. Запись включается только через `--write`,
> после этого запускается `check`, а при ошибке изменения откатываются.

Показать в выводе: `changes`, `preview`, `migrationNotes`.

### 2:05-2:30 — внешний агент через MCP

Команды:

```bash
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  | go run ./cmd/onebase mcp --project examples/minimal \
  | rg -o '"name":"[^"]+"' \
  | rg 'refactor_write|fmt_write|config_rollback|procrun' || true
```

```bash
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  | go run ./cmd/onebase mcp --project examples/minimal --allow-refactor-write \
  | rg -o '"name":"[^"]+"' \
  | rg 'refactor_write|fmt_write|config_rollback|procrun'
```

Сказать:

> Эти же инструменты доступны внешним AI-клиентам через MCP. По умолчанию всё
> read-only. Write-tools включаются точечно: например, только refactor write,
> а не весь набор опасных операций.

### 2:30-2:50 — история и safety

На экране: открыть UI конфигуратора или просто показать файл/скрин истории, если UI
не хочется поднимать.

Сказать:

> В конфигураторе генерация идёт через staging: можно выбрать файлы, посмотреть
> diff, отредактировать результат, запустить pre-apply check. История сохраняет
> tool trace: видно, какие инструменты вызывала модель и где была ошибка.
> Для database-backed конфигураций создаются snapshots до и после apply, так что
> есть rollback path.

### 2:50-3:00 — финал

Сказать:

> То есть AI в OneBase стал не "чатиком рядом с YAML", а контролируемым
> разработчиком: он видит проект, проверяет гипотезы, оценивает impact,
> предлагает безопасные патчи и оставляет проверяемый след действий.

## Бонус-сценарий: маленький продукт с нуля за 90 секунд

Этот блок лучше для более "крутого" видео: не просто показываем инструменты на
готовом `examples/minimal`, а за минуту создаём новую мини-конфигурацию кофейни
в `/tmp`, затем прогоняем её через новые developer tools.

Проверено локально: `fmt`, `check`, `forms validate`, `describe`,
`report explain`, `widget explain`, `impact` и `refactor preview` проходят.

### 0:00-0:10 — чистый старт

Сказать:

> Сейчас с нуля соберём маленький feature slice: кофейня, напитки, управляемая
> форма карточки, документ продажи, регистр выручки, отчёт и красивый dashboard
> с KPI и bar chart. Потом не будем верить YAML на слово — прогоним всё через
> инструменты.

Команда:

```bash
DEMO=/tmp/onebase-ai-coffee-demo
rm -rf "$DEMO"
go run ./cmd/onebase init "$DEMO"
mkdir -p "$DEMO/catalogs" "$DEMO/documents" "$DEMO/registers" "$DEMO/reports" "$DEMO/widgets" "$DEMO/src" "$DEMO/forms/напиток" "$DEMO/config"
```

### 0:10-0:35 — "AI создал feature slice"

Для записи можно вставить весь блок целиком. Это deterministic замена внешнего
LLM-вызова: в реальном конфигураторе эти файлы создаёт AI generator через
staging tools.

```bash
cat > "$DEMO/catalogs/напиток.yaml" <<'YAML'
name: Напиток
title: Напиток
fields:
  - name: Наименование
    type: string
  - name: Цена
    type: number
  - name: Активен
    type: bool
YAML

cat > "$DEMO/documents/продажакофе.yaml" <<'YAML'
name: ПродажаКофе
title: Продажа кофе
posting: true
fields:
  - name: Номер
    type: string
  - name: Дата
    type: date
  - name: Бариста
    type: string
tableparts:
  - name: Напитки
    fields:
      - name: Напиток
        type: reference:Напиток
      - name: Количество
        type: number
      - name: Цена
        type: number
      - name: Сумма
        type: number
YAML

cat > "$DEMO/registers/выручкакофе.yaml" <<'YAML'
name: ВыручкаКофе
dimensions:
  - name: Напиток
    type: reference:Напиток
resources:
  - name: Количество
    type: number
  - name: Сумма
    type: number
YAML

cat > "$DEMO/src/продажакофе.posting.os" <<'OS'
Процедура ОбработкаПроведения()
  Движения.ВыручкаКофе.Очистить();
  Для Каждого Строка Из this.Напитки Цикл
    Дв = Движения.ВыручкаКофе.Добавить();
    Дв.ВидДвижения = "Приход";
    Дв.Напиток = Строка.Напиток;
    Дв.Количество = Строка.Количество;
    Дв.Сумма = Строка.Количество * Строка.Цена;
  КонецЦикла;
КонецПроцедуры
OS

cat > "$DEMO/forms/напиток/объекта.form.yaml" <<'YAML'
schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Напиток
  title:
    ru: Карточка напитка
  auto_save_settings: true
  vertical_scroll: auto
events:
  ПриОткрытии: ПриОткрытии
attributes:
  - name: Объект
    type: CatalogRef.Напиток
    save: true
    main: true
elements:
  - kind: СтраницыФормы
    name: ОсновныеЗакладки
    children:
      - kind: Страница
        name: СтраницаОсновное
        title:
          ru: Основное
        children:
          - kind: ГруппаФормы
            name: ГруппаКарточка
            title:
              ru: Карточка
            children:
              - kind: ПолеВвода
                name: ПолеНаименование
                title:
                  ru: Название напитка
                data_path: Объект.Наименование
                required: true
              - kind: ПолеВвода
                name: ПолеЦена
                title:
                  ru: Цена
                data_path: Объект.Цена
                required: true
              - kind: Флажок
                name: ПолеАктивен
                title:
                  ru: Продаётся сейчас
                data_path: Объект.Активен
      - kind: Страница
        name: СтраницаПодсказки
        title:
          ru: Подсказки
        children:
          - kind: Надпись
            name: ПодсказкаМаржи
            title:
              ru: Следите за топом напитков на стартовом дашборде.
YAML

cat > "$DEMO/forms/напиток/объекта.form.os" <<'OS'
Процедура ПриОткрытии()
  Если НЕ ЗначениеЗаполнено(Объект.Активен) Тогда
    Объект.Активен = Истина;
  КонецЕсли;
КонецПроцедуры
OS

cat > "$DEMO/reports/топнапитков.yaml" <<'YAML'
name: ТопНапитков
title: Топ напитков
query: |
  ВЫБРАТЬ
    Напиток,
    КоличествоОборот КАК Количество,
    СуммаОборот КАК Сумма
  ИЗ РегистрНакопления.ВыручкаКофе.Обороты()
  УПОРЯДОЧИТЬ ПО Сумма
YAML

cat > "$DEMO/widgets/выручкакофе.yaml" <<'YAML'
name: ВыручкаКофе
type: kpi
title: Выручка кофе
format: money
query: |
  ВЫБРАТЬ СУММА(СуммаОборот) КАК Значение
  ИЗ РегистрНакопления.ВыручкаКофе.Обороты()
YAML

cat > "$DEMO/widgets/топнапитковchart.yaml" <<'YAML'
name: ТопНапитковChart
type: chart
chart_kind: bar
title: Топ напитков по выручке
query: |
  ВЫБРАТЬ
    Напиток,
    СуммаОборот КАК Сумма
  ИЗ РегистрНакопления.ВыручкаКофе.Обороты()
  УПОРЯДОЧИТЬ ПО Сумма
x_field: Напиток
y_fields: [Сумма]
YAML

cat > "$DEMO/config/home_page.yaml" <<'YAML'
title: Дашборд кофейни
layout: rows
rows:
  - widgets:
      - ВыручкаКофе
  - widgets:
      - ТопНапитковChart
YAML
```

Сказать:

> Получился маленький вертикальный срез: справочник с управляемой формой,
> проводимый документ, регистр, отчёт, KPI, bar chart и стартовый дашборд.
> Теперь показываем, что это не просто текстовые файлы, а проверяемый
> продуктовый кусок.

### 0:35-0:55 — canonical format + check

Команды:

```bash
go run ./cmd/onebase fmt --project "$DEMO"
go run ./cmd/onebase check --project "$DEMO"
go run ./cmd/onebase forms validate --src "$DEMO/forms/напиток/объекта.form.yaml"
```

Ожидаемый вывод:

```text
OK: ошибок не найдено
✓ Форма прошла валидацию.
```

Сказать:

> AI-результат сразу проходит canonical formatter, полный configuration check
> и отдельную валидацию managed form. Если бы YAML, DSL, форма или запросы были
> сломаны, apply можно было бы заблокировать.

### 0:55-1:15 — OneBase понимает новый проект

Команда:

```bash
go run ./cmd/onebase describe --project "$DEMO" --compact | head -40
```

Сказать:

> Describe показывает, что платформа поняла структуру: у справочника есть
> управляемая форма, документ проводится, у него есть табличная часть, регистр
> доступен как `Остатки/Обороты`, отчёт виден как готовый объект.

### 1:15-1:35 — отчёт компилируется в SQL

Команда:

```bash
go run ./cmd/onebase report explain --project "$DEMO" ТопНапитков --json | head -80
```

Сказать:

> Отчёт не просто лежит YAML-файлом. Инструмент компилирует запрос, показывает
> SQL и sources: регистр `ВыручкаКофе` и справочник `Напиток`.

Потом показать красивый chart widget:

```bash
go run ./cmd/onebase widget explain --project "$DEMO" ТопНапитковChart --json | head -80
```

Сказать:

> Это уже похоже на продуктовый экран: KPI выручки и bar chart по напиткам
> подключены в `config/home_page.yaml`, а widget explain показывает, что chart
> тоже компилируется и знает свои sources.

Опционально, если в видео хочется показать реальный UI, а не только CLI:

```bash
go run ./cmd/onebase migrate --project "$DEMO" --sqlite "$DEMO/coffee.db"
go run ./cmd/onebase run --project "$DEMO" --sqlite "$DEMO/coffee.db" --port 18080
```

Открыть:

```text
http://127.0.0.1:18080/ui/
```

Показать:

- стартовый дашборд `Дашборд кофейни`;
- KPI `Выручка кофе`;
- bar chart `Топ напитков по выручке`;
- карточку справочника `Напиток` с управляемой формой `Карточка напитка`.

### 1:35-1:55 — impact и safe refactor

Команды:

```bash
go run ./cmd/onebase impact --project "$DEMO" --object Напиток --json | head -80
```

```bash
go run ./cmd/onebase refactor rename-field \
  --project "$DEMO" \
  --object Напиток \
  --from Цена \
  --to ЦенаПродажи \
  --json | head -80
```

Сказать:

> Теперь можно безопасно менять модель. Переименование цены затрагивает не
> только справочник: refactor preview показывает форму и posting-модуль, где
> поле используется. Без `--write` ничего не записывается.

### 1:55-2:05 — финал демо

Сказать:

> За две минуты мы с нуля получили рабочий кусок прикладной системы: форма,
> дашборд, отчёт, виджет, проведение и safety-инструменты. Это уже не генерация
> YAML, а контролируемая разработка feature slice.

### One-liner для быстрой репетиции

После создания файлов можно прогнать всё важное так:

```bash
go run ./cmd/onebase fmt --project "$DEMO" \
  && go run ./cmd/onebase check --project "$DEMO" \
  && go run ./cmd/onebase forms validate --src "$DEMO/forms/напиток/объекта.form.yaml" \
  && go run ./cmd/onebase migrate --project "$DEMO" --sqlite "$DEMO/coffee.db" \
  && go run ./cmd/onebase widget explain --project "$DEMO" ТопНапитковChart --json | head -50
```

## Самая короткая версия на 60 секунд

1. `describe --compact`: "AI видит контракт проекта".
2. `report explain`: "AI сам компилирует и объясняет запросы отчётов".
3. `impact`: "AI понимает последствия".
4. `refactor rename-field`: "AI показывает безопасный patch preview".
5. `mcp tools/list`: "это доступно внешним агентам, read-only по умолчанию".

Команды:

```bash
go run ./cmd/onebase describe --project examples/minimal --compact | head -40
go run ./cmd/onebase report explain --project examples/minimal ОстаткиТоваров --json | head -50
go run ./cmd/onebase impact --project examples/minimal --object Поступление --json | head -60
go run ./cmd/onebase refactor rename-field --project examples/minimal --object Поступление --from Номер --to НомерДокумента --json | head -80
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | go run ./cmd/onebase mcp --project examples/minimal --allow-refactor-write | rg -o '"name":"[^"]+"' | rg 'refactor_write|fmt_write|config_rollback|procrun'
```

## Что важно не показывать

- Не делать реальный `--write` в коротком видео, чтобы не отвлекаться на rollback.
- Не углубляться в JSON Schema и все команды сразу.
- Не показывать длинные полные JSON-выводы: использовать `head`/`rg`.
- Не объяснять внутреннюю реализацию MCP; достаточно показать read-only default и
  точечный `--allow-refactor-write`.

## Фраза для заголовка видео

> OneBase AI tools: от генерации YAML к безопасному AI-разработчику.
