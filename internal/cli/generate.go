package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ivantit66/onebase/internal/gengen"
	"github.com/spf13/cobra"
)

var (
	genPrompt string
	genOutput string
	genDomain string
	genList   bool
	genAddons []string
	genMerge  bool
)

var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate a new project from a natural-language prompt",
	Long: `Создаёт новый проект OneBase по описанию на естественном языке.

Примеры:
  onebase generate --prompt "оптовые продажи, склад, контрагенты"
  onebase generate --prompt "учёт задач и проектов" --output ./my-tasks
  onebase generate --prompt "тексты и переводы" --domain texts
  onebase generate --prompt "добавить документ Сделка" --merge --output ./trade
  onebase generate --list   # показать доступные домены`,
	RunE: runGenerate,
}

func init() {
	generateCmd.Flags().StringVar(&genPrompt, "prompt", "", "описание проекта на естественном языке")
	generateCmd.Flags().StringVar(&genOutput, "output", "", "директория для вывода (по умолчанию = имя домена)")
	generateCmd.Flags().StringVar(&genDomain, "domain", "", "явно указать домен (trade, crm, tasks, ...)")
	generateCmd.Flags().StringSliceVar(&genAddons, "addon", nil, "дополнительные модули (через запятую)")
	generateCmd.Flags().BoolVar(&genList, "list", false, "показать доступные домены и выйти")
	generateCmd.Flags().BoolVar(&genMerge, "merge", false, "добавить сущности в существующий проект (не затирать)")
}

func runGenerate(cmd *cobra.Command, args []string) error {
	if genList {
		outln("Доступные домены:")
		domains := gengen.AvailableDomains()
		for name, keywords := range domains {
			outf("  %-14s %s\n", name, keywords[0]+", "+keywords[1])
		}
		return nil
	}

	if genPrompt == "" && genDomain == "" {
		return fmt.Errorf("укажите --prompt \"описание\" или --domain <имя>\n\nИспользуйте --list для просмотра доступных доменов")
	}

	// 1. Analyze
	var result *gengen.AnalyzeResult
	if genDomain != "" {
		// Direct domain override
		result = &gengen.AnalyzeResult{
			Domain:    genDomain,
			Confident: true,
		}
	} else {
		result = gengen.Analyze(genPrompt)
	}

	if result.Domain == "unknown" {
		fmt.Fprintf(os.Stderr, "⚠ Домен не определён по промпту.\n\n")
		fmt.Fprintf(os.Stderr, "Попробуйте:\n")
		fmt.Fprintf(os.Stderr, "  1. Указать домен явно: --domain trade\n")
		fmt.Fprintf(os.Stderr, "  2. Использовать другие ключевые слова\n\n")
		fmt.Fprintf(os.Stderr, "Доступные домены:\n")
		for name := range gengen.AvailableDomains() {
			fmt.Fprintf(os.Stderr, "  %s\n", name)
		}
		return fmt.Errorf("domain detection failed")
	}

	if !result.Confident {
		fmt.Fprintf(os.Stderr, "⚠ Амбигуозный результат, возможны варианты: %v\n", result.Ambiguous)
		fmt.Fprintf(os.Stderr, "  Выбран: %s (уточните --domain для явного выбора)\n\n", result.Domain)
	}

	// 2. Resolve template
	if result.Template == "" {
		// Try to find template by domain name
		candidates := []string{
			"examples/" + result.Domain,
			"templates/" + result.Domain,
		}
		for _, t := range candidates {
			if dirExists(t) {
				result.Template = t
				break
			}
		}
	}

	if result.Template == "" {
		return fmt.Errorf("нет шаблона для домена %q (искали: examples/%s, templates/%s)",
			result.Domain, result.Domain, result.Domain)
	}

	// 3. Determine output directory
	outDir := genOutput
	if outDir == "" {
		outDir = gengen.SanitizePrompt(genPrompt)
		if outDir == "" {
			outDir = result.Domain
		}
	}

	// 4. Generate (with or without merge)
	if genMerge {
		return runGenerateMerge(outDir, result)
	}

	// Normal: create new project
	gen := &gengen.Generator{OutputDir: outDir}
	if err := gen.Generate(result.Template, genAddons); err != nil {
		return err
	}

	// 5. Success
	outf("✓ Проект сгенерирован в %s\n", outDir)
	outf("  Домен: %s\n", result.Domain)
	outf("  Шаблон: %s\n", result.Template)
	if len(genAddons) > 0 {
		outf("  Аддоны: %v\n", genAddons)
	}
	outf("\nЗапуск:\n")
	absPath, _ := filepath.Abs(outDir)
	outf("  onebase run --project %s --sqlite %s.db\n", absPath, result.Domain)

	return nil
}

// runGenerateMerge adds entities from a template to an existing project.
func runGenerateMerge(outDir string, result *gengen.AnalyzeResult) error {
	// Check that the project directory exists
	if !dirExists(outDir) {
		return fmt.Errorf("проект %q не найден — нельзя добавить в несуществующий проект\nСоздайте проект: onebase generate --prompt \"...\" --output %s", outDir, outDir)
	}

	// 1. Scan existing project
	existing, err := gengen.ScanProjectFromFiles(outDir)
	if err != nil {
		return fmt.Errorf("сканирование проекта: %w", err)
	}

	// 2. Generate (copyDir skips existing files — merge-safe by design)
	gen := &gengen.Generator{OutputDir: outDir}
	if err := gen.Generate(result.Template, genAddons); err != nil {
		return err
	}

	// 3. Scan again to see what was added
	after, err := gengen.ScanProjectFromFiles(outDir)
	if err != nil {
		return fmt.Errorf("сканирование после генерации: %w", err)
	}

	// 4. Build a ResolvedManifest from the "after" state and compute delta
	//    This is a simplified approach — we compare before vs after
	//    to show what was actually added.
	reportMergeDiff(existing, after, result, outDir)

	return nil
}

// reportMergeDiff compares two manifests and prints what was added.
func reportMergeDiff(before, after *gengen.ExistingManifest, result *gengen.AnalyzeResult, outDir string) {
	outf("✓ Сущности добавлены в %s\n", result.Domain)
	outf("  Домен: %s\n\n", result.Domain)

	// New catalogs
	for name := range after.Catalogs {
		if _, ok := before.Catalogs[name]; !ok {
			outf("  + Справочник: %s\n", name)
		}
	}

	// New documents
	for name := range after.Documents {
		if _, ok := before.Documents[name]; !ok {
			outf("  + Документ: %s\n", name)
		}
	}

	// New enums
	for name := range after.Enums {
		if _, ok := before.Enums[name]; !ok {
			outf("  + Перечисление: %s\n", name)
		}
	}

	// New DSL files
	for path := range after.DSLFiles {
		if _, ok := before.DSLFiles[path]; !ok {
			outf("  + DSL: %s\n", path)
		}
	}

	// New fields in existing entities
	for catName, afterCat := range after.Catalogs {
		beforeCat, ok := before.Catalogs[catName]
		if !ok {
			continue
		}
		beforeFields := make(map[string]bool)
		for _, f := range beforeCat.Fields {
			beforeFields[f.Name] = true
		}
		for _, f := range afterCat.Fields {
			if !beforeFields[f.Name] {
				outf("  + Поле %s → %s\n", catName, f.Name)
			}
		}
	}

	for docName, afterDoc := range after.Documents {
		beforeDoc, ok := before.Documents[docName]
		if !ok {
			continue
		}
		beforeFields := make(map[string]bool)
		for _, f := range beforeDoc.Fields {
			beforeFields[f.Name] = true
		}
		for _, f := range afterDoc.Fields {
			if !beforeFields[f.Name] {
				outf("  + Поле %s → %s\n", docName, f.Name)
			}
		}
	}

	outf("\nЗапуск:\n")
	absPath, _ := filepath.Abs(outDir)
	outf("  onebase run --project %s\n", absPath)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
