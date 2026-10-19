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
	"github.com/ivantit66/onebase/internal/storage"
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
			case "ref":
				// Пустая строка в структуре неотличима от отсутствующего ключа,
				// поэтому `ref: ""` ловится здесь, по самому YAML (#1820).
				if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
					add(value, fmt.Sprintf("%s.ref должен быть строкой с UUID", conditionPath))
				} else if strings.TrimSpace(value.Value) == "" {
					add(value, fmt.Sprintf("%s.ref пуст: нужен UUID записи справочника", conditionPath))
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

	// Владельцы форм: документы и справочники, а с #1840 и обработки —
	// виртуальная сущность из параметров, та же, что видит рендер формы
	// обработки. Без них choice_filter формы обработки проходил молча.
	type choiceFormOwner struct {
		entity *metadata.Entity
		forms  []*metadata.FormModule
		label  func(*metadata.FormModule) string
	}
	var owners []choiceFormOwner
	for _, entity := range proj.Entities {
		if entity != nil {
			owners = append(owners, choiceFormOwner{entity, entity.Forms, func(form *metadata.FormModule) string {
				return formFileLabel(entity, form)
			}})
		}
	}
	for _, proc := range proj.Processors {
		if proc != nil {
			owners = append(owners, choiceFormOwner{proc.VirtualEntity(), proc.Forms, func(form *metadata.FormModule) string {
				return procFormFileLabel(proc.Name, form)
			}})
		}
	}

	var issues []Issue
	for _, item := range owners {
		owner := item.entity
		for _, form := range item.forms {
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
				label := item.label(form)
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
					sourceCount := 0
					for _, present := range []bool{hasFrom, hasValue, hasRef} {
						if present {
							sourceCount++
						}
					}
					if sourceCount != 1 {
						add("%s: требуется ровно одно из from, value и ref", where)
						continue
					}

					// «<ТЧ>.<Колонка>» — отбор по табличной части цели (#1822):
					// только eq, только ссылочная колонка, источник — from или ref
					// той же сущности, на которую ссылается колонка.
					if tpName, columnName, isTablePart := strings.Cut(fieldName, "."); isTablePart {
						if problem := formChoiceTablePartProblem(owner, form, cond, target, tpName, columnName, hasValue, hasRef, entities); problem != "" {
							add("%s: %s", where, problem)
						}
						continue
					}

					isFolder := strings.EqualFold(fieldName, "is_folder")
					targetField := entityFieldFold(target, fieldName)
					isRoot := targetField == nil && strings.EqualFold(fieldName, metadata.FormChoiceRootField)
					if !isFolder && !isRoot {
						// parent_id — служебная ссылка иерархического справочника на
						// себя (#1819): дальше проверяется как обычный ссылочный реквизит.
						if targetField == nil && strings.EqualFold(fieldName, metadata.FormChoiceParentField) {
							targetField = metadata.FormChoiceParentFieldOf(target)
							if targetField == nil {
								add("%s: parent_id допустим только у иерархического справочника, а %s не иерархический", where, target.Name)
								continue
							}
						}
						if targetField == nil {
							add("%s: у справочника %s нет реквизита %q", where, target.Name, fieldName)
							continue
						}
					}

					// Постоянная ссылка (#1820): UUID записи справочника, на который
					// ссылается field. Существование записи статически не проверить —
					// рантайм при её отсутствии даёт пустой подбор (fail-closed).
					if hasRef {
						if problem := formChoiceRefProblem(cond, isFolder || isRoot, target, targetField, entities); problem != "" {
							add("%s: %s", where, problem)
						}
						continue
					}

					switch cond.Op {
					case metadata.FormChoiceOpEqual:
						if isFolder || isRoot {
							if !target.Hierarchical {
								add("%s: %s допустим только у иерархического справочника", where, fieldName)
							}
							if !hasValue {
								add("%s: %s требует boolean value, а не from", where, fieldName)
							}
							continue
						}
						if hasValue {
							if targetField.Type != metadata.FieldTypeBool {
								add("%s: литерал value допустим для is_folder, is_root и булева реквизита, а %s.%s имеет тип %q", where, target.Name, targetField.Name, targetField.Type)
							}
							continue
						}
						// Строковый реквизит цели сравнивается со строковым концом
						// пути: ВладелецКод дома хранит ИД улицы, а не ссылку на
						// неё. Источник без перехода по ссылке проверяется ниже
						// прежним путём и остаётся несовместимым.
						if deep, parsed := metadata.ParseFormChoiceSource(cond.From); parsed && deep.Deep() && formChoiceTextField(targetField) {
							if problem := formChoiceStringSource(owner, form, deep, cond.From, entities); problem != "" {
								add("%s: %s", where, problem)
							}
							continue
						}
						source, problem := formChoiceSourceEntity(owner, form, cond.From, entities)
						if problem != "" {
							add("%s: %s", where, problem)
							continue
						}
						if targetField.RefEntity == "" || !strings.EqualFold(targetField.RefEntity, source.Name) {
							add("%s: eq сравнивает несовместимые ссылки %s.%s и %q", where, target.Name, targetField.Name, cond.From)
						}

					case metadata.FormChoiceOpEqualOrEmpty:
						// Только ссылка со ссылочным источником того же типа:
						// «пусто» у служебного is_folder не нужно.
						if isFolder || isRoot || hasValue {
							add("%s: eq_or_empty требует ссылочный field и from", where)
							continue
						}
						// Источник — тот же общий разбор, что у eq: прямая ссылка
						// формы или один переход по ссылке (план 183, срез B1).
						source, problem := formChoiceSourceEntity(owner, form, cond.From, entities)
						if problem != "" {
							add("%s: %s", where, problem)
							continue
						}
						if targetField.RefEntity == "" || !strings.EqualFold(targetField.RefEntity, source.Name) {
							add("%s: eq_or_empty сравнивает несовместимые ссылки %s.%s и %q", where, target.Name, targetField.Name, cond.From)
						}

					// not_in_hierarchy (#1821) — дополнение in_hierarchy: те же
					// требования к полю и источнику.
					case metadata.FormChoiceOpInHierarchy, metadata.FormChoiceOpNotInHierarchy:
						if isFolder || isRoot || hasValue {
							add("%s: %s требует ссылочный field и from", where, cond.Op)
							continue
						}
						hierarchy := entities[strings.ToLower(targetField.RefEntity)]
						source, problem := formChoiceSourceEntity(owner, form, cond.From, entities)
						if targetField.RefEntity == "" || hierarchy == nil || hierarchy.Kind != metadata.KindCatalog || !hierarchy.Hierarchical {
							add("%s: %s.%s не ссылается на иерархический справочник", where, target.Name, targetField.Name)
							continue
						}
						if problem != "" {
							add("%s: %s", where, problem)
							continue
						}
						if !strings.EqualFold(source.Name, hierarchy.Name) {
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

// formChoiceTablePartProblem проверяет условие по колонке табличной части
// выбираемого справочника (#1822). Пусто — условие корректно; рантайм
// компилирует его в EXISTS (storage.choicePredicateSQL) и закрывает выдачу,
// если колонка под полевой политикой или ПДн.
func formChoiceTablePartProblem(owner *metadata.Entity, form *metadata.FormModule, cond metadata.FormChoiceCondition, target *metadata.Entity, tpName, columnName string, hasValue, hasRef bool, entities map[string]*metadata.Entity) string {
	tp, column := storage.ChoiceTablePartColumn(target, tpName, columnName)
	switch {
	case tp == nil:
		return fmt.Sprintf("у справочника %s нет табличной части %q", target.Name, strings.TrimSpace(tpName))
	case column == nil:
		return fmt.Sprintf("в табличной части %s.%s нет колонки %q", target.Name, tp.Name, strings.TrimSpace(columnName))
	case strings.TrimSpace(column.RefEntity) == "":
		return fmt.Sprintf("%s.%s.%s не ссылка: по табличной части отбирают только по ссылочной колонке", target.Name, tp.Name, column.Name)
	case cond.Op != metadata.FormChoiceOpEqual:
		return fmt.Sprintf("по колонке табличной части допустим только eq, а не %q", cond.Op)
	case hasValue:
		return "литерал value у колонки табличной части недопустим: нужен from или ref"
	}
	if hasRef {
		id, err := uuid.Parse(strings.TrimSpace(cond.Ref))
		if err != nil {
			return fmt.Sprintf("ref %q не является UUID", cond.Ref)
		}
		if id == uuid.Nil {
			return "ref — нулевой UUID: нужен UUID записи справочника"
		}
		return ""
	}
	source, problem := formChoiceSourceEntity(owner, form, cond.From, entities)
	if problem != "" {
		return problem
	}
	if !strings.EqualFold(column.RefEntity, source.Name) {
		return fmt.Sprintf("eq сравнивает несовместимые ссылки %s.%s.%s и %q", target.Name, tp.Name, column.Name, cond.From)
	}
	return ""
}

// formChoiceRefProblem проверяет условие с постоянной ссылкой (ref, #1820):
// формат UUID и сочетание field/op. Пусто — условие корректно.
func formChoiceRefProblem(cond metadata.FormChoiceCondition, isFolder bool, target *metadata.Entity, targetField *metadata.Field, entities map[string]*metadata.Entity) string {
	literal := strings.TrimSpace(cond.Ref)
	id, err := uuid.Parse(literal)
	if err != nil {
		return fmt.Sprintf("ref %q не является UUID", cond.Ref)
	}
	if id == uuid.Nil {
		return "ref — нулевой UUID: нужен UUID записи справочника"
	}
	if isFolder || targetField == nil || strings.TrimSpace(targetField.RefEntity) == "" {
		return fmt.Sprintf("ref допустим только у ссылочного реквизита или parent_id, а %q им не является", cond.Field)
	}
	switch cond.Op {
	case metadata.FormChoiceOpEqual, metadata.FormChoiceOpEqualOrEmpty:
		return ""
	case metadata.FormChoiceOpInHierarchy, metadata.FormChoiceOpNotInHierarchy:
		hierarchy := entities[strings.ToLower(targetField.RefEntity)]
		if hierarchy == nil || hierarchy.Kind != metadata.KindCatalog || !hierarchy.Hierarchical {
			return fmt.Sprintf("%s.%s не ссылается на иерархический справочник", target.Name, targetField.Name)
		}
		return ""
	default:
		return fmt.Sprintf("неизвестный оператор %q", cond.Op)
	}
}

// formChoiceRefSource resolves an explicit two-segment form path to the entity
// referenced by that value: data_path of the element itself and the leading
// segment of a condition source. Bare names and deeper paths are intentionally
// rejected here so future syntax cannot reinterpret an existing configuration;
// the one allowed hop lives in formChoiceSourceEntity.
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

// formChoiceSourceEntity разрешает источник условия: `Объект.<Поле>` и
// `Форма.<Поле>` (значение самого элемента формы) либо путь с одним переходом
// по ссылке — `Объект.<Поле>.<Реквизит>` (план 183, срез B1). Возвращает
// справочник, на который ссылается значение источника, и причину отказа
// человеческим текстом: сообщение «не является ссылкой» для трёхсегментного
// пути не подсказало бы, какой из двух сегментов неверен.
func formChoiceSourceEntity(owner *metadata.Entity, form *metadata.FormModule, path string, entities map[string]*metadata.Entity) (*metadata.Entity, string) {
	source, ok := metadata.ParseFormChoiceSource(path)
	if !ok {
		return nil, fmt.Sprintf("from %q должен быть путём Объект.<Поле>, Форма.<Поле> или Объект.<Поле>.<Реквизит> — не более одного перехода по ссылке", path)
	}
	lead, leadOK := formChoiceRefSource(owner, form, source.Root+"."+source.Field, entities)
	if !leadOK || lead == nil {
		return nil, fmt.Sprintf("from %q: %s.%s не является явной ссылкой Объект.* или Форма.*", path, source.Root, source.Field)
	}
	if !source.Deep() {
		return lead, ""
	}
	attr := entityFieldFold(lead, source.Attr)
	if attr == nil {
		return nil, fmt.Sprintf("from %q: у %s нет реквизита %q", path, lead.Name, source.Attr)
	}
	if strings.TrimSpace(attr.RefEntity) == "" {
		return nil, fmt.Sprintf("from %q: реквизит %s.%s не ссылочный, сравнивать нечего", path, lead.Name, attr.Name)
	}
	target := entities[strings.ToLower(attr.RefEntity)]
	if target == nil {
		return nil, fmt.Sprintf("from %q: реквизит %s.%s ссылается на неизвестный объект %q", path, lead.Name, attr.Name, attr.RefEntity)
	}
	return target, ""
}

// formChoiceStringSource проверяет глубокий источник строкового field: конец
// пути <Корень>.<Поле>.<Реквизит> обязан быть строковым реквизитом. Строку от
// браузера подбор не принимает — сравнивается значение, которое сервер сам
// прочитал из записи посредника под правами пользователя.
func formChoiceStringSource(owner *metadata.Entity, form *metadata.FormModule, source metadata.FormChoiceSource, path string, entities map[string]*metadata.Entity) string {
	lead, leadOK := formChoiceRefSource(owner, form, source.Root+"."+source.Field, entities)
	if !leadOK || lead == nil {
		return fmt.Sprintf("from %q: %s.%s не является явной ссылкой Объект.* или Форма.*", path, source.Root, source.Field)
	}
	attr := entityFieldFold(lead, source.Attr)
	if attr == nil {
		return fmt.Sprintf("from %q: у %s нет реквизита %q", path, lead.Name, source.Attr)
	}
	if !formChoiceTextField(attr) {
		return fmt.Sprintf("from %q: строковый реквизит сравнивается только со строковым, а %s.%s имеет тип %q", path, lead.Name, attr.Name, attr.Type)
	}
	return ""
}

// formChoiceTextField — строковый нессылочный реквизит. Служебные поля вида
// id/parent_id тоже объявлены строкой, но несут ссылку (RefEntity).
func formChoiceTextField(field *metadata.Field) bool {
	return field != nil && field.Type == metadata.FieldTypeString && strings.TrimSpace(field.RefEntity) == ""
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
