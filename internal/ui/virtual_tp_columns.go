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
			s.fillVirtualColumn(ctx, *tp, vc, rows)
		}
		return true
	})
}

func (s *Server) fillVirtualColumn(
	ctx context.Context,
	tp metadata.TablePart,
	vc metadata.FormVirtualColumn,
	rows []map[string]any,
) {
	refName, ok := vc.RefFieldName()
	if !ok {
		return
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
		return
	}
	target := s.reg.GetEntity(refField.RefEntity)
	if target == nil {
		return
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
		return
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
		return
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
			return
		}
		for idStr, refRow := range refRows {
			s.maskRecord(ctx, target, refRow)
			if v, ok := refRow[targetField.Name]; ok && v != nil {
				values[idStr] = fmt.Sprintf("%v", v)
			}
		}
	}

	for _, row := range rows {
		// Пустая или битая ссылка даёт пустую ячейку без маркера: строка ТЧ с
		// незаполненной ссылкой — рабочее состояние ввода, и «—» в такой ячейке
		// читался бы как значение.
		row[vc.Name] = ""
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
}

// formElementTablePart — табличная часть сущности, к которой привязан элемент
// формы (ключ table_part или data_path «Объект.<ТЧ>»).
func formElementTablePart(entity *metadata.Entity, el *metadata.FormElement) *metadata.TablePart {
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
