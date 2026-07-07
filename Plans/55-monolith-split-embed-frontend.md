# План 55 — Раскол монолитов и фронт в go:embed

**Статус:** 🟡 Этап 1 реализован (2026-06-10, ветка `refactor/split-ui-handlers`); этап 2 **Фаза 1** (конфигуратор на `html/template`, XSS-долг плана 47 §1.3 закрыт) — реализована 2026-06-19 (ветка `feature/55-configurator-htmltemplate`, см. `55-impl-htmltemplate-embed.md`); этап 2 **Фаза 2** (вынос CSS/JS конфигуратора в `/static` + go:embed, bootstrap `window.__cfg`/`__cfgI18n`) — реализована 2026-06-19 (`configurator_tmpl.go` 6726→2443 строк; `static/configurator.{css,js}`); этап 3 **Фаза 3a** (глобальные UI-скрипты из `tplHead` + общий reference picker в `/static/ui.js` через `go:embed`) — реализована 2026-07-07; этап 3 **Фаза 3b** (nav drawer/collapsible nav, dashboard/page/report charts через JSON bootstrap, runtime-настройки отчётов, сворачивание отчётов, dirty-watch формы и вложения) — реализована 2026-07-07; этап 3 **Фаза 3c** (`form-shared-js`: richtext/image/tablepart helpers/item picker + runtime списка: selection/context menu/tree lazy-load/feed) — реализована 2026-07-07; этап 3 **Фаза 3d** (runtime управляемых форм: `obFire`, ValueTable, SlickGrid, авто-`ПриОткрытии`, восстановление вкладок) — реализована 2026-07-07; этап 3 **Фаза 3e** (inline handlers `templates_managed.go` заменены на `data-ob-*` + delegated runtime) — реализована 2026-07-07; этап 3 **Фаза 3f** (layout/list inline handlers в `templates.go` заменены на `data-ob-*` + delegated runtime) — реализована 2026-07-07; этап 3 **Фаза 3g** (inline handlers обычной `page-form` заменены на `data-ob-*` + delegated runtime) — реализована 2026-07-07; этап 3 **Фаза 3h** (inline handlers `page-report` заменены на `data-ob-*` + delegated runtime) — реализована 2026-07-07; этап 3 **Фаза 3i** (inline handlers регистров, процессоров, констант и удаления помеченных заменены на `data-ob-*`) — реализована 2026-07-07; этап 3 **Фаза 3j** (inline handlers `page-journal` заменены на `data-ob-*`, runtime журнала вынесен в `/static/ui.js`) — реализована 2026-07-07; этап 3 **Фаза 3k** (query/dev и utility pages очищены от `onclick`/`onchange`/`oninput`, действия переведены на `data-ob-*`) — реализована 2026-07-07; остаток этапа 3 — inline handlers в админках.

> **Как реализовано (этап 1).** `ui/handlers.go` разнесён механически (as-is,
> вместе с doc-комментариями) с 3908 до 660 строк: `handlers_entity.go` (CRUD
> сущностей), `handlers_registers.go`, `handlers_reports.go`,
> `handlers_processors.go`, `handlers_journals.go`, `handlers_print.go`,
> `handlers_export.go`, `handlers_attachments.go`, `handlers_dsl.go`
> (DSL-хуки), `handlers_home.go`, `handlers_audit_enrich.go`. В handlers.go
> остались общие хелперы (can/requirePerm/render, resolve*/enrich*-помощники).
> Все тесты прошли без изменений — поведение не менялось.
**Источник:** `АнализПроекта-2026-06-10.md` §2.8; продолжение плана 43 («раскол монолитов»).
**Приоритет:** 🟠 Средний — не баг, но тормозит поддержку, review и онбординг.

---

## Контекст

Несколько файлов разрослись до размеров, мешающих работе:

| Файл | Строк | Функций | Что внутри |
|---|---|---|---|
| `internal/launcher/configurator_tmpl.go` | 4882 | 0 | HTML/JS как Go-строковые литералы |
| `internal/ui/handlers.go` | 3908 | 119 | почти все HTTP-обработчики UI |
| `internal/launcher/configurator.go` | ~3369 | 67 | логика конфигуратора |
| `internal/ui/templates.go` | 2512 | — | автоген-шаблоны + inline-JS (ИИ-виджет, панель сообщений) |

`ui` (15.8 K строк) и `launcher` (16.2 K) — крупнейшие пакеты. Проблемы:
- 119 хендлеров в одном файле → постоянные merge-конфликты, тяжёлый review;
- ~5 K строк HTML/JS в строковых литералах → нет подсветки, форматирования, eslint;
  опечатка в JS ловится только в рантайме в браузере;
- `text/template` в строках конфигуратора — исторический источник XSS-рисков (см. план 47, 1.3 «отложено»).

> Это чистый рефакторинг: **поведение не меняется**, поэтому главный риск — регресс. Делать
> механически, мелкими коммитами, с прогоном тестов и ручной проверкой страниц после каждого шага.

---

## Этап 1 — Разнести `ui/handlers.go` по доменам

Файл уже логически секционирован (комментарии-разделители). Перенести функции **as-is**
в новые файлы пакета `ui` (тот же package — внешний API не меняется):

| Новый файл | Что переносим (по префиксам функций) |
|---|---|
| `handlers_documents.go` | list/form/submit/post/unpost/delete сущностей |
| `handlers_registers.go` | register/inforeg/accountreg movements & balances |
| `handlers_reports.go` | reportForm/reportRun/reportExcel |
| `handlers_journals.go` | journalList/journalExcel |
| `handlers_attachments.go` | attachments* |
| `handlers_admin.go` | adminUsers/roles/sessions/audit/cleanup/scheduled |
| `handlers_print.go` | printDocument*/PDF/DSLPF |
| `handlers_misc.go` | about/logo/constants/messages |

Метод-ресиверы остаются `*Server`. `can/requirePerm/render` — общий `handlers_common.go`.

**Критерий готовности этапа:** `handlers.go` < ~600 строк (только общие хелперы);
`go build`, `go vet`, `go test ./internal/ui/...` — зелёные; диффы — чистый move (без правок логики).

---

## Этап 2 — Фронт конфигуратора в файлы + `go:embed`

Вынести HTML/JS/CSS из `configurator_tmpl.go` в реальные файлы и встраивать через `embed.FS`.

```
internal/launcher/assets/
  configurator.html
  configurator.js
  configurator.css
  forms.js
  widgets.js
```

```go
// internal/launcher/assets.go
//go:embed assets/*
var assetsFS embed.FS
```

Шаги:
1. Скопировать литералы в файлы as-is, проверить визуально каждую страницу.
2. Параметризацию (`fmt.Sprintf` с `%s`) заменить на:
   - статику — отдавать из `assetsFS` напрямую;
   - динамику — `html/template` с явным экранированием (закрывает XSS-долг плана 47, 1.3).
3. Подключить eslint/prettier к `assets/*.js` (отдельный шаг DX, см. план 56).

**Риск и смягчение.** `html/template` экранирует внутри `<script>` иначе, чем
строковая склейка (предупреждение из плана 47). Поэтому: данные для JS прокидывать как
JSON через `template.JS(jsonBytes)` (как уже делается в `dev_handlers.go:32`), а не
интерполяцией в текст скрипта. Проверять каждую страницу после переноса.

---

## Этап 3 — `templates.go` (ui): вынести inline-JS виджетов

Аналогично этапу 2 для `internal/ui`: ИИ-виджет (`templates.go:514-559`), панель
сообщений (`:560+`) и прочий крупный inline-JS вынести в `internal/ui/assets/*.js` +
`go:embed`, отдавать через существующий `/static/` маршрут (`mountStatic`).

Реализация 2026-07-07:
- добавлен embedded asset `internal/ui/static/ui.js`, отдаётся как
  `/static/ui.js` через `mountStatic`;
- из `tplHead` вынесены вкладочная shell-интеграция, AI-виджет, панель
  сообщений, PWA service-worker registration, `onebaseDevice` bridge и
  realtime/telephony toast listener;
- верхний общий `ref-picker-js` также вынесен в `/static/ui.js`;
- следующим срезом вынесены drawer навигации, сохранение раскрытых разделов
  меню, графики главной/страниц/отчётов через `<script type="application/json">`,
  runtime-настройки отчётов, сворачивание блоков отчёта, dirty-watch обычной
  формы и панель вложений через `data-attachments-url`;
- следующим срезом вынесены `form-shared-js` и runtime списка: richtext init,
  image upload/clear, добавление строк табличных частей, пересчёт итогов,
  item picker, selection/context menu, tree lazy-load и feed infinite scroll;
  шаблон оставляет только JSON bootstrap (`ob-tp-ref-*`, `ob-list-config`);
- следующим срезом добавлен embedded asset `internal/ui/static/managed.js`:
  из `templates_managed.go` вынесены `obFire`, применение значений/табличных
  частей/ValueTable, file picker, dirty/Esc handling, SlickGrid initializer,
  авто-`ПриОткрытии` и восстановление активной вкладки; шаблон оставляет JSON
  bootstrap (`ob-managed-*`) и vendor-подключения SlickGrid;
- следующим срезом `templates_managed.go` очищен от inline event attributes:
  `onclick`/`onchange`/`oninput`/`onfocus`/`onsubmit` заменены на `data-ob-*`,
  а `/static/managed.js` получил delegated listeners для `obFire`, ref picker,
  file picker, добавления/удаления строк, пересчёта ТЧ, dropdown toggle,
  popup cancel, confirm и `obGridSync`;
- следующим срезом из `templates.go` вынесены inline handlers layout/list:
  mobile nav toggle, системный dropdown, prevent-default developer submenu,
  кнопка действий списка, debounce поиска, ref picker фильтра, click/dblclick/
  contextmenu строк таблицы/дерева/плитки и lazy-loaded tree rows теперь
  вызываются через `data-ob-*` delegation в `/static/ui.js`;
- следующим срезом очищена обычная `page-form`: popup cancel, dropdown toggle,
  delete confirm, ref picker/current, image upload/clear, tablepart recalc/add/
  remove/ref buttons и attachments upload переведены на `data-ob-*` delegation;
  новые строки ТЧ, создаваемые `addTpRow`, также больше не генерируют inline
  handlers;
- следующим срезом очищена `page-report`: autosubmit выбора пресета/варианта,
  ref picker/current параметров отчёта, `rsBeforeSubmit`, `rsChoosePreset`,
  добавление и удаление строк отбора переведены на `data-ob-*` delegation;
- следующим срезом очищены шаблоны регистров, обработок, констант и удаления
  помеченных: ref picker/current и confirm переведены на уже существующие
  `data-ob-ref-picker`, `data-ob-ref-current`, `data-ob-confirm`;
- следующим срезом очищена `page-journal`: ref picker фильтра, сохранение
  настроек колонок, перемещение колонок и открытие строки журнала переведены на
  `data-ob-*`, а `jlMove`/`jlCollect`/`jlBeforeSubmit` вынесены в `/static/ui.js`;
- следующим срезом очищены query/dev и utility pages: `/ui/query-builder`,
  `/ui/dev/query-console`, `/ui/dev/code-console`, `/ui/dev/gengen`,
  `/ui/all-functions` и forbidden page больше не рендерят `onclick`/`onchange`/
  `oninput`, действия переведены на `data-ob-*` delegated listeners; модальный
  picker консоли запросов больше не держит тип справочника в устаревшем
  замыкании;
- оставшийся долг — inline handlers в админках. Их нужно выносить отдельным PR
  через явный bootstrap JSON/data-атрибуты и браузерную проверку конкретных
  экранов, а не механическим копированием.

---

## Тесты

- Существующие `internal/ui/*_test.go` и `internal/launcher/*_test.go` должны проходить
  **без изменений** — это доказательство, что рефакторинг не сменил поведение.
- Добавить smoke-тест рендера ключевых страниц конфигуратора (status 200, наличие
  якорных элементов) — `configurator_render_test.go`.
- eslint в CI на `assets/*.js` (план 56).

## Verification

1. `go build ./...`, `go vet ./...`, `go test ./...` — зелёные после каждого этапа.
2. Ручной обход конфигуратора: дерево метаданных, редактор YAML/форм, бэкапы, ИИ-страница,
   виджеты — всё рендерится и работает как до рефакторинга.
3. `git diff --stat` по этапу 1 — почти чистые перемещения (move), без правок тел функций.

## Связанные

- План 43 — «раскол монолитов» (это его конкретизация и продолжение).
- План 47 (1.3) — переключение конфигуратора на `html/template` (XSS) — закрывается этапом 2.
- План 56 — eslint/prettier в CI для вынесенного фронта.

## Эстимейт

- Этап 1 (handlers.go): **1 день** (механически, но с проверкой).
- Этап 2 (фронт конфигуратора + html/template): **2–3 дня** (осторожно, по странице).
- Этап 3 (ui inline-JS): **1 день**.
- **Итого ≈ 4–5 дней.** Делается независимо от остальных планов, мелкими коммитами.
