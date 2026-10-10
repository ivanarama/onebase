package metadata

import (
	"fmt"
	"strings"
)

// EntityFieldPath identifies a declared header field or a field of a table part.
// Path preserves the declared spelling; TablePart is nil for header fields.
type EntityFieldPath struct {
	Path      string
	Field     Field
	TablePart *TablePart
}

// ResolveEntityFieldPath resolves exactly one field or TablePart.Field without
// traversing references or accepting generated service columns.
func ResolveEntityFieldPath(e *Entity, path string) (EntityFieldPath, error) {
	fail := func(reason string) (EntityFieldPath, error) {
		name := ""
		if e != nil {
			name = e.Name
		}
		return EntityFieldPath{}, fmt.Errorf("entity %s: путь %q: %s", name, path, reason)
	}
	parts := strings.Split(path, ".")
	if e == nil || len(parts) > 2 {
		return fail("ожидается реквизит или ТабличнаяЧасть.Поле")
	}
	for _, part := range parts {
		if part == "" || strings.TrimSpace(part) != part {
			return fail("пустой сегмент или пробелы в имени")
		}
		switch strings.ToLower(part) {
		case "id", "parent_id", "строка", "is_folder", "deletion_mark", "_version", "posted":
			return fail("служебные колонки не участвуют в поиске")
		}
	}
	if len(parts) == 1 {
		if f := findEntityFieldFold(e, parts[0]); f != nil {
			return EntityFieldPath{Path: f.Name, Field: *f}, nil
		}
		return fail("неизвестный реквизит шапки")
	}
	for i := range e.TableParts {
		tp := &e.TableParts[i]
		if !strings.EqualFold(tp.Name, parts[0]) {
			continue
		}
		for _, f := range tp.Fields {
			if strings.EqualFold(f.Name, parts[1]) {
				return EntityFieldPath{Path: tp.Name + "." + f.Name, Field: f, TablePart: tp}, nil
			}
		}
		return fail("неизвестное поле табличной части")
	}
	return fail("неизвестная табличная часть")
}

// SearchFieldPaths is the single source of paths for list and reference search.
// An absent key selects only header strings; an explicit empty list disables search.
func SearchFieldPaths(e *Entity) ([]EntityFieldPath, error) {
	if e == nil {
		return nil, nil
	}
	return entityFieldPaths(e, e.Search, e.SearchSet, "search_fields")
}

// FullTextFieldPaths resolves the shared path contract. User validation still
// rejects table-part fulltext until incremental indexing is atomic (plan 175C).
func FullTextFieldPaths(e *Entity) ([]EntityFieldPath, error) {
	if e == nil {
		return nil, nil
	}
	return entityFieldPaths(e, e.FullText, e.FullTextSet, "fulltext")
}

func entityFieldPaths(e *Entity, names []string, set bool, key string) ([]EntityFieldPath, error) {
	var out []EntityFieldPath
	if !set {
		for _, f := range e.Fields {
			if f.Type == FieldTypeString && f.RefEntity == "" {
				out = append(out, EntityFieldPath{Path: f.Name, Field: f})
			}
		}
		return out, nil
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		path, err := ResolveEntityFieldPath(e, name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		f := path.Field
		ref := f.RefEntity != "" || strings.HasPrefix(string(f.Type), "reference:")
		if key == "search_fields" && ref {
			return nil, fmt.Errorf("entity %s: search_fields путь %q — ссылка, поиск по UUID не поддерживается", e.Name, name)
		}
		if key == "fulltext" && (ref || f.EnumName != "" || (f.Type != FieldTypeString && !IsRichText(f.Type))) {
			return nil, fmt.Errorf("entity %s: fulltext путь %q нельзя индексировать — нужен тип string или richtext", e.Name, name)
		}
		canonical := strings.ToLower(path.Path)
		if seen[canonical] {
			return nil, fmt.Errorf("entity %s: путь %q указан в %s дважды", e.Name, name, key)
		}
		seen[canonical] = true
		out = append(out, path)
	}
	return out, nil
}

// HeaderFullTextFields adapts typed paths for the existing header-only FTS
// consumers. It fails closed on any unresolved or table-part path, so a dotted
// path cannot be silently discarded while the rest of an index is published.
func HeaderFullTextFields(e *Entity) []Field {
	paths, err := FullTextFieldPaths(e)
	if err != nil {
		return nil
	}
	fields := make([]Field, 0, len(paths))
	for _, path := range paths {
		if path.TablePart != nil {
			return nil
		}
		fields = append(fields, path.Field)
	}
	return fields
}
