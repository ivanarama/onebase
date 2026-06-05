package writer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/converter/parser1c"
	"gopkg.in/yaml.v3"
)

type yamlField struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
}

type yamlTablePart struct {
	Name   string      `yaml:"name"`
	Fields []yamlField `yaml:"fields"`
}

type yamlCatalog struct {
	Name       string          `yaml:"name"`
	Title      string          `yaml:"title,omitempty"`
	Fields     []yamlField     `yaml:"fields"`
	TableParts []yamlTablePart `yaml:"tableparts,omitempty"`
}

type yamlDocument struct {
	Name       string          `yaml:"name"`
	Title      string          `yaml:"title,omitempty"`
	Fields     []yamlField     `yaml:"fields"`
	TableParts []yamlTablePart `yaml:"tableparts,omitempty"`
}

type yamlRegister struct {
	Name       string      `yaml:"name"`
	Dimensions []yamlField `yaml:"dimensions"`
	Resources  []yamlField `yaml:"resources"`
	Attributes []yamlField `yaml:"attributes,omitempty"`
}

// WriteCatalogs записывает справочники в out/catalogs/*.yaml.
func WriteCatalogs(cats []*parser1c.CatalogMeta, outDir string, notes *ConversionReport) error {
	dir := filepath.Join(outDir, "catalogs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, cat := range cats {
		obj := yamlCatalog{
			Name:   cat.Name,
			Title:  synonymTitle(cat.Name, cat.Synonym),
			Fields: withStandardCatalogFields(convertFields(cat.Attributes, notes)),
		}
		for _, ts := range cat.TabularSections {
			obj.TableParts = append(obj.TableParts, yamlTablePart{
				Name:   ts.Name,
				Fields: convertFields(ts.Attributes, notes),
			})
		}
		if err := writeYAML(filepath.Join(dir, fileName(cat.Name)+".yaml"), obj); err != nil {
			return err
		}
		notes.Catalogs++
	}
	return nil
}

// WriteDocuments записывает документы в out/documents/*.yaml.
func WriteDocuments(docs []*parser1c.DocumentMeta, outDir string, notes *ConversionReport) error {
	dir := filepath.Join(outDir, "documents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, doc := range docs {
		obj := yamlDocument{
			Name:   doc.Name,
			Title:  synonymTitle(doc.Name, doc.Synonym),
			Fields: convertFields(doc.Attributes, notes),
		}
		for _, ts := range doc.TabularSections {
			obj.TableParts = append(obj.TableParts, yamlTablePart{
				Name:   ts.Name,
				Fields: convertFields(ts.Attributes, notes),
			})
		}
		if err := writeYAML(filepath.Join(dir, fileName(doc.Name)+".yaml"), obj); err != nil {
			return err
		}
		notes.Documents++
	}
	return nil
}

// WriteRegisters записывает регистры накопления в out/registers/*.yaml.
func WriteRegisters(regs []*parser1c.RegisterMeta, outDir string, notes *ConversionReport) error {
	dir := filepath.Join(outDir, "registers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, reg := range regs {
		obj := yamlRegister{
			Name:       reg.Name,
			Dimensions: convertFields(reg.Dimensions, notes),
			Resources:  convertFields(reg.Resources, notes),
			Attributes: convertFields(reg.Attributes, notes),
		}
		if err := writeYAML(filepath.Join(dir, fileName(reg.Name)+".yaml"), obj); err != nil {
			return err
		}
		notes.Registers++
	}
	return nil
}

type yamlEnum struct {
	Name   string   `yaml:"name"`
	Values []string `yaml:"values"`
}

type yamlConstant struct {
	Name  string `yaml:"name"`
	Type  string `yaml:"type"`
	Label string `yaml:"label,omitempty"`
}

type yamlConstants struct {
	Constants []yamlConstant `yaml:"constants"`
}

type yamlInfoReg struct {
	Name       string      `yaml:"name"`
	Periodic   bool        `yaml:"periodic,omitempty"`
	Dimensions []yamlField `yaml:"dimensions"`
	Resources  []yamlField `yaml:"resources"`
	Attributes []yamlField `yaml:"attributes,omitempty"`
}

type yamlAccountReg struct {
	Name      string      `yaml:"name"`
	Resources []yamlField `yaml:"resources"`
}

type yamlAccountEntry struct {
	Code string `yaml:"code"`
	Name string `yaml:"name"`
	Kind string `yaml:"kind"`
}

type yamlChartOfAccounts struct {
	Name     string             `yaml:"name"`
	Accounts []yamlAccountEntry `yaml:"accounts"`
}

type yamlScheduledJob struct {
	Name      string `yaml:"name"`
	Schedule  string `yaml:"schedule"`
	Processor string `yaml:"processor"`
	Enabled   bool   `yaml:"enabled"`
}

// WriteEnums записывает перечисления в out/enums/*.yaml.
func WriteEnums(enums []*parser1c.EnumMeta, outDir string, notes *ConversionReport) error {
	dir := filepath.Join(outDir, "enums")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, e := range enums {
		obj := yamlEnum{Name: e.Name, Values: e.Values}
		if len(obj.Values) == 0 {
			obj.Values = []string{"TODO"}
		}
		if err := writeYAML(filepath.Join(dir, fileName(e.Name)+".yaml"), obj); err != nil {
			return err
		}
		notes.Enums++
	}
	return nil
}

// WriteConstants записывает все константы в out/constants/constants.yaml.
func WriteConstants(consts []*parser1c.ConstantMeta, outDir string, notes *ConversionReport) error {
	if len(consts) == 0 {
		return nil
	}
	dir := filepath.Join(outDir, "constants")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var items []yamlConstant
	for _, c := range consts {
		t, note := parser1c.MapType(c.Type)
		if note != "" {
			notes.TypeWarnings = append(notes.TypeWarnings, fmt.Sprintf("constant %s: %s", c.Name, note))
		}
		items = append(items, yamlConstant{Name: c.Name, Type: t, Label: c.Synonym})
		notes.Constants++
	}
	obj := yamlConstants{Constants: items}
	return writeYAML(filepath.Join(dir, "constants.yaml"), obj)
}

// WriteInfoRegisters записывает регистры сведений в out/inforegs/*.yaml.
func WriteInfoRegisters(regs []*parser1c.InfoRegMeta, outDir string, notes *ConversionReport) error {
	dir := filepath.Join(outDir, "inforegs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, reg := range regs {
		obj := yamlInfoReg{
			Name:       reg.Name,
			Periodic:   reg.Periodic,
			Dimensions: convertFields(reg.Dimensions, notes),
			Resources:  convertFields(reg.Resources, notes),
			Attributes: convertFields(reg.Attributes, notes),
		}
		if err := writeYAML(filepath.Join(dir, fileName(reg.Name)+".yaml"), obj); err != nil {
			return err
		}
		notes.InfoRegisters++
	}
	return nil
}

// WriteAccountRegisters записывает регистры бухгалтерии в out/accountregs/*.yaml.
func WriteAccountRegisters(regs []*parser1c.AccountRegMeta, outDir string, notes *ConversionReport) error {
	dir := filepath.Join(outDir, "accountregs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, reg := range regs {
		obj := yamlAccountReg{
			Name:      reg.Name,
			Resources: convertFields(reg.Resources, notes),
		}
		if len(obj.Resources) == 0 {
			obj.Resources = []yamlField{{Name: "Сумма", Type: "number"}}
		}
		if err := writeYAML(filepath.Join(dir, fileName(reg.Name)+".yaml"), obj); err != nil {
			return err
		}
		notes.AccountRegisters++
	}
	return nil
}

// WriteChartsOfAccounts записывает планы счетов в out/accounts/*.yaml.
func WriteChartsOfAccounts(charts []*parser1c.ChartOfAccountsMeta, outDir string, notes *ConversionReport) error {
	dir := filepath.Join(outDir, "accounts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, chart := range charts {
		obj := yamlChartOfAccounts{
			Name: chart.Name,
			Accounts: []yamlAccountEntry{
				{Code: "TODO", Name: "Добавьте счета вручную", Kind: "active_passive"},
			},
		}
		if err := writeYAML(filepath.Join(dir, fileName(chart.Name)+".yaml"), obj); err != nil {
			return err
		}
		notes.ChartsOfAccounts++
	}
	return nil
}

// WriteScheduledJobs записывает регламентные задания в out/scheduled/*.yaml.
func WriteScheduledJobs(jobs []*parser1c.ScheduledJobMeta, outDir string, notes *ConversionReport) error {
	dir := filepath.Join(outDir, "scheduled")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, job := range jobs {
		processor := job.Handler
		if processor == "" {
			processor = job.Name
		}
		schedule := job.Schedule
		if schedule == "" {
			schedule = "0 * * * *"
		}
		obj := yamlScheduledJob{
			Name:      job.Name,
			Schedule:  schedule,
			Processor: processor,
			Enabled:   false,
		}
		if err := writeYAML(filepath.Join(dir, fileName(job.Name)+".yaml"), obj); err != nil {
			return err
		}
		notes.ScheduledJobs++
	}
	return nil
}

// synonymTitle возвращает синоним 1С как title объекта. Пустой синоним и
// синоним, совпадающий с именем, отбрасываются — title не нужен.
func synonymTitle(name, synonym string) string {
	synonym = strings.TrimSpace(synonym)
	if synonym == "" || synonym == name {
		return ""
	}
	return synonym
}

// withStandardCatalogFields добавляет стандартные реквизиты справочника 1С
// (Код и Наименование) в начало списка полей. В выгрузке 1С они хранятся вне
// секции <Attributes>, поэтому при конвертации терялись (issue #26 п.2).
// Если пользовательский реквизит уже носит такое имя — не дублируем.
func withStandardCatalogFields(fields []yamlField) []yamlField {
	has := func(name string) bool {
		for _, f := range fields {
			if strings.EqualFold(f.Name, name) {
				return true
			}
		}
		return false
	}
	var std []yamlField
	if !has("Код") {
		std = append(std, yamlField{Name: "Код", Type: "string"})
	}
	if !has("Наименование") {
		std = append(std, yamlField{Name: "Наименование", Type: "string"})
	}
	return append(std, fields...)
}

func convertFields(attrs []parser1c.Attribute, notes *ConversionReport) []yamlField {
	var fields []yamlField
	for _, a := range attrs {
		t, note := parser1c.MapType(a.Type)
		f := yamlField{Name: a.Name, Type: t}
		fields = append(fields, f)
		if note != "" {
			notes.TypeWarnings = append(notes.TypeWarnings, fmt.Sprintf("%s.%s: %s", "field", a.Name, note))
		}
	}
	return fields
}

func writeYAML(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := yaml.NewEncoder(f)
	enc.SetIndent(2)
	return enc.Encode(v)
}

// fileName преобразует имя объекта 1С в имя файла (lowercase, без пробелов).
func fileName(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, " ", "_"))
}

// WriteProcessors записывает обработки в out/processors/*.yaml.
func WriteProcessors(procs []*parser1c.ProcessorMeta, outDir string, notes *ConversionReport) error {
	dir := filepath.Join(outDir, "processors")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	srcDir := filepath.Join(outDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		return err
	}
	for _, proc := range procs {
		var fields []yamlField
		for _, a := range proc.Attributes {
			t, note := parser1c.MapType(a.Type)
			fields = append(fields, yamlField{Name: a.Name, Type: t})
			if note != "" {
				notes.TypeWarnings = append(notes.TypeWarnings, fmt.Sprintf("processor %s.%s: %s", proc.Name, a.Name, note))
			}
		}
		obj := map[string]any{
			"name":   proc.Name,
			"title":  synonymTitle(proc.Name, proc.Synonym),
			"params": fields,
		}
		if err := writeYAML(filepath.Join(dir, fileName(proc.Name)+".yaml"), obj); err != nil {
			return err
		}
		source := proc.Source
		if source == "" {
			source = fmt.Sprintf("// %s\n// Обработка\n\nПроцедура Главная()\nКонецПроцедуры\n", proc.Name)
		}
		srcPath := filepath.Join(srcDir, fileName(proc.Name)+".proc.os")
		if err := os.WriteFile(srcPath, []byte(source), 0o644); err != nil {
			return err
		}
		notes.Processors++
	}
	return nil
}
