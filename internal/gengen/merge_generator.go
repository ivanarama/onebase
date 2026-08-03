package gengen

import (
	"fmt"
	"github.com/ivantit66/onebase/internal/fsmode"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// MergeGenerator applies a DeltaManifest to an existing project directory,
// creating only new entities, fields, and files.
type MergeGenerator struct {
	ProjectDir string
}

// Merge applies the delta to the project directory.
func (mg *MergeGenerator) Merge(delta *DeltaManifest) error {
	if !delta.HasChanges() {
		return nil
	}

	// 1. Create new catalogs
	for _, cat := range delta.NewCatalogs {
		if err := mg.createEntity("catalogs", cat); err != nil {
			return fmt.Errorf("create catalog %s: %w", cat.Name, err)
		}
	}

	// 2. Create new documents
	for _, doc := range delta.NewDocuments {
		if err := mg.createEntity("documents", doc); err != nil {
			return fmt.Errorf("create document %s: %w", doc.Name, err)
		}
	}

	// 3. Create new registers (stored as YAML in registers/ dir)
	for _, reg := range delta.NewRegisters {
		if err := mg.createEntity("registers", reg); err != nil {
			return fmt.Errorf("create register %s: %w", reg.Name, err)
		}
	}

	// 4. Create new enums
	for _, enum := range delta.NewEnums {
		if err := mg.createEnum(enum); err != nil {
			return fmt.Errorf("create enum %s: %w", enum.Name, err)
		}
	}

	// 5. Add new fields to existing entities
	for entityName, fields := range delta.NewFields {
		if err := mg.addFieldsToEntity(entityName, fields); err != nil {
			return fmt.Errorf("add fields to %s: %w", entityName, err)
		}
	}

	// 6. Add new table parts to existing entities
	for entityName, tps := range delta.NewTableParts {
		if err := mg.addTablePartsToEntity(entityName, tps); err != nil {
			return fmt.Errorf("add table parts to %s: %w", entityName, err)
		}
	}

	// 7. Create new DSL files
	for relPath, content := range delta.NewDSLFiles {
		fullPath := filepath.Join(mg.ProjectDir, "src", relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), fsmode.Dir); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(fullPath), err)
		}
		if err := os.WriteFile(fullPath, []byte(content), fsmode.File); err != nil {
			return fmt.Errorf("write %s: %w", fullPath, err)
		}
	}

	return nil
}

// createEntity creates a new entity YAML file.
func (mg *MergeGenerator) createEntity(kindDir string, spec EntitySpec) error {
	dir := filepath.Join(mg.ProjectDir, kindDir)
	if err := os.MkdirAll(dir, fsmode.Dir); err != nil {
		return err
	}

	filename := sanitizeFilename(spec.Name) + ".yaml"
	path := filepath.Join(dir, filename)

	// Don't overwrite existing files
	if fileExists(path) {
		return nil
	}

	raw := buildRawEntity(spec)
	data, err := yaml.Marshal(raw)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, fsmode.File)
}

// createEnum creates a new enum YAML file.
func (mg *MergeGenerator) createEnum(spec EnumSpec) error {
	dir := filepath.Join(mg.ProjectDir, "enums")
	if err := os.MkdirAll(dir, fsmode.Dir); err != nil {
		return err
	}

	filename := sanitizeFilename(spec.Name) + ".yaml"
	path := filepath.Join(dir, filename)

	if fileExists(path) {
		return nil
	}

	raw := map[string]interface{}{
		"name":   spec.Name,
		"values": spec.Values,
	}
	data, err := yaml.Marshal(raw)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, fsmode.File)
}

// addFieldsToEntity finds an existing entity YAML file and appends new fields.
func (mg *MergeGenerator) addFieldsToEntity(entityName string, fields []FieldSpec) error {
	path := mg.findEntityFile(entityName)
	if path == "" {
		return fmt.Errorf("entity %s not found", entityName)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return err
	}

	// Find the "fields" key node
	fieldsNode := findMapKey(&node, "fields")
	if fieldsNode == nil {
		// No fields key yet — create it
		fieldsNode = &yaml.Node{
			Kind:  yaml.SequenceNode,
			Tag:   "!!seq",
			Style: yaml.FlowStyle,
		}
		// Append to root mapping
		if len(node.Content) > 0 && node.Content[0].Kind == yaml.MappingNode {
			root := node.Content[0]
			root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "fields", Tag: "!!str"})
			root.Content = append(root.Content, fieldsNode)
		}
	}

	// Append new field nodes
	for _, f := range fields {
		fieldNode := buildFieldNode(f)
		fieldsNode.Content = append(fieldsNode.Content, fieldNode)
	}

	// Write back
	out, err := yaml.Marshal(&node)
	if err != nil {
		return err
	}

	return os.WriteFile(path, out, fsmode.File)
}

// addTablePartsToEntity finds an existing entity YAML file and appends new table parts.
func (mg *MergeGenerator) addTablePartsToEntity(entityName string, tps []TablePartSpec) error {
	path := mg.findEntityFile(entityName)
	if path == "" {
		return fmt.Errorf("entity %s not found", entityName)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return err
	}

	// Find the "tableparts" key node
	tpNode := findMapKey(&node, "tableparts")
	if tpNode == nil {
		tpNode = &yaml.Node{
			Kind:  yaml.SequenceNode,
			Tag:   "!!seq",
			Style: yaml.FlowStyle,
		}
		if len(node.Content) > 0 && node.Content[0].Kind == yaml.MappingNode {
			root := node.Content[0]
			root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "tableparts", Tag: "!!str"})
			root.Content = append(root.Content, tpNode)
		}
	}

	// Group TPs by name (in case multiple TPs have new fields for the same TP)
	tpByName := make(map[string]*yaml.Node)
	for _, tp := range tps {
		existing := tpByName[tp.Name]
		if existing == nil {
			existing = buildTablePartNode(tp)
			tpNode.Content = append(tpNode.Content, existing)
			tpByName[tp.Name] = existing
		} else {
			// Add fields to existing TP
			fieldsNode := findMapKeyInNode(existing, "fields")
			if fieldsNode == nil {
				fieldsNode = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
				existing.Content = append(existing.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "fields", Tag: "!!str"})
				existing.Content = append(existing.Content, fieldsNode)
			}
			for _, f := range tp.Fields {
				fieldsNode.Content = append(fieldsNode.Content, buildFieldNode(f))
			}
		}
	}

	out, err := yaml.Marshal(&node)
	if err != nil {
		return err
	}

	return os.WriteFile(path, out, fsmode.File)
}

// findEntityFile searches for an entity YAML file across known directories.
func (mg *MergeGenerator) findEntityFile(entityName string) string {
	dirs := []string{"catalogs", "documents", "registers"}
	for _, dir := range dirs {
		path := filepath.Join(mg.ProjectDir, dir, sanitizeFilename(entityName)+".yaml")
		if fileExists(path) {
			return path
		}
	}
	return ""
}

// findMapKey finds a value node for a given key in a YAML document node.
func findMapKey(doc *yaml.Node, key string) *yaml.Node {
	if len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(root.Content)-1; i += 2 {
		if root.Content[i].Value == key {
			return root.Content[i+1]
		}
	}
	return nil
}

// findMapKeyInNode finds a value node for a given key inside a mapping node.
func findMapKeyInNode(node *yaml.Node, key string) *yaml.Node {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(node.Content)-1; i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// buildRawEntity converts an EntitySpec to a map suitable for YAML marshalling.
func buildRawEntity(spec EntitySpec) map[string]interface{} {
	raw := map[string]interface{}{
		"name": spec.Name,
	}

	if len(spec.Fields) > 0 {
		fields := make([]map[string]interface{}, len(spec.Fields))
		for i, f := range spec.Fields {
			fields[i] = buildFieldMap(f)
		}
		raw["fields"] = fields
	}

	if len(spec.TableParts) > 0 {
		tps := make([]map[string]interface{}, len(spec.TableParts))
		for i, tp := range spec.TableParts {
			fields := make([]map[string]interface{}, len(tp.Fields))
			for j, f := range tp.Fields {
				fields[j] = buildFieldMap(f)
			}
			tps[i] = map[string]interface{}{
				"name":   tp.Name,
				"fields": fields,
			}
		}
		raw["tableparts"] = tps
	}

	if spec.Posting {
		raw["posting"] = true
	}

	return raw
}

// buildFieldMap converts a FieldSpec to a map for YAML.
func buildFieldMap(f FieldSpec) map[string]interface{} {
	m := map[string]interface{}{
		"name": f.Name,
		"type": f.Type,
	}
	return m
}

// buildFieldNode creates a YAML mapping node for a field.
func buildFieldNode(f FieldSpec) *yaml.Node {
	return &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "name", Tag: "!!str"},
			{Kind: yaml.ScalarNode, Value: f.Name, Tag: "!!str"},
			{Kind: yaml.ScalarNode, Value: "type", Tag: "!!str"},
			{Kind: yaml.ScalarNode, Value: f.Type, Tag: "!!str"},
		},
	}
}

// buildTablePartNode creates a YAML mapping node for a table part.
func buildTablePartNode(tp TablePartSpec) *yaml.Node {
	fieldsContent := make([]*yaml.Node, 0, len(tp.Fields)*2)
	for _, f := range tp.Fields {
		fieldsContent = append(fieldsContent,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "name", Tag: "!!str"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: f.Name, Tag: "!!str"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: "type", Tag: "!!str"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: f.Type, Tag: "!!str"},
		)
	}

	return &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "name", Tag: "!!str"},
			{Kind: yaml.ScalarNode, Value: tp.Name, Tag: "!!str"},
			{Kind: yaml.ScalarNode, Value: "fields", Tag: "!!str"},
			{Kind: yaml.SequenceNode, Tag: "!!seq", Content: fieldsContent},
		},
	}
}

// sanitizeFilename converts an entity name to a safe filename.
func sanitizeFilename(name string) string {
	// Приводим к нижнему регистру через strings.ToLower — он умеет Unicode,
	// поэтому кириллица («Контрагент» → «контрагент») тоже опускается. Прежний
	// ASCII-only вариант (A-Z) оставлял кириллицу как есть, из-за чего на
	// регистрозависимой ФС (Linux CI) файл «Контрагент.yaml» не совпадал с
	// ожидаемым «контрагент.yaml».
	return strings.ToLower(name)
}
