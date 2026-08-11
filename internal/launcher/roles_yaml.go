package launcher

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
	"gopkg.in/yaml.v3"
)

// Матрица чекбоксов управляет лишь частью файла роли. Поэтому существующий
// YAML правится точечно: processors, row_access, field_access, ai_data_access,
// неизвестные операции, комментарии, anchors и порядок ключей не принадлежат
// матрице и не должны исчезать при сохранении.

type roleSectionEdit struct {
	kind    string
	key     string
	managed map[string]bool
	perms   map[string][]string
}

type roleWanted struct {
	entity string
	ops    []string
}

const roleYAMLNodeLimit = 10000

type roleYAMLIndex struct {
	aliasUsers map[*yaml.Node]int
	expansions int
	cloneNodes int
}

func parseRoleYAMLStrict(data []byte) (*auth.Role, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("role YAML is empty")
	}
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("role YAML: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("role YAML: multiple documents are not supported")
		}
		return nil, fmt.Errorf("role YAML: trailing document: %w", err)
	}
	if _, err := roleDocumentMapping(&doc); err != nil {
		return nil, err
	}
	if _, err := roleBuildYAMLIndex(&doc); err != nil {
		return nil, err
	}
	role, err := auth.ParseRole(data)
	if err != nil {
		return nil, fmt.Errorf("role YAML: %w", err)
	}
	return role, nil
}

// applyRoleMatrixToYAML обновляет name/description и управляемые операции.
// Непустой повреждённый файл никогда не считается новым: иначе временная
// ошибка или незнакомая YAML-конструкция превращала сохранение в потерю policy.
func applyRoleMatrixToYAML(existing []byte, name, desc string, edits []roleSectionEdit) ([]byte, error) {
	var doc yaml.Node
	before := &auth.Role{}
	if len(bytes.TrimSpace(existing)) == 0 {
		root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	} else {
		dec := yaml.NewDecoder(bytes.NewReader(existing))
		if err := dec.Decode(&doc); err != nil {
			return nil, fmt.Errorf("role YAML: %w", err)
		}
		var extra yaml.Node
		if err := dec.Decode(&extra); err != io.EOF {
			if err == nil {
				return nil, fmt.Errorf("role YAML: multiple documents are not supported")
			}
			return nil, fmt.Errorf("role YAML: trailing document: %w", err)
		}
		var err error
		before, err = auth.ParseRole(existing)
		if err != nil {
			return nil, fmt.Errorf("role YAML: %w", err)
		}
	}
	root, err := roleDocumentMapping(&doc)
	if err != nil {
		return nil, err
	}
	index, err := roleBuildYAMLIndex(&doc)
	if err != nil {
		return nil, err
	}
	if root.Anchor != "" && index.aliasUsers[root] > 0 {
		return nil, fmt.Errorf("role YAML root uses shared anchor %q and cannot be edited safely", root.Anchor)
	}

	if err := roleSetScalar(root, "name", name, index); err != nil {
		return nil, err
	}
	if desc != "" {
		if err := roleSetScalar(root, "description", desc, index); err != nil {
			return nil, err
		}
	} else {
		if err := roleDeleteKey(root, "description", index); err != nil {
			return nil, err
		}
	}

	permsValue := roleMappingValue(root, "permissions")
	var perms *yaml.Node
	if permsValue == nil {
		perms = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		roleSetValue(root, "permissions", perms)
	} else {
		perms, _, err = roleOwnMapping(permsValue, "permissions", index, nil)
		if err != nil {
			return nil, err
		}
	}
	for _, edit := range edits {
		if err := applyRoleSection(perms, edit, index); err != nil {
			return nil, err
		}
	}
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
	out := buf.Bytes()
	after, err := auth.ParseRole(out)
	if err != nil {
		return nil, fmt.Errorf("updated role YAML: %w", err)
	}
	if err := roleVerifySemanticUpdate(before, after, name, desc, edits); err != nil {
		return nil, err
	}
	return out, nil
}

// applyRoleSection находит все секции одного смысла, включая синонимы внутри
// runtime-wrapper'ов policies/права. Ни одна вторая секция не удаляется:
// управляемые операции снимаются во всех, а неизвестные остаются на месте.
func applyRoleSection(perms *yaml.Node, edit roleSectionEdit, index *roleYAMLIndex) error {
	sections, err := collectRoleSections(perms, edit.kind, index)
	if err != nil {
		return err
	}
	if len(sections) == 0 {
		if len(edit.perms) == 0 {
			return nil
		}
		section := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		perms.Content = append(perms.Content, roleScalarNode(edit.key), section)
		sections = []*yaml.Node{section}
	}
	return mergeRoleSectionEntries(sections, edit, index)
}

// collectRoleSections повторяет структуру auth.permissionFromYAMLMap: wrapper
// может быть вложен рекурсивно или задан alias'ом. Каждый alias-use разворачивается
// локально, а active-стек блокирует циклы без изменения общего anchor-target.
func collectRoleSections(root *yaml.Node, kind string, index *roleYAMLIndex) ([]*yaml.Node, error) {
	var sections []*yaml.Node
	active := map[*yaml.Node]bool{}
	var walk func(*yaml.Node) error
	walk = func(node *yaml.Node) error {
		mapping, identity, err := roleOwnMapping(node, "permission wrapper", index, active)
		if err != nil {
			return err
		}
		active[identity] = true
		defer delete(active, identity)
		for i := 0; i+1 < len(mapping.Content); i += 2 {
			keyNode, value := mapping.Content[i], mapping.Content[i+1]
			if keyNode.Kind != yaml.ScalarNode {
				continue
			}
			key := keyNode.Value
			if auth.IsPermissionWrapperKey(key) {
				if err := walk(value); err != nil {
					return fmt.Errorf("role YAML wrapper %q: %w", key, err)
				}
				continue
			}
			if auth.PermissionKindFromKey(key) != kind {
				continue
			}
			section, identity, err := roleOwnMapping(value, "permission section "+key, index, active)
			if err != nil {
				return err
			}
			if active[identity] {
				return fmt.Errorf("role YAML permission section %q contains a cyclic alias", key)
			}
			sections = append(sections, section)
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	return sections, nil
}

// mergeRoleSectionEntries применяет желаемое состояние ко всем синонимичным
// секциям. Уже выбранная операция остаётся в каждом прежнем вхождении (вместе
// с комментариями), отсутствующая добавляется в первое; неизвестные матрице
// операции остаются на своих местах.
func mergeRoleSectionEntries(sections []*yaml.Node, edit roleSectionEdit, index *roleYAMLIndex) error {
	want, err := normalizedRoleWants(edit.perms)
	if err != nil {
		return err
	}
	type occurrence struct {
		value *yaml.Node
		ops   []string
	}
	byEntity := make(map[string][]occurrence)
	spellings := make(map[string]string)
	for _, section := range sections {
		for i := 0; i+1 < len(section.Content); i += 2 {
			keyNode, value := section.Content[i], section.Content[i+1]
			if keyNode.Kind != yaml.ScalarNode {
				continue
			}
			folded := strings.ToLower(keyNode.Value)
			if previous, ok := spellings[folded]; ok && previous != keyNode.Value {
				return fmt.Errorf("role YAML has case-ambiguous entities %q and %q", previous, keyNode.Value)
			}
			spellings[folded] = keyNode.Value
			oldOps, err := roleOpsOf(value)
			if err != nil {
				return fmt.Errorf("role YAML permission %q: %w", keyNode.Value, err)
			}
			byEntity[keyNode.Value] = append(byEntity[keyNode.Value], occurrence{value: value, ops: oldOps})
		}
	}

	assigned := make(map[*yaml.Node][]string)
	used := make(map[string]bool, len(want))
	for entity, desired := range want {
		occurrences := byEntity[entity]
		if len(occurrences) == 0 {
			continue
		}
		used[entity] = true
		for _, op := range desired.ops {
			found := false
			for _, item := range occurrences {
				if containsRoleOp(item.ops, op) {
					assigned[item.value] = append(assigned[item.value], op)
					found = true
				}
			}
			if !found {
				assigned[occurrences[0].value] = append(assigned[occurrences[0].value], op)
			}
		}
	}

	for _, section := range sections {
		for i := 0; i+1 < len(section.Content); {
			keyNode, value := section.Content[i], section.Content[i+1]
			if keyNode.Kind != yaml.ScalarNode {
				i += 2
				continue
			}
			oldOps, err := roleOpsOf(value)
			if err != nil {
				return fmt.Errorf("role YAML permission %q: %w", keyNode.Value, err)
			}
			newOps := make([]string, 0, len(oldOps))
			newOps = append(newOps, assigned[value]...)
			for _, op := range oldOps {
				if !edit.managed[op] {
					newOps = append(newOps, op)
				}
			}
			if len(newOps) == 0 {
				if anchor := roleSharedAnchor(keyNode, index); anchor != "" {
					return fmt.Errorf("role YAML permission key %q uses shared anchor %q and cannot be removed safely", keyNode.Value, anchor)
				}
				if anchor := roleSharedAnchor(value, index); anchor != "" {
					return fmt.Errorf("role YAML permission %q uses shared anchor %q and cannot be removed safely", keyNode.Value, anchor)
				}
				section.Content = append(section.Content[:i], section.Content[i+2:]...)
				continue
			}
			if !roleOpsEquivalent(oldOps, newOps) {
				if err := roleSetOps(value, newOps, index); err != nil {
					return fmt.Errorf("role YAML permission %q: %w", keyNode.Value, err)
				}
			}
			i += 2
		}
	}

	var added []roleWanted
	for key, desired := range want {
		if !used[key] {
			added = append(added, desired)
		}
	}
	sort.Slice(added, func(i, j int) bool {
		return strings.ToLower(added[i].entity) < strings.ToLower(added[j].entity)
	})
	for _, desired := range added {
		sections[0].Content = append(sections[0].Content,
			roleScalarNode(desired.entity), roleOpsNode(desired.ops, yaml.FlowStyle))
	}
	return nil
}

func normalizedRoleWants(perms map[string][]string) (map[string]roleWanted, error) {
	entities := make([]string, 0, len(perms))
	for entity := range perms {
		if strings.TrimSpace(entity) != "" {
			entities = append(entities, entity)
		}
	}
	sort.Slice(entities, func(i, j int) bool {
		li, lj := strings.ToLower(entities[i]), strings.ToLower(entities[j])
		if li == lj {
			return entities[i] < entities[j]
		}
		return li < lj
	})
	out := make(map[string]roleWanted, len(entities))
	spellings := make(map[string]string, len(entities))
	for _, entity := range entities {
		folded := strings.ToLower(entity)
		if previous, ok := spellings[folded]; ok && previous != entity {
			return nil, fmt.Errorf("role matrix has case-ambiguous entities %q and %q", previous, entity)
		}
		spellings[folded] = entity
		item := out[entity]
		item.entity = entity
		for _, op := range perms[entity] {
			op = strings.ToLower(strings.TrimSpace(op))
			if op != "" && !containsRoleOp(item.ops, op) {
				item.ops = append(item.ops, op)
			}
		}
		if len(item.ops) != 0 {
			out[entity] = item
		}
	}
	return out, nil
}

func containsRoleOp(ops []string, want string) bool {
	for _, op := range ops {
		if op == want {
			return true
		}
	}
	return false
}

func roleOpsEquivalent(left, right []string) bool {
	leftSet := make(map[string]bool, len(left))
	for _, op := range left {
		leftSet[op] = true
	}
	rightSet := make(map[string]bool, len(right))
	for _, op := range right {
		rightSet[op] = true
	}
	if len(leftSet) != len(rightSet) {
		return false
	}
	for op := range leftSet {
		if !rightSet[op] {
			return false
		}
	}
	return true
}

func roleOpsOf(value *yaml.Node) ([]string, error) {
	resolved, err := roleResolveNode(value)
	if err != nil {
		return nil, err
	}
	switch resolved.Kind {
	case yaml.SequenceNode:
		var out []string
		for _, item := range resolved.Content {
			ops, err := roleOpsOf(item)
			if err != nil {
				return nil, err
			}
			out = append(out, ops...)
		}
		return out, nil
	case yaml.ScalarNode:
		return auth.SplitPermissionOps(resolved.Value), nil
	default:
		return nil, fmt.Errorf("operations must be a scalar or sequence, got kind %d", resolved.Kind)
	}
}

// roleSetOps заменяет только конкретное значение объекта. Alias списка
// разворачивается локально, не меняя общий anchor-target других объектов.
// Изменять сам anchor с потребителями неоднозначно, поэтому такой случай
// блокируется вместо скрытого расширения/снятия чужих прав.
func roleSetOps(value *yaml.Node, ops []string, index *roleYAMLIndex) error {
	resolved, err := roleResolveNode(value)
	if err != nil {
		return err
	}
	if value.Kind != yaml.AliasNode {
		if anchor := roleSharedAnchor(value, index); anchor != "" {
			return fmt.Errorf("cannot safely rewrite shared anchored operation list %q", anchor)
		}
	}
	style := resolved.Style
	if resolved.Kind != yaml.SequenceNode {
		style = yaml.FlowStyle
	}
	decorations := roleOperationDecorations(resolved, ops)
	head, line, foot := resolved.HeadComment, resolved.LineComment, resolved.FootComment
	if value.HeadComment != "" {
		head = value.HeadComment
	}
	if value.LineComment != "" {
		line = value.LineComment
	}
	if value.FootComment != "" {
		foot = value.FootComment
	}
	*value = *roleOpsNode(ops, style)
	value.HeadComment, value.LineComment, value.FootComment = head, line, foot
	for _, item := range value.Content {
		key := strings.ToLower(item.Value)
		queue := decorations[key]
		if len(queue) == 0 {
			continue
		}
		dec := queue[0]
		decorations[key] = queue[1:]
		item.Style = dec.style
		item.HeadComment, item.LineComment, item.FootComment = dec.head, dec.line, dec.foot
	}
	return nil
}

type roleOpDecoration struct {
	style            yaml.Style
	head, line, foot string
}

func roleOperationDecorations(value *yaml.Node, surviving []string) map[string][]roleOpDecoration {
	out := map[string][]roleOpDecoration{}
	if value == nil || value.Kind != yaml.SequenceNode {
		return out
	}
	remaining := make(map[string]int, len(surviving))
	for _, op := range surviving {
		remaining[op]++
	}
	for _, item := range value.Content {
		resolved, err := roleResolveNode(item)
		if err != nil || resolved.Kind != yaml.ScalarNode {
			continue
		}
		ops := auth.SplitPermissionOps(resolved.Value)
		if len(ops) == 0 {
			continue
		}
		source := resolved
		if item.HeadComment != "" || item.LineComment != "" || item.FootComment != "" {
			source = item
		}
		key := ""
		for _, op := range ops {
			if remaining[op] > 0 {
				key = op
				remaining[op]--
				break
			}
		}
		if key == "" {
			continue
		}
		out[key] = append(out[key], roleOpDecoration{
			style: source.Style, head: source.HeadComment, line: source.LineComment, foot: source.FootComment,
		})
	}
	return out
}

func roleOpsNode(ops []string, style yaml.Style) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: style}
	for _, op := range ops {
		n.Content = append(n.Content, roleScalarNode(op))
	}
	return n
}

func roleScalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func roleBuildYAMLIndex(doc *yaml.Node) (*roleYAMLIndex, error) {
	index := &roleYAMLIndex{aliasUsers: map[*yaml.Node]int{}}
	nodes := 0
	var validate func(*yaml.Node) error
	validate = func(node *yaml.Node) error {
		if node == nil {
			return fmt.Errorf("role YAML contains a nil node")
		}
		nodes++
		if nodes > roleYAMLNodeLimit {
			return fmt.Errorf("role YAML exceeds the %d-node safety limit", roleYAMLNodeLimit)
		}
		if node.Kind == yaml.AliasNode {
			target, err := roleResolveNode(node)
			if err != nil {
				return err
			}
			index.aliasUsers[target]++
			return nil
		}
		if node.Kind == yaml.MappingNode {
			if len(node.Content)%2 != 0 {
				return fmt.Errorf("role YAML contains an incomplete mapping")
			}
			seen := map[string]bool{}
			for i := 0; i < len(node.Content); i += 2 {
				key := node.Content[i]
				if key.Kind != yaml.ScalarNode {
					return fmt.Errorf("role YAML contains a non-scalar mapping key")
				}
				if key.Tag == "!!merge" || key.Value == "<<" {
					return fmt.Errorf("role YAML merge keys are not supported by the role editor")
				}
				identity := key.Value
				if seen[identity] {
					return fmt.Errorf("role YAML contains duplicate key %q", key.Value)
				}
				seen[identity] = true
			}
		}
		for _, child := range node.Content {
			if err := validate(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := validate(doc); err != nil {
		return nil, err
	}

	state := map[*yaml.Node]uint8{}
	var checkCycles func(*yaml.Node) error
	checkCycles = func(node *yaml.Node) error {
		if node == nil {
			return fmt.Errorf("role YAML contains an invalid alias")
		}
		switch state[node] {
		case 1:
			return fmt.Errorf("role YAML contains a cyclic alias")
		case 2:
			return nil
		}
		state[node] = 1
		if node.Kind == yaml.AliasNode {
			if node.Alias == nil {
				return fmt.Errorf("role YAML contains an invalid alias")
			}
			if err := checkCycles(node.Alias); err != nil {
				return err
			}
		} else {
			for _, child := range node.Content {
				if err := checkCycles(child); err != nil {
					return err
				}
			}
		}
		state[node] = 2
		return nil
	}
	if err := checkCycles(doc); err != nil {
		return nil, err
	}
	return index, nil
}

func roleOwnMapping(node *yaml.Node, label string, index *roleYAMLIndex, active map[*yaml.Node]bool) (*yaml.Node, *yaml.Node, error) {
	identity := node
	if node != nil && node.Kind == yaml.AliasNode {
		target, err := roleResolveNode(node)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", label, err)
		}
		identity = target
		if active != nil && active[identity] {
			return nil, nil, fmt.Errorf("%s contains a cyclic alias", label)
		}
		index.expansions++
		if index.expansions > roleYAMLNodeLimit {
			return nil, nil, fmt.Errorf("%s exceeds the alias expansion safety limit", label)
		}
		clone, err := roleDetachedClone(target, index)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", label, err)
		}
		clone.HeadComment = roleJoinComment(node.HeadComment, clone.HeadComment)
		clone.LineComment = roleJoinComment(node.LineComment, clone.LineComment)
		clone.FootComment = roleJoinComment(node.FootComment, clone.FootComment)
		*node = *clone
	}
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("%s must be a mapping", label)
	}
	if active != nil && active[identity] {
		return nil, nil, fmt.Errorf("%s contains a cyclic alias", label)
	}
	if node.Anchor != "" && index.aliasUsers[node] > 0 {
		return nil, nil, fmt.Errorf("%s uses shared anchor %q and cannot be edited safely", label, node.Anchor)
	}
	return node, identity, nil
}

func roleDetachedClone(node *yaml.Node, index *roleYAMLIndex) (*yaml.Node, error) {
	if node == nil {
		return nil, fmt.Errorf("invalid YAML alias target")
	}
	index.cloneNodes++
	if index.cloneNodes > roleYAMLNodeLimit {
		return nil, fmt.Errorf("alias expansion exceeds the %d-cloned-node safety limit", roleYAMLNodeLimit)
	}
	clone := *node
	clone.Anchor = ""
	clone.Content = nil
	for _, child := range node.Content {
		childClone, err := roleDetachedClone(child, index)
		if err != nil {
			return nil, err
		}
		clone.Content = append(clone.Content, childClone)
	}
	return &clone, nil
}

func roleJoinComment(first, second string) string {
	if first == "" {
		return second
	}
	if second == "" || first == second {
		return first
	}
	return first + "\n" + second
}

func roleSharedAnchor(node *yaml.Node, index *roleYAMLIndex) string {
	if node == nil || node.Kind == yaml.AliasNode {
		return ""
	}
	if node.Anchor != "" && index.aliasUsers[node] > 0 {
		return node.Anchor
	}
	for _, child := range node.Content {
		if anchor := roleSharedAnchor(child, index); anchor != "" {
			return anchor
		}
	}
	return ""
}

func roleVerifySemanticUpdate(before, after *auth.Role, name, desc string, edits []roleSectionEdit) error {
	expected := before.Permissions
	for _, edit := range edits {
		current, err := rolePermissionMap(expected, edit.kind)
		if err != nil {
			return err
		}
		updated, err := roleExpectedPermissionMap(current, edit)
		if err != nil {
			return err
		}
		roleSetPermissionMap(&expected, edit.kind, updated)
	}
	if after.Name != name || after.Description != desc {
		return fmt.Errorf("updated role YAML did not preserve the requested name and description")
	}
	if !rolePermissionsEqual(after.Permissions, expected) {
		return fmt.Errorf("updated role YAML changed permissions outside the editor matrix")
	}
	return nil
}

func rolePermissionsEqual(left, right auth.Permission) bool {
	if left.AIDataAccess != right.AIDataAccess ||
		!reflect.DeepEqual(left.RowAccess, right.RowAccess) ||
		!reflect.DeepEqual(left.FieldAccess, right.FieldAccess) ||
		(left.Processors == nil) != (right.Processors == nil) {
		return false
	}
	return rolePermissionMapsEqual(left.Catalogs, right.Catalogs) &&
		rolePermissionMapsEqual(left.Documents, right.Documents) &&
		rolePermissionMapsEqual(left.Registers, right.Registers) &&
		rolePermissionMapsEqual(left.InfoRegs, right.InfoRegs) &&
		rolePermissionMapsEqual(left.Reports, right.Reports) &&
		rolePermissionMapsEqual(left.Processors, right.Processors)
}

func rolePermissionMapsEqual(left, right map[string][]string) bool {
	if len(left) != len(right) {
		return false
	}
	for entity, leftOps := range left {
		rightOps, ok := right[entity]
		if !ok || !roleOpsEquivalent(leftOps, rightOps) {
			return false
		}
	}
	return true
}

func roleExpectedPermissionMap(current map[string][]string, edit roleSectionEdit) (map[string][]string, error) {
	wants, err := normalizedRoleWants(edit.perms)
	if err != nil {
		return nil, err
	}
	var out map[string][]string
	if current != nil {
		out = make(map[string][]string, len(current))
	}
	spelling := map[string]string{}
	used := map[string]bool{}
	for entity, oldOps := range current {
		folded := strings.ToLower(entity)
		if previous, ok := spelling[folded]; ok && previous != entity {
			return nil, fmt.Errorf("role YAML has case-ambiguous entities %q and %q", previous, entity)
		}
		spelling[folded] = entity
		newOps := make([]string, 0, len(oldOps))
		if desired, ok := wants[entity]; ok {
			newOps = append(newOps, desired.ops...)
			used[entity] = true
		}
		for _, op := range oldOps {
			if !edit.managed[op] {
				newOps = append(newOps, op)
			}
		}
		if len(newOps) != 0 {
			if out == nil {
				out = map[string][]string{}
			}
			out[entity] = newOps
		}
	}
	for entity, desired := range wants {
		if used[entity] || len(desired.ops) == 0 {
			continue
		}
		if out == nil {
			out = map[string][]string{}
		}
		out[desired.entity] = append([]string(nil), desired.ops...)
	}
	return out, nil
}

func rolePermissionMap(p auth.Permission, kind string) (map[string][]string, error) {
	switch kind {
	case "catalog":
		return p.Catalogs, nil
	case "document":
		return p.Documents, nil
	case "register":
		return p.Registers, nil
	case "inforeg":
		return p.InfoRegs, nil
	case "report":
		return p.Reports, nil
	case "processor":
		return p.Processors, nil
	default:
		return nil, fmt.Errorf("unknown role permission kind %q", kind)
	}
}

func roleSetPermissionMap(p *auth.Permission, kind string, value map[string][]string) {
	switch kind {
	case "catalog":
		p.Catalogs = value
	case "document":
		p.Documents = value
	case "register":
		p.Registers = value
	case "inforeg":
		p.InfoRegs = value
	case "report":
		p.Reports = value
	case "processor":
		p.Processors = value
	}
}

func roleDocumentMapping(doc *yaml.Node) (*yaml.Node, error) {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("role YAML: document root must be a mapping")
	}
	return doc.Content[0], nil
}

func roleResolveNode(node *yaml.Node) (*yaml.Node, error) {
	seen := map[*yaml.Node]bool{}
	for node != nil && node.Kind == yaml.AliasNode {
		if seen[node] || node.Alias == nil {
			return nil, fmt.Errorf("invalid or cyclic YAML alias")
		}
		seen[node] = true
		node = node.Alias
	}
	if node == nil {
		return nil, fmt.Errorf("nil YAML node")
	}
	return node, nil
}

func roleMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Kind == yaml.ScalarNode && mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func roleSetValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Kind != yaml.ScalarNode || mapping.Content[i].Value != key {
			continue
		}
		old := mapping.Content[i+1]
		value.HeadComment, value.LineComment, value.FootComment = old.HeadComment, old.LineComment, old.FootComment
		mapping.Content[i+1] = value
		return
	}
	mapping.Content = append(mapping.Content, roleScalarNode(key), value)
}

func roleSetScalar(mapping *yaml.Node, key, value string, index *roleYAMLIndex) error {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Kind != yaml.ScalarNode || mapping.Content[i].Value != key {
			continue
		}
		if anchor := roleSharedAnchor(mapping.Content[i+1], index); anchor != "" {
			return fmt.Errorf("role YAML key %q uses shared anchor %q and cannot be replaced safely", key, anchor)
		}
		roleSetValue(mapping, key, roleScalarNode(value))
		return nil
	}
	roleSetValue(mapping, key, roleScalarNode(value))
	return nil
}

func roleDeleteKey(mapping *yaml.Node, key string, index *roleYAMLIndex) error {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Kind == yaml.ScalarNode && mapping.Content[i].Value == key {
			if anchor := roleSharedAnchor(mapping.Content[i], index); anchor != "" {
				return fmt.Errorf("role YAML key %q uses shared anchor %q and cannot be removed safely", key, anchor)
			}
			if anchor := roleSharedAnchor(mapping.Content[i+1], index); anchor != "" {
				return fmt.Errorf("role YAML value %q uses shared anchor %q and cannot be removed safely", key, anchor)
			}
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return nil
		}
	}
	return nil
}
