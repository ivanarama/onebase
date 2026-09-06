package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateAIGuide_HasSignaturesAndSections(t *testing.T) {
	g := generateAIGuide("")
	for _, want := range []string{
		"## Язык DSL",
		"### Методы объектов",
		"### Язык запросов",
		"СтрЗаменить(",     // сигнатура функции
		"Запрос.Выполнить", // метод объекта
		// секция-протокол «Проверка результата» (план 91)
		"## Проверка результата",
		"procrun",
		"UI не проверен headless",
	} {
		if !strings.Contains(g, want) {
			t.Errorf("в guide нет ожидаемого фрагмента: %q", want)
		}
	}
	if strings.Contains(g, "Сигнатуры смотрите в примерах") {
		t.Error("guide всё ещё содержит устаревший дисклеймер о сигнатурах")
	}
	// По умолчанию (без проекта) руководство портируемо — без блока «Окружение».
	if strings.Contains(g, "## Окружение") {
		t.Error("guide без проекта не должен содержать discovered-блок «Окружение»")
	}
}

// TestAIGuideCommand_HasIndexesUniqueness — публичная команда печатает механизм
// уникальности по произвольному реквизиту (#1177). Без этого абзаца агент,
// пишущий конфигурацию, про `indexes:` не знает и предлагает проверку дубля в
// модуле, которая при одновременной записи двумя пользователями не срабатывает.
func TestAIGuideCommand_HasIndexesUniqueness(t *testing.T) {
	g := runAIGuideCommand(t)
	for _, want := range []string{
		"indexes:",             // сам ключ
		"unique: true",         // как включается уникальность
		"fields: [Сайт, Слаг]", // составной индекс = уникальность пары
		"ПриЗаписи",            // оговорка: проверка в модуле уникальности не даёт
		"уже занято другой записью", // текст отказа, по которому его узнают
	} {
		if !strings.Contains(g, want) {
			t.Errorf("в guide нет ожидаемого фрагмента про indexes: %q", want)
		}
	}
}

// runAIGuideCommand проходит настоящий пользовательский путь: argv → cobra →
// runAIGuide → файл, созданный публичным флагом --output.
func runAIGuideCommand(t *testing.T) string {
	t.Helper()
	outPath := filepath.Join(t.TempDir(), "AGENTS.md")
	rootCmd.SetArgs([]string{"ai-guide", "--output", outPath})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		for _, name := range []string{"output", "project", "claude"} {
			flag := aiguideCmd.Flags().Lookup(name)
			if flag == nil {
				t.Fatalf("флаг ai-guide --%s не зарегистрирован", name)
			}
			if err := flag.Value.Set(flag.DefValue); err != nil {
				t.Fatalf("сбросить флаг ai-guide --%s: %v", name, err)
			}
			flag.Changed = false
		}
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("`onebase ai-guide --output`: %v", err)
	}
	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("прочитать вывод `onebase ai-guide`: %v", err)
	}
	return string(raw)
}

// TestEnvironmentNote — discovered-блок «Окружение» (план 91): пуст без проекта
// (портируемость по умолчанию), с путём и ОС-заметкой при заданном каталоге.
func TestEnvironmentNote(t *testing.T) {
	if got := environmentNote(""); got != "" {
		t.Errorf("environmentNote(\"\") должно быть пустым для портируемости, получено: %q", got)
	}
	note := environmentNote(".")
	for _, want := range []string{"## Окружение", "- Проект:", "- ОС:"} {
		if !strings.Contains(note, want) {
			t.Errorf("environmentNote(dir) не содержит %q", want)
		}
	}
	// Блок встраивается в руководство при генерации с проектом.
	if !strings.Contains(generateAIGuide(note), "## Окружение") {
		t.Error("generateAIGuide(envBlock) должно включать блок «Окружение»")
	}
}
