package configfmt

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/fsmode"
	"gopkg.in/yaml.v3"
)

// CollectYAMLFiles returns all YAML files under paths in stable order.
func CollectYAMLFiles(paths []string) ([]string, error) {
	var files []string
	for _, root := range paths {
		info, err := os.Stat(root)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if IsYAMLPath(root) {
				files = append(files, root)
			}
			continue
		}
		if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", ".hg", ".svn", "node_modules", "vendor":
					return filepath.SkipDir
				}
				return nil
			}
			if IsYAMLPath(path) {
				files = append(files, path)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

// IsYAMLPath reports whether path has a YAML extension.
func IsYAMLPath(path string) bool {
	low := strings.ToLower(path)
	return strings.HasSuffix(low, ".yaml") || strings.HasSuffix(low, ".yml")
}

// FormatYAMLBytes canonicalizes a YAML document: sorted mapping keys and
// deterministic indentation.
func FormatYAMLBytes(data []byte) ([]byte, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return data, nil
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, err
	}
	sortYAMLNode(&node)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&node); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// FormatYAMLFile formats a YAML file in place unless check is true.
// It returns true when the file already matched the canonical form.
func FormatYAMLFile(path string, check bool) (alreadyFormatted bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	out, err := FormatYAMLBytes(data)
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	if bytes.Equal(data, out) {
		return true, nil
	}
	if check {
		return false, nil
	}
	if err := os.WriteFile(path, out, fsmode.File); err != nil { //nolint:gosec // G703: путь получен обходом каталога проекта (os.ReadDir/WalkDir), из запроса он не приходит
		return false, err
	}
	return false, nil
}

func sortYAMLNode(n *yaml.Node) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			sortYAMLNode(c)
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			sortYAMLNode(c)
		}
	case yaml.MappingNode:
		type pair struct {
			key *yaml.Node
			val *yaml.Node
		}
		pairs := make([]pair, 0, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			sortYAMLNode(n.Content[i+1])
			pairs = append(pairs, pair{key: n.Content[i], val: n.Content[i+1]})
		}
		sort.SliceStable(pairs, func(i, j int) bool {
			return pairs[i].key.Value < pairs[j].key.Value
		})
		n.Content = n.Content[:0]
		for _, p := range pairs {
			n.Content = append(n.Content, p.key, p.val)
		}
	}
}
