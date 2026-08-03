package converter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/converter/parser1c"
	"github.com/ivantit66/onebase/internal/converter/writer"
	"github.com/ivantit66/onebase/internal/fsmode"
	"github.com/ivantit66/onebase/internal/onec_forms"
	"gopkg.in/yaml.v3"
)

// Options настройки конвертации.
type Options struct {
	// SourceDir — путь к выгрузке конфигурации 1С (папка с Catalogs/, Documents/, ...)
	SourceDir string
	// OutDir — куда писать результат (создаётся если нет)
	OutDir string
}

// Convert читает 1С XML-выгрузку и создаёт onebase-проект в OutDir.
func Convert(opts Options) (*writer.ConversionReport, error) {
	if opts.SourceDir == "" {
		return nil, fmt.Errorf("convert: source dir is required")
	}
	if opts.OutDir == "" {
		return nil, fmt.Errorf("convert: output dir is required")
	}

	dump, err := parser1c.ParseDir(opts.SourceDir)
	if err != nil {
		return nil, fmt.Errorf("convert: parse: %w", err)
	}

	if err := os.MkdirAll(opts.OutDir, fsmode.Dir); err != nil {
		return nil, err
	}

	report := &writer.ConversionReport{}

	downgradeDanglingEnums(dump, report)

	// Справочники
	if err := writer.WriteCatalogs(dump.Catalogs, opts.OutDir, report); err != nil {
		return nil, fmt.Errorf("convert: write catalogs: %w", err)
	}

	// Документы
	if err := writer.WriteDocuments(dump.Documents, opts.OutDir, report); err != nil {
		return nil, fmt.Errorf("convert: write documents: %w", err)
	}

	// Регистры накопления
	if err := writer.WriteRegisters(dump.Registers, opts.OutDir, report); err != nil {
		return nil, fmt.Errorf("convert: write registers: %w", err)
	}

	// Перечисления
	if err := writer.WriteEnums(dump.Enums, opts.OutDir, report); err != nil {
		return nil, fmt.Errorf("convert: write enums: %w", err)
	}

	// Константы
	if err := writer.WriteConstants(dump.Constants, opts.OutDir, report); err != nil {
		return nil, fmt.Errorf("convert: write constants: %w", err)
	}

	// Регистры сведений
	if err := writer.WriteInfoRegisters(dump.InfoRegisters, opts.OutDir, report); err != nil {
		return nil, fmt.Errorf("convert: write info registers: %w", err)
	}

	// Регистры бухгалтерии
	if err := writer.WriteAccountRegisters(dump.AccountRegisters, opts.OutDir, report); err != nil {
		return nil, fmt.Errorf("convert: write account registers: %w", err)
	}

	// Планы счетов
	if err := writer.WriteChartsOfAccounts(dump.ChartsOfAccounts, opts.OutDir, report); err != nil {
		return nil, fmt.Errorf("convert: write charts of accounts: %w", err)
	}

	// Регламентные задания
	if err := writer.WriteScheduledJobs(dump.ScheduledJobs, opts.OutDir, report); err != nil {
		return nil, fmt.Errorf("convert: write scheduled jobs: %w", err)
	}

	// Общие модули
	if err := writer.WriteModules(dump.Modules, opts.OutDir, report); err != nil {
		return nil, fmt.Errorf("convert: write modules: %w", err)
	}

	// Обработки
	if err := writer.WriteProcessors(dump.Processors, opts.OutDir, report); err != nil {
		return nil, fmt.Errorf("convert: write processors: %w", err)
	}

	// Управляемые формы справочников и документов
	if err := writeForms(dump, opts.OutDir, report); err != nil {
		return nil, fmt.Errorf("convert: write forms: %w", err)
	}

	// Шаблоны (макеты) → заготовки печатных форм
	if err := writer.WriteTemplates(opts.SourceDir, opts.OutDir, report); err != nil {
		return nil, fmt.Errorf("convert: write templates: %w", err)
	}

	// DSL-заглушки
	if err := writer.WriteDSLStubs(dump.Documents, opts.SourceDir, opts.OutDir, report); err != nil {
		return nil, fmt.Errorf("convert: write dsl stubs: %w", err)
	}

	// app.yaml — use source dir name (the 1C config folder), not the temp workspace dir
	appName := filepath.Base(opts.SourceDir)
	if appName == "" || appName == "." {
		appName = filepath.Base(opts.OutDir)
	}
	if err := writeAppYAML(opts.OutDir, appName); err != nil {
		return nil, err
	}

	// Пропущенные объекты
	for _, s := range dump.SkippedDirs {
		report.Skipped = append(report.Skipped, s.Kind+"/"+s.Name)
	}

	// Записать отчёт в файл
	reportPath := filepath.Join(opts.OutDir, "conversion_report.txt")
	if err := os.WriteFile(reportPath, []byte(report.String()), fsmode.File); err != nil {
		return nil, err
	}

	return report, nil
}

// writeForms импортирует управляемые формы справочников, документов и обработок через
// пакет onec_forms (issue #26 п.4, #48 п.3). Формы пишутся в out/forms/<entity>/.
func writeForms(dump *parser1c.ConfigDump, outDir string, report *writer.ConversionReport) error {
	var all []parser1c.FormSource
	for _, c := range dump.Catalogs {
		all = append(all, c.Forms...)
	}
	for _, d := range dump.Documents {
		all = append(all, d.Forms...)
	}
	for _, p := range dump.Processors {
		all = append(all, p.Forms...)
	}

	for _, f := range all {
		entityDir := strings.ToLower(strings.ReplaceAll(f.Entity, " ", "_"))
		formBase := strings.ToLower(strings.ReplaceAll(f.FormName, " ", "_"))
		dstYAML := filepath.Join(outDir, "forms", entityDir, formBase+".form.yaml")
		dstOS := filepath.Join(outDir, "forms", entityDir, formBase+".form.os")
		dstRes := filepath.Join(outDir, "forms", entityDir, formBase, "_resources")

		bsl := filepath.Join(f.ExtDir, "Form", "Module.bsl")
		if _, err := os.Stat(bsl); err != nil {
			bsl = ""
		}
		items := filepath.Join(f.ExtDir, "Form", "Items")
		if _, err := os.Stat(items); err != nil {
			items = ""
		}

		rep, err := onec_forms.ImportFromOneC(onec_forms.ImportOptions{
			XMLPath:         filepath.Join(f.ExtDir, "Form.xml"),
			BSLPath:         bsl,
			ItemsDir:        items,
			EntityName:      f.Entity,
			FormName:        f.FormName,
			DstYAMLPath:     dstYAML,
			DstOSPath:       dstOS,
			DstResourcesDir: dstRes,
		})
		if err != nil {
			report.FormWarnings = append(report.FormWarnings,
				fmt.Sprintf("%s/%s: %v", f.Entity, f.FormName, err))
			continue
		}
		report.Forms++
		for _, w := range rep.Warnings {
			if w.Severity == onec_forms.SeverityInfo {
				continue
			}
			report.FormWarnings = append(report.FormWarnings,
				fmt.Sprintf("%s/%s: %s", f.Entity, f.FormName, w.String()))
		}
	}
	return nil
}

type appConfig struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

// downgradeDanglingEnums деградирует ссылки на перечисления, которых нет в
// выгрузке, обратно в string: иначе сконвертированная конфигурация падает на
// metadata.Validate («references unknown enum X»). Сверка по точному имени —
// так же резолвит перечисления и платформа.
func downgradeDanglingEnums(dump *parser1c.ConfigDump, report *writer.ConversionReport) {
	known := map[string]bool{}
	for _, e := range dump.Enums {
		known[e.Name] = true
	}
	fixAttrs := func(owner string, attrs []parser1c.Attribute) {
		for i := range attrs {
			mapped, _ := parser1c.MapType(attrs[i].Type)
			name, ok := strings.CutPrefix(mapped, "enum:")
			if !ok || known[name] {
				continue
			}
			attrs[i].Type = parser1c.FieldType{Primary: "String"}
			report.TypeWarnings = append(report.TypeWarnings,
				fmt.Sprintf("%s.%s: перечисление %s не найдено в выгрузке → string", owner, attrs[i].Name, name))
		}
	}
	for _, c := range dump.Catalogs {
		fixAttrs(c.Name, c.Attributes)
		for _, ts := range c.TabularSections {
			fixAttrs(c.Name+"."+ts.Name, ts.Attributes)
		}
	}
	for _, d := range dump.Documents {
		fixAttrs(d.Name, d.Attributes)
		for _, ts := range d.TabularSections {
			fixAttrs(d.Name+"."+ts.Name, ts.Attributes)
		}
	}
	for _, r := range dump.Registers {
		fixAttrs(r.Name, r.Dimensions)
		fixAttrs(r.Name, r.Resources)
		fixAttrs(r.Name, r.Attributes)
	}
	for _, r := range dump.InfoRegisters {
		fixAttrs(r.Name, r.Dimensions)
		fixAttrs(r.Name, r.Resources)
		fixAttrs(r.Name, r.Attributes)
	}
	for _, r := range dump.AccountRegisters {
		fixAttrs(r.Name, r.Dimensions)
		fixAttrs(r.Name, r.Resources)
		fixAttrs(r.Name, r.Attributes)
	}
	for _, ch := range dump.ChartsOfAccounts {
		fixAttrs(ch.Name, ch.Attributes)
	}
	for _, p := range dump.Processors {
		fixAttrs(p.Name, p.Attributes)
	}
	for _, c := range dump.Constants {
		mapped, _ := parser1c.MapType(c.Type)
		name, ok := strings.CutPrefix(mapped, "enum:")
		if !ok || known[name] {
			continue
		}
		c.Type = parser1c.FieldType{Primary: "String"}
		report.TypeWarnings = append(report.TypeWarnings,
			fmt.Sprintf("константа %s: перечисление %s не найдено в выгрузке → string", c.Name, name))
	}
}

func writeAppYAML(outDir, name string) error {
	configDir := filepath.Join(outDir, "config")
	if err := os.MkdirAll(configDir, fsmode.Dir); err != nil {
		return err
	}
	cfg := appConfig{Name: name, Version: "1.0"}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(configDir, "app.yaml"), data, fsmode.File)
}
