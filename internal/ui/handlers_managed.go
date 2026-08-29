package ui

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/report/compose"
)

// pickManagedForm возвращает первую managed-форму нужного Kind из Entity.Forms
// или nil, если такой нет. Используется в рантайме для опционального
// переключения с авто-генерации на ручную форму из forms/<entity>/*.form.yaml.
//
// kind: "object" — карточка элемента/документа, "list" — форма списка,
// "choice" — форма выбора, "" — любая (берётся первая managed).
func pickManagedForm(entity *metadata.Entity, kind string) *metadata.FormModule {
	if entity == nil {
		return nil
	}
	kindLower := strings.ToLower(kind)
	for _, fm := range entity.Forms {
		if fm == nil || !fm.IsManaged() {
			continue
		}
		if kindLower == "" || strings.ToLower(fm.Kind) == kindLower {
			return fm
		}
	}
	return nil
}

// renderEntityForm — единая точка рендера формы объекта/документа.
// Если для Entity есть managed-форма с подходящим Kind — рендерит
// page-managed-form с теми же data + "Form": managed-форма.
// Иначе — текущий page-form (auto-generated).
//
// Это даёт пользователю опциональность: создание .form.yaml в проекте
// автоматически активирует managed-рендер для выбранной сущности; без
// .form.yaml продолжает работать существующая авто-форма без изменений.
func (s *Server) renderEntityForm(w http.ResponseWriter, r *http.Request, kind string, data map[string]any) {
	entity, _ := data["Entity"].(*metadata.Entity)
	// #481: заголовок вкладки/карточки = представление записи (Наименование…),
	// а не имя сущности. Для существующей записи кладём TabTitle (его читает
	// ui.js и шлёт оболочке obSetTitle) и RecordTitle (для заголовка h1).
	if isNew, _ := data["IsNew"].(bool); !isNew {
		if title := recordCardTitle(entity, data["Values"]); title != "" {
			data["TabTitle"] = title
			data["RecordTitle"] = title
		}
	}
	// Иерархия: при создании через управляемую форму признак группы и родитель
	// приходят query-параметрами кнопок «📁 Группа» / «＋ в группе». Автоформа
	// рисует их полями, managed-форма — нет, поэтому переносим их в data, чтобы
	// шаблон отрисовал скрытые поля (#618). Только для новой записи: у
	// существующей is_folder/parent_id восстанавливает restoreUnsubmittedFields.
	isNew, _ := data["IsNew"].(bool)
	isFolder, parentID := hierarchyCreateHints(r, entity, isNew)
	// Создание копированием (?copy=): признак группы и родитель приходят не
	// query-параметром, а значениями, снятыми с оригинала (issue #762). Без
	// этого копия группы записалась бы элементом в корне — тот же #618, только
	// источник значений другой.
	if isNew && entity != nil && entity.Hierarchical {
		if vals, ok := data["Values"].(map[string]string); ok {
			isFolder = isFolder || vals["is_folder"] == "true"
			if parentID == "" {
				parentID = strings.TrimSpace(vals["parent_id"])
			}
		}
	}
	if isFolder || parentID != "" {
		if isFolder {
			data["NewIsFolder"] = true
		}
		if parentID != "" {
			data["NewParentID"] = parentID
		}
	}
	managed := pickManagedForm(entity, kind)
	if managed != nil {
		data["Form"] = managed
		// Фикс A: команды формы, не размещённые вручную элементом kind: Кнопка,
		// рисуются автоматической командной панелью (иначе объявленная в commands:
		// команда в UI не видна — её кнопку рисует только kind: Кнопка). Fire-click
		// авто-кнопки резолвится на процедуру команды в resolveHandlerProc.
		data["FormCommands"] = unplacedCommands(managed)
		// Фикс B: реквизиты формы (save:false) ссылочного типа получают пикер —
		// грузим их опции и домешиваем в RefOptions (у полей сущности пикер уже был).
		s.mergeFormLocalRefOptions(r.Context(), managed, data)
		// Списки значений (СписокВыбора) объявлены на элементах формы, а не на
		// полях сущности, поэтому собираем их из самой managed-формы. Единая
		// точка покрывает все пути рендера (new/edit/повторный показ с ошибкой).
		data["ChoiceOptions"] = loadChoiceOptions(managed, s.resolveLang(r))
		s.prepareManagedFormData(r.Context(), data, managed)
		s.render(w, r, "page-managed-form", data)
		return
	}
	s.render(w, r, "page-form", data)
}

// hierarchyCreateHints читает признак группы и родителя из query-параметров
// кнопок создания («📁 Группа» → ?is_folder=true, «＋ в группе» → ?parent=…)
// для управляемой формы НОВОЙ иерархической записи. Автоформа рисует их полями,
// managed-форма — скрытыми: при создании восстановить их неоткуда, и без переноса
// Upsert пишет is_folder=false, а элемент улетает в корень (#618).
//
// Родитель приходит в параметре `parent` — так его называют и кнопки списка
// (templates.go, «+ Элемент»/«📁 Группа»), и автоформа (handlers_entity.go).
// Здесь читался `parent_id`, которого не шлёт НИКТО, поэтому половина фикса
// #618 не работала: признак группы ставился, а родитель терялся и запись
// улетала в корень. Тест при этом был зелёный — он звал эту функцию напрямую с
// рукописным query и закреплял неверное имя (#615).
func hierarchyCreateHints(r *http.Request, entity *metadata.Entity, isNew bool) (isFolder bool, parentID string) {
	if entity == nil || !entity.Hierarchical || !isNew {
		return false, ""
	}
	isFolder = r.URL.Query().Get("is_folder") == "true"
	parentID = strings.TrimSpace(r.URL.Query().Get("parent"))
	return isFolder, parentID
}

func (s *Server) prepareManagedFormData(ctx context.Context, data map[string]any, form *metadata.FormModule) {
	if form == nil || data == nil {
		return
	}
	if css := formConditionalCSS(form); css != "" {
		data["FormConditionalCSS"] = template.CSS(css) //nolint:gosec // G203: стиль собран cssStyle → csssafe.Color, произвольная строка в CSS не попадает
	}
	entity, _ := data["Entity"].(*metadata.Entity)
	header := managedFormHeaderValues(entity, data["Values"])

	// Условная доступность элементов (readonly_when/hidden_when) — по полям
	// ЗАПИСИ, поэтому считается здесь же, где известны Values, и заново после
	// каждого события формы.
	if s.interp != nil {
		ro, hidden, warns := managedFormElementStates(form, header, newInterpEvaluator(s.interp))
		if len(ro) > 0 {
			data["ElReadOnly"] = ro
		}
		if len(hidden) > 0 {
			data["ElHidden"] = hidden
		}
		if len(warns) > 0 {
			data["FormWarnings"] = appendManagedFormWarnings(data["FormWarnings"], warns)
		}
	}

	rows, _ := data["TablePartRows"].(map[string][]map[string]any)
	// Виртуальные колонки заполняются здесь — в единственной точке подготовки
	// данных управляемой формы. Любой путь рендера (новая запись, правка,
	// повторный показ с ошибкой, событие формы) проходит через неё, поэтому
	// колонка не может оказаться заполненной «через раз» (#845).
	s.applyVirtualTPColumns(ctx, entity, form, rows)
	if len(rows) == 0 || len(form.Conditional) == 0 || s.interp == nil {
		return
	}
	warnings := applyManagedFormConditionalStyles(form, rows, header, newInterpEvaluator(s.interp))
	if len(warnings) > 0 {
		data["FormWarnings"] = appendManagedFormWarnings(data["FormWarnings"], warnings)
	}
}

// managedFormElementStates вычисляет состояние readonly/hidden каждого элемента
// формы по полям записи. Возвращает множества имён элементов, которые должны
// быть нередактируемы и скрыты, и предупреждения по нерабочим условиям.
//
// В карту readonly кладётся ИТОГОВОЕ состояние элемента — «условие ИЛИ
// эффективный статический readonly», — а не одно условие. Карту применяет
// клиент после каждого события формы, причём в обе стороны: значение false
// снимает запрет. Пока в ней лежало голое условие, эти два ответа противоречили
// друг другу у элемента, у которого есть и постоянный запрет (свой или
// унаследованный от группы), и условие: сервер рисовал его нередактируемым, а
// первое же событие формы его отпирало — постоянный запрет конфигурации
// снимался одним нажатием кнопки. Эффективный статический readonly считает
// walkBrowserFormElements — тот же обход, которым его берёт серверная отрисовка
// ($ro в шаблоне managed-element).
//
// Ошибка вычисления НЕ скрывает и НЕ блокирует элемент: неверное условие — это
// ошибка конфигурации, и молча запертое поле объяснить пользователю нечем.
// Вместо этого условие игнорируется, а конфигуратор получает предупреждение на
// форме — тем же способом, что и у условного оформления.
func managedFormElementStates(form *metadata.FormModule, header map[string]any, ev compose.Evaluator) (map[string]bool, map[string]bool, []string) {
	if form == nil || ev == nil {
		return nil, nil, nil
	}
	ro := map[string]bool{}
	hidden := map[string]bool{}
	wc := &formWarnCollector{}
	row := compose.Row(header)
	eval := func(expr, kind, elName string) bool {
		if strings.TrimSpace(expr) == "" {
			return false
		}
		ok, err := ev.EvalBool(expr, row)
		if err != nil {
			wc.add(fmt.Sprintf("%s элемента «%s» («%s»): %v", kind, elName, expr, err))
			return false
		}
		return ok
	}
	walkBrowserFormElements(form, func(visit browserFormElementVisit) {
		el := visit.element
		if el == nil {
			return
		}
		// В карту попадает КАЖДЫЙ элемент с условием — в том числе с ложным.
		// Ответ события формы переносит эти карты на клиент, и без явного
		// «false» он не смог бы снять запрет, когда условие перестало
		// выполняться (отличить «условия нет» от «условие ложно» было бы нечем).
		if strings.TrimSpace(el.ReadOnlyWhen) != "" {
			// Условие считается всегда, даже под постоянным запретом: иначе
			// сломанное выражение под readonly-группой молчало бы вместо
			// предупреждения конфигуратору.
			cond := eval(el.ReadOnlyWhen, "условие readonly_when", el.Name)
			ro[el.Name] = cond || visit.effectiveReadOnly
		}
		if strings.TrimSpace(el.HiddenWhen) != "" {
			hidden[el.Name] = eval(el.HiddenWhen, "условие hidden_when", el.Name)
		}
	})
	return ro, hidden, wc.msgs
}

// unplacedCommands возвращает команды формы, НЕ размещённые вручную элементом
// kind: Кнопка (с обработчиком той же процедуры-Action). Для них renderEntityForm
// рисует автоматическую командную панель — иначе объявленная в commands: команда
// в UI не видна (кнопку рисует только kind: Кнопка). Так конфигуратору не нужно
// дублировать каждую команду кнопкой вручную: объявил в commands: — она на форме.
func unplacedCommands(form *metadata.FormModule) []*metadata.FormCommand {
	if form == nil || len(form.Commands) == 0 {
		return nil
	}
	placed := map[string]bool{}
	var walk func(*metadata.FormElement)
	walk = func(el *metadata.FormElement) {
		if el == nil {
			return
		}
		if string(el.Kind) == "Кнопка" && el.Handlers != nil {
			if proc := el.Handlers[metadata.FormEventOnClick]; proc != "" {
				placed[proc] = true
			}
		}
		for _, c := range el.Children {
			walk(c)
		}
	}
	for _, el := range form.Elements {
		walk(el)
	}
	out := make([]*metadata.FormCommand, 0, len(form.Commands))
	for _, c := range form.Commands {
		if c != nil && c.Action != "" && !placed[c.Action] {
			out = append(out, c)
		}
	}
	return out
}

// attrRefEntityName извлекает имя сущности-справочника/документа из типа реквизита
// формы ("CatalogRef.X" / "DocumentRef.X" → "X"). Пусто, если тип не ссылочный.
func attrRefEntityName(typeRef string) string {
	for _, p := range []string{"CatalogRef.", "DocumentRef."} {
		if strings.HasPrefix(typeRef, p) {
			return strings.TrimPrefix(typeRef, p)
		}
	}
	return ""
}

// mergeFormLocalRefOptions грузит варианты выбора для реквизитов формы (save:false)
// ссылочного типа и домешивает их в data["RefOptions"] под именем реквизита. Это
// даёт таким полям рабочий пикер: managed-рендер сам по себе выбирает варианты
// только полям сущности, а реквизит формы иначе остаётся вводом без выбора.
func (s *Server) mergeFormLocalRefOptions(ctx context.Context, form *metadata.FormModule, data map[string]any) {
	if form == nil || len(form.Attributes) == 0 {
		return
	}
	ref, _ := data["RefOptions"].(map[string][]map[string]any)
	if ref == nil {
		ref = map[string][]map[string]any{}
	}
	entity, _ := data["Entity"].(*metadata.Entity)
	for _, a := range form.Attributes {
		if a == nil || a.Save {
			continue
		}
		// Имя реквизита формы ничем не связано с именами полей сущности и вполне
		// может совпасть (в 1С неглавный реквизит формы по умолчанию SaveData=false
		// и часто называется как реквизит объекта). Шаблон в таком случае рисует
		// поле сущности, и подмена ключа отдала бы ему варианты чужого справочника,
		// а текущее значение выпало бы из списка — при следующем сохранении ссылка
		// молча очищается. Поле сущности всегда важнее: свои опции оно уже собрало.
		if _, isEntityField := entityFieldByName(entity, a.Name); isEntityField {
			continue
		}
		if _, busy := ref[a.Name]; busy {
			continue
		}
		refEntityName := attrRefEntityName(a.TypeRef)
		if refEntityName == "" {
			continue
		}
		refEntity := s.reg.GetEntity(refEntityName)
		if refEntity == nil {
			continue
		}
		rows, err := s.initialReferenceOptions(ctx, refEntity, refOptionsChoice, nil)
		if err != nil {
			continue
		}
		ref[a.Name] = rows
	}
	data["RefOptions"] = ref
}

// managedFormHeaderValues приводит значения шапки к типам event-пути. На
// первичном рендере Values — map[string]string, а DSL-равенство нечисловых
// типов сравнивает строковые ключи (equal намеренно не парсит строки, в
// отличие от </>): `when: Поле = 10.5` по полю шапки со значением "10.50"
// (PG numeric) не срабатывало до первого события. Числа и булево типизируем
// по метаданным сущности — как formToFields; даты обоих путей уже в одном
// строковом формате 2006-01-02T15:04.
func managedFormHeaderValues(entity *metadata.Entity, v any) map[string]any {
	out := map[string]any{}
	switch m := v.(type) {
	case map[string]string:
		for k, val := range m {
			out[k] = val
		}
	case map[string]any:
		for k, val := range m {
			out[k] = val
		}
	}
	if entity == nil {
		return out
	}
	for _, f := range entity.Fields {
		raw, ok := out[f.Name].(string)
		if !ok {
			continue
		}
		switch f.Type {
		case metadata.FieldTypeNumber:
			if n, err := strconv.ParseFloat(raw, 64); err == nil {
				out[f.Name] = n
			}
		case metadata.FieldTypeBool:
			out[f.Name] = raw == "true"
		}
	}
	return out
}

func appendManagedFormWarnings(existing any, warnings []string) []string {
	out, _ := existing.([]string)
	out = append(out, warnings...)
	return out
}

// recordCardTitle возвращает представление записи для заголовка карточки/вкладки.
// Явный presentation перебирается до первого непустого значения; без него
// сохраняется прежний выбор одного поля через aiRefLookupField. "" — если поля
// нет или пусто (напр. новая запись). Используется для #481.
func recordCardTitle(entity *metadata.Entity, values any) string {
	if entity == nil {
		return ""
	}
	if len(entity.Presentation) > 0 {
		switch m := values.(type) {
		case map[string]string:
			row := make(map[string]any, len(m))
			for key, value := range m {
				row[key] = value
			}
			label, _ := explicitPresentationValue(entity, row)
			return label
		case map[string]any:
			label, _ := explicitPresentationValue(entity, m)
			return label
		}
		return ""
	}

	field := aiRefLookupField(entity)
	if field == "" {
		return ""
	}
	switch m := values.(type) {
	case map[string]string:
		return strings.TrimSpace(m[field])
	case map[string]any:
		if s, ok := m[field].(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
