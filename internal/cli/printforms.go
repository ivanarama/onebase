package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/fsmode"
	"github.com/ivantit66/onebase/internal/printform"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/xlsximport"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// printformsCmd — родительская команда для работы с печатными формами.
var printformsCmd = &cobra.Command{
	Use:   "printforms",
	Short: "Печатные формы OneBase (миграция legacy YAML → макет v2)",
	Long: `Подкоманды для работы с печатными формами OneBase.

OneBase v2 описывает печатную форму декларативным макетом (.layout.yaml):
именованные области ячеек + binding к данным документа. Команда migrate
конвертирует устаревший плоский YAML-формат (title/header/table/footer)
в макет v2.`,
}

var printformsMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Конвертировать legacy YAML печатные формы в макет v2 (.layout.yaml)",
	Long: `Для каждого printforms/*.yaml (устаревший формат) выполняет конвертацию в
макет v2 и пишет рядом <имя>.layout.yaml. Старый .yaml по умолчанию удаляется
(сохранить — флаг --keep). Файлы .os и существующие .layout.yaml не трогаются.

Примеры:
  onebase printforms migrate --project examples/trade
  onebase printforms migrate --project examples/accounting --keep`,
	RunE:          runPrintformsMigrate,
	SilenceUsage:  true,
	SilenceErrors: true,
}

var printformsImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Собрать макет печатной формы из бланка Excel (.xlsx)",
	Long: `Импортирует бланк .xlsx с тегами полей в макет printforms/<имя>.layout.yaml.

Бланк рисуется в Excel как обычно (объединения, ширины колонок, границы, поля
листа), в ячейки пишутся те же теги, что понимает макет:

  {{Номер}}   {{Дата | date}}   {{Контрагент.Наименование}}   {{Константы.Организация}}

Строка табличной части помечается тегами с именем ТЧ — она размножится по
строкам документа, а приставка внутри области снимается:

  {{@row}}   {{Товары.Номенклатура}}   {{Товары.Цена | number:2}}
  {{Итог.Товары.Сумма | number:2}}

Разбиение на области берётся из Диспетчера имён Excel, если имена заданы;
иначе — Шапка / Строка / Подвал автоматически.

Примеры:
  onebase printforms import --project examples/trade --file бланк.xlsx --name ТоварнаяНакладная --document Реализация
  onebase printforms import --project . --file бланк.xlsx --name Счёт --document СчётНаОплату --sheet "Бланк"`,
	RunE:          runPrintformsImport,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	printformsMigrateCmd.Flags().String("project", ".", "путь к каталогу конфигурации")
	printformsMigrateCmd.Flags().Bool("keep", false, "сохранить исходные .yaml (по умолчанию удаляются)")
	printformsCmd.AddCommand(printformsMigrateCmd)

	printformsImportCmd.Flags().String("project", ".", "путь к каталогу конфигурации")
	printformsImportCmd.Flags().String("file", "", "бланк .xlsx (обязательно)")
	printformsImportCmd.Flags().String("name", "", "имя печатной формы (обязательно)")
	printformsImportCmd.Flags().String("document", "", "документ/справочник, к которому привязывается форма (обязательно)")
	printformsImportCmd.Flags().String("sheet", "", "лист книги (по умолчанию первый)")
	printformsImportCmd.Flags().Bool("force", false, "перезаписать существующий макет")
	printformsCmd.AddCommand(printformsImportCmd)

	rootCmd.AddCommand(printformsCmd)
}

// runPrintformsImport — «onebase printforms import»: бланк Excel → макет v2.
func runPrintformsImport(cmd *cobra.Command, _ []string) error {
	dir, _ := cmd.Flags().GetString("project")
	src, _ := cmd.Flags().GetString("file")
	name, _ := cmd.Flags().GetString("name")
	document, _ := cmd.Flags().GetString("document")
	sheetName, _ := cmd.Flags().GetString("sheet")
	force, _ := cmd.Flags().GetBool("force")

	for flag, val := range map[string]string{"file": src, "name": name, "document": document} {
		if strings.TrimSpace(val) == "" {
			return fmt.Errorf("printforms import: не задан --%s", flag)
		}
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return fmt.Errorf("printforms import: недопустимое имя формы %q", name)
	}

	data, err := os.ReadFile(src) //nolint:gosec // G304: путь к бланку задаёт оператор команды
	if err != nil {
		return fmt.Errorf("printforms import: чтение %s: %w", src, err)
	}

	// Состав табличных частей — из метаданных: без него {{Товары.Цена}} не
	// отличить от {{Склад.Наименование}}, и строки таблицы не размножатся.
	tps, tpErr := documentTableParts(dir, document)
	if tpErr != nil {
		outf("Предупреждение: %v — строки табличной части не размножатся.\n", tpErr)
	}

	res, err := xlsximport.ImportBytes(data, xlsximport.Options{Sheet: sheetName, TableParts: tps})
	if err != nil {
		return fmt.Errorf("printforms import: %w", err)
	}
	res.Layout.Name = name
	res.Layout.Document = document

	out, err := yaml.Marshal(res.Layout)
	if err != nil {
		return fmt.Errorf("printforms import: сериализация: %w", err)
	}

	dstDir := filepath.Join(dir, "printforms")
	if err := os.MkdirAll(dstDir, fsmode.Dir); err != nil {
		return fmt.Errorf("printforms import: %w", err)
	}
	dst := filepath.Join(dstDir, name+".layout.yaml")
	if _, err := os.Stat(dst); err == nil && !force {
		return fmt.Errorf("printforms import: %s уже существует (перезаписать — флаг --force)", dst)
	}
	if err := os.WriteFile(dst, out, fsmode.File); err != nil {
		return fmt.Errorf("printforms import: запись %s: %w", dst, err)
	}

	outf("Макет создан: %s\n", dst)
	outf("  областей: %d", len(res.Layout.Areas))
	if b := res.Layout.Binding; b != nil && len(b.Repeat) > 0 {
		outf(", строк табличной части: %s", b.Repeat[0].Source)
	}
	outln("")
	if len(res.Warnings) > 0 {
		outln("\nПеренесено не всё:")
		for _, note := range res.Warnings {
			outf("  • %s\n", note)
		}
	}
	return nil
}

// documentTableParts возвращает имена табличных частей документа конфигурации.
func documentTableParts(dir, document string) ([]string, error) {
	proj, err := project.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("конфигурация %s не загрузилась: %w", dir, err)
	}
	for _, ent := range proj.Entities {
		if !strings.EqualFold(ent.Name, document) {
			continue
		}
		names := make([]string, 0, len(ent.TableParts))
		for _, tp := range ent.TableParts {
			names = append(names, tp.Name)
		}
		return names, nil
	}
	return nil, fmt.Errorf("документ/справочник %q в конфигурации не найден", document)
}

func runPrintformsMigrate(cmd *cobra.Command, _ []string) error {
	dir, _ := cmd.Flags().GetString("project")
	keep, _ := cmd.Flags().GetBool("keep")

	converted, errs := migrateLegacyPrintForms(dir, keep)
	if len(converted) == 0 && len(errs) == 0 {
		outln("Устаревших печатных форм (.yaml) не найдено — конвертировать нечего.")
		return nil
	}
	if len(converted) > 0 {
		outf("Конвертировано форм: %d\n", len(converted))
		for _, c := range converted {
			outf("  %s → %s\n", c.From, c.To)
		}
	}
	if keep && len(converted) > 0 {
		outln("\nИсходные .yaml сохранены (--keep). ВНИМАНИЕ: и .yaml, и .layout.yaml")
		outln("одной формы одновременно приведут к коллизии — удалите .yaml вручную.")
	}
	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "\nОшибки конвертации (%d):\n", len(errs))
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  %s\n", e)
		}
		return fmt.Errorf("printforms migrate: %d файл(ов) не удалось сконвертировать", len(errs))
	}
	return nil
}

// migrateResult описывает одну конвертированную форму (для вывода).
type migrateResult struct {
	From string
	To   string
}

// migrateError описывает ошибку конвертации одного файла.
type migrateError struct {
	File string
	Err  error
}

func (e migrateError) Error() string {
	return fmt.Sprintf("%s: %v", e.File, e.Err)
}

// migrateLegacyPrintForms конвертирует все устаревшие printforms/*.yaml каталога
// projectDir в макеты v2 (.layout.yaml). keep=false удаляет исходные .yaml.
// Возвращает список конвертированных форм и срез ошибок по файлам. При ошибке
// отдельного файла функция продолжает обработку остальных (fail-continues):
// успешно сконвертированные формы остаются; повторный запуск доделает остальные
// после устранения причин ошибок. Отсутствие каталога printforms — не ошибка
// (пустой результат). Файлы *.layout.yaml и *.os пропускаются.
func migrateLegacyPrintForms(projectDir string, keep bool) ([]migrateResult, []migrateError) {
	pfDir := filepath.Join(projectDir, "printforms")
	entries, err := os.ReadDir(pfDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, []migrateError{{File: pfDir, Err: fmt.Errorf("чтение каталога: %w", err)}}
	}

	var out []migrateResult
	var errs []migrateError
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Только плоские legacy *.yaml: не *.layout.yaml, не *.os.
		if !strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".layout.yaml") {
			continue
		}
		srcPath := filepath.Join(pfDir, name)
		pf, err := printform.LoadFile(srcPath)
		if err != nil {
			errs = append(errs, migrateError{File: name, Err: err})
			continue
		}
		lt, err := printform.ConvertLegacy(pf)
		if err != nil {
			errs = append(errs, migrateError{File: name, Err: fmt.Errorf("конвертация: %w", err)})
			continue
		}
		data, err := yaml.Marshal(lt)
		if err != nil {
			errs = append(errs, migrateError{File: name, Err: fmt.Errorf("сериализация: %w", err)})
			continue
		}
		base := strings.TrimSuffix(name, ".yaml")
		dstPath := filepath.Join(pfDir, base+".layout.yaml")
		if err := os.WriteFile(dstPath, data, fsmode.File); err != nil {
			errs = append(errs, migrateError{File: name, Err: fmt.Errorf("запись %s: %w", dstPath, err)})
			continue
		}
		if !keep {
			if err := os.Remove(srcPath); err != nil {
				errs = append(errs, migrateError{File: name, Err: fmt.Errorf("удаление: %w", err)})
				continue
			}
		}
		out = append(out, migrateResult{From: name, To: base + ".layout.yaml"})
	}
	return out, errs
}
