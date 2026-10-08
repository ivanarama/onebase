package cli

import (
	"os"
	"path/filepath"
	"runtime"
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

// TestAIGuideUniquenessClaimMatchesCode — сторож от молчаливого протухания
// (#1446, класс проблемы из #1201). До #1407 человеческое сообщение о дубле
// давал только обычный Upsert, и ai-guide честно оговаривал: «при правке
// существующего пока приходит текст драйвера про constraint». #1407 добавил
// ExplainUniqueViolation и на версионный путь записи, оговорка стала ложной, но
// текст никто не тронул — агент, который её прочитает, спорить не станет.
//
// Проверяются обе стороны утверждения: в поставляемом тексте оговорки нет, а в
// коде есть ровно то, что делает её ненужной. Если версионный путь однажды
// перестанет объяснять дубль, тест упадёт здесь — и станет видно, что вернуть
// надо не только код, но и оговорку в тексте.
func TestAIGuideUniquenessClaimMatchesCode(t *testing.T) {
	g := runAIGuideCommand(t)
	for _, stale := range []string{
		"текст драйвера про constraint",
		"текстом драйвера про constraint",
	} {
		if strings.Contains(g, stale) {
			t.Errorf("ai-guide снова обещает текст драйвера при правке: %q", stale)
		}
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller не сработал")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	versioned, err := os.ReadFile(filepath.Join(root, "internal", "storage", "optimistic_lock.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(versioned), "ExplainUniqueViolation") {
		t.Error("версионный путь записи больше не объясняет дубль: верните ExplainUniqueViolation " +
			"или верните оговорку в ai-guide и DEVELOPER.md")
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
