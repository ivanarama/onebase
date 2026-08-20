# План 149б — Визуальный конструктор форм: доводка (follow-up #164)

> Номер сменён с 71 на 149 (issue #1035): под 71 жили разные планы, и ссылка «план 71» перестала быть однозначной. Номер 71 остался за планом «Оформление вывода отчёта» (71-report-appearance). Ссылки «план 71» на эту тему читать как «план 149».

Продолжение плана `149-visual-form-designer.md`. MVP (слайсы 1–6) влит PR #187 в
`feature/164-visual-form-designer`: round-trip `internal/formdoc`, холст
(`renderFormCanvas`), эндпоинт `POST /forms/edit-op` (op `render`/`setProp`/
`insert`/`move`), клиент (палитра-drag на холст, панель свойств, синхронизация с
Monaco). Этот план — отложенные пункты + баг навигации, найденный при тестировании.

Архитектура без изменений: команда → `/forms/edit-op` → formdoc правит `yaml.Node`
→ `{yaml, canvasHtml, selectedId, model}`. Новые операции/виды встраиваются в тот
же конвейер. TDD, как в плане 149.

## A. Баг: «← В конфигуратор» открывает не тот объект
Симптом: возврат из редактора формы ведёт на корень конфигуратора, теряя объект,
из которого зашли.

Причина: back-link статичен — `internal/launcher/forms_tmpl.go:95`
(`<a href="/bases/{{.Base.ID}}/configurator">`), а форма открывается из узла дерева
объекта (`/configurator?tab=tree&select=e-<Entity>`, см. `treeNodeID`,
`configurator.go:2603`; обработка `select` — `configurator.go:489`).

Фикс — пробросить контекст происхождения (`from`):
- Ссылки на `forms/edit` добавляют `&from=<nodeID>`:
  `configurator_tmpl.go:634,1314,1327,2122,2139,2143` и forms-list в `forms_tmpl.go`.
  nodeID = `treeNodeID(kind, entity)` (`e-<Entity>` для справочника/документа;
  где доступен `$e` — считать `treeNodeID($e.Kind,$e.Name)`; иначе дефолт `e-<Entity>`).
- `configuratorFormsEdit` (`forms_handlers.go:248`) читает `from`, кладёт в
  `configuratorData`; шаблон рендерит back-link как
  `/configurator?tab=tree&select={{from}}`, фолбэк `e-{{.EditingForm.Entity}}`.
- Сохранить `from` в hidden-полях save-формы и в редиректах save/preview/delete
  (`forms_handlers.go:435,441,507`), чтобы после сохранения контекст не терялся.

Тест: рендер редактора с `?from=e-X` → back-link `select=e-X`; без `from` → фолбэк
`e-<Entity>`. Мелкий быстрый фикс — можно отдельным PR вперёд остальных.

## B. Удаление и перестановка элементов
**B1. Удаление.** Сейчас op удаления НЕТ (слайс 3 — только SetProp/Insert/Move).
- formdoc: `func (d *Doc) DeleteElement(nodeID string) error` в `ops.go` — через
  `locate(nodeID)` вырезать узел из родительской sequence (контейнер с детьми
  удаляется целиком). Тест: комментарии соседей целы, round-trip валиден.
- эндпоинт: `case "delete"` в `applyEditOp` (`forms_editop.go`), `selected=""`.
- клиент: кнопка «Удалить элемент» в панели свойств → `op:delete`; для контейнера
  с детьми — `confirm`.

**B2. Перестановка.** `Move` уже реализован и протестирован на сервере (слайс 3),
в клиент не выведен. Варианты:
- MVP (надёжнее): кнопки ↑/↓ в панели свойств → `op:move` в соседний индекс того
  же родителя.
- drag-reorder (опц.): `.fc-el` → `draggable`, drop на `.fc-drop` шлёт `op:move`
  {node,parent,index}. Использовать СВОЙ mime (`text/onebase-node`), чтобы не
  пересекаться с палиткой (`text/onebase-attr`); dragover-зоны различать по типу.

Тесты: applyEditOp delete (узел исчез, в `model` его нет); HTTP move меняет порядок.

## C. Операции с группами и страницами
- Палитра структурных элементов (новые чипы рядом с палитрой реквизитов):
  «Группа», «Страницы»/«Страница», «Надпись». Drop на холст → `op:insert` с нужным
  kind (группа — `children: []`).
- Заголовок/имя группы/страницы — уже через панель свойств (`title.ru`).
- Добавление страницы в `СтраницыФормы`: в рендере холста у `fc-pages` сейчас нет
  drop-зон уровня страниц — добавить зону для вставки `kind:Страница`
  (`forms_canvas.go`, ветка `FormElementPages`).

## D. Табличные части на холсте + состав колонок
- **Палитра ТЧ.** Расширить источник палитры: сейчас `objectScaffoldAttrs`
  (`forms_handlers.go:313`) отдаёт только `entity.Fields`. Добавить секцию
  «Табличные части» из `entity.TableParts` (`metadata/types.go:117`).
- **Drop ТЧ.** chip ТЧ → `op:insert` kind:ТабличнаяЧасть, name:Таб<Name>,
  data_path:Объект.<Name> (формат — как `examples/crm/forms/КоммерческоеПредложение.form.yaml:128`).
- **Рендер ТЧ на холсте.** `renderCanvasElement` для `FormElementTablePart` сейчас
  заглушка `fc-tp` — показать колонки с их node-id (дети `kind:Колонка`,
  `FormElementColumn`, `form_module.go:71`).
- **Состав колонок.** В панели свойств выбранной ТЧ — список колонок из
  `tablePart.Fields` с чекбоксами «показывать» и порядком. Запись — дочерние
  `Колонка` (`data_path`/`field`), управляются теми же Insert/Delete/Move.
- **Источник колонок.** `applyEditOp` сейчас чист (только YAML). Колонки сущности
  знает только metadata → либо прокинуть состав ТЧ с клиента (он уже грузит attrs),
  либо передать `*project.Project` в рендер/эндпоинт (как `objectScaffoldAttrs`).

Тесты: insert ТЧ; рендер ТЧ с колонками (node-id колонок); toggle колонки →
insert/delete `Колонка`.

## Слайсы (порядок реализации)
1. **A** — фикс возврата (быстрый, отдельный коммит/PR вперёд).
2. **B1** — DeleteElement + `op:delete` + кнопка удаления.
3. **B2** — reorder: кнопки ↑/↓ (Move в клиент); drag-reorder — опционально.
4. **C** — структурная палитра (Группа/Страница/Надпись) + drop-зона страниц.
5. **D1** — палитра ТЧ + insert ТЧ + рендер колонок на холсте.
6. **D2** — состав колонок (toggle/порядок) через `Колонка`-элементы.

## Вне объёма
Редактирование данных ТЧ в дизайнере (это рантайм-рендер SlickGrid, не дизайн);
внешние обработки/формы (отдельный путь).

---

# Часть 2 — доводка по итогам живого тестирования (batch A/B/C)

После теста MVP (A–D2) добавляются типы элементов, свойства и события. Слайсы A–D2
и серия фиксов (drag-перенос, дисциплина страниц, рендер ПолеКартинки) уже влиты.
Архитектура та же: команда → `/forms/edit-op` → formdoc правит `yaml.Node`.

## Batch A — новые элементы и свойства (СДЕЛАНО)
Палитра: «умный» дроп реквизита по типу поля (bool→Флажок, date→ПолеДаты;
enum/ссылка → ПолеВвода, само рисуется списком), чипы Кнопка / Картинка /
Командная панель; рендер `ПолеКартинки` на холсте; свойства `mask`, `type=file`
(ПолеВвода), `picture`/`width`/`height` (ПолеКартинки), `no_grid` per-ТЧ; числовые
свойства пишутся числом (`numProps`). Выводятся только свойства, влияющие на
рантайм: `Choice`/`Visible`/`Enabled` у элемента рантайм НЕ читает — не показываем.

# Batch B — события и свойства формы

Делает формы «живыми»: кнопки/поля вызывают обработчики DSL; правятся свойства
самой формы и видимость штатных кнопок. Рантайм-инфраструктура уже есть, не хватает
редактирования в конструкторе.

## B1. Привязка событий элемента к процедурам `.form.os`
**Что уже есть:** рантайм (`internal/ui/templates_managed.go`) на элементах вызывает
`obFire('<имя элемента>','<событие>')` → ищет одноимённую процедуру в `.form.os`.
Видно в ветках: Кнопка → `Нажатие` (стр. ~122), ПолеВвода → `ПриИзменении`
(`hasHandler $el "ПриИзменении"`, стр. ~50/93), КнопкаКП и ТЧ → `Нажатие`/`ПриИзменении`.
Модель: `FormElement.Handlers map[FormEventType]string` (yaml `events`,
`metadata/form_module.go:100`). Константы: `FormEventOnClick="Нажатие"`,
`FormEventOnChange="ПриИзменении"`, `FormEventOnChoice="Выбор"`. **Не хватает только
UI для записи `events:`** + удаления ключа.

**Сервер.**
- `formdoc/ops.go`: `func (d *Doc) DeleteProp(nodeID, key string) error` — `NodeByID`
  до родительского mapping (поддержать вложенный `events.Нажатие`, как в `SetProp`
  через `Split(".")`), вырезать пару ключ+значение из `m.Content` (два элемента).
  Отсутствующий ключ — no-op (не ошибка). Соседние ключи/комментарии целы.
- `forms_editop.go`: `case "delProp"` → `doc.DeleteProp(req.Node, req.Key)`,
  `selected=req.Node`. (Запись события — обычный `setProp` с `key="events.Нажатие"`,
  `value="ИмяПроцедуры"`; пустое значение клиент шлёт как `delProp`.)
- `forms_canvas.go`: в `canvasElementInfo` добавить `Events map[string]string`
  (json `events`); в `canvasModel` заполнить из `el.Handlers` (ключи привести
  `string(k)`).

**Клиент (`forms_tmpl.go`).**
- `renderProps`: секция «События» по типу элемента. `applicableEvents(kind)`:
  Кнопка/КнопкаКП → `['Нажатие']`; ПолеВвода/Флажок/ПолеДаты/ТабличнаяЧасть →
  `['ПриИзменении']`; (опц.) ссылочное поле → `['Выбор']`.
- Для каждого события — `<select>`: «— нет —» + процедуры из `.form.os` +
  «Создать обработчик…». Текущее значение = `info.events[событие]`.
  - выбор процедуры → `editOp({op:'setProp', node:_selected, key:'events.'+ev, value:proc})`.
  - «— нет —» → `editOp({op:'delProp', node:_selected, key:'events.'+ev})`.
  - «Создать…» → имя по умолчанию `info.name+ev` (или prompt), дописать в OS-редактор
    `\nПроцедура Имя()\n\t\nКонецПроцедуры\n` (через `setOS(getOS()+...)`), затем setProp.
- helper `osProcedures()` = `getOS().match(/Процедура\s+([A-Za-zА-Яа-я0-9_]+)\s*\(/g)`
  → имена. После setProp удерживать выделение (`.then(selectNode(_selected))`).

**Тесты:** `formdoc` DeleteProp (вложенный `events.X`, round-trip соседей);
`applyEditOp` setProp `events.Нажатие` пишет `events:`; `delProp` убирает;
`canvasModel` отдаёт `events`.

**Грабли:** ключи событий русские — `setProp` пишет их как есть (как `title.ru`).
`events` декодируется в `map[FormEventType]string` — проверить, что узел `events:`
с одним ключом поднимается без ошибок.

## B2. Свойства формы (корень)
Править `form.title.ru`, `form.kind` (object/list/choice/folder/custom), события
формы `form.events.*` (`ПриОткрытии`, `ПередЗаписью`, `ПриЗаписи`, … — полный список
в `form_module.go:13-50`; форменные хранятся в `FormModule.Handlers`).

**Сервер.** `formdoc/elements.go`: в `NodeByID` спец-кейс — сегмент `form`
разрешать в `mappingValue(topMapping(),"form")` (сейчас `NodeByID` стартует с
topMapping и идёт по сегментам; `form` как первый сегмент уже сработает, **проверить**
— возможно, ничего менять не надо, только адресовать `form` с клиента). `SetProp`/
`DeleteProp` тогда работают по `node="form"`, `key="title.ru"|"kind"|"events.ПриОткрытии"`.

**Клиент.** Кнопка «Свойства формы» над панелью (или клик по пустому месту холста →
`selectNode('form')`). В `renderProps` ветка `if (_selected==='form')`: title.ru,
`<select>` kind, события формы (как B1, но событий формы-уровня). Данные брать из
нового `info` для `form` в `canvasModel` (добавить запись `m["form"]` с title/kind/
events) либо отдельным полем ответа.

**Тесты:** `applyEditOp` setProp node=`form` key=`title.ru`/`kind`; node=form +
`events.ПриОткрытии`.

## B3. Штатные действия формы
`FormModule.Actions map[string]*FormAction` (yaml `actions`), `FormAction.Visible *bool`
(`form_module.go:127,147`). Рантайм скрывает кнопку при `Visible==false`
(`templates.go:161`). В панели «Свойства формы» — галочки «Показывать
Удалить/Провести/Записать/…»: запись `actions.<имя>.visible` (bool). Адресация как
у `form`. Низкий приоритет.

---

# Batch C — новые типы элементов (движок + конструктор)

Требуют рантайм-рендера: сейчас `Переключатель`/`ПолеСписка` попадают в default
«рендеринг не реализован» (`templates_managed.go:291`). `FormElement.Props` есть,
но рантаймом не читается — модель опций делаем **типизированным полем**.

## C1. Переключатель / поле с набором значений (в т.ч. числовое)
1С: поле выводится переключателем (радио) со списком значение→представление.

**Модель (`metadata/form_module.go`).**
- Новый тип:
  ```go
  type FormOption struct {
      Value  any               `yaml:"value"`           // 1, "A", … (тип под поле)
      Title  string            `yaml:"-"`               // ru-представление (из Titles)
      Titles map[string]string `yaml:"label,omitempty"` // локализованное (как TitleMap)
  }
  ```
- `FormElement`: `Options []FormOption `yaml:"options,omitempty"`` +
  `View string `yaml:"view,omitempty"`` (radio|select, для C2). `FormElementSwitch =
  "Переключатель"` уже есть (`:65`).
- В `ToFormModule`/загрузчике заполнить `FormOption.Title` из `Titles["ru"]` (как у
  элементов делается с TitleMap).

**Рантайм (`internal/ui/templates_managed.go`).** Ветка
`{{else if eq (str $el.Kind) "Переключатель"}}`:
- `{{$fn := dpField $el.DataPath}}`; опции: enum-поле → `index $ctx.EnumOptions $fn`
  (как в `<select>` ПолеВвода), иначе → `$el.Options`.
- view=radio (по умолчанию): `<label><input type=radio name=$fn value=opt.value
  {{if eq opt.value cur}}checked{{end}}> opt.label</label>` по опциям; view=select →
  `<select name=$fn>`.
- submit: один `name=$fn`, значение сохраняется существующим парсером поля
  (number/string/enum) — проверить `handlers.go` сохранение (radio шлёт ту же пару).

**Конструктор.**
- `forms_canvas.go`: `canvasElementInfo` += `Options []canvasOption` (value+label) и
  `View string`; ветка `FormElementSwitch` в `renderCanvasElement` — рисовать радио-набор
  (по Options/enum) с node-id; `canvasModel` заполнить Options.
- Палитра: чип «Переключатель» (struct-mime). При «умном» дропе enum-реквизита можно
  предлагать Переключатель как альтернативу ПолеВвода (или просто отдельный чип).
- Панель свойств: редактор опций — список (value+label), add/remove/reorder; кнопка
  «Заполнить из перечисления» (для enum-поля, тянет EnumOptions). Хранение: опции —
  НЕ children, поэтому новый op `setOptions` (node + JSON массив) →
  `func (d *Doc) SetOptions(nodeID string, opts []FormOption) error` пишет узел
  `options:` целиком (build sequence). View — `<select> radio|select`.

**Тесты:** round-trip `options`/`view`; рантайм-рендер `<input type=radio name=поле>`;
сохранение выбранного значения (number → число); enum-автозаполнение; `SetOptions`
round-trip; `canvasModel.Options`.

**Грабли:** value числового поля писать числом (`value: 1`, не `"1"`) — как `numProps`.
Submit radio → строка → парсер поля приводит к типу (проверить number/enum).

## C2. ПолеСписка (произвольный `<select>`)
То же, что C1, но представление `<select>`. Реализуется флагом `view: select` у
Переключателя — **отдельный тип не нужен**. В конструкторе — переключатель
представления radio|select в свойствах.

## C3. Таблица (не ТЧ)
Обобщённая таблица по коллекции (не табличная часть документа). Низкий приоритет,
нужна редко — реализовать по запросу.

---

## Порядок (часть 2)
1. **B1** — события: наибольшая отдача, формы оживают (самый ценный пункт).
2. **C1** — переключатель/значения: частый кейс из 1С (вопрос пользователя).
3. **B2 / B3** — свойства формы и штатные действия.
4. **C2** — список значений (надстройка над C1). **C3** — по необходимости.

## Состояние на момент написания (для резюме после /clear)
Batch A + серия фиксов влиты локально на `feature/164-visual-form-designer`,
НЕ запушены (стратегия PR — за пользователем; MVP = PR #187 в
`upstream/feature/164`). `go test ./...` зелёные, `go build ./...` чист.
Тулчейн Go: `C:\Users\i.titov\go-sdk\go\bin` (не в PATH). Грабли из части 1:
`cfgEntity.Kind` = «Справочник»/«Документ» (рус.), не совпадает с ключами
`treeNodeID` (catalog/document) → в ссылках литерал `e-`/`proc-`. Палитра реквизитов
прячется без attrs (тест `class="attr-palette"`) → структурная палитра отдельным
классом `struct-palette`. Живой drag-drop в браузере не автоматизирован — проверяется
серверными тестами (`applyEditOp`, `renderFormCanvas`, `canvasModel`).
