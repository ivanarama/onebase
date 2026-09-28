package configcheck

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"gopkg.in/yaml.v3"
)

// CheckFormChoiceFilterYAML validates the small part of the public YAML
// contract that yaml.v3 would otherwise silently discard before Project.Load:
// unknown keys inside choice_filter conditions. Shape errors are reported here
// as well so they retain the stable form.choice-filter code even when the
// typed project loader cannot decode the file.
func CheckFormChoiceFilterYAML(dir string) []Issue {
	formsDir := filepath.Join(dir, "forms")
	formsRoot, err := os.OpenRoot(formsDir)
	if err != nil {
		return nil
	}
	defer func() { _ = formsRoot.Close() }()

	var issues []Issue
	_ = fs.WalkDir(formsRoot.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry == nil || entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".form.yaml") {
			return nil
		}
		data, err := formsRoot.ReadFile(path)
		if err != nil || len(strings.TrimSpace(string(data))) == 0 {
			return nil
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
			return nil // syntax/type diagnostics are emitted by the normal loader
		}
		rootNode := doc.Content[0]
		elements := yamlMapValue(rootNode, "elements")
		fullPath := filepath.Join(formsDir, filepath.FromSlash(path))
		walkChoiceFilterYAML(elements, "elements", relLabel(dir, fullPath), &issues)
		return nil
	})
	return issues
}

func walkChoiceFilterYAML(elements *yaml.Node, path, file string, issues *[]Issue) {
	if elements == nil || elements.Kind != yaml.SequenceNode {
		return
	}
	for index, element := range elements.Content {
		if element == nil || element.Kind != yaml.MappingNode {
			continue
		}
		elementPath := fmt.Sprintf("%s[%d]", path, index)
		for i := 0; i+1 < len(element.Content); i += 2 {
			key, value := element.Content[i], element.Content[i+1]
			switch key.Value {
			case "choice_filter":
				validateChoiceFilterYAML(value, elementPath+".choice_filter", file, issues)
			case "children":
				walkChoiceFilterYAML(value, elementPath+".children", file, issues)
			}
		}
	}
}

func validateChoiceFilterYAML(node *yaml.Node, path, file string, issues *[]Issue) {
	add := func(at *yaml.Node, message string) {
		issue := Issue{File: file, Kind: "Управляемая форма", Code: "form.choice-filter", Message: message}
		if at != nil {
			issue.Line, issue.Column = at.Line, at.Column
		}
		*issues = append(*issues, issue)
	}
	if node == nil || node.Kind != yaml.SequenceNode {
		add(node, fmt.Sprintf("%s должен быть списком условий", path))
		return
	}
	allowed := map[string]bool{"field": true, "op": true, "from": true, "value": true}
	for index, condition := range node.Content {
		conditionPath := fmt.Sprintf("%s[%d]", path, index)
		if condition == nil || condition.Kind != yaml.MappingNode {
			add(condition, conditionPath+" должен быть объектом")
			continue
		}
		for i := 0; i+1 < len(condition.Content); i += 2 {
			key, value := condition.Content[i], condition.Content[i+1]
			if !allowed[key.Value] {
				add(key, fmt.Sprintf("%s: неизвестный ключ %q", conditionPath, key.Value))
				continue
			}
			switch key.Value {
			case "field", "op", "from":
				if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
					add(value, fmt.Sprintf("%s.%s должен быть строкой", conditionPath, key.Value))
				}
			case "value":
				if value.Kind != yaml.ScalarNode || value.Tag != "!!bool" {
					add(value, fmt.Sprintf("%s.value должен быть boolean", conditionPath))
				}
			}
		}
	}
}

// CheckFormChoiceFilter validates the closed, server-authoritative
// choice_filter contract from plan 170. The check is blocking: accepting an
// ambiguous or mistyped condition would either expose the full catalog or make
// a picker silently empty at runtime.
func CheckFormChoiceFilter(proj *project.Project) []Issue {
	if proj == nil {
		return nil
	}
	entities := make(map[string]*metadata.Entity, len(proj.Entities))
	for _, entity := range proj.Entities {
		if entity != nil {
			entities[strings.ToLower(entity.Name)] = entity
		}
	}

	var issues []Issue
	for _, owner := range proj.Entities {
		if owner == nil {
			continue
		}
		for _, form := range owner.Forms {
			if form == nil {
				continue
			}
			idCount := make(map[string]int)
			form.Walk(func(el *metadata.FormElement) bool {
				if el != nil && strings.TrimSpace(el.ID) != "" {
					idCount[strings.TrimSpace(el.ID)]++
				}
				return true
			})

			form.Walk(func(el *metadata.FormElement) bool {
				if el == nil || el.ChoiceFilter == nil {
					return true
				}
				label := formFileLabel(owner, form)
				name := formElementName(el)
				add := func(format string, args ...any) {
					issues = append(issues, Issue{
						File:    label,
						Object:  owner.Name,
						Kind:    "Управляемая форма",
						Code:    "form.choice-filter",
						Message: fmt.Sprintf("поле %q: %s", name, fmt.Sprintf(format, args...)),
					})
				}

				if el.Kind != metadata.FormElementField {
					add("choice_filter допустим только у kind: %s", metadata.FormElementField)
				}
				id := strings.TrimSpace(el.ID)
				if id == "" {
					add("для choice_filter обязателен непустой стабильный id элемента")
				} else if id != el.ID {
					add("id %q содержит пробелы по краям", el.ID)
				} else if idCount[id] != 1 {
					add("id %q не уникален в форме", el.ID)
				}
				if len(el.ChoiceFilter) < 1 || len(el.ChoiceFilter) > 8 {
					add("choice_filter содержит %d условий; допустимо от 1 до 8", len(el.ChoiceFilter))
					if len(el.ChoiceFilter) == 0 {
						return true
					}
				}

				target, ok := formChoiceRefSource(owner, form, el.DataPath, entities)
				if !ok || target == nil || target.Kind != metadata.KindCatalog {
					add("data_path %q не выбирает ссылку на справочник", el.DataPath)
					return true
				}

				seenFields := make(map[string]bool, len(el.ChoiceFilter))
				for i, cond := range el.ChoiceFilter {
					where := fmt.Sprintf("choice_filter[%d]", i)
					fieldName := strings.TrimSpace(cond.Field)
					if fieldName == "" {
						add("%s: field обязателен", where)
						continue
					}
					fieldKey := strings.ToLower(fieldName)
					if seenFields[fieldKey] {
						add("%s: field %q повторяется", where, fieldName)
						continue
					}
					seenFields[fieldKey] = true

					hasFrom := strings.TrimSpace(cond.From) != ""
					hasValue := cond.Value != nil
					if hasFrom == hasValue {
						add("%s: требуется ровно одно из from и value", where)
						continue
					}

					isFolder := strings.EqualFold(fieldName, "is_folder")
					var targetField *metadata.Field
					if !isFolder {
						targetField = entityFieldFold(target, fieldName)
						if targetField == nil {
							add("%s: у справочника %s нет реквизита %q", where, target.Name, fieldName)
							continue
						}
					}

					switch cond.Op {
					case metadata.FormChoiceOpEqual:
						if isFolder {
							if !target.Hierarchical {
								add("%s: is_folder допустим только у иерархического справочника", where)
							}
							if !hasValue {
								add("%s: is_folder требует boolean value, а не from", where)
							}
							continue
						}
						if hasValue {
							add("%s: литерал value допустим только для is_folder", where)
							continue
						}
						source, sourceOK := formChoiceRefSource(owner, form, cond.From, entities)
						if !sourceOK || source == nil {
							add("%s: from %q не является явной ссылкой Объект.* или Форма.*", where, cond.From)
							continue
						}
						if targetField.RefEntity == "" || !strings.EqualFold(targetField.RefEntity, source.Name) {
							add("%s: eq сравнивает несовместимые ссылки %s.%s и %q", where, target.Name, targetField.Name, cond.From)
						}

					case metadata.FormChoiceOpInHierarchy:
						if isFolder || hasValue {
							add("%s: in_hierarchy требует ссылочный field и from", where)
							continue
						}
						hierarchy := entities[strings.ToLower(targetField.RefEntity)]
						source, sourceOK := formChoiceRefSource(owner, form, cond.From, entities)
						if targetField.RefEntity == "" || hierarchy == nil || hierarchy.Kind != metadata.KindCatalog || !hierarchy.Hierarchical {
							add("%s: %s.%s не ссылается на иерархический справочник", where, target.Name, targetField.Name)
							continue
						}
						if !sourceOK || source == nil || !strings.EqualFold(source.Name, hierarchy.Name) {
							add("%s: from %q должен ссылаться на тот же иерархический справочник %s", where, cond.From, hierarchy.Name)
						}

					default:
						add("%s: неизвестный оператор %q", where, cond.Op)
					}
				}
				return true
			})
		}
	}
	return issues
}

// formChoiceRefSource resolves an explicit two-segment form path to the entity
// referenced by that value. Bare names and deeper paths are intentionally
// rejected so future syntax cannot reinterpret an existing configuration.
func formChoiceRefSource(owner *metadata.Entity, form *metadata.FormModule, path string, entities map[string]*metadata.Entity) (*metadata.Entity, bool) {
	parts := strings.Split(strings.TrimSpace(path), ".")
	if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
		return nil, false
	}
	prefix, name := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	var ref string
	switch {
	case strings.EqualFold(prefix, "Объект"):
		if field := entityFieldFold(owner, name); field != nil {
			ref = field.RefEntity
		}
	case strings.EqualFold(prefix, "Форма"):
		for _, attr := range form.Attributes {
			if attr != nil && strings.EqualFold(attr.Name, name) {
				ref = formChoiceTypeRefEntity(attr.TypeRef)
				break
			}
		}
	default:
		return nil, false
	}
	if strings.TrimSpace(ref) == "" {
		return nil, false
	}
	entity := entities[strings.ToLower(ref)]
	return entity, entity != nil
}

func formChoiceTypeRefEntity(typeRef string) string {
	typeRef = strings.TrimSpace(typeRef)
	separator := strings.Index(typeRef, ".")
	if separator <= 0 || separator == len(typeRef)-1 {
		return ""
	}
	prefix := typeRef[:separator]
	if strings.EqualFold(prefix, "CatalogRef") || strings.EqualFold(prefix, "DocumentRef") {
		return strings.TrimSpace(typeRef[separator+1:])
	}
	return ""
}

func entityFieldFold(entity *metadata.Entity, name string) *metadata.Field {
	if entity == nil {
		return nil
	}
	for i := range entity.Fields {
		if strings.EqualFold(entity.Fields[i].Name, name) {
			return &entity.Fields[i]
		}
	}
	return nil
}
