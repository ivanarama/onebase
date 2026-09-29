package metadata

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Сторож реестра props (#1492, в духе #1466).
//
// Предупреждение configcheck «ключ props не используется» верно ровно до тех
// пор, пока реестр перечисляет всё, что читают и пишут потребители. Новый
// ключ, добавленный в конвертер мимо реестра, превратил бы проверку в источник
// ложных срабатываний на каждой импортированной форме — то есть в шум, ради
// избавления от которого её и заводили. Сторож сверяет реестр с исходниками.

var (
	reLiteralProp = regexp.MustCompile(`Props\["([^"]+)"\]`)
	reCaseLine    = regexp.MustCompile(`^\s*case\s+(.+):\s*$`)
	reQuoted      = regexp.MustCompile(`"([^"]*)"`)
)

func TestFormPropsRegistryIsConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, spec := range FormPropRegistry() {
		if spec.Key == "" {
			t.Error("в реестре props есть запись с пустым ключом")
		}
		if seen[spec.Key] {
			t.Errorf("ключ props %q объявлен в реестре дважды", spec.Key)
		}
		seen[spec.Key] = true
		if spec.Uses == 0 {
			t.Errorf("ключ props %q числится в реестре без единого потребителя: "+
				"либо назовите потребителя, либо уберите ключ — иначе configcheck "+
				"молчит о том, о чём должен предупреждать", spec.Key)
		}
		if strings.TrimSpace(spec.Note) == "" {
			t.Errorf("ключ props %q без Note: подсказка check останется пустой", spec.Key)
		}
	}
	if len(FormPropOneCXMLKeys()) == 0 {
		t.Fatal("в реестре не осталось ни одного ключа для Form.xml — writeKnownProps перестал бы писать что-либо")
	}
}

// TestFormPropsRegistryCoversConsumers — каждый ключ props, который называет
// код конвертера 1С, есть в реестре.
func TestFormPropsRegistryCoversConsumers(t *testing.T) {
	for _, file := range goSourcesIn(t, filepath.Join("..", "onec_forms")) {
		src := readSource(t, file)
		for _, m := range reLiteralProp.FindAllStringSubmatch(src, -1) {
			if _, ok := LookupFormProp(m[1]); !ok {
				t.Errorf("%s: ключ props %q записан конвертером, но его нет в реестре "+
					"metadata.formPropRegistry — configcheck будет ложно предупреждать о нём "+
					"на каждой импортированной форме", filepath.Base(file), m[1])
			}
		}
		for _, key := range dynamicPropKeys(src) {
			if _, ok := LookupFormProp(key); !ok {
				t.Errorf("%s: ключ props %q записан конвертером по имени XML-узла, "+
					"но его нет в реестре metadata.formPropRegistry", filepath.Base(file), key)
			}
		}
	}
}

// TestFormPropsRuntimeReadsAreRegistered — если рендерер управляемой формы
// когда-нибудь начнёт читать props, ключ обязан появиться в реестре с флагом
// FormPropManagedForm. Иначе формулировка «не используется управляемой формой»
// станет неправдой.
func TestFormPropsRuntimeReadsAreRegistered(t *testing.T) {
	for _, file := range goSourcesIn(t, filepath.Join("..", "ui")) {
		src := readSource(t, file)
		for _, m := range reLiteralProp.FindAllStringSubmatch(src, -1) {
			spec, ok := LookupFormProp(m[1])
			if !ok || spec.Uses&FormPropManagedForm == 0 {
				t.Errorf("%s: рендерер читает props[%q], но в реестре нет записи с FormPropManagedForm — "+
					"предупреждение configcheck «не используется управляемой формой» перестало быть верным",
					filepath.Base(file), m[1])
			}
		}
	}
}

// TestWriteKnownPropsUsesRegistry — экспорт берёт список ключей из реестра, а
// не из собственного литерала рядом.
func TestWriteKnownPropsUsesRegistry(t *testing.T) {
	src := readSource(t, filepath.Join("..", "onec_forms", "writer_xml.go"))
	if !strings.Contains(src, "metadata.FormPropOneCXMLKeys()") {
		t.Error("writer_xml.go больше не берёт список ключей из metadata.FormPropOneCXMLKeys(): " +
			"реестр и экспорт разойдутся, и configcheck начнёт врать про 1С-ключи")
	}
}

// dynamicPropKeys восстанавливает ключи, которые импорт пишет по имени
// XML-узла: `el.Props[c.Name.Local]`, `el.Props[c.Name.Local+"_name"]`.
// Имя узла берётся из ближайшего предшествующего `case`.
func dynamicPropKeys(src string) []string {
	var out []string
	var labels []string
	for _, line := range strings.Split(src, "\n") {
		if m := reCaseLine.FindStringSubmatch(line); m != nil {
			labels = nil
			for _, q := range reQuoted.FindAllStringSubmatch(m[1], -1) {
				labels = append(labels, q[1])
			}
			continue
		}
		idx := strings.Index(line, "Props[c.Name.Local")
		if idx < 0 {
			continue
		}
		suffix := ""
		if q := reQuoted.FindStringSubmatch(line[idx:]); q != nil {
			suffix = q[1]
		}
		for _, l := range labels {
			out = append(out, l+suffix)
		}
	}
	return out
}

func goSourcesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("не читается каталог %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	if len(out) == 0 {
		t.Fatalf("в %s не найдено ни одного .go — сторож перестал бы что-либо проверять", dir)
	}
	return out
}

func readSource(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("не читается %s: %v", path, err)
	}
	return string(data)
}
