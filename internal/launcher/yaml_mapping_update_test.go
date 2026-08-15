package launcher

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestUpdateYAMLMapping_PreservesCommentsAnchorsAndAliases(t *testing.T) {
	const raw = `# комментарий документа
shared_contents: &shared
  # комментарий списка
  catalogs: [Старый] # состав
contents: *shared
title: &label "Старый заголовок" # заголовок
mirror_title: *label
experimental:
  enabled: true
`

	out, err := updateYAMLMapping([]byte(raw), "subsystems/test.yaml", func(doc *yaml.Node) error {
		if err := setYAMLMapField(doc, "title", "Новый заголовок"); err != nil {
			return err
		}
		contents, err := yamlSubMap(doc, "contents")
		if err != nil {
			return err
		}
		return setYAMLMapField(contents, "catalogs", []string{"Новый"})
	})
	if err != nil {
		t.Fatalf("updateYAMLMapping: %v", err)
	}

	saved := string(out)
	for _, fragment := range []string{
		"# комментарий документа",
		"# комментарий списка",
		"# состав",
		"# заголовок",
		"&shared",
		"*shared",
		"&label",
		"*label",
		"experimental:",
	} {
		if !strings.Contains(saved, fragment) {
			t.Errorf("после точечной правки потеряно %q:\n%s", fragment, saved)
		}
	}

	var decoded struct {
		SharedContents struct {
			Catalogs []string `yaml:"catalogs"`
		} `yaml:"shared_contents"`
		Contents struct {
			Catalogs []string `yaml:"catalogs"`
		} `yaml:"contents"`
		Title       string `yaml:"title"`
		MirrorTitle string `yaml:"mirror_title"`
	}
	if err := yaml.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("результат не разбирается: %v\n%s", err, saved)
	}
	if got := decoded.Contents.Catalogs; len(got) != 1 || got[0] != "Новый" {
		t.Errorf("contents.catalogs = %v", got)
	}
	if got := decoded.SharedContents.Catalogs; len(got) != 1 || got[0] != "Новый" {
		t.Errorf("shared_contents.catalogs = %v", got)
	}
	if decoded.Title != "Новый заголовок" || decoded.MirrorTitle != "Новый заголовок" {
		t.Errorf("alias заголовка потерял семантику: title=%q mirror=%q", decoded.Title, decoded.MirrorTitle)
	}
}

func TestUpdateYAMLMapping_RejectsAmbiguousOrMalformedDocuments(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		edit func(*yaml.Node) error
	}{
		{
			name: "duplicate key",
			raw:  "name: Первый\nname: Второй\n",
			edit: func(doc *yaml.Node) error { return setYAMLMapField(doc, "title", "Тест") },
		},
		{
			name: "multiple documents",
			raw:  "name: Первый\n---\nname: Второй\n",
			edit: func(doc *yaml.Node) error { return setYAMLMapField(doc, "title", "Тест") },
		},
		{
			name: "broken syntax",
			raw:  "name: [\n",
			edit: func(doc *yaml.Node) error { return setYAMLMapField(doc, "title", "Тест") },
		},
		{
			name: "contents is not a mapping",
			raw:  "name: Тест\ncontents: []\n",
			edit: func(doc *yaml.Node) error {
				_, err := yamlSubMap(doc, "contents")
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := updateYAMLMapping([]byte(tt.raw), "subsystems/test.yaml", tt.edit)
			if err == nil {
				t.Fatalf("ожидалась ошибка, получено:\n%s", out)
			}
			if out != nil {
				t.Fatalf("при ошибке появился результат: %q", out)
			}
		})
	}
}
