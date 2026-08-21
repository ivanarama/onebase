package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
)

// applyVirtualTPColumns заполняет виртуальные колонки табличных частей формы
// (#845): реквизит по ссылке из строки, который показывается, но не хранится.
//
// Механика та же, что у подписей ссылок (enrichTPRowsWithRefs): собрать
// уникальные id по ссылочному реквизиту, прочитать их батчами и разложить
// значения обратно по строкам. Разница ровно одна — читается запрошенный
// реквизит, а не поле представления.
//
// Доступ намеренно строже, чем у подписи. Подпись чужой записи UI отдаёт и
// сегодня, но виртуальная колонка показывает ПРОИЗВОЛЬНЫЙ реквизит, и
// расширять на него исторический зазор нельзя. Поэтому чтение идёт через
// readableFieldsByIDs (RBAC и строковые ограничения целевой сущности в SQL), а
// значение проходит маску ПДн: строка, недоступная пользователю, даёт пусто, а
// не значение.
func (s *Server) applyVirtualTPColumns(
	ctx context.Context,
	entity *metadata.Entity,
	form *metadata.FormModule,
	tpRows map[string][]map[string]any,
) {
	if entity == nil || form == nil || len(tpRows) == 0 {
		return
	}
	seen := make(map[string]bool)
	form.Walk(func(el *metadata.FormElement) bool {
		if len(el.VirtualColumns) == 0 {
			return true
		}
		tp := formElementTablePart(entity, el)
		if tp == nil {
			return true
		}
		rows := tpRows[tp.Name]
		if len(rows) == 0 {
			return true
		}
		for _, vc := range el.VirtualColumns {
			if !usableVirtualTPColumnName(tp.Fields, vc.Name) {
				continue
			}
			name := strings.TrimSpace(vc.Name)
			key := strings.ToLower(strings.TrimSpace(tp.Name)) + "\x00" + strings.ToLower(name)
			if seen[key] {
				continue
			}
			if s.fillVirtualColumn(ctx, *tp, vc, rows) {
				seen[key] = true
			}
		}
		return true
	})
}

// usableVirtualTPColumnName protects the shared row-map contract even when a
// project is started without running `onebase check` first. In particular, a
// virtual column must never overwrite a stored table-part value: the browser
// would otherwise serialize the projected value back into that stored field.
func usableVirtualTPColumnName(fields []metadata.Field, name string) bool {
	trimmed := strings.TrimSpace(name)
	if name != trimmed || !metadata.ValidIdent(name) || metadata.IsReservedFormVirtualColumnName(name) {
		return false
	}
	for _, field := range fields {
		if strings.EqualFold(field.Name, name) {
			return false
		}
	}
	return true
}

func filterVirtualTPColumns(fields []metadata.Field, virtual []metadata.FormVirtualColumn) []metadata.FormVirtualColumn {
	filtered := make([]metadata.FormVirtualColumn, 0, len(virtual))
	seen := make(map[string]bool, len(virtual))
	for _, vc := range virtual {
		name := strings.TrimSpace(vc.Name)
		key := strings.ToLower(name)
		if !usableVirtualTPColumnName(fields, vc.Name) || seen[key] {
			continue
		}
		seen[key] = true
		filtered = append(filtered, vc)
	}
	return filtered
}

func (s *Server) fillVirtualColumn(
	ctx context.Context,
	tp metadata.TablePart,
	vc metadata.FormVirtualColumn,
	rows []map[string]any,
) bool {
	// The virtual key may already be present after a form event. Clear it before
	// every validation/read exit so a broken reference or denied batch cannot
	// leave a stale or handler-supplied value visible as a trusted projection.
	for _, row := range rows {
		row[vc.Name] = ""
	}

	refName, ok := vc.RefFieldName()
	if !ok {
		return false
	}
	targetName, _ := vc.TargetFieldName()

	var refField metadata.Field
	found := false
	for _, f := range tp.Fields {
		if strings.EqualFold(f.Name, refName) && f.RefEntity != "" {
			refField, found = f, true
			break
		}
	}
	if !found {
		return false
	}
	target := s.reg.GetEntity(refField.RefEntity)
	if target == nil {
		return false
	}
	var targetField metadata.Field
	found = false
	for _, f := range target.Fields {
		if strings.EqualFold(f.Name, targetName) {
			targetField, found = f, true
			break
		}
	}
	if !found {
		return false
	}

	idsByString := map[string]uuid.UUID{}
	for _, row := range rows {
		if _, v, ok := lookupMapCI(row, refField.Name); ok {
			if idStr, id, ok := uuidFromValue(v); ok {
				idsByString[idStr] = id
			}
		}
	}
	if len(idsByString) == 0 {
		return true
	}
	ids := make([]uuid.UUID, 0, len(idsByString))
	for _, id := range idsByString {
		ids = append(ids, id)
	}

	values := make(map[string]string, len(ids))
	for start := 0; start < len(ids); start += refLabelBatchSize {
		end := start + refLabelBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		refRows, err := s.readableFieldsByIDs(ctx, target, ids[start:end], []metadata.Field{targetField})
		if err != nil {
			// Отказ в доступе или ошибка чтения оставляют колонку пустой. Пустая
			// колонка честнее частичной: иначе пользователь не отличил бы «нет
			// значения» от «часть батча не прочиталась».
			return true
		}
		for idStr, refRow := range refRows {
			s.maskRecord(ctx, target, refRow)
			if v, ok := refRow[targetField.Name]; ok && v != nil {
				values[idStr] = virtualColumnCellText(targetField, v)
			}
		}
	}

	for _, row := range rows {
		// Пустая или битая ссылка даёт пустую ячейку без маркера: строка ТЧ с
		// незаполненной ссылкой — рабочее состояние ввода, и «—» в такой ячейке
		// читался бы как значение.
		_, v, ok := lookupMapCI(row, refField.Name)
		if !ok || v == nil {
			continue
		}
		idStr, _, ok := uuidFromValue(v)
		if !ok {
			continue
		}
		if val, ok := values[idStr]; ok {
			row[vc.Name] = val
		}
	}
	return true
}

// virtualColumnCellText превращает значение реквизита цели в текст ячейки С
// УЧЁТОМ ЕГО ТИПА.
//
// Почему это обязано жить на сервере. Клиенту виртуальная колонка объявлена
// строковой (managedTPColumnsJSON в templates.go прибивает `Type` константой), а
// в buildColumns ветка `if (c.virtual)` замыкает рендер до всех типовых
// проверок — то есть что сервер положил, то пользователь и увидит. Одной только
// отправки настоящего типа клиенту не хватило бы.
//
// До #1071 здесь стоял `fmt.Sprintf("%v", v)`, и в ячейку попадало внутреннее
// представление Go. Для bool и date оно вдобавок ЗАВИСЕЛО ОТ ДИАЛЕКТА: одна и та
// же конфигурация на одних и тех же данных показывала «1» на SQLite и «true» на
// PostgreSQL, а дату — «1985-03-13 21:00:00 +0000 UTC» против
// «1985-03-14 00:00:00 +0300 MSK», причём на SQLite ещё и днём раньше
// записанного.
//
// Разобраны ровно те два типа, по которым расходились диалекты. Перечисление
// показывает код вместо подписи, ссылка — UUID, richtext — теги: это тот же
// корень «тип известен, но не используется», однако одинаково на обеих СУБД, и
// им нужны подписи перечислений и чтение представления ссылки — своя обвязка.
// Вынесено в отдельную заявку, чтобы не мешать с починкой расхождения.
func virtualColumnCellText(f metadata.Field, v any) string {
	switch f.Type {
	case metadata.FieldTypeBool:
		// Тот же вид, что рисует клиентский форматтер у ХРАНИМОЙ bool-колонки
		// (managed.js): иначе две соседние колонки одного смысла выглядели бы
		// по-разному. Пустая строка тут не годится — она уже занята под
		// «ссылки нет», и «ложь» стала бы неотличима от незаполненной строки.
		if asBool(v) {
			return "✓"
		}
		return "—"
	case metadata.FieldTypeDate:
		return fmtDateValue(v)
	}
	return fmt.Sprintf("%v", v)
}

// formElementTablePart — табличная часть сущности, к которой привязан элемент
// формы (ключ table_part или data_path «Объект.<ТЧ>»).
func formElementTablePart(entity *metadata.Entity, el *metadata.FormElement) *metadata.TablePart {
	if entity == nil || el == nil || el.Kind != metadata.FormElementTablePart {
		return nil
	}
	name := el.TablePart
	if name == "" {
		if parts := strings.Split(el.DataPath, "."); len(parts) == 2 && strings.EqualFold(parts[0], "Объект") {
			name = parts[1]
		}
	}
	if name == "" {
		return nil
	}
	for i := range entity.TableParts {
		if strings.EqualFold(entity.TableParts[i].Name, name) {
			return &entity.TableParts[i]
		}
	}
	return nil
}
