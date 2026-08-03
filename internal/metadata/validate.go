package metadata

import (
	"fmt"
	"regexp"
)

// ValidateConstants проверяет, что enum-/reference-константы ссылаются на
// существующие перечисления и сущности — ловит опечатки в `type:` на
// `onebase check`, до рантайма (как Validate это делает для реквизитов
// сущностей). Раньше типы констант нигде не сверялись, а при опечатке
// GetEnum/GetEntity молча возвращали nil.
func ValidateConstants(constants []*Constant, entities []*Entity, enums []*Enum) error {
	entityNames := make(map[string]bool, len(entities))
	for _, e := range entities {
		entityNames[e.Name] = true
	}
	enumNames := make(map[string]bool, len(enums))
	for _, en := range enums {
		enumNames[en.Name] = true
	}
	for _, c := range constants {
		if c.RefEntity != "" && !entityNames[c.RefEntity] {
			return fmt.Errorf("constant %s references unknown entity %s", c.Name, c.RefEntity)
		}
		if c.EnumName != "" && !enumNames[c.EnumName] {
			return fmt.Errorf("constant %s references unknown enum %s", c.Name, c.EnumName)
		}
	}
	return nil
}

func Validate(entities []*Entity, enums []*Enum) error {
	entityNames := make(map[string]bool, len(entities))
	for _, e := range entities {
		entityNames[e.Name] = true
	}
	enumNames := make(map[string]bool, len(enums))
	for _, en := range enums {
		enumNames[en.Name] = true
	}
	for _, e := range entities {
		for _, f := range e.Fields {
			if f.RefEntity != "" && !entityNames[f.RefEntity] {
				return fmt.Errorf("entity %s: field %s references unknown entity %s", e.Name, f.Name, f.RefEntity)
			}
			if f.EnumName != "" && len(enums) > 0 && !enumNames[f.EnumName] {
				return fmt.Errorf("entity %s: field %s references unknown enum %s", e.Name, f.Name, f.EnumName)
			}
		}
		if err := validateFieldIDs(e); err != nil {
			return err
		}
		if err := validateTileView(e); err != nil {
			return err
		}
		if err := validateActivity(e); err != nil {
			return err
		}
		if err := validateIndexes(e); err != nil {
			return err
		}
		for _, tp := range e.TableParts {
			for _, f := range tp.Fields {
				if IsRichText(f.Type) {
					return fmt.Errorf("поле %s.%s: тип richtext не поддерживается в табличных частях", tp.Name, f.Name)
				}
				if IsImage(f.Type) {
					return fmt.Errorf("поле %s.%s: тип image не поддерживается в табличных частях", tp.Name, f.Name)
				}
			}
		}
		for _, src := range e.BasedOn {
			if !entityNames[src] {
				return fmt.Errorf("entity %s: based_on references unknown entity %s", e.Name, src)
			}
		}
	}
	return nil
}

// fieldIDPattern ограничивает идентификатор поля латиницей, цифрами и «_»:
// он попадает в служебную таблицу соответствия и в вывод плана миграции, где
// от него нужна однозначность, а не выразительность (имя поля — отдельно).
var fieldIDPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validateFieldIDs проверяет устойчивые идентификаторы полей (план 81):
// формат и уникальность в пределах таблицы. Уникальность именно потабличная —
// у шапки и у каждой табличной части своя таблица, поэтому совпадение ID между
// ними безвредно, а внутри одной таблицы означало бы две колонки с одной
// идентичностью.
func validateFieldIDs(e *Entity) error {
	check := func(scope string, fields []Field) error {
		seen := make(map[string]string, len(fields))
		for _, f := range fields {
			if f.ID == "" {
				continue
			}
			if !fieldIDPattern.MatchString(f.ID) {
				return fmt.Errorf("%s: поле %s: id %q — допустимы латиница, цифры и подчёркивание, первый знак не цифра",
					scope, f.Name, f.ID)
			}
			if prev, dup := seen[f.ID]; dup {
				return fmt.Errorf("%s: id %q задан у двух полей (%s и %s) — идентификатор должен быть уникален",
					scope, f.ID, prev, f.Name)
			}
			seen[f.ID] = f.Name
		}
		return nil
	}
	if err := check("entity "+e.Name, e.Fields); err != nil {
		return err
	}
	for _, tp := range e.TableParts {
		if err := check("entity "+e.Name+", табличная часть "+tp.Name, tp.Fields); err != nil {
			return err
		}
	}
	return nil
}

func validateIndexes(e *Entity) error {
	for i, idx := range e.Indexes {
		if len(idx.Fields) == 0 {
			return fmt.Errorf("entity %s: indexes[%d].fields is required", e.Name, i)
		}
		for _, name := range idx.Fields {
			if findEntityField(e, name) == nil {
				return fmt.Errorf("entity %s: index references unknown field %s", e.Name, name)
			}
		}
	}
	return nil
}

func validateActivity(e *Entity) error {
	if e == nil || e.Activity == nil {
		return nil
	}
	if e.Kind != KindCatalog {
		return fmt.Errorf("entity %s: activity is supported only for catalogs", e.Name)
	}
	if e.Activity.Field == "" {
		return fmt.Errorf("entity %s: activity.field is required", e.Name)
	}
	f := findEntityField(e, e.Activity.Field)
	if f == nil {
		return fmt.Errorf("entity %s: activity.field references unknown field %s", e.Name, e.Activity.Field)
	}
	if f.Type != FieldTypeBool || f.RefEntity != "" {
		return fmt.Errorf("entity %s: activity.field %s must have type bool", e.Name, e.Activity.Field)
	}
	switch e.Activity.DefaultScope {
	case "", ActivityScopeActive, ActivityScopeAll:
	default:
		return fmt.Errorf("entity %s: activity.default_scope must be active or all", e.Name)
	}
	return nil
}

func validateTileView(e *Entity) error {
	if e == nil || e.TileView == nil {
		return nil
	}
	if e.TileView.Image != "" {
		f := findEntityField(e, e.TileView.Image)
		if f == nil {
			return fmt.Errorf("entity %s: tile_view.image references unknown field %s", e.Name, e.TileView.Image)
		}
		if !IsImage(f.Type) {
			return fmt.Errorf("entity %s: tile_view.image field %s must have type image", e.Name, e.TileView.Image)
		}
	}
	for _, item := range []struct {
		role string
		name string
	}{
		{"title", e.TileView.Title},
		{"subtitle", e.TileView.Subtitle},
	} {
		if item.name == "" {
			continue
		}
		if findEntityField(e, item.name) == nil {
			return fmt.Errorf("entity %s: tile_view.%s references unknown field %s", e.Name, item.role, item.name)
		}
	}
	for _, name := range e.TileView.Fields {
		if findEntityField(e, name) == nil {
			return fmt.Errorf("entity %s: tile_view.fields references unknown field %s", e.Name, name)
		}
	}
	return nil
}

func findEntityField(e *Entity, name string) *Field {
	for i := range e.Fields {
		if e.Fields[i].Name == name {
			return &e.Fields[i]
		}
	}
	return nil
}
