package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ivantit66/onebase/internal/onec_forms"
)

// formsCmd — родительская команда для работы с управляемыми формами.
// План 37: реализованы import/export/validate для round-trip управляемых форм.
var formsCmd = &cobra.Command{
	Use:   "forms",
	Short: "Управляемые формы OneBase (импорт/экспорт с XML управляемых форм Enterprise-системы)",
	Long: `Подкоманды для работы с управляемыми формами OneBase.

OneBase описывает форму декларативно в YAML (.form.yaml) плюс DSL-модуль
с процедурами-обработчиками (.form.os). Эта группа команд импортирует
форму из выгрузки конфигурации Enterprise-системы и экспортирует обратно.

Подробнее: docs/forms-1c-converter.md`,
}

var formsConvertFromCmd = &cobra.Command{
	Use:   "convert-from-1c",
	Short: "Импорт управляемой формы из выгрузки 1С в .form.yaml + .form.os",
	Long: `Читает Form.xml + Module.bsl + Items/* из каталога выгрузки 1С и
создаёт .form.yaml, .form.os и _resources/ в каталоге проекта OneBase.

Пример:
  onebase forms convert-from-1c \
    --src C:\Projects\АА5БП3\УТ11УТ11\ПереносДанныхУТ11УТ11_52\Forms\Форма \
    --entity ПереносДанныхУТ11 \
    --form-name Форма \
    --dst C:\Projects\my-project`,
	RunE: runFormsConvertFrom,
}

var formsValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Проверка корректности .form.yaml",
	Long: `Проверяет схему onebase.form/v1, обязательные поля и согласованность.
Выходной код 1 при наличии error-предупреждений, 0 иначе.

Пример:
  onebase forms validate --src C:\Projects\my-project\forms\контрагент\объекта.form.yaml`,
	RunE: runFormsValidate,
}

var formsConvertToCmd = &cobra.Command{
	Use:   "convert-to-1c",
	Short: "Экспорт управляемой формы OneBase в Form.xml + Module.bsl + Items/",
	Long: `Читает .form.yaml + .form.os + _resources/ из проекта OneBase и
создаёт Form.xml, Form/Module.bsl, Form/Items/* в каталоге выгрузки 1С.

Пример:
  onebase forms convert-to-1c \
    --src C:\Projects\my-project\forms\контрагент\объекта.form.yaml \
    --dst C:\Projects\my-1c-config\Forms\ФормаКонтрагента\Ext`,
	RunE: runFormsConvertTo,
}

func init() {
	formsConvertFromCmd.Flags().String("src", "", "путь к Forms/<FormName> в выгрузке 1С (содержит Ext/Form.xml)")
	formsConvertFromCmd.Flags().String("entity", "", "имя сущности OneBase, к которой привязывается форма")
	formsConvertFromCmd.Flags().String("form-name", "", "имя создаваемой формы (по умолчанию из имени каталога)")
	formsConvertFromCmd.Flags().String("form-kind", "custom", "тип формы: object|list|choice|folder|custom")
	formsConvertFromCmd.Flags().String("dst", "", "корень проекта OneBase (внутри будет создан forms/<entity>/)")
	mustMarkRequired(formsConvertFromCmd, "src")
	mustMarkRequired(formsConvertFromCmd, "entity")
	mustMarkRequired(formsConvertFromCmd, "dst")

	formsConvertToCmd.Flags().String("src", "", "путь к .form.yaml в проекте OneBase")
	formsConvertToCmd.Flags().String("dst", "", "целевой каталог Forms/<X>/Ext в выгрузке 1С")
	mustMarkRequired(formsConvertToCmd, "src")
	mustMarkRequired(formsConvertToCmd, "dst")

	formsValidateCmd.Flags().String("src", "", "путь к .form.yaml")
	mustMarkRequired(formsValidateCmd, "src")

	formsCmd.AddCommand(formsConvertFromCmd)
	formsCmd.AddCommand(formsConvertToCmd)
	formsCmd.AddCommand(formsValidateCmd)
	rootCmd.AddCommand(formsCmd)
}

func runFormsValidate(cmd *cobra.Command, _ []string) error {
	src, _ := cmd.Flags().GetString("src")
	warns, err := onec_forms.Validate(src)
	if err != nil {
		return err
	}
	totals := map[onec_forms.Severity]int{}
	for _, w := range warns {
		totals[w.Severity]++
	}
	outf("Проверка %s\n", src)
	outf("  Errors: %d, Warnings: %d, Info: %d\n",
		totals[onec_forms.SeverityError], totals[onec_forms.SeverityWarn], totals[onec_forms.SeverityInfo])
	for _, w := range warns {
		outf("  %s\n", w)
	}
	if totals[onec_forms.SeverityError] > 0 {
		// cobra превратит non-nil error в exit code 1.
		return fmt.Errorf("обнаружены ошибки в форме (%d)", totals[onec_forms.SeverityError])
	}
	outln("✓ Форма прошла валидацию.")
	return nil
}

func runFormsConvertTo(cmd *cobra.Command, _ []string) error {
	src, _ := cmd.Flags().GetString("src")
	dst, _ := cmd.Flags().GetString("dst")

	// Соседний .form.os и _resources рассчитываем по соглашению:
	//   <name>.form.yaml + <name>.form.os + <name>/_resources/...
	srcDir := filepath.Dir(src)
	base := strings.TrimSuffix(filepath.Base(src), ".form.yaml")
	osPath := filepath.Join(srcDir, base+".form.os")
	if _, err := os.Stat(osPath); err != nil {
		osPath = ""
	}
	resourcesDir := filepath.Join(srcDir, base, "_resources")
	if _, err := os.Stat(resourcesDir); err != nil {
		resourcesDir = ""
	}

	outf("Экспорт формы OneBase → 1С\n")
	outf("  YAML: %s\n", src)
	if osPath != "" {
		outf("  Модуль: %s\n", osPath)
	}
	if resourcesDir != "" {
		outf("  Ресурсы: %s\n", resourcesDir)
	}
	outf("  → %s\n", dst)

	report, err := onec_forms.ExportToOneC(onec_forms.ExportOptions{
		YAMLPath:     src,
		OSPath:       osPath,
		ResourcesDir: resourcesDir,
		DstFormDir:   dst,
	})
	if err != nil {
		return err
	}

	totals := map[onec_forms.Severity]int{}
	byCode := map[string]int{}
	for _, w := range report.Warnings {
		totals[w.Severity]++
		byCode[w.Code]++
	}
	outln("\nГотово.")
	outf("  Каталог: %s\n", report.FormDir)
	outf("\nПредупреждения: info=%d, warn=%d, error=%d\n",
		totals[onec_forms.SeverityInfo], totals[onec_forms.SeverityWarn], totals[onec_forms.SeverityError])
	for code, count := range byCode {
		outf("  %s: %d\n", code, count)
	}
	shown := 0
	for _, w := range report.Warnings {
		if w.Severity == onec_forms.SeverityInfo {
			continue
		}
		if shown >= 10 {
			outf("  ... (ещё %d, см. полный отчёт)\n", len(report.Warnings)-10)
			break
		}
		outf("  %s\n", w)
		shown++
	}
	return nil
}

func runFormsConvertFrom(cmd *cobra.Command, _ []string) error {
	src, _ := cmd.Flags().GetString("src")
	entity, _ := cmd.Flags().GetString("entity")
	formName, _ := cmd.Flags().GetString("form-name")
	formKind, _ := cmd.Flags().GetString("form-kind")
	dst, _ := cmd.Flags().GetString("dst")

	// Разрешаем пути в выгрузке 1С:
	//   <src>/Ext/Form.xml
	//   <src>/Ext/Form/Module.bsl
	//   <src>/Ext/Form/Items
	// Если src уже указывает на Ext/ — тоже сработает.
	srcBase := src
	xmlPath := filepath.Join(srcBase, "Ext", "Form.xml")
	if _, err := os.Stat(xmlPath); err != nil {
		// возможно пользователь указал прямо на Ext/
		alt := filepath.Join(srcBase, "Form.xml")
		if _, e2 := os.Stat(alt); e2 == nil {
			xmlPath = alt
		} else {
			return fmt.Errorf("файл Form.xml не найден ни в %s, ни в %s", xmlPath, alt)
		}
	}
	bslPath := filepath.Join(filepath.Dir(xmlPath), "Form", "Module.bsl")
	itemsDir := filepath.Join(filepath.Dir(xmlPath), "Form", "Items")

	// formName: если не указан — берём имя родителя каталога с Form.xml
	if formName == "" {
		// родитель xmlPath — это Ext/, его родитель — Forms/<FormName>/
		formName = filepath.Base(filepath.Dir(filepath.Dir(xmlPath)))
		if formName == "." || formName == string(filepath.Separator) {
			formName = "Форма"
		}
	}

	// Расположение результата:
	//   <dst>/forms/<entityLower>/<formNameLower>.form.yaml
	//   <dst>/forms/<entityLower>/<formNameLower>.form.os
	//   <dst>/forms/<entityLower>/<formNameLower>/_resources/...
	entityLower := strings.ToLower(entity)
	formLower := strings.ToLower(formName)
	formsRoot := filepath.Join(dst, "forms", entityLower)
	dstYAML := filepath.Join(formsRoot, formLower+".form.yaml")
	dstOS := filepath.Join(formsRoot, formLower+".form.os")
	dstResources := filepath.Join(formsRoot, formLower, "_resources")

	outf("Импорт формы 1С → OneBase\n")
	outf("  Form.xml: %s\n", xmlPath)
	outf("  Module.bsl: %s\n", bslPath)
	outf("  Items/: %s\n", itemsDir)
	outf("  → %s\n", dstYAML)

	report, err := onec_forms.ImportFromOneC(onec_forms.ImportOptions{
		XMLPath:         xmlPath,
		BSLPath:         bslPath,
		ItemsDir:        itemsDir,
		EntityName:      entity,
		FormName:        formName,
		FormKind:        formKind,
		DstYAMLPath:     dstYAML,
		DstOSPath:       dstOS,
		DstResourcesDir: dstResources,
	})
	if err != nil {
		return err
	}

	// Сводный отчёт по предупреждениям с группировкой по severity и коду.
	totals := map[onec_forms.Severity]int{}
	byCode := map[string]int{}
	for _, w := range report.Warnings {
		totals[w.Severity]++
		byCode[w.Code]++
	}

	outln("\nГотово.")
	if report.YAMLPath != "" {
		outf("  YAML: %s\n", report.YAMLPath)
	}
	if report.ModulePath != "" {
		outf("  Модуль: %s\n", report.ModulePath)
	}
	if report.ResourcesDir != "" {
		outf("  Ресурсы: %s\n", report.ResourcesDir)
	}
	outf("\nПредупреждения: info=%d, warn=%d, error=%d\n",
		totals[onec_forms.SeverityInfo], totals[onec_forms.SeverityWarn], totals[onec_forms.SeverityError])
	for code, count := range byCode {
		outf("  %s: %d\n", code, count)
	}

	// Первые 10 warn/error — наружу для контекста.
	shown := 0
	for _, w := range report.Warnings {
		if w.Severity == onec_forms.SeverityInfo {
			continue
		}
		if shown >= 10 {
			outf("  ... (ещё %d, см. полный отчёт)\n", len(report.Warnings)-10)
			break
		}
		outf("  %s\n", w)
		shown++
	}

	outf("\nПодсказка: откройте %s, ознакомьтесь с предупреждениями W040 в .form.os\n", report.YAMLPath)
	outf("и поправьте конструкции BSL, не имеющие аналогов в DSL OneBase.\n")
	return nil
}
