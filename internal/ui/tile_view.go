package ui

import (
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

type tileViewRender struct {
	ImageFields   []metadata.Field
	TitleField    *metadata.Field
	SubtitleField *metadata.Field
	Fields        []metadata.Field
}

func resolveTileView(entity *metadata.Entity) tileViewRender {
	var out tileViewRender
	if entity == nil {
		return out
	}
	cfg := entity.TileView
	if cfg == nil {
		for _, f := range entity.Fields {
			if metadata.IsImage(f.Type) {
				out.ImageFields = append(out.ImageFields, f)
				continue
			}
			if out.TitleField == nil {
				out.TitleField = fieldPtr(f)
				continue
			}
			out.Fields = append(out.Fields, f)
		}
		return out
	}

	if cfg.Image != "" {
		if f := uiFieldByName(entity, cfg.Image); f != nil && metadata.IsImage(f.Type) {
			out.ImageFields = append(out.ImageFields, *f)
		}
	} else if f := firstImageField(entity); f != nil {
		out.ImageFields = append(out.ImageFields, *f)
	}

	if cfg.Title != "" {
		out.TitleField = uiFieldByName(entity, cfg.Title)
	}
	if out.TitleField == nil {
		out.TitleField = firstNonImageField(entity)
	}
	if cfg.Subtitle != "" {
		out.SubtitleField = uiFieldByName(entity, cfg.Subtitle)
	}

	if cfg.FieldsSet || len(cfg.Fields) > 0 {
		for _, name := range cfg.Fields {
			if f := uiFieldByName(entity, name); f != nil && !tileFieldUsed(out, f.Name) {
				out.Fields = append(out.Fields, *f)
			}
		}
		return out
	}

	for _, f := range entity.Fields {
		if metadata.IsImage(f.Type) || tileFieldUsed(out, f.Name) {
			continue
		}
		out.Fields = append(out.Fields, f)
	}
	return out
}

// resolveListColumns возвращает набор колонок для табличных режимов списка
// (страницы/лента/дерево) и экспорта. Источники настройки применяются от более
// специализированного к общему: управляемая ФормаСписка, list_form сущности,
// tile_view.fields, затем все поля. Так обычный конфигуратор и редактор
// управляемой формы задают один и тот же состав и порядок колонок (#386).
func resolveListColumns(entity *metadata.Entity) []metadata.Field {
	if entity == nil {
		return nil
	}
	if form := pickManagedForm(entity, "list"); form != nil {
		if cols := managedListColumns(entity, form); len(cols) > 0 {
			return cols
		}
	}
	if len(entity.ListForm) > 0 {
		return namedListColumns(entity, entity.ListForm)
	}
	tv := entity.TileView
	// Тот же критерий «набор задан», что и в resolveTileView: явный fields: []
	// (FieldsSet) ИЛИ непустой список. Иначе — все поля, как раньше.
	if tv == nil || (!tv.FieldsSet && len(tv.Fields) == 0) {
		return entity.Fields
	}
	view := resolveTileView(entity)
	cols := make([]metadata.Field, 0, len(view.Fields)+2)
	seen := map[string]bool{}
	add := func(f *metadata.Field) {
		if f == nil || seen[f.Name] {
			return
		}
		seen[f.Name] = true
		cols = append(cols, *f)
	}
	add(view.TitleField)
	add(view.SubtitleField)
	for i := range view.Fields {
		add(&view.Fields[i])
	}
	return cols
}

// namedListColumns сохраняет заданный пользователем порядок и отбрасывает
// дубликаты. Неизвестные имена не превращают настройку обратно в «все поля»:
// валидатор конфигурации отдельно сообщит о такой ссылке.
func namedListColumns(entity *metadata.Entity, names []string) []metadata.Field {
	cols := make([]metadata.Field, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		field := uiFieldByNameFold(entity, strings.TrimSpace(name))
		if field == nil {
			continue
		}
		key := strings.ToLower(field.Name)
		if seen[key] {
			continue
		}
		seen[key] = true
		cols = append(cols, *field)
	}
	return cols
}

// managedListColumns извлекает колонки из дерева элементов управляемой формы.
// Для импортированных форм это обычно Колонка с data_path "Список.Поле";
// нативный редактор также может хранить ПолеВвода/ПолеФормы с путём
// "Объект.Поле". Контейнеры обходятся рекурсивно, поэтому порядок на форме
// становится порядком колонок списка.
func managedListColumns(entity *metadata.Entity, form *metadata.FormModule) []metadata.Field {
	if entity == nil || form == nil {
		return nil
	}
	var cols []metadata.Field
	seen := make(map[string]bool)
	var walk func([]*metadata.FormElement)
	walk = func(elements []*metadata.FormElement) {
		for _, element := range elements {
			if element == nil {
				continue
			}
			if isManagedListColumnElement(element.Kind) {
				name := formDataPathFieldName(element.DataPath)
				if name == "" {
					name = strings.TrimSpace(element.FieldName)
				}
				field := uiFieldByNameFold(entity, name)
				if field != nil {
					key := strings.ToLower(field.Name)
					if !seen[key] {
						seen[key] = true
						cols = append(cols, managedColumnField(*field, element))
					}
				}
			}
			walk(element.Children)
		}
	}
	walk(form.Elements)
	return cols
}

func isManagedListColumnElement(kind metadata.FormElementType) bool {
	switch kind {
	case metadata.FormElementColumn,
		metadata.FormElementField,
		metadata.FormElementFormField,
		metadata.FormElementCheckbox,
		metadata.FormElementSwitch,
		metadata.FormElementInputList,
		metadata.FormElementDatePicker,
		metadata.FormElementPicture:
		return true
	default:
		return false
	}
}

func isTreeListColumn(cols []metadata.Field, idx int) bool {
	if idx < 0 || idx >= len(cols) {
		return false
	}
	for _, col := range cols {
		if col.Name == "Наименование" {
			return cols[idx].Name == "Наименование"
		}
	}
	return idx == 0
}

func fieldPtr(f metadata.Field) *metadata.Field {
	ff := f
	return &ff
}

func uiFieldByName(entity *metadata.Entity, name string) *metadata.Field {
	for i := range entity.Fields {
		if entity.Fields[i].Name == name {
			return &entity.Fields[i]
		}
	}
	return nil
}

func uiFieldByNameFold(entity *metadata.Entity, name string) *metadata.Field {
	if field := uiFieldByName(entity, name); field != nil {
		return field
	}
	for i := range entity.Fields {
		if strings.EqualFold(entity.Fields[i].Name, name) {
			return &entity.Fields[i]
		}
	}
	return nil
}

func firstImageField(entity *metadata.Entity) *metadata.Field {
	for i := range entity.Fields {
		if metadata.IsImage(entity.Fields[i].Type) {
			return &entity.Fields[i]
		}
	}
	return nil
}

func firstNonImageField(entity *metadata.Entity) *metadata.Field {
	for i := range entity.Fields {
		if !metadata.IsImage(entity.Fields[i].Type) {
			return &entity.Fields[i]
		}
	}
	return nil
}

func tileFieldUsed(view tileViewRender, name string) bool {
	if view.TitleField != nil && view.TitleField.Name == name {
		return true
	}
	if view.SubtitleField != nil && view.SubtitleField.Name == name {
		return true
	}
	for _, f := range view.ImageFields {
		if f.Name == name {
			return true
		}
	}
	return false
}
