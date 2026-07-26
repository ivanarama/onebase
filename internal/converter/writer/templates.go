package writer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ivantit66/onebase/internal/printform"
)

// templateSource — найденный макет (шаблон) в выгрузке 1С.
type templateSource struct {
	Owner     string // объект-владелец ("" для общих макетов)
	OwnerKind string // раздел выгрузки (Catalogs/DataProcessors/…)
	Name      string // имя макета
	Src       string // путь к файлу-исходнику (Template.xml/.mxl/.bin/...)
}

// objectKindsWithTemplates — разделы выгрузки, у объектов которых бывают макеты.
var objectKindsWithTemplates = []string{"Catalogs", "Documents", "DataProcessors", "Reports"}

// WriteTemplates импортирует макеты 1С как заготовки печатных форм OneBase
// (issue #26 п.3). Полная конвертация .mxl нецелесообразна, поэтому для каждого
// макета создаётся scaffold printforms/<owner>_<name>.yaml и копируется исходник
// рядом как <...>.src.<ext> — оформление переносится вручную.
func WriteTemplates(sourceDir, outDir string, notes *ConversionReport) error {
	tmpls := scanTemplates(sourceDir)
	if len(tmpls) == 0 {
		return nil
	}

	// Макеты обработок → заготовки макетов src/<имя>.proc.layout.yaml
	// (issue #48 п.4): printform-привязка работает только для документов и
	// справочников, а layout подхватывается FindLayoutFile для .proc.os и
	// доступен из DSL через Макет.Область("<имя макета 1С>").
	procTmpls := map[string][]templateSource{}
	var rest []templateSource
	for _, t := range tmpls {
		if t.OwnerKind == "DataProcessors" && t.Owner != "" {
			procTmpls[t.Owner] = append(procTmpls[t.Owner], t)
		} else {
			rest = append(rest, t)
		}
	}
	if err := writeProcessorLayouts(procTmpls, outDir, notes); err != nil {
		return err
	}
	if len(rest) == 0 {
		return nil
	}

	dir := filepath.Join(outDir, "printforms")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, t := range rest {
		base := fileName(t.Name)
		if t.Owner != "" {
			base = fileName(t.Owner) + "_" + base
		} else {
			base = "common_" + base
		}

		// scaffold .yaml
		owner := t.Owner
		if owner == "" {
			owner = "TODO"
		}
		var sb strings.Builder
		sb.WriteString("# Заготовка печатной формы из макета 1С.\n")
		sb.WriteString("# TODO: перенесите оформление макета из 1С вручную (см. *.src.* рядом).\n")
		fmt.Fprintf(&sb, "name: %s\n", t.Name)
		fmt.Fprintf(&sb, "document: %s\n", owner)
		fmt.Fprintf(&sb, "title: %q\n", t.Name)
		sb.WriteString("header: |\n  TODO: оформление макета 1С не конвертируется автоматически.\n")
		if err := os.WriteFile(filepath.Join(dir, base+".yaml"), []byte(sb.String()), 0o644); err != nil {
			return err
		}

		// копия исходника
		if t.Src != "" {
			ext := filepath.Ext(t.Src)
			if err := copyFileRaw(t.Src, filepath.Join(dir, base+".src"+ext)); err != nil {
				notes.FormWarnings = append(notes.FormWarnings,
					fmt.Sprintf("макет %s: не удалось скопировать исходник: %v", t.Name, err))
			}
		}
		notes.Templates++
	}
	return nil
}

// writeProcessorLayouts пишет для каждой обработки src/<имя>.proc.layout.yaml:
// каждый макет 1С — именованная область с TODO-ячейкой; исходники макетов
// копируются рядом как src/<обработка>_<макет>.src.<ext>.
func writeProcessorLayouts(groups map[string][]templateSource, outDir string, notes *ConversionReport) error {
	if len(groups) == 0 {
		return nil
	}
	srcDir := filepath.Join(outDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		return err
	}
	owners := make([]string, 0, len(groups))
	for o := range groups {
		owners = append(owners, o)
	}
	sort.Strings(owners)
	for _, owner := range owners {
		lt := printform.LayoutTemplate{Name: owner}
		for _, t := range groups[owner] {
			srcNote := ""
			if t.Src != "" {
				ext := filepath.Ext(t.Src)
				srcName := fileName(owner) + "_" + fileName(t.Name) + ".src" + ext
				if err := copyFileRaw(t.Src, filepath.Join(srcDir, srcName)); err != nil {
					notes.FormWarnings = append(notes.FormWarnings,
						fmt.Sprintf("макет %s/%s: не удалось скопировать исходник: %v", owner, t.Name, err))
				} else {
					srcNote = " (исходник: src/" + srcName + ")"
				}
			}
			lt.Areas = append(lt.Areas, &printform.LayoutArea{
				Name: t.Name,
				Rows: []printform.LayoutRow{{
					Cells: []printform.LayoutCell{{Text: "TODO: перенесите оформление макета 1С «" + t.Name + "»" + srcNote}},
				}},
			})
			// notes.Templates НЕ инкрементируем: макеты обработок теперь не
			// printform-шаблоны, а заготовки src/*.proc.layout.yaml. Они
			// перечисляются отдельно в секции ProcessorLayouts отчёта, поэтому
			// строка «Шаблонов (макетов): N → N printform» остаётся честной
			// (считает только реальные printform макеты документов/справочников).
		}
		data, err := yaml.Marshal(&lt)
		if err != nil {
			return err
		}
		header := "# Заготовка макета обработки из 1С. Области доступны из DSL:\n" +
			"# Макет.Область(\"<имя макета 1С>\"). TODO: перенесите оформление вручную.\n"
		name := fileName(owner) + ".proc.layout.yaml"
		if err := os.WriteFile(filepath.Join(srcDir, name), append([]byte(header), data...), 0o644); err != nil {
			return err
		}
		notes.ProcessorLayouts = append(notes.ProcessorLayouts, name)
	}
	return nil
}

// scanTemplates обходит выгрузку и собирает макеты объектов и общие макеты.
func scanTemplates(sourceDir string) []templateSource {
	var out []templateSource

	for _, kind := range objectKindsWithTemplates {
		kindDir := filepath.Join(sourceDir, kind)
		objs, err := os.ReadDir(kindDir)
		if err != nil {
			continue
		}
		for _, obj := range objs {
			if !obj.IsDir() {
				continue
			}
			out = append(out, scanTemplateDir(filepath.Join(kindDir, obj.Name(), "Templates"), obj.Name(), kind)...)
		}
	}

	// Общие макеты
	out = append(out, scanTemplateDir(filepath.Join(sourceDir, "CommonTemplates"), "", "")...)
	return out
}

// scanTemplateDir читает каталог Templates/<Name>/Ext и возвращает источники.
func scanTemplateDir(templatesDir, owner, ownerKind string) []templateSource {
	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		return nil
	}
	var out []templateSource
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		src := findTemplateSrc(filepath.Join(templatesDir, e.Name(), "Ext"))
		out = append(out, templateSource{Owner: owner, OwnerKind: ownerKind, Name: e.Name(), Src: src})
	}
	return out
}

// findTemplateSrc выбирает файл-исходник макета в каталоге Ext (Template.* в
// приоритете, иначе первый попавшийся файл). Возвращает "" если ничего нет.
func findTemplateSrc(extDir string) string {
	entries, err := os.ReadDir(extDir)
	if err != nil {
		return ""
	}
	var fallback string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), "Template.") {
			return filepath.Join(extDir, e.Name())
		}
		if fallback == "" {
			fallback = filepath.Join(extDir, e.Name())
		}
	}
	return fallback
}

func copyFileRaw(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
