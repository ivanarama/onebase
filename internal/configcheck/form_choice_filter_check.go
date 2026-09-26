package configcheck

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
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
	allowed := map[string]bool{"field": true, "op": true, "from": true, "value": true, "ref": true}
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
					hasRef := strings.TrimSpace(cond.Ref) != ""
					указано := 0
					for _, есть := range []bool{hasFrom, hasValue, hasRef} {
						if есть {
							указано++
						}
					}
					if указано != 1 {
						add("%s: требуется ровно одно из from, value и ref", where)
						continue
					}

					isFolder := strings.EqualFold(fieldName, "is_folder")
					isRoot := strings.EqualFold(fieldName, "is_root")
					// «ТЧ.Поле» — отбор по табличной части справочника-цели.
					tpName, tpField, isTablePart := strings.Cut(fieldName, ".")
					// parent_id — собственная иерархия справочника-цели, а не его
					// реквизит: отбирает по месту самой записи в дереве.
					isParent := strings.EqualFold(fieldName, "parent_id")
					if isTablePart {
						var tp *metadata.TablePart
						for i := range target.TableParts {
							if strings.EqualFold(target.TableParts[i].Name, tpName) {
								tp = &target.TableParts[i]
								break
							}
						}
						if tp == nil {
							add("%s: у справочника %s нет табличной части %q", where, target.Name, tpName)
							continue
						}
						var поле *metadata.Field
						for i := range tp.Fields {
							if strings.EqualFold(tp.Fields[i].Name, tpField) {
								поле = &tp.Fields[i]
								break
							}
						}
						if поле == nil || strings.TrimSpace(поле.RefEntity) == "" {
							add("%s: в табличной части %q нет ссылочного реквизита %q", where, tpName, tpField)
							continue
						}
						if cond.Op != metadata.FormChoiceOpEqual {
							add("%s: отбор по табличной части поддерживает только eq", where)
							continue
						}
						source, sourceOK := formChoiceRefSource(owner, form, cond.From, entities)
						if !hasFrom || !sourceOK || source == nil || !strings.EqualFold(source.Name, поле.RefEntity) {
							add("%s: from %q должен ссылаться на %s", where, cond.From, поле.RefEntity)
						}
						continue
					}
					// Service fields have no metadata.Field. Validate their grammar
					// before entering branches that dereference an ordinary field.
					if (isFolder || isRoot) && cond.Op != metadata.FormChoiceOpEqual {
						add("%s: %s поддерживает только eq", where, fieldName)
						continue
					}
					if isParent && cond.Op != metadata.FormChoiceOpInHierarchy && cond.Op != metadata.FormChoiceOpNotInHierarchy {
						add("%s: parent_id поддерживает только in_hierarchy и not_in_hierarchy", where)
						continue
					}
					var targetField *metadata.Field
					if !isFolder && !isRoot && !isParent {
						targetField = entityFieldFold(target, fieldName)
						if targetField == nil {
							add("%s: у справочника %s нет реквизита %q", where, target.Name, fieldName)
							continue
						}
					}

					switch cond.Op {
					case metadata.FormChoiceOpEqual:
						if isRoot {
							if !target.Hierarchical {
								add("%s: is_root допустим только у иерархического справочника", where)
							}
							if !hasValue {
								add("%s: is_root требует boolean value", where)
							}
							continue
						}
						if targetField != nil && targetField.Type == metadata.FieldTypeBool {
							if !hasValue {
								add("%s: булев реквизит %q сравнивается с value, а не from", where, fieldName)
							}
							continue
						}
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
						// Строковый реквизит сравнивается со строковым же
						// значением по разыменованному пути: так связан дом с
						// улицей в адресном классификаторе — ВладелецКод хранит
						// ИД записи иерархии, а не ссылку. Без этого подбор
						// умел сравнивать только ссылку со ссылкой.
						// Разрешаем только корректную пару «строка ↔ строка».
						// Всё остальное падает в прежние проверки: строковый
						// реквизит со ссылочным источником по-прежнему ошибка,
						// и сообщение у неё прежнее.
						if targetField != nil && targetField.Type == metadata.FieldTypeString &&
							cond.Op == metadata.FormChoiceOpEqual &&
							formChoiceStringSource(owner, form, cond.From, entities) {
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

					case metadata.FormChoiceOpInHierarchy, metadata.FormChoiceOpNotInHierarchy:
						if isFolder || hasValue {
							add("%s: %s требует field и from", where, cond.Op)
							continue
						}
						if isParent {
							if !target.Hierarchical {
								add("%s: parent_id допустим только у иерархического справочника", where)
								continue
							}
							if hasRef {
								if _, err := uuid.Parse(strings.TrimSpace(cond.Ref)); err != nil {
									add("%s: ref %q не является идентификатором", where, cond.Ref)
								}
								continue
							}
							source, sourceOK := formChoiceRefSource(owner, form, cond.From, entities)
							if !sourceOK || source == nil || !strings.EqualFold(source.Name, target.Name) {
								add("%s: from %q должен ссылаться на сам справочник %s", where, cond.From, target.Name)
							}
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
// formChoiceStringSource проверяет, что путь вида Объект.Ссылка.Реквизит
// приводит к СТРОКОВОМУ реквизиту промежуточной записи. Такой источник
// сравнивается со строковым реквизитом справочника-цели.
func formChoiceStringSource(owner *metadata.Entity, form *metadata.FormModule, path string, entities map[string]*metadata.Entity) bool {
	parts := strings.Split(strings.TrimSpace(path), ".")
	if len(parts) != 3 {
		return false
	}
	prefix, name, deref := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])
	if name == "" || deref == "" {
		return false
	}
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
		return false
	}
	entity := entities[strings.ToLower(strings.TrimSpace(ref))]
	if entity == nil {
		return false
	}
	field := entityFieldFold(entity, deref)
	return field != nil && field.Type == metadata.FieldTypeString
}

func formChoiceRefSource(owner *metadata.Entity, form *metadata.FormModule, path string, entities map[string]*metadata.Entity) (*metadata.Entity, bool) {
	parts := strings.Split(strings.TrimSpace(path), ".")
	if len(parts) < 2 || len(parts) > 3 || strings.TrimSpace(parts[1]) == "" {
		return nil, false
	}
	prefix, name := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	deref := ""
	if len(parts) == 3 {
		deref = strings.TrimSpace(parts[2])
		if deref == "" {
			return nil, false
		}
	}
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
	if entity == nil {
		return nil, false
	}
	// Разыменование в одно звено: берём реквизит промежуточной записи. Он
	// обязан быть ссылкой — сравнивать подбор умеет только ссылки.
	if deref != "" {
		field := entityFieldFold(entity, deref)
		if field == nil || strings.TrimSpace(field.RefEntity) == "" {
			return nil, false
		}
		entity = entities[strings.ToLower(field.RefEntity)]
		return entity, entity != nil
	}
	return entity, true
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
