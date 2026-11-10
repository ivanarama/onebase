package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/gherkin"
	"github.com/spf13/cobra"
)

// Команда `onebase gherkin` — срез C плана 218: тестовые сценарии Gherkin
// компилируются в тест-обработки (kind: test), которые гоняет `onebase test`.

var gherkinCmd = &cobra.Command{
	Use:           "gherkin",
	Short:         "Gherkin → тест-обработки (kind: test)",
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
}

var gherkinCompileCmd = &cobra.Command{
	Use:   "compile",
	Short: "Скомпилировать .feature в тест-обработки",
	Long: `Собирает тест-обработки (kind: test) из файлов Gherkin и реестра шагов проекта.
Реестр шагов (YAML со списком «шаги») связывает текст шага с DSL: шаг «действие»
выполняет код, шаг «проверка» вычисляет значение и даёт проверку Утверждать.*.
Компиляция отказывается, если хоть один шаг не сопоставлен с реестром или
сценарий не содержит проверок: половина теста записанного компилятора нет.

Каждый сценарий и каждая строка Структуры сценария дают свою обработку
(имя Тест<Функция>_<Сценарий>): пара processors/<имя>.yaml + src/<имя>.proc.os.
Шаг-таблица печатается в модуле комментарием, правка руками рвёт связь с .feature.

Реестр шагов:
  шаги:
    - шаг: "заведена функция с кодом «<Код>»"
      тип: действие
      код: |
        Ф = Справочники.Функция.Создать()
        Ф.Код = <Код>
    - шаг: "функция с кодом «<Код>» заведена"
      тип: проверка
      выражение: ФункцияСсылка
      проверка: Заполнено

Примеры:
  onebase gherkin compile --project examples/sppr
  onebase gherkin compile --project examples/sppr --dry-run
  onebase gherkin compile --project examples/sppr --src features --steps features/шаги.yaml
  onebase test --project examples/sppr --sqlite :memory: --run Тест`,
	RunE: runGherkinCompile,
}

func init() {
	gherkinCompileCmd.Flags().String("project", ".", "путь к каталогу конфигурации")
	gherkinCompileCmd.Flags().String("src", "features", "каталог или файл .feature (относительно проекта)")
	gherkinCompileCmd.Flags().String("steps", "features/шаги.yaml", "реестр шагов (относительно проекта)")
	gherkinCompileCmd.Flags().Bool("dry-run", false, "напечатать компиляцию, ничего не записывать")
	gherkinCmd.AddCommand(gherkinCompileCmd)
	rootCmd.AddCommand(gherkinCmd)
}

func runGherkinCompile(cmd *cobra.Command, _ []string) error {
	projectDir, _ := cmd.Flags().GetString("project")
	srcFlag, _ := cmd.Flags().GetString("src")
	stepsFlag, _ := cmd.Flags().GetString("steps")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	stepsPath := resolveInProject(projectDir, stepsFlag)
	reg, err := gherkin.LoadRegistry(stepsPath)
	if err != nil {
		return err
	}

	srcPath := resolveInProject(projectDir, srcFlag)
	files, err := collectFeatures(srcPath)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("в %s нет .feature-файлов — заведите каталог features/ и сценарии", srcPath)
	}

	var all []gherkin.CompiledTest
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("прочитать %s: %w", path, err)
		}
		feat, err := gherkin.Parse(data, relPath(projectDir, path))
		if err != nil {
			return err
		}
		tests, err := gherkin.Compile(feat, reg, relPath(projectDir, path))
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s: сценариев %d, тестов %d\n", relPath(projectDir, path),
			len(feat.Scenarios)+len(feat.Outlines), len(tests))
		all = append(all, tests...)
	}

	if dryRun {
		for _, t := range all {
			fmt.Fprintf(cmd.OutOrStdout(), "\n--- processors/%s.yaml ---\n%s", strings.ToLower(t.Name), t.YAML)
			fmt.Fprintf(cmd.OutOrStdout(), "\n--- src/%s.proc.os ---\n%s", strings.ToLower(t.Name), t.Source)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "\nТестов: %d (dry-run, ничего не записано).\n", len(all))
		return nil
	}

	written := 0
	for _, t := range all {
		base := strings.ToLower(t.Name)
		yamlPath := filepath.Join(projectDir, "processors", base+".yaml")
		srcProc := filepath.Join(projectDir, "src", base+".proc.os")
		if err := os.MkdirAll(filepath.Dir(yamlPath), 0o755); err != nil {
			return fmt.Errorf("каталог processors: %w", err)
		}
		if err := os.WriteFile(yamlPath, []byte(t.YAML), 0o644); err != nil {
			return fmt.Errorf("записать %s: %w", yamlPath, err)
		}
		if err := os.WriteFile(srcProc, []byte(t.Source), 0o644); err != nil {
			return fmt.Errorf("записать %s: %w", srcProc, err)
		}
		written++
		fmt.Fprintf(cmd.OutOrStdout(), "  %s → processors/%s.yaml + src/%s.proc.os\n", t.Title, base, base)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Тестов: %d, файлов записано %d.\n", len(all), written*2)
	fmt.Fprintln(cmd.OutOrStdout(), "Прогнать: onebase test --project <проект> --sqlite :memory:")
	return nil
}

// resolveInProject: путь относительно каталога конфигурации; существующий путь
// относительно cwd подхватывается, чтобы `--src ./features` работал из проекта.
func resolveInProject(projectDir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	cand := filepath.Join(projectDir, p)
	if _, err := os.Stat(cand); err == nil {
		return cand
	}
	return p
}

func collectFeatures(srcPath string) ([]string, error) {
	st, err := os.Stat(srcPath)
	if err != nil {
		return nil, fmt.Errorf("источник сценариев %s: %w", srcPath, err)
	}
	if !st.IsDir() {
		if !strings.HasSuffix(strings.ToLower(st.Name()), ".feature") {
			return nil, fmt.Errorf("%s: не .feature-файл", srcPath)
		}
		return []string{srcPath}, nil
	}
	var files []string
	err = filepath.WalkDir(srcPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".feature") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("обход %s: %w", srcPath, err)
	}
	sort.Strings(files)
	return files, nil
}

func relPath(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return rel
	}
	return path
}
