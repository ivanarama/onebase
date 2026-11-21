// Package resultfilter proves and evaluates user report filters before row caps.
// It never trusts a client-supplied type or source and never interpolates SQL.
package resultfilter

import (
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/typedempty"
)

type Lookup func(kind, name string) *metadata.Entity

// Allowed is the shared report output/field policy, applied after lineage proof.
// A nil policy denies all columns. UUID describes a link, not its presentation.
type Allowed func(source query.SourceRef, meta *metadata.Entity, field string, uuid bool) bool

type Field struct {
	Values      []string        `json:"values,omitempty"`
	Name        string          `json:"name"`
	Type        string          `json:"type"`
	RefEntity   string          `json:"ref_entity,omitempty"`
	EnumName    string          `json:"enum_name,omitempty"`
	Operators   []string        `json:"operators"`
	Source      query.SourceRef `json:"-"`
	SourceField string          `json:"-"`
}

// Schema rejects ambiguous/derived output. Aliases only name a proven column.
// Virtual tables, wildcards and reference presentations need further compiler
// proof and deliberately do not become filterable here.
func Schema(res query.Result, lookup Lookup, allowed Allowed) []Field {
	if res.Limited || !res.Projection.Simple || lookup == nil || allowed == nil {
		return nil
	}
	counts := map[string]int{}
	for _, col := range res.Projection.Columns {
		if col.Star || col.Output == "" {
			return nil
		}
		counts[strings.ToLower(outputName(res, col))]++
	}
	var out []Field
	for _, col := range res.Projection.Columns {
		name := outputName(res, col)
		if counts[strings.ToLower(name)] != 1 || len(col.Path) == 0 {
			continue
		}
		source, path, ok := sourcePath(res.SourceOccurrences, col.Path)
		if !ok || source.Virtual || source.Kind == "" {
			continue
		}
		meta := lookup(source.Kind, source.Name)
		if meta == nil {
			continue
		}
		if source.TablePart != "" {
			var found *metadata.Entity
			for i := range meta.TableParts {
				if strings.EqualFold(meta.TableParts[i].Name, source.TablePart) {
					found = metadata.TablePartQueryEntity(meta, &meta.TableParts[i], meta.Name)
					break
				}
			}
			meta = found
			if meta == nil {
				continue
			}
		}
		var desc typedempty.Descriptor
		sourceField := ""
		isUUID := false
		if len(path) == 1 && (strings.EqualFold(path[0], "Ссылка") || strings.EqualFold(path[0], "Ref")) && source.TablePart == "" && res.RefColumns[name] != "" && strings.EqualFold(res.RefColumns[name], source.Name) {
			desc = typedempty.Descriptor{Type: metadata.FieldType("reference:" + source.Name), RefEntity: source.Name}
			sourceField = path[0]
			isUUID = true
		} else {
			if len(path) < 1 || len(path) > 2 {
				continue
			}
			var field *metadata.Field
			for i := range meta.Fields {
				if strings.EqualFold(meta.Fields[i].Name, path[0]) {
					if field != nil {
						field = nil
						break
					}
					field = &meta.Fields[i]
				}
			}
			if field == nil {
				continue
			}
			desc, ok = typedempty.FromField(field)
			if !ok {
				continue
			}
			sourceField = field.Name
			if desc.RefEntity != "" {
				if len(path) != 2 || (!strings.EqualFold(path[1], "Ссылка") && !strings.EqualFold(path[1], "Ref")) || !strings.EqualFold(res.RefColumns[name], desc.RefEntity) {
					continue
				}
				isUUID = true
			} else if len(path) != 1 {
				continue
			}
		}
		if !allowed(source.SourceRef, meta, sourceField, isUUID) {
			continue
		}
		f := Field{Name: name, Type: string(desc.Type), RefEntity: desc.RefEntity, EnumName: desc.EnumName, Source: source.SourceRef, SourceField: sourceField}
		f.Operators = operators(f)
		if len(f.Operators) > 0 {
			out = append(out, f)
		}
	}
	return out
}

func operators(f Field) []string {
	if f.RefEntity != "" || f.EnumName != "" {
		return []string{"eq", "ne", "is_null", "not_null"}
	}
	switch f.Type {
	case "string":
		return []string{"eq", "ne", "contains", "is_null", "not_null"}
	case "number", "date", "datetime":
		return []string{"eq", "ne", "gt", "ge", "lt", "le", "is_null", "not_null"}
	case "bool":
		return []string{"eq", "ne", "is_null", "not_null"}
	}
	return nil
}
func sourcePath(sources []query.SourceOccurrence, path []string) (query.SourceOccurrence, []string, bool) {
	if len(path) > 1 {
		var found query.SourceOccurrence
		n := 0
		for _, s := range sources {
			if strings.EqualFold(s.Qualifier, path[0]) {
				found = s
				n++
			}
		}
		if n == 1 {
			return found, path[1:], true
		}
		if n > 1 {
			return query.SourceOccurrence{}, nil, false
		}
	}
	if len(sources) == 1 {
		return sources[0], path, true
	}
	return query.SourceOccurrence{}, nil, false
}
func outputName(res query.Result, col query.ProjectionColumn) string {
	name := strings.ToLower(col.Output)
	if col.Alias == "" {
		if actual := res.DSLColumnAliases[name]; actual != "" {
			name = actual
		}
		if len(col.Path) > 0 && (strings.EqualFold(col.Path[len(col.Path)-1], "Ссылка") || strings.EqualFold(col.Path[len(col.Path)-1], "Ref")) {
			if res.RefColumns["id"] != "" {
				name = "id"
			}
		}
	}
	if logical := res.LongIdents[name]; logical != "" {
		name = logical
	}
	return name
}
