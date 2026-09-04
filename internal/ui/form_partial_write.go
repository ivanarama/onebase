package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

type managedFormTablePayloadSource uint8

const (
	managedFormTableNamedPayload managedFormTablePayloadSource = 1 << iota
	managedFormTableJSONPayload
)

type managedFormTableAuthority struct {
	source  managedFormTablePayloadSource
	element *metadata.FormElement
}

// managedFormTableAuthorities derives one authoritative browser control and
// encoding per canonical table from server-owned form metadata. A placement is
// writable only when both the metadata tree and the caller's actual permission
// allow writes. Client-provided markers can never promote another namespace.
//
// Even two writable placements using the same renderer are ambiguous: they
// keep independent browser state but submit the same canonical namespace. The
// form must therefore have exactly zero or one writable placement per table.
func managedFormTableAuthorities(
	form *metadata.FormModule,
	declared []metadata.TablePart,
	canWrite bool,
) (map[string]managedFormTableAuthority, error) {
	definitions, err := metadata.FormTableDefinitions(form, declared)
	if err != nil {
		return nil, err
	}
	authorities := make(map[string]managedFormTableAuthority)
	if form == nil || !canWrite {
		return authorities, nil
	}
	var policyErr error
	walkBrowserFormElements(form, func(visit browserFormElementVisit) {
		if policyErr != nil {
			return
		}
		el := visit.element
		if el.Kind != metadata.FormElementTablePart || visit.effectiveReadOnly || el.DataPath == "" || strings.Count(el.DataPath, ".") > 1 {
			return
		}
		formName := dpFieldName(el.DataPath)
		for _, definition := range definitions {
			if !strings.EqualFold(definition.Name, formName) {
				continue
			}
			source := managedFormTableJSONPayload
			// ValueTable currently has only the DOM renderer and therefore posts
			// vt.* regardless of the element's NoGrid flag. NoGrid selects the
			// representation only for persistent entity/processor table parts.
			if definition.Source == metadata.FormTableSourceValueTable || el.NoGrid {
				source = managedFormTableNamedPayload
			}
			if previous, exists := authorities[definition.Name]; exists && previous.element != nil {
				policyErr = fmt.Errorf(
					"неоднозначные редактируемые представления таблицы формы %q: элементы %q и %q",
					definition.Name, previous.element.Name, el.Name,
				)
				return
			}
			authorities[definition.Name] = managedFormTableAuthority{source: source, element: el}
			return
		}
	})
	if policyErr != nil {
		return nil, policyErr
	}
	return authorities, nil
}

func managedFormTablePayloadSources(
	form *metadata.FormModule,
	declared []metadata.TablePart,
	canWrite bool,
) (map[string]managedFormTablePayloadSource, error) {
	authorities, err := managedFormTableAuthorities(form, declared, canWrite)
	if err != nil {
		return nil, err
	}
	sources := make(map[string]managedFormTablePayloadSource, len(authorities))
	for name, authority := range authorities {
		sources[name] = authority.source
	}
	return sources, nil
}

// validateManagedFormTableEventTarget binds table and child-command events to
// the exact server-authorized placement. This prevents a readonly duplicate
// from borrowing authority from another writable element for the same table.
func validateManagedFormTableEventTarget(
	authorities map[string]managedFormTableAuthority,
	target browserFormEventTarget,
) error {
	tableElement := target.parentTablePart
	if target.element != nil && target.element.Kind == metadata.FormElementTablePart {
		tableElement = target.element
	}
	if tableElement == nil {
		return nil
	}
	for _, authority := range authorities {
		if authority.element == tableElement {
			return nil
		}
	}
	return fmt.Errorf("табличная часть %q доступна только для чтения", tableElement.Name)
}

// managedFormSinglePayloadValue resolves one protocol key from POST body only.
// Case variants map to the same canonical key and are therefore duplicates,
// just like multiple values submitted under one exact key.
func managedFormSinglePayloadValue(values map[string][]string, canonicalKey string) (string, bool, error) {
	matchedKey := ""
	matchedValue := ""
	for key, submitted := range values {
		if !strings.EqualFold(key, canonicalKey) {
			continue
		}
		if matchedKey != "" {
			return "", true, fmt.Errorf("ключ payload %q указан повторно как %q и %q", canonicalKey, matchedKey, key)
		}
		if len(submitted) != 1 {
			return "", true, fmt.Errorf("ключ payload %q должен иметь ровно одно значение", canonicalKey)
		}
		matchedKey = key
		matchedValue = submitted[0]
	}
	return matchedValue, matchedKey != "", nil
}

func managedFormCanonicalColumn(columns []string, raw string) (string, bool) {
	for _, column := range columns {
		if strings.EqualFold(column, raw) {
			return column, true
		}
	}
	return "", false
}

// managedFormFoldKey returns one representative per unicode.SimpleFold orbit,
// preserving strings.EqualFold's rune-by-rune equivalence while allowing
// duplicate JSON keys to be detected with an O(1) map lookup.
func managedFormFoldKey(value string) string {
	var folded strings.Builder
	folded.Grow(len(value))
	for _, r := range value {
		canonical := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < canonical {
				canonical = next
			}
		}
		folded.WriteRune(canonical)
	}
	return folded.String()
}

// managedFormNamedTableRows canonicalizes tp./vt. body keys against server
// metadata and rejects two client keys which address the same row+column.
// Unknown columns remain ignored for compatibility, but can never make a row
// authoritative or collide with a declared column.
func managedFormNamedTableRows(
	values map[string][]string,
	namespace string,
	tableName string,
	columns []string,
) ([]map[string]string, bool, error) {
	rowsByIndex := make(map[int]map[string]string)
	seen := make(map[string]string)
	present := false
	for key, submitted := range values {
		parts := strings.Split(key, ".")
		if len(parts) < 2 || !strings.EqualFold(parts[0], namespace) || !strings.EqualFold(parts[1], tableName) {
			continue
		}
		if len(parts) != 4 {
			return nil, false, fmt.Errorf("некорректный ключ payload таблицы %q: %q", tableName, key)
		}
		rowIndex, err := strconv.Atoi(parts[2])
		if err != nil || rowIndex < 0 {
			return nil, false, fmt.Errorf("некорректный индекс строки таблицы %q в ключе %q", tableName, key)
		}
		column, known := managedFormCanonicalColumn(columns, parts[3])
		if !known {
			continue
		}
		canonicalKey := strconv.Itoa(rowIndex) + "." + column
		if previous, duplicate := seen[canonicalKey]; duplicate {
			return nil, false, fmt.Errorf("ключ payload таблицы %q указан повторно как %q и %q", tableName, previous, key)
		}
		if len(submitted) != 1 {
			return nil, false, fmt.Errorf("ключ payload таблицы %q %q должен иметь ровно одно значение", tableName, key)
		}
		seen[canonicalKey] = key
		if rowsByIndex[rowIndex] == nil {
			rowsByIndex[rowIndex] = make(map[string]string)
		}
		rowsByIndex[rowIndex][column] = submitted[0]
		present = true
	}
	if !present {
		return nil, false, nil
	}
	indices := make([]int, 0, len(rowsByIndex))
	for rowIndex := range rowsByIndex {
		indices = append(indices, rowIndex)
	}
	sort.Ints(indices)
	rows := make([]map[string]string, 0, len(indices))
	for _, rowIndex := range indices {
		rows = append(rows, rowsByIndex[rowIndex])
	}
	return rows, true, nil
}

// decodeManagedFormJSONRows accepts exactly one JSON array of objects. It uses
// streaming tokens so duplicate object keys (including case variants) cannot
// be silently overwritten by encoding/json's map decoder.
func decodeManagedFormJSONRows(blob string, columns []string) ([]map[string]any, error) {
	if strings.TrimSpace(blob) == "" {
		return nil, fmt.Errorf("JSON payload пуст")
	}
	decoder := json.NewDecoder(strings.NewReader(blob))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '[' {
		return nil, fmt.Errorf("ожидался JSON-массив строк")
	}
	rows := make([]map[string]any, 0)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return nil, err
		}
		if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
			return nil, fmt.Errorf("строка таблицы должна быть JSON-объектом")
		}
		row := make(map[string]any)
		seenKeys := make(map[string]string)
		for decoder.More() {
			keyToken, keyErr := decoder.Token()
			if keyErr != nil {
				return nil, keyErr
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("имя поля строки таблицы должно быть строкой")
			}
			foldedKey := managedFormFoldKey(key)
			if previous, duplicate := seenKeys[foldedKey]; duplicate {
				return nil, fmt.Errorf("поле JSON-строки указано повторно как %q и %q", previous, key)
			}
			seenKeys[foldedKey] = key
			var value any
			if err := decoder.Decode(&value); err != nil {
				return nil, err
			}
			if canonical, known := managedFormCanonicalColumn(columns, key); known {
				row[canonical] = value
			}
		}
		if token, err = decoder.Token(); err != nil {
			return nil, err
		} else if delimiter, ok := token.(json.Delim); !ok || delimiter != '}' {
			return nil, fmt.Errorf("некорректное завершение JSON-строки")
		}
		rows = append(rows, row)
	}
	if token, err = decoder.Token(); err != nil {
		return nil, err
	} else if delimiter, ok := token.(json.Delim); !ok || delimiter != ']' {
		return nil, fmt.Errorf("некорректное завершение JSON-массива")
	}
	if _, err = decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("после JSON-массива обнаружены лишние данные")
		}
		return nil, err
	}
	return rows, nil
}

// Частичная запись управляемой формы.
//
// Прежний контракт: POST карточки трактовался как ПОЛНОЕ состояние записи —
// formToFields обходит все entity.Fields, а r.FormValue не отличает «ключа нет»
// от «прислали пусто», и оба случая дают nil. Управляемая форма при этом
// партиальна по построению: она рендерит только свои elements, а ReadOnly-
// контролы ссылок/перечислений/флажков получают disabled и браузером вовсе не
// отправляются. Итог — открыть карточку и нажать «Записать», ничего не меняя,
// затирало неразмещённые поля в NULL (воспроизведено на examples/crm и
// examples/tasks).
//
// Новый контракт: для сущности с управляемой формой объекта присутствие ключа —
// даже с пустым значением — означает «применить» (включая очистку в NULL), а
// отсутствие ключа означает «поле не передавалось» и его значение перечитывается
// из БД. Единственное исключение — bool-поле, отрисованное этой формой как
// не-ReadOnly kind: Флажок: неотмеченный чекбокс браузер не шлёт, поэтому там
// отсутствие ключа по-прежнему значит «снято».

// dpFieldName извлекает имя поля из data_path вида «Объект.Контрагент».
// Тот же разбор, что у шаблонной функции dpField, — источник один.
func dpFieldName(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}

// submittedFormKeys — множество ключей, реально пришедших В ТЕЛЕ запроса.
// Именно PostForm, а не Form: query-параметры не должны считаться присланными
// формой, иначе ?Поле= из ссылки очищал бы данные. Событийный путь шлёт
// FormData (multipart), поэтому объединяем и его. Ключи кладём в обоих
// регистрах — formToFields пишет имена как в метаданных, а DSL нормализует их
// в нижний регистр.
func submittedFormKeys(r *http.Request) map[string]bool {
	keys := make(map[string]bool)
	add := func(k string) {
		keys[k] = true
		keys[strings.ToLower(k)] = true
	}
	for k := range r.PostForm {
		add(k)
	}
	if r.MultipartForm != nil {
		for k := range r.MultipartForm.Value {
			add(k)
		}
		for k := range r.MultipartForm.File {
			add(k)
		}
	}
	return keys
}

// formKeySubmitted проверяет присутствие ключа регистронезависимо.
func formKeySubmitted(keys map[string]bool, name string) bool {
	return keys[name] || keys[strings.ToLower(name)]
}

// managedFormCheckboxPresenceKey — префикс маркера присутствия редактируемого
// Флажка. Скрытое поле рядом с флажком шаблон рисует давно (оно же служит
// признаком присутствия булева параметра обработки), здесь оно читается как
// ответ на вопрос «был ли этот флажок на отрисованной форме».
const managedFormCheckboxPresenceKey = processorParamPresencePrefix

// checkboxOmittedFields собирает поля сущности типа bool, отрисованные этой
// формой как не-ReadOnly kind: Флажок. Только для них отсутствие ключа значит
// «снято» — у ReadOnly-флажка контрол disabled, пользователь его не менял, и
// восстановление обязано сработать. Обход рекурсивный: ГруппаФормы,
// СтраницыФормы и Страница держат элементы в Children.
//
// У флажка, на который влияет собственное readonly_when либо hidden_when (своё
// или контейнера-предка), метаданные больше не описывают отрисованную форму:
// скрытого флажка в разметке нет вовсе, а нередактируемый отрисован disabled —
// браузер в обоих случаях не шлёт ни значения, ни маркера. Считать это «снято»
// — молча затирать реквизит ложью при каждой записи, поэтому для таких флажков
// решает факт отправки маркера, а не размещение на форме. Это то же правило, по
// которому managedFormTableSubmitted судит о табличных частях.
func checkboxOmittedFields(form *metadata.FormModule, entity *metadata.Entity, submitted map[string]bool) map[string]bool {
	out := make(map[string]bool)
	if form == nil || entity == nil {
		return out
	}
	walkBrowserFormElements(form, func(visit browserFormElementVisit) {
		el := visit.element
		if string(el.Kind) == "Флажок" && !visit.effectiveReadOnly && el.DataPath != "" {
			// Поле шапки: data_path вида «Объект.Флаг». Путь табличной части
			// («Объект.Товары.Цена») к полю шапки отношения не имеет, даже если
			// имя последнего сегмента совпало с полем сущности.
			if strings.Count(el.DataPath, ".") <= 1 {
				name := dpFieldName(el.DataPath)
				if f, ok := entityFieldByName(entity, name); ok && f.Type == metadata.FieldTypeBool {
					if visit.conditional && !formKeySubmitted(submitted, managedFormCheckboxPresenceKey+name) {
						return
					}
					out[strings.ToLower(name)] = true
				}
			}
		}
	})
	return out
}

// managedFormTableSubmitted — прислал ли браузер табличную часть в этом POST.
// Смотрит на ПРИСУТСТВИЕ ключа, а не на его содержимое: пустой `tp_json.X`
// значит «строк не осталось», а отсутствие ключа — «таблицы на отрисованной
// форме не было» (скрыта hidden_when либо не размещена вовсе). Разница
// существенная: в первом случае строки надо стереть, во втором — сохранить.
//
// Это то же правило, по которому restoreUnsubmittedFields решает судьбу
// реквизита шапки. Табличные части шли по другому — по метаданным формы, — и
// скрытая условием таблица затиралась пустым срезом при первой же записи.
func managedFormTableSubmitted(keys map[string]bool, name string, source managedFormTablePayloadSource) bool {
	switch source {
	case managedFormTableJSONPayload:
		// Скрытое поле tp_json.X шаблон рисует рядом с гридом всегда, когда
		// таблица доступна на запись, — оно и есть признак присутствия.
		return formKeySubmitted(keys, "tp_json."+name)
	case managedFormTableNamedPayload:
		// У простой таблицы (no_grid) строки и есть ключи tp.X.<i>.<колонка>,
		// поэтому «строк не осталось» от «таблицы не было» отличает только
		// маркер, который шаблон рисует рядом с таблицей.
		if formKeySubmitted(keys, managedFormTablePresenceKey+name) {
			return true
		}
		prefix := strings.ToLower("tp." + name + ".")
		for key := range keys {
			if strings.HasPrefix(strings.ToLower(key), prefix) {
				return true
			}
		}
	}
	return false
}

// managedFormTablePresenceKey — префикс маркера присутствия простой (no_grid)
// таблицы. Значение не используется, важен сам факт ключа в теле запроса.
const managedFormTablePresenceKey = "tp_present."

// restoreUneditableTableParts protects table parts which a managed form does
// not allow the user to edit. For an existing object their persisted rows are
// restored (partial-write preservation); for a new object forged rows are
// removed. In both cases incomplete/forged browser state cannot reach form
// hooks, Объект.Записать(), or the ordinary Save path. Server-side hooks remain
// free to populate those tables after this boundary.
//
// «Не даёт править» — это не только readonly в метаданных, но и таблица,
// которой на отрисованной форме не было: элемент, скрытый hidden_when, не
// рендерится вовсе, и его строки в теле запроса не приходят.
func (s *Server) restoreUneditableTableParts(
	ctx context.Context,
	r *http.Request,
	entity *metadata.Entity,
	form *metadata.FormModule,
	id uuid.UUID,
	rows map[string][]map[string]any,
	canWrite bool,
) error {
	if entity == nil || form == nil || rows == nil {
		return nil
	}
	editable := make(map[string]bool)
	authorities, err := managedFormTableAuthorities(form, entity.TableParts, canWrite)
	if err != nil {
		return err
	}
	var submitted map[string]bool
	if r != nil {
		submitted = submittedFormKeys(r)
	}
	for _, tablePart := range entity.TableParts {
		// Браузеру верим, только когда форма разрешает править таблицу И она
		// действительно пришла в теле запроса. Результат hidden_when виден
		// здесь именно фактом отправки: пересчитывать условие заново нельзя —
		// пользователь мог тем же POST изменить поле, от которого оно зависит.
		source := authorities[tablePart.Name].source
		editable[tablePart.Name] = source != 0 &&
			(submitted == nil || managedFormTableSubmitted(submitted, tablePart.Name, source))
	}
	for _, tablePart := range entity.TableParts {
		if editable[tablePart.Name] {
			continue
		}
		for postedName := range rows {
			if strings.EqualFold(postedName, tablePart.Name) {
				delete(rows, postedName)
			}
		}
		if id == uuid.Nil {
			continue
		}
		stored, err := s.store.GetTablePartRows(ctx, entity.Name, tablePart.Name, id, tablePart)
		if err != nil {
			return err
		}
		rows[tablePart.Name] = stored
	}
	return nil
}

// normalizeRestoredValue приводит прочитанное из БД значение к типу, который
// ждут DSL и запись. Критично для bool: SQLite отдаёт его как int64, а truthy в
// интерпретаторе про int64 не знает и по default возвращает истину — тогда
// восстановленная «ложь» стала бы истиной внутри «Если Объект.Флаг Тогда», а
// Upsert разбирает только bool/string и группа справочника превратилась бы в
// элемент. Числа (decimal), даты (time.Time) и ссылки/строки принимаются
// записью как есть.
func normalizeRestoredValue(f metadata.Field, v any) any {
	if f.Type != metadata.FieldTypeBool || v == nil {
		return v
	}
	switch t := v.(type) {
	case bool:
		return t
	case int64:
		return t != 0
	case int:
		return t != 0
	case float64:
		return t != 0
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(t))
		if err != nil {
			return v
		}
		return b
	}
	return v
}

// restoreUnsubmittedFields подставляет в fields значения из БД для тех полей
// сущности, ключи которых не пришли в теле запроса. Вызывается ДО маскирования,
// проверок построчного доступа и серверных хуков формы, чтобы и предикаты
// доступа, и DSL видели реальные данные, а ПередЗаписью мог перекрыть значение.
//
// Отсутствие строки (конкурентное удаление) — не ошибка: восстанавливать нечего.
func (s *Server) restoreUnsubmittedFields(
	ctx context.Context,
	r *http.Request,
	entity *metadata.Entity,
	form *metadata.FormModule,
	id uuid.UUID,
	fields map[string]any,
) error {
	if entity == nil || form == nil || fields == nil {
		return nil
	}
	submitted := submittedFormKeys(r)
	checkboxes := checkboxOmittedFields(form, entity, submitted)

	// Есть ли вообще что восстанавливать — чтобы не ходить в БД зря.
	need := false
	for _, f := range entity.Fields {
		if !formKeySubmitted(submitted, f.Name) && !checkboxes[strings.ToLower(f.Name)] {
			need = true
			break
		}
	}
	// parent_id/is_folder — служебные колонки, их нет в entity.Fields, но Upsert
	// пишет их всегда: отсутствие ключа в map даёт parent_id = NULL и
	// is_folder = false, то есть элемент улетает в корень и перестаёт быть
	// группой. Управляемая форма их не рендерит, поэтому восстанавливаем тоже.
	if entity.Hierarchical &&
		(!formKeySubmitted(submitted, "parent_id") || !formKeySubmitted(submitted, "is_folder")) {
		need = true
	}
	if !need {
		return nil
	}

	row, err := s.store.GetByID(ctx, entity.Name, id, entity)
	if err != nil {
		return err
	}
	if row == nil {
		return nil
	}
	for _, f := range entity.Fields {
		if formKeySubmitted(submitted, f.Name) || checkboxes[strings.ToLower(f.Name)] {
			continue
		}
		stored, ok := maskCIKeyValue(row, f.Name)
		if !ok {
			continue
		}
		if key, exists := maskCIKey(fields, f.Name); exists {
			fields[key] = normalizeRestoredValue(f, stored)
			continue
		}
		fields[f.Name] = normalizeRestoredValue(f, stored)
	}

	if entity.Hierarchical {
		if !formKeySubmitted(submitted, "parent_id") {
			if v, ok := row["parent_id"]; ok && v != nil {
				fields["parent_id"] = v
			}
		}
		if !formKeySubmitted(submitted, "is_folder") {
			if v, ok := row["is_folder"]; ok {
				// Upsert разбирает только bool и string, а SQLite отдаёт int64 —
				// без приведения группа молча стала бы элементом.
				fields["is_folder"] = normalizeRestoredValue(
					metadata.Field{Type: metadata.FieldTypeBool}, v)
			}
		}
	}
	return nil
}

// applyDefaultsToUnsubmittedFields — то же для НОВОГО объекта управляемой формы.
// У существующего объекта неприсланный реквизит перечитывается из БД; у нового
// читать неоткуда — строки ещё нет, и его значением обязано быть ровно то, что
// вычислил бы GET формы: декларативный дефолт (план 153) плюс ПриСозданииНового.
//
// Без этого дефолт и хук доезжали до базы только через автоформу — она рисует
// все реквизиты, и они возвращаются в POST. Управляемая форма рисует
// размещённые, остальные до сервера не доходили и записывались пустыми — при
// том что тот же реквизит через DSL и REST заполнялся, а
// entityservice/defaults.go обещает единую реализацию на все пути создания
// (#1189). Отказа не было, в логе тишина: расхождение находилось отчётами.
//
// Правило присутствия ключа общее с restoreUnsubmittedFields, включая
// исключение для редактируемого Флажка: снятый пользователем флажок браузер не
// шлёт, и дефолт `истина` не имеет права поставить его обратно.
//
// Ошибка вычисления или ПриСозданииНового останавливает POST: баннер на GET не
// доказывает, что пользователь его видел, а продолжение записи сохранило бы
// частично инициализированный объект. Результат возвращается вызывающему, чтобы
// тот перерисовал форму с ошибкой и сообщениями хука до Save.
//
// Табличные части сюда не переносятся намеренно: строки, созданные хуком, всё
// равно снял бы restoreUneditableTableParts — он для нового объекта чистит
// таблицы, которых на форме не было, защищаясь от подделанных строк. Хук
// заполняет такие таблицы после этой границы, в ПередЗаписью/ПриЗаписи.
func (s *Server) applyDefaultsToUnsubmittedFields(
	r *http.Request,
	entity *metadata.Entity,
	form *metadata.FormModule,
	obj *runtime.Object,
) (entityservice.NewObjectResult, error) {
	if entity == nil || form == nil || obj == nil || s.entitySvc == nil {
		return entityservice.NewObjectResult{}, nil
	}
	submitted := submittedFormKeys(r)
	checkboxes := checkboxOmittedFields(form, entity, submitted)

	// NewObject вызывается и когда форма прислала все реквизиты: на POST хук
	// ПриСозданииНового служит самостоятельной серверной проверкой и его ошибка
	// обязана остановить Save. Фильтр присутствия нужен только при переносе
	// вычисленных значений ниже — ввод пользователя главнее и дефолта, и хука.
	// Без Fields GET и POST вычисляют то же начальное состояние объекта.
	newRes, err := s.entitySvc.NewObject(r.Context(), entityservice.NewObjectRequest{
		Entity:    entity,
		FormEntry: true,
	})
	if err != nil || newRes.DSLError != "" {
		return newRes, err
	}
	if newRes.Object == nil {
		return newRes, fmt.Errorf("создание объекта %s не вернуло объект", entity.Name)
	}
	for _, f := range entity.Fields {
		if formKeySubmitted(submitted, f.Name) || checkboxes[strings.ToLower(f.Name)] {
			continue
		}
		value, ok := maskCIKeyValue(newRes.Object.Fields, f.Name)
		if !ok || value == nil {
			continue
		}
		obj.Set(f.Name, value)
	}
	return newRes, nil
}
