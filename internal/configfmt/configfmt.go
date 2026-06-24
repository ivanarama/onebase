package configfmt

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// IsYAMLPath reports whether path names a YAML configuration file.
func IsYAMLPath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}

// FormatConfigContent returns canonical YAML for YAML files and the original
// content for all other file types.
func FormatConfigContent(path string, content []byte) ([]byte, error) {
	if !IsYAMLPath(path) {
		return content, nil
	}
	return FormatBytes(content)
}

// FormatBytes normalizes one YAML stream with deterministic key ordering and a
// stable two-space indentation.
func FormatBytes(src []byte) ([]byte, error) {
	if len(bytes.TrimSpace(src)) == 0 {
		return []byte{}, nil
	}

	dec := yaml.NewDecoder(bytes.NewReader(src))
	var docs []yaml.Node
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		normalizeNode(&doc)
		docs = append(docs, doc)
	}

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	for i := range docs {
		if err := enc.Encode(&docs[i]); err != nil {
			_ = enc.Close()
			return nil, err
		}
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// FormatFile formats one YAML file in place when it changed.
func FormatFile(path string) (bool, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	out, err := FormatBytes(src)
	if err != nil {
		return false, err
	}
	if bytes.Equal(src, out) {
		return false, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(path, out, info.Mode().Perm()); err != nil {
		return false, err
	}
	return true, nil
}

// CheckFile reports whether one YAML file would change.
func CheckFile(path string) (bool, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	out, err := FormatBytes(src)
	if err != nil {
		return false, err
	}
	return !bytes.Equal(src, out), nil
}

// CollectYAMLFiles expands files/directories into a sorted unique YAML file list.
func CollectYAMLFiles(paths []string) ([]string, error) {
	seen := map[string]bool{}
	var files []string
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if IsYAMLPath(p) {
				addFile(&files, seen, p)
			}
			continue
		}
		if err := filepath.WalkDir(p, func(path string, de fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if de.IsDir() {
				if shouldSkipDir(de.Name()) && path != p {
					return filepath.SkipDir
				}
				return nil
			}
			if IsYAMLPath(path) {
				addFile(&files, seen, path)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

func addFile(files *[]string, seen map[string]bool, path string) {
	clean := filepath.Clean(path)
	if seen[clean] {
		return
	}
	seen[clean] = true
	*files = append(*files, clean)
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".git", ".codex", "node_modules", "vendor":
		return true
	default:
		return false
	}
}

func normalizeNode(n *yaml.Node) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.DocumentNode:
		n.Style = 0
		for _, c := range n.Content {
			normalizeNode(c)
		}
	case yaml.MappingNode:
		n.Style = 0
		pairs := make([]yamlPair, 0, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			normalizeNode(n.Content[i])
			normalizeNode(n.Content[i+1])
			pairs = append(pairs, yamlPair{key: n.Content[i], value: n.Content[i+1], index: i / 2})
		}
		sort.SliceStable(pairs, func(i, j int) bool {
			return compareKeys(pairs[i], pairs[j])
		})
		n.Content = n.Content[:0]
		for _, p := range pairs {
			n.Content = append(n.Content, p.key, p.value)
		}
	case yaml.SequenceNode:
		n.Style = 0
		for _, c := range n.Content {
			normalizeNode(c)
		}
	case yaml.ScalarNode:
		if n.Tag == "!!str" && strings.Contains(n.Value, "\n") {
			n.Style = yaml.LiteralStyle
		} else {
			n.Style = 0
		}
	case yaml.AliasNode:
		normalizeNode(n.Alias)
	}
}

type yamlPair struct {
	key   *yaml.Node
	value *yaml.Node
	index int
}

func compareKeys(a, b yamlPair) bool {
	ak, bk := keyString(a.key), keyString(b.key)
	ar, br := keyRank(ak), keyRank(bk)
	unknown := len(yamlKeyOrder) + 1
	if ar == unknown && br == unknown {
		return a.index < b.index
	}
	if ar != br {
		return ar < br
	}
	al, bl := strings.ToLower(ak), strings.ToLower(bk)
	if al != bl {
		return al < bl
	}
	if ak != bk {
		return ak < bk
	}
	return a.index < b.index
}

func keyString(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	return n.Value
}

func keyRank(key string) int {
	if r, ok := yamlKeyRanks[strings.ToLower(key)]; ok {
		return r
	}
	return len(yamlKeyOrder) + 1
}

var yamlKeyOrder = []string{
	"$schema", "schema",
	"name", "title", "titles", "description",
	"version", "author", "copyright", "license", "lang", "logo",
	"enabled", "kind", "type",
	"root_url", "auth", "secret", "rate_limit", "roles",
	"schedule", "processor", "params", "timeout", "on_error",
	"posting", "numerator", "prefix", "length", "period", "scope",
	"hierarchical", "hierarchy_kind", "based_on", "list_form", "item_form",
	"list_mode", "tile_view", "image", "subtitle", "predefined",
	"fields", "dimensions", "resources", "attributes", "tableparts",
	"accounts", "subconto", "values", "constants", "permissions",
	"catalogs", "documents", "registers", "inforegs", "reports", "processors",
	"pages", "widgets", "services",
	"default", "label", "labels", "allow_inline_create",
	"periodic", "query", "composition", "groupings", "measures", "columns",
	"filters", "variants", "chart_proc", "chart_kind", "x_field", "y_fields",
	"format", "limit", "items", "entities",
	"icon", "order", "contents", "home_page",
	"document", "areas", "rows", "cells", "width", "height", "text", "parameter",
	"bold", "italic", "underline", "align", "valign", "font_size", "fontsize",
	"colspan", "rowspan", "border", "borders", "background", "color",
	"form", "entity", "layout_kind", "auto_save_settings", "vertical_scroll",
	"commands", "command_bar", "elements", "children", "data_path", "required",
	"hint", "events", "actions", "onec_meta",
	"templates", "template", "methods",
	"email", "smtp_host", "smtp_port", "smtp_user", "smtp_password",
	"from_name", "from_address",
	"attachments", "max_file_size_mb", "allowed_types",
	"demo", "reset_backup", "reset_schedule", "message",
	"backup", "keep_last", "directory",
	"llm", "endpoints", "models", "profiles", "default_profile", "log_history",
	"base_url", "api_key", "headers", "timeout_sec", "endpoint", "vision",
	"max_tokens", "task",
	"webhooks", "url", "headers", "body",
}

var yamlKeyRanks = func() map[string]int {
	m := make(map[string]int, len(yamlKeyOrder))
	for i, k := range yamlKeyOrder {
		m[k] = i
	}
	return m
}()

// FormatError wraps a YAML parse error with file context.
func FormatError(path string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", path, err)
}
