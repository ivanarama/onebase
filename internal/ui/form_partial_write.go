package ui

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

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

// checkboxOmittedFields собирает поля сущности типа bool, отрисованные этой
// формой как не-ReadOnly kind: Флажок. Только для них отсутствие ключа значит
// «снято» — у ReadOnly-флажка контрол disabled, пользователь его не менял, и
// восстановление обязано сработать. Обход рекурсивный: ГруппаФормы,
// СтраницыФормы и Страница держат элементы в Children.
func checkboxOmittedFields(form *metadata.FormModule, entity *metadata.Entity) map[string]bool {
	out := make(map[string]bool)
	if form == nil || entity == nil {
		return out
	}
	var walk func(el *metadata.FormElement)
	walk = func(el *metadata.FormElement) {
		if el == nil {
			return
		}
		if string(el.Kind) == "Флажок" && !el.ReadOnly && el.DataPath != "" {
			// Поле шапки: data_path вида «Объект.Флаг». Путь табличной части
			// («Объект.Товары.Цена») к полю шапки отношения не имеет, даже если
			// имя последнего сегмента совпало с полем сущности.
			if strings.Count(el.DataPath, ".") <= 1 {
				name := dpFieldName(el.DataPath)
				if f, ok := entityFieldByName(entity, name); ok && f.Type == metadata.FieldTypeBool {
					out[strings.ToLower(name)] = true
				}
			}
		}
		for _, c := range el.Children {
			walk(c)
		}
	}
	for _, el := range form.Elements {
		walk(el)
	}
	return out
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
	checkboxes := checkboxOmittedFields(form, entity)

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
