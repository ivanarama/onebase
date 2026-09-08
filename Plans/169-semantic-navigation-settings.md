# Этап 169 — Смысловая навигация и настраиваемое меню

## Контекст

План подготовлен для [issue #1362](https://github.com/ivanarama/onebase/issues/1362).
Автор строит школьное приложение с сотнями объектов и описывает навигацию так,
как её видит конечный пользователь: «Классы», «Учебные годы», «Приказы»,
«Журналы». OneBase сегодня показывает устройство движка: «Справочники»,
«Документы», «Регистры», «Обработки». Объекты одной задачи оказываются в разных
разделах, вложенных смысловых групп нет, а порядок из конфигуратора заменяется
алфавитной сортировкой.

Triage подтвердил все четыре части заявки по коду. `metadata.SubsystemContents`
хранит восемь отдельных списков по типам. `internal/ui/server.go` превращает их
в жёстко названные `navGroup`, предварительно сортируя registry-объекты по
внутреннему имени. `internal/launcher/tree_order.go` уже хранит ручной порядок,
но применяет его только к дереву конфигуратора; runtime его не читает. Общие и
персональные настройки уже имеют безопасный прецедент в `_settings`, однако
навигационного контракта там нет.

Выбран **вариант 3**: разработчик задаёт смысловую базу в конфигурации, администратор
накладывает общую для базы настройку в режиме «Предприятие», пользователь — свою
персональную настройку. Каждый слой хранит только дельту и сбрасывается к
предыдущему уровню. Это закрывает переименование разделов, смешанные по типам
папки, порядок и настройку без конфигуратора.

Issue #1368 (представления объектов в меню) и #1369 (сортировка по внутреннему
имени при показе представления) остаются самостоятельными дефектами. Их
исправления должны использовать общий resolver этого плана, если будут влиты
позже, но не входят в критерии закрытия #1362.

## Граница и зависимости

План реализуется несколькими небольшими PR. До начала продуктового среза A
обязателен bootstrap частичных FIX-срезов из плана 163 / issue #1379:

- промежуточные PR A–E используют `Part of #1362` и публикуют доказуемый маркер
  следующего среза;
- только последний PR F использует `Fixes #1362`;
- пока bootstrap не влит, автоматический FIX не должен создавать первый
  продуктовый PR: текущая процедура требует closing keyword и преждевременно
  закроет issue после одного среза;
- отсутствие bootstrap нельзя обходить одним гигантским PR: это ухудшит review,
  откат и безопасность настроек.

Не входят в первую версию: произвольные внешние URL, пункты с исполняемым HTML,
условия видимости на DSL, изменение объектного RBAC, меню мобильного клиента как
отдельный контракт и импорт структуры команд из конфигурации 1С. Для внешней
ссылки разработчик использует существующую DSL-страницу с проверяемым маршрутом.

## Базовый YAML-контракт

`contents` остаётся источником членства объектов в подсистеме. Для глобальной
«Главной» ту же роль выполняет существующий `home_page.nav`; его
nil/пустая форма сохраняет особую legacy-семантику «все объекты глобального
плоского меню». Новый `menu` задаёт только представление и может смешивать типы:

```yaml
name: ОбразовательнаяДеятельность
title: Образовательная деятельность
contents:
  catalogs:
    - УчебныеГоды
    - ПериодыОбучения
    - Классы
  documents:
    - ПриказОНачалеУчебногоГода
  inforegs:
    - РасписаниеЗвонков
    - ГрафикКаникул
  processors:
    - КлассныйЖурнал

menu:
  sections:
    - id: academic-years
      title: Учебные годы
      titles:
        en: Academic years
      icon: calendar-days
      items:
        - id: academic-years-list
          target: catalog:УчебныеГоды
      groups:
        - id: periods
          title: Периоды обучения
          items:
            - id: periods-list
              target: catalog:ПериодыОбучения
        - id: schedules
          title: Расписание и каникулы
          items:
            - id: bells
              target: inforeg:РасписаниеЗвонков
            - id: vacations
              target: inforeg:ГрафикКаникул
        - id: orders
          title: Приказы по учебному году
          items:
            - id: opening-order
              target: document:ПриказОНачалеУчебногоГода
    - id: journals
      title: Журналы
      items:
        - id: class-journal
          target: processor:КлассныйЖурнал
          title: Классный журнал
```

Первая версия допускает ровно три уровня: section → optional group → item.
Произвольная рекурсия запрещена, чтобы не получить плохо управляемое дерево,
неоднозначный keyboard UX и неограниченную стоимость merge дельт. Section и
group могут одновременно иметь собственные `items`.

Поля узлов:

- `id` — обязательный стабильный ASCII slug `[a-z][a-z0-9-]{0,62}`, уникальный
  среди section/group/item одного menu-context;
- `title`, `titles`, `icon` — те же правила локализации и нормализации иконок,
  что у подсистем; item без override использует `DisplayName` цели;
- `target` — типизированная ссылка, а не URL и не строка шаблона;
- порядок YAML-массивов является наблюдаемым порядком.

Грамматика `target`:

```text
catalog:<name>
document:<name>
register:<name>:movements
register:<name>:balances
inforeg:<name>
report:<name>
processor:<name>
journal:<name>
page:<name>
system:constants
```

URL, подпись по умолчанию и требуемое действие RBAC строит единый серверный
resolver. Пользовательский YAML/JSON никогда не поставляет URL. Один target
может осознанно встречаться несколько раз в разных смысловых местах, но у
каждого вхождения отдельный `id`; повтор в одном parent даёт предупреждение.

`menu` разрешён в `subsystems/*.yaml` и для глобального контекста в
`config/home_page.yaml`. В подсистеме, если `menu` отсутствует, действует
legacy-генератор из `contents`. Если `menu` есть, члены `contents`, не
упомянутые ни в одном item, автоматически попадают в стабильную системную
секцию `other` («Другое»). Поэтому добавленный разработчиком объект не исчезает
у пользователей с сохранёнными настройками. Намеренно убрать объект из
навигации можно явной hide-операцией слоя или удалением его из `contents`; это
не меняет доступ по прямому URL.

Для глобального контекста действует отдельная точная матрица совместимости:

| `home_page.nav` | `home_page.menu` | Допустимое множество и fallback |
|---|---|---|
| отсутствует или задан пустым | отсутствует | Точный текущий `buildFlatNav`: все читаемые catalog/document, обе проекции register, inforeg, report, processor и journal; `system:constants` — только при наличии констант. Порядок внутри технических групп остаётся алфавитным. |
| непустой | отсутствует | Точный текущий `buildNavFromContents`: только перечисленные в `nav` объекты, включая pages; один register даёт movements и balances. Технические группы и их fallback не меняются. |
| отсутствует или задан пустым | присутствует | Допустимое множество равно target-множеству текущего `buildFlatNav`; все допустимые, но не упомянутые в `menu` targets попадают в `cfg:other`. |
| непустой | присутствует | Допустимое множество строится только из `nav`; все не упомянутые в `menu` члены `nav` попадают в `cfg:other`. Target вне `nav` — ошибка `check`. |

Nil и явно пустой `nav` намеренно эквивалентны, как сейчас в
`HomePage.Nav != nil && !HomePage.Nav.IsEmpty()`: пустой блок не означает
«скрыть всё». `other` создаётся только для semantic `menu`; в чистом legacy
fallback сохраняются существующие технические группы. `system:constants`
разрешён только в глобальном контексте, только если в registry есть хотя бы
одна константа и только при nil/пустом `nav`; в подсистеме и при непустом
глобальном `nav` этот target отвергается. Admin/user delta никогда не меняет
эту область допустимых targets.

## Нормализованная модель

Добавляется чистый пакет `internal/navigation`, не зависящий от HTTP и SQL:

```go
type Tree struct {
    Context  string
    Sections []Section
}

type Section struct {
    ID, Title, Icon string
    Titles         map[string]string
    Items          []Item
    Groups         []Group
}

type Item struct {
    ID, Target, Title string
    Titles            map[string]string
}

type Delta struct {
    Version  int
    BaseHash string
    Ops      []Operation
}
```

Метаданные разбираются в эту модель, затем resolver проверяет target и создаёт
runtime DTO с `ID`, `Label`, `URL`, `Children`, `Open`. `navGroup.Kind` больше
не служит идентичностью: перевод или rename не должны сбрасывать сохранённое
состояние раскрытия. DOM получает `data-nav-id`, построенный из стабильных IDs.

Все преобразования детерминированы. Canonical JSON и `BaseHash` используют
versioned schema, UTF-8, стабильный порядок полей и SHA-256. Hash — средство
обнаружения смены базы, а не причина выбросить всю пользовательскую настройку.

## Три слоя и алгоритм merge

Эффективное меню вычисляется так:

```text
configuration menu (или legacy projection из contents)
  → admin delta базы
  → personal delta текущего пользователя
  → object/row-independent RBAC filter
  → удаление пустых group/section
  → localized runtime DTO
```

Дельта содержит операции над ID, а не полную копию дерева:

```json
{
  "version": 1,
  "base_hash": "sha256:...",
  "ops": [
    {"op":"rename", "node":"cfg:academic-years", "title":"Учебный год"},
    {"op":"move", "node":"cfg:class-journal", "parent":"cfg:journals", "after":"cfg:attendance"},
    {"op":"hide", "node":"cfg:opening-order"},
    {"op":"add_group", "id":"adm:<uuid>", "parent":"cfg:academic-years", "title":"Контроль"}
  ]
}
```

Поддерживаются `rename`, `move`, `hide`, `show`, `add_section`, `add_group`,
`remove_custom` и `set_icon`. Сервер принимает от UI желаемое дерево, повторно
проверяет права и сам вычисляет минимальную дельту к предыдущему уровню. Клиент
не формирует SQL key и не присылает готовый effective tree как источник истины.

Инварианты merge:

1. Config IDs получают namespace `cfg:`, созданные администратором — `adm:`,
   персональные — `usr:`. UUID для custom node создаёт сервер.
2. Пользовательская дельта применяется к уже скрытому/переставленному admin
   дереву. Она не может вернуть item, скрытый администратором, сослаться на
   config target вне admin tree или менять admin custom node для других людей.
3. Новая config-нода, которой нет в старой дельте, появляется в canonical
   позиции. Настройка никогда не замораживает старую копию меню.
4. Операция на удалённый/переименованный ID игнорируется fail-soft, считается
   stale и показывается в настройке как диагностика. По title или похожему имени
   автоматически не перепривязываем: это может открыть другой объект.
5. Цикл parent, выход за максимальную глубину, duplicate ID, неизвестная
   операция или превышение лимита отвергают весь новый JSON до записи.
6. RBAC применяется после layout merge, но visibility монотонна: настройки
   меняют навигацию, не полномочия. Пустые родители исчезают только из DTO.
7. Два параллельных сохранения не перетирают друг друга. Форма передаёт revision
   текущего raw JSON; storage делает compare-and-swap и отвечает `409 Conflict`
   при устаревшей вкладке.

## Хранение

Новая таблица не требуется. `_settings` получает versioned JSON:

```text
ui.navigation.admin.<len>:<context>
ui.navigation.user.<len>:<login>.<len>:<context>
```

`context` — `global` или внутреннее имя подсистемы. Length-prefix повторяет
проверенный приём report/journal settings и исключает коллизии точек в именах.
Логин берётся только из `auth.UserFromContext`; значение из формы не выбирает
чужой ключ.

Storage API возвращает raw JSON и revision, предоставляет `Get`, CAS `Save` и
`Delete`. Отсутствие ключа означает наследование предыдущего слоя. Пустой массив
ops канонизируется в DELETE, а не хранится как вечный override.

Ограничения первой версии: не более 64 KiB JSON на слой/context, 1000 операций,
100 sections, 500 groups и 5000 item occurrences после merge. Проверки идут до
SQL; SQLite и PostgreSQL имеют одинаковые CAS-сценарии. Full JSON не пишется в
обычный лог или audit payload.

## HTTP и UX-контракт

Фиксированные маршруты, которые сами не зависят от настраиваемого меню:

```text
GET  /ui/settings/navigation?subsystem=<name>
POST /ui/settings/navigation/save
POST /ui/settings/navigation/reset

GET  /ui/admin/navigation?subsystem=<name>
POST /ui/admin/navigation/save
POST /ui/admin/navigation/reset
```

User route работает только с текущим логином. Admin route на каждом handler
повторяет fail-closed `s.isAdmin(r)` и пишет общую дельту базы. POST использует
существующие same-origin/CSRF ограничения UI, `http.MaxBytesReader`, revision и
Post/Redirect/Get. Reset удаляет ровно один key и возвращает к предыдущему
уровню: user → admin → configuration.

Редактор показывает слева effective preview, справа палитру доступных объектов.
Можно создать section/group, переименовать, выбрать icon, переместить, скрыть и
вернуть пункт. Drag-and-drop — progressive enhancement: у каждого узла есть
кнопки «выше/ниже/внутрь/наружу», работающие без pointer и пригодные для
клавиатуры. Focus после операции сохраняется, live-region сообщает результат.

Администратор видит banner «общая настройка базы» и число stale rules.
Пользователь видит «моя настройка» и источник каждого узла: конфигурация или
администратор. Кнопка reset требует подтверждения и заранее показывает уровень,
к которому произойдёт возврат. Ссылка «Настроить меню» находится в фиксированном
user/profile chrome, а «Настройка приложения → Навигация» — в фиксированном
admin chrome; эти ссылки нельзя скрыть самой дельтой.

Сохранение не принимает arbitrary HTML/CSS/URL. Titles рендерятся стандартным
`html/template`, JSON для JavaScript кодируется сервером и вставляется безопасным
способом. Preview выполняет тот же target resolver и RBAC, что production menu.

## Наблюдаемая семантика и инварианты

1. Проект без `menu` и без navigation keys запускается без миграции. Группы и
   доступность остаются legacy; внутри подсистемы порядок `contents` становится
   объявленным порядком вместо сортировки по внутреннему имени. Глобальный flat
   nav без `contents` остаётся алфавитным.
2. `tree_order.yaml` остаётся порядком дерева конфигуратора и не становится
   скрытой runtime-зависимостью. Новый редактор может явно импортировать его как
   начальную раскладку, после чего материализует результат в `menu`.
3. `contents` определяет допустимое множество targets. `menu` не может добавить
   объект из другой подсистемы; admin/user слой также не расширяет membership.
4. Один resolver задаёт label, localized title, URL и RBAC action для legacy,
   YAML menu, admin preview и user preview. Параллельных switch по kind быть не
   должно.
5. Register movements и balances — разные targets и могут находиться в разных
   папках. Периодический info register использует `DisplayName`, а не внутреннее
   имя; этим новый путь не воспроизводит дефект #1368.
6. Настройка не является ACL. Скрытие не запрещает прямой URL, показ не обходит
   `s.can`; изменение ролей отражается при следующем запросе без переписывания
   дельты.
7. Невалидная сохранённая дельта не ломает всё приложение: сервер логирует
   безопасную диагностику без содержимого, пропускает повреждённый слой и
   рендерит предыдущий. Новый невалидный ввод, напротив, получает 400 и не
   сохраняется.
8. Добавление объекта или config-ноды после сохранения пользовательской дельты
   делает её видимой автоматически. Удаление цели не создаёт битую ссылку.
9. Rename title не меняет ID и не сбрасывает порядок/open state. Rename
   внутреннего имени metadata target в v1 считается удалением старой цели и
   требует явного исправления stale rule; автоматическое угадывание запрещено.
10. Каждый admin save/reset пишет audit event с context, revision before/after и
    количеством операций, но без полного дерева. Персональный save не попадает
    в административный журнал содержимым.

## Инвентаризация затронутых границ

- `internal/metadata/subsystem.go` и модель global home page — YAML `menu`,
  стабильные IDs, localized titles, typed targets;
- новый `internal/navigation` — normalization, legacy projection, target parser,
  deterministic delta, hash, limits и diagnostics;
- `internal/configcheck/lint.go`, `internal/configcheck/cross_refs.go` — schema,
  IDs, targets, membership, duplicate/stale-prone конструкции;
- `internal/ui/server.go` — единый resolver и применение трёх слоёв вместо
  восьми независимых sort/build веток;
- `internal/ui/templates.go`, `internal/ui/static/ui.js` — section/group/item,
  стабильные DOM IDs, keyboard и collapse state;
- `internal/storage/settings.go` — collision-safe keys, JSON validation, CAS и
  delete для admin/user layers;
- `internal/backup/universal.go` — безопасные navigation-prefix при export,
  очистка прежних переносимых navigation keys и import для clone/disaster
  restore;
- новые handlers рядом с `internal/ui/admin.go` и регистрация маршрутов в
  `internal/ui/server.go` — страницы общей и персональной настройки;
- `internal/launcher/configurator_types.go`,
  `internal/launcher/configurator_home_app.go`,
  `internal/launcher/configurator_tmpl_tree.go` — menu editor и точечное
  сохранение YAML без потери неизвестных ключей/комментариев;
- `internal/launcher/tree_order.go` — только явный import в menu, без чтения
  runtime-сервером;
- `internal/ui/subsys_access_test.go` и новые navigation HTTP/DOM tests —
  публичная семантика и RBAC;
- `DEVELOPER.md`, документация подсистем/форм, `docs/features.md`, AI guide и
  пример школьной подсистемы — один публичный контракт.

## Совместимость и миграция

- SQL migration не нужна: `_settings` уже существует. Новые keys игнорируются
  старой версией OneBase.
- YAML `menu` opt-in. Старые конфигурации продолжают работать; единственное
  намеренное изменение legacy subsystem path — уважение порядка массивов
  `contents`. Это отмечается в CHANGELOG.
- Configurator не создаёт `menu` при обычном сохранении старой формы. Пользователь
  нажимает отдельное «Создать смысловое меню из текущего», видит preview и только
  затем записывает YAML.
- Импорт строит технические sections из текущего `contents`, применяет порядок
  массивов и использует `tree_order` только как подсказку для ещё не упорядоченных
  элементов. Исходный `contents` не удаляется.
- Admin/user delta содержит `base_hash`, но при обновлении конфигурации валидные
  ops применяются к новой базе, новые nodes появляются, stale ops видны для
  cleanup. Нет destructive auto-migration.
- Downgrade: удалить `menu` из YAML для возврата старого UI. Navigation keys в
  `_settings` можно оставить — старая версия их не читает — либо удалить через
  reset перед downgrade. `contents` остаётся полным fallback.
- Универсальный `.obz` не переносит `_settings` целиком: сейчас export/import
  допускает только `safeSettingKeys`, `exchange.this_node.*` и
  `scheduled.enabled.*`. Срез D добавляет два точных безопасных семейства
  `ui.navigation.admin.` и `ui.navigation.user.` в export, import и
  `clearPortableSettings`; остальные `_settings`, включая секреты, по-прежнему
  не попадают в архив.
- Оба navigation-prefix переносятся одинаково в режимах clone и disaster
  recovery: это раскладка интерфейса восстановленных данных и пользователей,
  а не идентичность узла или секрет. Перед import оба режима удаляют прежние
  navigation keys целевой базы и затем восстанавливают точный набор из архива;
  отсутствие ключа в архиве не оставляет старый target override. После restore
  на конфигурацию с другим `base_hash` resolver применяет только валидные ops и
  показывает stale diagnostics.

## Последовательность небольших PR-срезов

<!-- pp:plan-slice key=A next=B -->
### Срез A — контракт, legacy order и pure resolver

Добавить YAML-модель, target grammar, `internal/navigation`, legacy projection,
normalization и `onebase check`. Runtime ещё не переключать на semantic tree,
кроме отдельной правки: subsystem legacy projection соблюдает порядок
`contents`, global flat nav остаётся алфавитным. Обновить schema и документацию.

Публичные тесты: `onebase check --project <fixture>` принимает mixed-kind menu;
unknown target, target вне contents, duplicate ID, depth > 3, неверный register
view и arbitrary URL дают точные diagnostics. Pure resolver доказывает порядок
YAML, section/group/items, `other`, duplicate occurrence с разными IDs и
стабильный canonical hash. HTTP-тест legacy subsystem фиксирует порядок
`contents` и прежний fallback без `menu`. Отдельная table-driven матрица
глобальной «Главной» покрывает nil/пустой/непустой `home_page.nav` с
отсутствующим/присутствующим `menu`, состав `other`, обе register-проекции,
pages из непустого `nav` и запрет `system:constants` вне разрешённого
глобального контекста.

<!-- pp:plan-slice key=B next=C -->
### Срез B — runtime semantic menu и RBAC

Перевести `buildNavForSubsystem` и scoped global nav на общий resolver. Расширить
DTO/template до section → group → item, заменить title-based DOM identity на ID,
сохранить collapsible behavior. Не добавлять storage и editor.

Публичные HTTP/DOM-тесты: школьная fixture смешивает catalog/document/inforeg/
processor в одной папке; register views ведут на разные URL; ru/en titles;
обычная роль не видит закрытый object, admin видит; пустые родители исчезают;
HTML в title экранируется; прямой URL по-прежнему проверяется старым RBAC. Проект
без `menu` даёт прежние labels/URLs и не требует настройки.

<!-- pp:plan-slice key=C next=D -->
### Срез C — визуальный редактор конфигурации

Добавить в конфигуратор отдельный semantic menu editor, palette из `contents`,
drag + keyboard controls, preview и explicit legacy/tree-order import. Сохранять
только `menu` через `updateYAMLMapping`; существующие `titles`, `roles`,
`home_page`, комментарии и неизвестные ключи не пересобирать.

Публичные тесты: file/database mode round-trip одного YAML; import не пишет до
подтверждения; reorder/mixed group/rename/icon сохраняются; удалённый из palette
item попадает в `other`; malformed form body и превышение лимита не меняют файл;
JS behavior test покрывает pointer и keyboard reorder, focus и preview. Обычное
сохранение старой subsystem form не удаляет новый `menu`.

<!-- pp:plan-slice key=D next=E -->
### Срез D — versioned delta и CAS storage

Добавить admin/user keys, JSON codec, minimal diff, deterministic merge, stale
diagnostics, size/count limits и compare-and-swap. UI пока не подключать.

Публичные matrix-тесты SQLite/PostgreSQL: save/get/delete; collision-safe login и
context; stale revision → 409-equivalent domain error; config → admin → user
precedence; admin hide необратим пользователем; new config node появляется;
removed node становится stale; corrupt stored JSON откатывает ровно один слой;
циклы/глубина/лимиты не записываются; параллельные writers не теряют update.
Тесты универсального backup/restore создают admin и user navigation keys плюс
небезопасный посторонний setting и проверяют оба режима: clone и disaster
recovery переносят оба navigation-слоя byte-for-byte, удаляют отсутствующие в
архиве старые navigation keys цели и не экспортируют посторонний setting.

<!-- pp:plan-slice key=E next=F -->
### Срез E — общая настройка администратора

Добавить фиксированную страницу «Настройка приложения → Навигация», admin-only
handlers, preview, save/reset и audit. Сервер принимает desired tree, вычисляет
дельту к configuration и пишет её CAS-методом.

Публичные HTTP-тесты: anonymous/non-admin получают 403 и не меняют `_settings`;
admin rename/move/add group видят два разных пользователя; reset возвращает
configuration; stale tab получает 409 без overwrite; audit содержит actor,
context и revisions, но не JSON; повреждённый stored layer показывает warning и
безопасный configuration preview.

<!-- pp:plan-slice key=F next=done -->
### Срез F — персональная настройка и end-to-end

Добавить фиксированную «Настроить меню», user handlers и личный reset. Сервер
вычисляет user delta к effective admin tree и после каждого запроса применяет
актуальный RBAC. Добавить школьную end-to-end fixture и полный user-facing текст.
Этот финальный PR использует `Fixes #1362`.

Публичные HTTP/browser-тесты: Alice и Bob независимо меняют порядок; admin rename
наследуется обоими, пока user override не переименовал свой node; user reset
возвращает admin, admin reset — config; user не возвращает admin-hidden item и
не видит закрытый RBAC target; новая config-нода появляется у обоих после
reload; concurrent tab conflict виден; restart и backup/restore сохраняют слои;
мышь и клавиатура проходят один сценарий; malicious titles остаются текстом.

## Verification

1. Создать fixture «Школа» из YAML выше и выполнить `onebase check --project`.
2. Открыть подсистему ограниченной ролью: увидеть «Учебные годы» с папками и
   mixed-kind items в заданном порядке, без технических названий типов.
3. В конфигураторе импортировать legacy menu, переставить объекты keyboard-only,
   сохранить и повторно открыть file- и database-проект без потери YAML
   комментариев.
4. Администратором добавить общую папку, переименовать section и скрыть item;
   проверить одинаковый результат у двух пользователей и audit revisions.
5. Alice создать личную папку и порядок; Bob оставить наследование. Перезапустить
   сервер и убедиться, что состояния независимы.
6. Изменить configuration menu: добавить новый target, удалить старый и сменить
   title. Новый target появляется, старый op отмечается stale, title change не
   сбрасывает ID/order.
7. Одновременно сохранить из двух вкладок: вторая получает conflict и preview
   актуальной версии, первая настройка не теряется.
8. Снять у Alice право на item после сохранения дельты: пункт исчезает сразу;
   прямой URL также запрещан существующим RBAC.
9. Сломать admin JSON вручную: приложение показывает configuration menu и
   диагностирует слой, но остальные страницы работают. Reset восстанавливает
   нормальный merge.
10. Проверить SQLite и PostgreSQL, затем backup/restore и documented downgrade.

Команды реализации по срезам: целевые `go test` для `internal/metadata`,
`internal/navigation`, `internal/configcheck`, `internal/storage`,
`internal/launcher`, `internal/ui`; browser/Node behavior tests; затем
`go test ./...`, `go build ./...`, `go vet ./...`, `git diff --check` и
`go run ./cmd/onebase check --project <school-fixture>`.

## Риски и откат

- **Настройка скрывает новый объект навсегда.** Храним ops, не snapshot; новые
  config nodes добавляются, unplaced contents идут в `other`.
- **Настройка становится обходом RBAC.** Membership и access проверяются
  server-side после каждого merge; target не содержит URL; user layer не может
  вернуть admin-hidden node.
- **Rename metadata перепривязывает правило не туда.** IDs не угадываются по
  title; stale op виден и удаляется явно.
- **Два администратора теряют изменения.** CAS revision и 409 вместо last-write
  wins; UI умеет reload/reapply.
- **XSS/phishing из названий и ссылок.** Только typed target, `html/template`,
  safe JSON, запрещены arbitrary URL/HTML/CSS.
- **Слишком глубокое/большое меню замедляет каждый запрос.** Глубина 3, пределы
  nodes/ops/bytes, deterministic O(nodes + ops) merge; parsed delta можно
  кэшировать по revision, но RBAC-фильтр остаётся per-request.
- **Configurator перезаписывает новый YAML.** Только точечный YAML mapping update
  и постоянный round-trip test неизвестных ключей/комментариев.
- **Откат к старой версии.** `contents` остаётся полным fallback, navigation
  settings keys изолированы. Сначала убрать runtime use/deltas, затем `menu`;
  SQL rollback не нужен.
- **Частичные PR закрывают issue раньше времени.** Жёсткая зависимость от Plan
  163/#1379, `Part of` до финального среза и `Fixes` только в F.

Откат выполняется в обратном порядке: отключить user handlers → удалить admin
handlers → прекратить чтение delta keys → вернуть legacy renderer → удалить
`menu` из YAML. Каждый шаг сохраняет `contents`; оставшиеся `_settings` keys
безопасно игнорируются и могут быть удалены reset-командой позже.

## Критерии завершения

- все четыре пользовательских требования #1362 доступны без знания типов
  объектов и без ручного редактирования базы;
- configuration, admin и user слои имеют документированный приоритет и отдельный
  reset к предыдущему уровню;
- настройки хранят дельту, новые объекты не исчезают, stale rules наблюдаемы;
- object membership и RBAC нельзя расширить настройкой;
- смешанные folders, localization, ordering, register views и empty-group
  pruning проходят публичный HTTP/DOM contract;
- admin endpoints fail-closed, user identity берётся из auth context, concurrent
  save не теряет данные;
- file/database configurator, SQLite/PostgreSQL storage, restart, backup/restore
  и downgrade покрыты проверками;
- legacy проекты без `menu` запускаются без миграции, а намеренная смена порядка
  `contents` отражена в CHANGELOG;
- срезы A–E не закрывают issue, финальный F закрывает #1362 после end-to-end;
- документация, AI guide и `docs/features.md` описывают тот же контракт, что код.

## Эстимейт

- bootstrap Plan 163/#1379: внешняя обязательная зависимость;
- срез A, contract/resolver/check/order: 2–3 дня;
- срез B, runtime/RBAC/templates: 2–3 дня;
- срез C, configurator editor: 3–4 дня;
- срез D, delta/CAS/storage matrix: 2–3 дня;
- срез E, admin UI/audit: 2–3 дня;
- срез F, personal UI/end-to-end/docs: 2–3 дня.

Итого после bootstrap: **13–19 дней**. План намеренно не сжимается в один PR:
каждый слой отдельно проверяем, откатываем и допускаем к следующему только после
review предыдущего.
