# План 33: Конвертер 1С → OneBase v2

## Цель

Текущий конвертер генерирует мусор: ложные каталоги из форм/модулей, пустые регистры сведений, документы без `posting: true`, 0 регистров накопления, 0 перечислений. Цель — **повторная конвертация familyBg даёт ≤ 5% ручных правок**.

## Источник

`C:\Users\ibrog\.claude\plans\cuddly-riding-sonnet.md` — Часть 2 (Приоритет 1 + критические из «work»).

## Объём (1–2 недели)

### Приоритет 1 — критические исправления

1. **Регистры накопления** — парсить `AccumulationRegisters/` → `registers/*.yaml`. Сейчас 0 из 5 в familyBg.
2. **Перечисления** — парсить `Enums/` → `enums/*.yaml`. Сейчас 0 из 1.
3. **Фильтрация мусора** — НЕ создавать YAML для `CommonForms`, `CommonModules`, `Roles`, `CommonPictures`, `StyleItems`, `CommandGroups`, `Subsystems`, `DataProcessors` (для DataProcessors отдельный путь `processors/`). Сейчас 253 лишних YAML в `work`.
4. **Регистры сведений** — заполнять `dimensions`/`resources` из XML. Сейчас YAML пустые.
5. **Posting flag** — проверять `<Post>` в XML документа → `posting: true`.
6. **Тип константы** — анализировать `Type/v8:Type` → `string` / `reference:Имя` / `enum:Имя` / `number` / `date`.

### Приоритет 1.5 — выявленные на «work» (УТ-подобной конфигурации)

7. **Каталоги без вложенной директории** (`work/W1`) — парсить ВСЕ `.xml` из `Catalogs/`, даже когда вложенной папки нет.
8. **Табличные части справочников** (`work/W3`) — парсить `<TabularSection>` в Catalog XML, не только в Document.
9. **Enum-типы полей** (`work/W4`, `work/F8` из исходного плана) — определять `CatalogRef.СтатусРаботы` и ставить `enum:Имя`, если это реальный enum.
10. **Bundle-формат констант** (`work/W9`) — 6 констант в 1 YAML → 6 отдельных записей в `constants/*.yaml`.

### Откладываем (на v3)

- Составные типы (W7) — пока пишем первый тип, оставляем TODO.
- `v8:ValueStorage` (W6) — string + TODO в DSL-комменте.
- BSL-модули объектов (W8) — создавать пустые `.os` с шапкой-комментарием.

## Critical fix: посмотреть как конвертер сейчас определяет «это каталог»

Корень проблемы W2: фильтр по «есть ли вложенная папка с XML» вместо «это файл из каталога `Catalogs/`». Нужно:
- идти от типа объекта в `Configuration.xml` (`MetaDataObject/Catalog`, `Document`, `AccumulationRegister`, `InformationRegister`, `Enum`, `Constant`, `DataProcessor`)
- НЕ полагаться на структуру файловой системы (она не нормирована между выгрузкой через XmlSchema и обычной)

## Критерий успеха

На входе три конфигурации:
1. **familyBg** (small, ~30 объектов) — ≤ 5% ручных правок после конверсии.
2. **work** (medium, BSP-библиотека) — ≤ 10% мусора в `catalogs/` (сейчас 96%).
3. (опционально) **trade-1c-export** — синтетическая конфигурация на основе нашего trade-примера, экспортированная в 1С-формат и обратно. Roundtrip-проверка.

## Файлы кода

- `internal/converter/parser1c/metadata.go` — парсинг XML
- `internal/converter/parser1c/typemap.go` — type resolution (enum/reference/scalar)
- `internal/converter/writer/yaml.go` — генерация YAML
- `internal/converter/convert.go` — main orchestrator (фильтр типов)

## Тесты

- `internal/converter/parser1c/*_test.go` — unit на каждый тип объекта (catalog, document, register, accumreg, enum, const)
- `internal/converter/*_test.go` — integration: фикстура «1С XML → ожидаемые YAML»

## Что НЕ делаем

- СКД / Reports — заглушки пустого YAML с TODO (Plan D).
- Подписки на события — отметить в reports.
- Web-сервисы, HTTP-сервисы, бизнес-процессы, планы обмена — НЕ конвертируем.
- Формы — НЕ конвертируем (платформа сама генерит дефолтные).

## Что попадёт в Plan A после v2

Только семантический мусор, который конвертер физически не может починить (бизнес-логика проведения, тонкости связей объектов). Цель — пользователю остаётся только написать DSL-код, без правки YAML.
