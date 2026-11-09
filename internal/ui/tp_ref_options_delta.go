package ui

import (
	"context"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Ответ события формы пересылал варианты выбора ссылочных колонок ВСЕХ
// табличных частей сущности на каждое событие: у заявки колл-центра это
// 17 ТЧ, десятки запросов к базе и 66 КБ JSON, одинаковых от события к
// событию. Браузер уже держит варианты со страницы и применяет tpRefOptions
// по ключам ТЧ: ТЧ, которой нет в ответе, сохраняет прежние варианты.
//
// Поэтому в ответе события варианты считаются только для ТЧ, которые стоят на
// этой форме, и только если обработчик изменил их строки (новые ссылки нужны
// с подписями) или ссылочная колонка зависит от владельца: владельца мог
// поменять сам пользователь до события, а этого сервер по снимку не отличит.

type tpRefDeltaKey struct{}

type tpRefDelta struct {
	form   *metadata.FormModule
	before map[string][]map[string]any
	after  map[string][]map[string]any
}

// withTPRefDelta включает отбор для ответа события: before — строки ТЧ до
// обработчика (как их прислал браузер), after — после.
func withTPRefDelta(ctx context.Context, form *metadata.FormModule, before, after map[string][]map[string]any) context.Context {
	if form == nil {
		return ctx
	}
	return context.WithValue(ctx, tpRefDeltaKey{}, &tpRefDelta{form: form, before: before, after: after})
}

// skipTablePart — варианты этой ТЧ браузеру не нужны: ТЧ нет на форме, или
// обработчик её не менял и варианты не зависят от владельца.
func (d *tpRefDelta) skipTablePart(entity *metadata.Entity, tp metadata.TablePart, placed map[string]bool, ownerScoped bool) bool {
	if !placed[strings.ToLower(tp.Name)] {
		return true
	}
	if ownerScoped {
		return false
	}
	return tpRowsEqual(d.after[tp.Name], d.before[tp.Name], tp)
}

// placedTableParts — канонические имена ТЧ сущности, стоящих на форме
// (элемент ТабличнаяЧасть), в нижнем регистре.
func (d *tpRefDelta) placedTableParts(entity *metadata.Entity) map[string]bool {
	placed := map[string]bool{}
	definitions, err := metadata.FormTableDefinitions(d.form, entity.TableParts)
	canonical := func(name string) string {
		if err == nil {
			for _, def := range definitions {
				if strings.EqualFold(def.Name, name) {
					return def.Name
				}
			}
		}
		return name
	}
	d.form.Walk(func(el *metadata.FormElement) bool {
		if el != nil && el.Kind == metadata.FormElementTablePart {
			// Шаблон берёт ТЧ из data_path; table_part учитываем тоже — лишняя
			// ТЧ в списке стоит только запроса, пропущенная — подписей.
			for _, name := range []string{dpFieldName(el.DataPath), strings.TrimSpace(el.TablePart)} {
				if name != "" {
					placed[strings.ToLower(canonical(name))] = true
				}
			}
		}
		return true
	})
	return placed
}

// tpRefOwnerScoped — у ссылочной колонки ТЧ варианты зависят от владельца
// (справочник подчинён тому, что выбрано в шапке).
func tpRefOwnerScoped(entity *metadata.Entity, tp metadata.TablePart, reg interface {
	GetEntity(string) *metadata.Entity
}) bool {
	for _, f := range tp.Fields {
		if f.RefEntity == "" || f.RefEntity == "_users" {
			continue
		}
		if ref := reg.GetEntity(f.RefEntity); ref != nil {
			if _, ok := ownerHolderField(entity, ref.Owner); ok {
				return true
			}
		}
	}
	return false
}

// formPlacedFieldNames — реквизиты (в нижнем регистре), на которые ссылается
// хоть один элемент управляемой формы: каждый сегмент data_path, — лишний
// реквизит в наборе стоит только запроса, пропущенный — подписи. nil —
// управляемой формы нет: авто-форма рисует все поля.
func formPlacedFieldNames(form *metadata.FormModule) map[string]bool {
	if form == nil {
		return nil
	}
	placed := map[string]bool{}
	form.Walk(func(el *metadata.FormElement) bool {
		if el != nil && strings.TrimSpace(el.DataPath) != "" {
			for _, part := range strings.Split(el.DataPath, ".") {
				if part = strings.TrimSpace(part); part != "" {
					placed[strings.ToLower(part)] = true
				}
			}
		}
		return true
	})
	return placed
}
