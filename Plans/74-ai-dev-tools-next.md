# План 74: AI-инструменты разработчика — довести до сильного продукта

**Статус:** ✅ Реализован. В коде есть `onebase describe`
`schemaVersion: 2` с режимами `--full/--compact`, структурированные объекты,
`onebase examples`, `fmt`, `schema`, `query`, `eval`, `widget/report explain`,
`impact`, `onebase mcp` и расширенный генератор конфигуратора с file-level
staging для YAML/`.os`/форм. Дополнительно `check` отдаёт machine-readable
`code/suggestedFix`, генератор умеет форматировать staging и прогонять запросы,
а UI генерации поддерживает выбор файлов, inline edit и pre-apply `check` gate.
Настройки ИИ включают `ai.daily_token_cap` и `ai.max_tool_rounds`. Финальный
срез закрыл refactoring helpers, graph-based `impact`, MCP-интеграцию для
рефакторинга/версий конфигурации, tool trace генерации и rollback-путь через
снимки `_config_versions`.

Дата анализа: 2026-06-24.

## Контекст

В OneBase уже есть рабочий фундамент AI-tooling:

- `onebase ai-guide` генерирует `AGENTS.md` с сигнатурами DSL из `internal/dsl/langref`.
- `onebase check` делает полноценный validation gate: YAML, DSL, cross-refs, запросы
  виджетов/отчётов, статические запросы из `.os`, PREPARE на временной SQLite-схеме.
- `onebase describe` отдаёт JSON-срез конфигурации.
- `onebase procrun` запускает обработки headless.
- Конфигуратор имеет AI-ассистента, объяснение ошибок, генератор каркаса через tool-use,
  staging diff и apply.
- `internal/llm` поддерживает Anthropic/OpenAI-compatible/OpenAI/Gemini, fallback,
  tool-use, token accounting.
- Пользовательский AI-чат имеет read-only инструменты данных, RBAC-режимы, audit,
  rate-limit и дневной token cap.
- `SandboxProfile` ограничивает недоверенный DSL: сеть, файлы, команды ОС, wall-clock,
  итерации.

Главная проблема уже не в отсутствии AI как такового. Проблема в том, что инструменты
не дают ИИ достаточно точного машинного контракта и быстрой обратной связи, а генератор
конфигуратора пока создаёт только узкий YAML-каркас. Из-за этого продукт выглядит
многообещающе, но не ощущается как автономный "сильный разработчик OneBase".

## Выполнено в первом срезе

Дата реализации: 2026-06-24.

- `onebase describe` переведён на контракт `schemaVersion: 2`.
- Отчёты, виджеты, журналы, подсистемы, страницы, HTTP-сервисы, RBAC-роли,
  scheduled jobs, регистры бухгалтерии и планы счетов описываются структурами, а не строками.
- Объекты, формы, модули и процедуры получили больше машинных атрибутов:
  поля, табличные части, параметры, export-флаг, события форм, source `{file,line}`.
- В `describe` встроены `langref` descriptors: `builtins` для функций и полный
  `language` для функций, объектов, методов и query-конструкций.
- Добавлена команда `onebase examples [kind]` с canonical YAML/DSL snippets для
  справочников, документов, регистров, отчётов, виджетов, форм, сервисов, ролей,
  запросов и проведения.
- Добавлены headless-команды `onebase fmt`, `schema`, `query`, `eval`,
  `widget explain`, `report explain` и `impact`.
- Добавлен `onebase mcp` stdio server: resources `ai-guide`/`describe`/`schema`/
  source tree и read-only tools поверх CLI; mutating tools включаются только
  через `--allow-write`.
- Генератор конфигуратора расширен: `создать_файл`, `прочитать_файл`,
  `список_объектов`, `проверить_конфигурацию(full=true)` и `объяснить_impact`;
  apply whitelist теперь допускает `.os` и managed forms.
- Документация ИИ-настроек синхронизирована с текущей формой UI; генератор
  перешёл от YAML-каркаса к file-level staging для вертикальных срезов.

## Выполнено во втором safety-срезе

Дата реализации: 2026-06-24.

- Вынесен общий `internal/configfmt` canonical YAML writer; CLI `onebase fmt`
  и генератор используют один и тот же форматтер.
- `configcheck.Issue` получил `code` и `suggestedFix`; `onebase check --json`,
  MCP и AI-генератор больше не зависят только от текста ошибки.
- В генератор добавлены tools `форматировать` и `прогнать_запрос`, чтобы модель
  могла стабилизировать YAML и проверять отчёты/виджеты на staging-схеме.
- Панель генерации показывает old/new side-by-side, позволяет снять отдельные
  файлы с применения и отредактировать `newContent` перед apply.
- `ai-apply` перед записью собирает staging и запускает `onebase check`; если
  исходная конфигурация была зелёной, а diff делает её красной, файлы не пишутся.
- В форму ИИ добавлены `ai.daily_token_cap` и `ai.max_tool_rounds`; второй лимит
  реально управляет числом tool-use раундов вместо жёсткой константы.

## Выполнено в финальном срезе

Дата реализации: 2026-06-24.

- Добавлен `onebase refactor rename-object/rename-field`: по умолчанию команда
  строит patch preview, а запись требует `--write`, запускает `check` и откатывает
  файловые изменения при красном результате.
- `onebase impact` усилен семантическим анализом проекта: ссылки метаданных,
  `based_on`, определения полей, compiled query sources виджетов/отчётов,
  `summary` и `migrationNotes`.
- MCP получил read-only tools `refactor_preview`, `config_versions`,
  `config_diff`; mutating `refactor_write` и `config_rollback` доступны только
  при явном `--allow-write`.
- Добавлен headless rollback path: `onebase config versions/diff/rollback` поверх
  `_config_versions`.
- `ai-apply` для database-backed конфигураций создаёт снимок до и после успешного
  применения, а ответ возвращает `beforeVersion/afterVersion`.
- История и UI генерации показывают tool trace, чтобы пользователь видел,
  какие инструменты запускала модель и чем они закончились.
- Добавлены golden-сценарии генератора без сетевых вызовов: документ с табличной
  частью, регистр и проведение; отчёт + виджет с `прогнать_запрос`; managed form
  с `.form.yaml` и обработчиком `.form.os`.
- История ИИ в конфигураторе разбирает audit summary на отдельные блоки:
  ответ модели, tool trace с `ok/error` статусами и список изменённых объектов.
- MCP write surface переведён на least-privilege флаги: `--allow-fmt-write`,
  `--allow-refactor-write`, `--allow-config-rollback`, `--allow-procrun`;
  `--allow-write` оставлен как совместимый broad-mode.
- Добавлена документация `docs/mcp.md` с настройкой stdio MCP-сервера, режимами
  read-only/write и безопасным агентным workflow.

## Что мешает сейчас

Исторический список ниже оставлен как исходный анализ и чек-лист приёмки плана.
Пункты из него, относящиеся к `describe`, `fmt`, `schema`, headless feedback,
генератору 2.0, MCP, `impact`, refactoring helpers, golden-сценариям генерации,
history/rollback/tool trace и policy-настройкам команд с записью, закрыты в
срезах выше. Дальше остаётся только обычная продуктовая эволюция за пределами
этого плана.

### 1. `describe` слабее реальных знаний платформы

Фактически `internal/cli/describe.go` всё ещё отдаёт часть объектов строками:
`reports`, `subsystems`, `journals`, `widgets`. Модули содержат только имена процедур,
без параметров, export-флага, source location. Builtins в `describe` — только строки,
хотя богатый `langref` уже есть.

Не хватает:

- `schemaVersion` контракта;
- source `{file,line}` для объектов, процедур, запросов;
- сигнатур процедур и функций;
- форм, ролей, страниц, scheduled jobs, services, printforms/layouts;
- полной структуры виджетов/отчётов/журналов/подсистем;
- графа зависимостей: кто читает/пишет какие объекты, движения документов по регистрам,
  ввод на основании, формы, обработчики событий;
- режима `--compact` для промпта и `--full` для tooling/MCP.

### 2. Нет детерминированного writer/formatter

Нет `onebase fmt`. Конфигуратор, генераторы и ручная правка могут писать YAML в разных
стилях. Для ИИ это критично: шумные diff'ы ухудшают ревью и мешают безопасному apply.

Нужен единый canonical writer для YAML и, по возможности, `.os`:

- фиксированный порядок ключей;
- стабильные отступы;
- идемпотентность;
- `--check` для CI;
- использование того же writer'а в конфигураторе и AI apply.

### 3. AI-генератор пока создаёт только минимальный YAML-каркас

`cfgAIGenerate` поддерживает `создать_объект` для ограниченного набора метаданных:
catalogs/documents/registers/inforegs/enums/accounts/accountregs. Это хорошо как MVP,
но не закрывает сильные пользовательские сценарии.

Не хватает генерации:

- `.os` модулей: проведение, обработчики форм, процедуры обработок, отчёты;
- управляемых форм;
- отчётов, виджетов, страниц, подсистем, ролей, scheduled jobs, services;
- миграционного dry-run после apply;
- более точного migration dry-run/impact summary перед apply;
- частичного apply: выбрать файлы/объекты, редактировать предложенный YAML до применения.

### 4. Нет headless sandbox-команд для гипотез

`procrun` есть, но нет быстрых:

- `onebase eval` — выполнить выражение/фрагмент DSL;
- `onebase query` — выполнить запрос OneBase на реальной базе/тестовой схеме;
- `onebase widget explain` — объяснить виджет: запрос, колонки, ось X, серии, sample rows;
- `onebase report explain` — то же для отчёта/композиции.

Без этого ИИ не может быстро проверять малые гипотезы. Ему приходится создавать обработку
или просить пользователя идти в UI.

### 5. Нет MCP-сервера

Сейчас внешние ИИ-клиенты работают через shell-команды. Для Claude Code/IDE/агентов нужен
`onebase mcp` как тонкая оболочка над проверенными командами.

Resources:

- `ai-guide`;
- `describe --compact/--full`;
- `schema`;
- исходники конфигурации.

Tools:

- `check`;
- `describe`;
- `query`;
- `eval`;
- `procrun`;
- `fmt`;
- `examples`;
- `impact`;
- `widget explain`.

По умолчанию MCP должен быть read-only. Любая запись — только с явным подтверждением.

### 6. Нет JSON Schema для YAML-конфигурации

Редакторы, MCP-клиенты и модели не получают строгой машинной схемы объектов. Сейчас модель
опирается на текстовую подсказку `metadataFormatGuide`, но это не заменяет schema contract.

Нужно:

- `onebase schema [--kind]`;
- schema для catalog/document/register/inforeg/report/widget/form/role/page/service/etc.;
- `$schema` hints для новых файлов;
- тесты, что schema соответствует реальным loader-структурам.

### 7. Не хватает анализа влияния

ИИ может предложить удалить/переименовать поле, но платформа не даёт ответ:
"что сломается?". Нужен `onebase impact --object X [--field Y]`.

Источники графа:

- YAML references;
- query sources/fields;
- module query literals;
- managed forms;
- print layouts;
- widgets/reports/composition;
- roles;
- based_on;
- register movements.

### 8. UX генерации не дотягивает до ощущения продукта

Текущий UI показывает предложенные файлы как full raw YAML и кнопку "Применить".
Для реального продукта нужно:

- diff viewer side-by-side;
- чек-лист файлов с partial apply;
- inline edit перед apply;
- статус `check` по каждому раунду генерации;
- "исправить ошибки" одной кнопкой;
- после apply: предложить/запустить миграцию;
- история генераций с возможностью повторить/откатить;
- шаблоны промптов для типовых задач: "документ с ТЧ и проведением", "отчёт по регистру",
  "форма объекта", "виджет графика".

### 9. Лимиты и наблюдаемость нужно распространить на конфигураторный ИИ

Пользовательский AI-чат уже имеет rate-limit и дневной token cap. Конфигураторные операции
дороже и тоже должны проходить через тот же бюджет/observability:

- rate-limit на `ai-assist`, `ai-query`, `ai-generate`, `ai-explain`;
- проверка дневного token cap до генерации;
- per-task counters: latency, tokens, model, failures, tool rounds;
- `ai.max_tool_rounds` вместо константы `llm.MaxToolIterations = 12`;
- в истории ИИ показывать не только ответ, но и tool trace генерации.

### 10. Документация местами расходится с кодом

`docs/features.md` всё ещё описывает настройки ИИ как JSON-редактор, хотя уже есть
форма `ai-settings.js`. Там же сказано, что "сгенерированный код исполняется в песочнице",
хотя текущий генератор каркаса не генерирует `.os` код. Документацию нужно синхронизировать,
иначе ожидания пользователя будут выше фактической возможности.

## Дорожная карта

### Фаза 1. Машинный контракт и стабильные артефакты

Цель: снизить число ошибок модели без изменения UX.

1. Расширить `describe` до v2:
   - добавить `schemaVersion`;
   - заменить строки отчётов/виджетов/журналов/подсистем на структуры;
   - добавить forms/roles/pages/scheduled/services/printforms/homePage;
   - добавить signatures процедур, params, export;
   - добавить `source:{file,line}`;
   - встроить `langref` descriptors вместо `[]string builtins`.

2. Вынести общий `aicontract` слой:
   - один источник для `describe`, `aicontext`, MCP resources и конфигураторных prompts;
   - режимы `compact`, `full`, `security-filtered`.

3. Реализовать `onebase fmt`:
   - YAML canonical writer;
   - `--check`;
   - идемпотентность;
   - интеграция в конфигураторные save paths и AI apply.

4. Реализовать `onebase schema`:
   - JSON Schema для всех YAML-kind;
   - golden tests;
   - ссылка из `ai-guide`.

Критерии приёмки:

- `describe --full` покрывает все основные виды объектов и source locations.
- `describe --compact` помещается в prompt и используется конфигуратором.
- `onebase fmt --check examples/...` стабилен.
- `ai-guide`, `describe`, UI langref используют один и тот же справочник языка.

### Фаза 2. Быстрая обратная связь для ИИ

Цель: дать ИИ headless-цикл "предположил → проверил → исправил".

1. `onebase query`:
   - compile + execute;
   - `--project`, `--id`, `--sqlite`, `--db`;
   - `--params JSON`;
   - `--limit`;
   - read-only by construction.

2. `onebase eval`:
   - expression/procedure snippet;
   - sandbox profile by default;
   - explicit `--commit` для мутаций, если потребуется.

3. `onebase widget explain <name>`:
   - query compile;
   - sample rows;
   - resolved chart mapping;
   - ECharts option summary;
   - JSON output for MCP.

4. `onebase report explain <name>`:
   - query/composition;
   - fields, groups, indicators, chart mapping;
   - sample rows.

5. Усилить `check`:
   - structured codes для типовых ошибок;
   - warning/lint rules для AI-ошибок;
   - `--fix` только для безопасных правок;
   - отдельный `--staging` API для генератора.

Критерии приёмки:

- ИИ может проверить запрос/выражение без создания временной обработки.
- Ошибки `check` имеют machine-readable code и понятный suggested fix.
- Виджет/отчёт можно объяснить одной CLI-командой без ручного SQL.

### Фаза 3. Генератор конфигуратора 2.0

Цель: перейти от "каркаса YAML" к рабочим feature slices.

1. Расширить tool-use генератора:
   - `создать_файл(path, content)` с whitelist по типам;
   - `прочитать_файл(path)`;
   - `список_объектов()`;
   - `проверить_конфигурацию(full=true)`;
   - `форматировать()`;
   - `прогнать_запрос()`;
   - `объяснить_impact()`.

2. Разрешить генерацию:
   - `.os` модулей;
   - managed forms;
   - reports/widgets/pages/subsystems/roles;
   - register movements в проведении документов.

3. Использовать песочницу при любых execution checks:
   - `RestrictedProfile`;
   - запрет net/file/exec по умолчанию;
   - короткий timeout;
   - bounded output.

4. Улучшить UI:
   - side-by-side diff;
   - partial apply;
   - inline edit;
   - visible check/tool trace;
   - "исправить ошибки";
   - after-apply migration prompt;
   - откат последней генерации.

5. Добавить golden сценарии генерации:
   - "справочник + документ с ТЧ + регистр + проведение";
   - "отчёт по регистру";
   - "виджет графика";
   - "форма объекта с событием";
   - fake LLM transcript, без реальных сетевых вызовов.

Критерии приёмки:

- По одному ТЗ создаётся рабочий вертикальный срез, проходящий `check`.
- Пользователь видит и может отредактировать каждый файл перед apply.
- После apply проект стартует/мигрирует без ручных исправлений в happy path.

### Фаза 4. MCP и агентная интеграция

Цель: сделать OneBase удобной целью для внешних ИИ-разработчиков.

1. `onebase mcp` stdio server:
   - resources: `ai-guide`, `describe`, `schema`, source tree;
   - tools: `check`, `fmt`, `query`, `eval`, `procrun`, `examples`, `impact`,
     `widget explain`, `report explain`.

2. Permission model:
   - read-only default;
   - write tools disabled unless explicitly enabled;
   - confirmation contract for mutations;
   - path whitelist.

3. Client docs:
   - Claude Code config;
   - Cursor/VS Code MCP config;
   - examples of prompts and safe workflow.

Критерии приёмки:

- MCP handshake проходит.
- Внешний агент может прочитать конфигурацию, запустить `check`, выполнить query/eval.
- Мутирующие операции недоступны по умолчанию.

### Фаза 5. Анализ влияния и качество изменений

Цель: сделать AI-правки безопасными на больших конфигурациях.

1. `onebase impact`:
   - object/field/procedure impact;
   - JSON + human output;
   - интеграция в генератор перед apply.

2. Refactoring helpers:
   - безопасное переименование поля/объекта с patch preview;
   - поиск всех ссылок;
   - migration notes.

3. Quality gates:
   - generated change must include check result;
   - generated change must include impact summary;
   - optional policy: forbid apply if check red.

Критерии приёмки:

- Перед удалением/переименованием поле показывает все затронутые формы, запросы,
  модули, отчёты, виджеты и роли.
- AI apply не может молча применить явно ломающий diff без предупреждения.

## Приоритеты

Рекомендуемый порядок:

1. `describe v2` + общий `aicontract`.
2. `onebase fmt`.
3. `onebase query` и `widget/report explain`.
4. `onebase schema`.
5. Генератор 2.0: `.os`, формы, отчёты, виджеты, full check.
6. MCP.
7. `impact` и refactoring helpers.

Обоснование: сначала надо дать ИИ точные знания и стабильный формат. Потом дать быструю
проверку гипотез. Только после этого расширять генерацию и открывать MCP, иначе внешний
агент будет быстрее делать больше неточных изменений.

## Быстрые задачи на 1-2 дня

1. [x] В `describe` заменить `builtins []string` на `langref.Descriptor` и добавить
   `schemaVersion`.
2. [x] В `describe` раскрыть `widgets` и `reports` структурами.
3. [x] Добавить `source` для файловых YAML-объектов через map path -> object.
4. [x] Исправить `docs/features.md`: настройки ИИ уже формовые, генератор пока не генерирует
   `.os` код.
5. [x] Добавить UI-настройку `ai.daily_token_cap` и `ai.max_tool_rounds` в форму ИИ.
6. [x] Добавить `onebase examples <kind>` как низкорисковую команду с canonical snippets.
7. [x] Добавить enforced self-correction loop генератора после full-check staging.

## Риски

- Генерация `.os` без полноценного sandbox/check loop быстро приведёт к опасным или
  неработающим предложениям. Сначала execution boundary и structured errors.
- `fmt` должен стать единым writer'ом для конфигуратора, иначе команда будет бороться
  с UI, а не помогать.
- MCP нельзя делать отдельной реализацией логики. Только thin wrapper над CLI/пакетами,
  иначе появится второй, расходящийся контракт.
- `describe v2` нужно версионировать сразу: внешние агенты и MCP начнут зависеть от JSON.

## Проверка текущего анализа

Локально проверены пакеты:

```bash
go test ./internal/llm ./internal/aicontext ./internal/configcheck ./internal/dsl/langref ./internal/launcher
```

Результат: PASS.

После первого среза дополнительно проверено:

```bash
go test ./internal/cli ./internal/launcher ./internal/llm ./internal/aicontext ./internal/configcheck ./internal/dsl/langref -count=1
go run ./cmd/onebase examples --list
go run ./cmd/onebase examples document
go run ./cmd/onebase describe --project examples/minimal
```

Результат: PASS.

После финального среза дополнительно проверено:

```bash
go test ./... -count=1
```

Результат: PASS.
