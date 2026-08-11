package launcher

import (
	"bytes"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
	"gopkg.in/yaml.v3"
)

// Матрица чекбоксов в конфигураторе управляет лишь частью файла роли: пять
// секций прав и по нескольку операций в каждой. Всё остальное редактору
// неизвестно — processors, row_access (план 79), field_access (план 88),
// ai_data_access, операции вроде disclose, комментарии и порядок ключей.
//
// Поэтому сохранение не пересобирает YAML с нуля, а правит исходный файл
// точечно. Пересборка означала бы, что один клик по чекбоксу молча снимает
// построчный доступ и маскирование ПДн: роль examples/callcenter/Оператор
// теряла блоки row_access («Мои задачи») и field_access (маска телефона)
// целиком, а вместе с ними и право disclose, которого в матрице нет.

// roleSectionEdit — новое состояние одной секции прав по данным матрицы.
type roleSectionEdit struct {
	kind    string              // catalog/document/register/inforeg/report
	key     string              // канонический ключ YAML (catalogs/documents/…)
	managed map[string]bool     // операции, за которые отвечает матрица
	perms   map[string][]string // объект → отмеченные в матрице операции
}

// applyRoleMatrixToYAML переписывает в YAML роли только то, чем управляет
// матрица: name, description и отмеченные операции в пяти секциях прав.
// Остальное содержимое existing переносится без изменений. Пустой (или
// неразбираемый) existing даёт новый файл роли.
func applyRoleMatrixToYAML(existing []byte, name, desc string, edits []roleSectionEdit) ([]byte, error) {
	var doc yaml.Node
	if len(bytes.TrimSpace(existing)) > 0 {
		// Битый файл не должен блокировать сохранение роли из интерфейса:
		// пишем поверх, как раньше делала полная пересборка.
		_ = yaml.Unmarshal(existing, &doc)
	}
	root := roleDocumentMapping(&doc)

	roleSetScalar(root, "name", name)
	if desc != "" {
		roleSetScalar(root, "description", desc)
	} else {
		roleDeleteKey(root, "description")
	}

	perms := roleMappingValue(root, "permissions")
	if perms == nil || perms.Kind != yaml.MappingNode {
		perms = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		roleSetValue(root, "permissions", perms)
	}
	for _, e := range edits {
		applyRoleSection(perms, e)
	}
	// Роль без единого права — оставляем пустой permissions, а не удаляем ключ:
	// auth.Role ожидает его, а «permissions: {}» читается однозначно.
	if len(perms.Content) == 0 {
		perms.Style = yaml.FlowStyle
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// applyRoleSection обновляет одну секцию прав. Секция ищется по смыслу ключа
// (auth.PermissionKindFromKey), поэтому написанная синонимом — «справочники» —
// правится на месте, а не дублируется каноническим ключом рядом.
func applyRoleSection(perms *yaml.Node, e roleSectionEdit) {
	idx := -1
	for i := 0; i+1 < len(perms.Content); i += 2 {
		if auth.PermissionKindFromKey(perms.Content[i].Value) != e.kind {
			continue
		}
		if idx < 0 {
			idx = i
			continue
		}
		// Вторая секция того же вида: рантайм сливает её с первой, в матрице
		// они неотличимы. Оставить её — значит применить правку наполовину.
		perms.Content = append(perms.Content[:i], perms.Content[i+2:]...)
		i -= 2
	}

	if idx < 0 {
		if len(e.perms) == 0 {
			return
		}
		section := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		mergeRoleSectionEntries(section, e)
		perms.Content = append(perms.Content, roleScalarNode(e.key), section)
		return
	}

	section := perms.Content[idx+1]
	if section.Kind != yaml.MappingNode {
		section = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		perms.Content[idx+1] = section
	}
	mergeRoleSectionEntries(section, e)
	if len(section.Content) == 0 {
		perms.Content = append(perms.Content[:idx], perms.Content[idx+2:]...)
	}
}

// mergeRoleSectionEntries правит объекты секции на месте: у существующих
// перезаписываются только операции матрицы (неизвестные ей — disclose и любые
// будущие — остаются), объекты без единой операции удаляются, новые
// дописываются в конец по алфавиту (порядок обхода карты в Go не определён).
func mergeRoleSectionEntries(section *yaml.Node, e roleSectionEdit) {
	want := make(map[string][]string, len(e.perms))
	for entity, ops := range e.perms {
		want[strings.ToLower(entity)] = ops
	}
	used := make(map[string]bool, len(want))

	for i := 0; i+1 < len(section.Content); {
		key := strings.ToLower(section.Content[i].Value)
		ops := roleUnmanagedOps(section.Content[i+1], e.managed)
		if checked, ok := want[key]; ok {
			used[key] = true
			ops = append(append([]string(nil), checked...), ops...)
		}
		if len(ops) == 0 {
			section.Content = append(section.Content[:i], section.Content[i+2:]...)
			continue
		}
		roleSetOps(section.Content[i+1], ops)
		i += 2
	}

	var added []string
	for entity := range e.perms {
		if !used[strings.ToLower(entity)] {
			added = append(added, entity)
		}
	}
	sort.Strings(added)
	for _, entity := range added {
		section.Content = append(section.Content,
			roleScalarNode(entity), roleOpsNode(e.perms[entity], yaml.FlowStyle))
	}
}

// roleUnmanagedOps возвращает операции объекта, за которые матрица не отвечает
// и которые поэтому надо сохранить как есть.
func roleUnmanagedOps(value *yaml.Node, managed map[string]bool) []string {
	var out []string
	for _, op := range roleOpsOf(value) {
		if !managed[op] {
			out = append(out, op)
		}
	}
	return out
}

// roleOpsOf читает список операций так же, как рантайм: и последовательность,
// и скаляр, и «read, write» одной строкой.
func roleOpsOf(value *yaml.Node) []string {
	if value == nil {
		return nil
	}
	var out []string
	switch value.Kind {
	case yaml.SequenceNode:
		for _, item := range value.Content {
			out = append(out, auth.SplitPermissionOps(item.Value)...)
		}
	case yaml.ScalarNode:
		out = auth.SplitPermissionOps(value.Value)
	}
	return out
}

// roleSetOps заменяет список операций, сохраняя стиль записи и комментарии.
func roleSetOps(value *yaml.Node, ops []string) {
	style := value.Style
	if value.Kind != yaml.SequenceNode {
		style = yaml.FlowStyle
	}
	head, line, foot := value.HeadComment, value.LineComment, value.FootComment
	*value = *roleOpsNode(ops, style)
	value.HeadComment, value.LineComment, value.FootComment = head, line, foot
}

func roleOpsNode(ops []string, style yaml.Style) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: style}
	for _, op := range ops {
		n.Content = append(n.Content, roleScalarNode(op))
	}
	return n
}

func roleScalarNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// roleDocumentMapping возвращает верхний mapping документа, заводя его, если
// файла не было или он не является отображением.
func roleDocumentMapping(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 && doc.Content[0].Kind == yaml.MappingNode {
		return doc.Content[0]
	}
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	*doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{m}}
	return m
}

func roleMappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// roleSetValue заменяет значение ключа, сохраняя его позицию и комментарии.
func roleSetValue(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != key {
			continue
		}
		old := m.Content[i+1]
		value.HeadComment, value.LineComment, value.FootComment = old.HeadComment, old.LineComment, old.FootComment
		m.Content[i+1] = value
		return
	}
	m.Content = append(m.Content, roleScalarNode(key), value)
}

func roleSetScalar(m *yaml.Node, key, value string) {
	roleSetValue(m, key, roleScalarNode(value))
}

func roleDeleteKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}
