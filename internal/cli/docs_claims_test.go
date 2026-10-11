package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/langref"
)

var languageCountsPattern = regexp.MustCompile(`([0-9]+) функций, ([0-9]+) метод(?:ов|а)`)
var fullLanguageCountsPattern = regexp.MustCompile(`([0-9]+) функций,\s+([0-9]+) метод(?:ов|а)\s+объектов,\s+([0-9]+) конструкций(?:\s+и|,)\s+([0-9]+) элементов языка запросов`)

func TestPublicDocsLanguageCountsUpToDate(t *testing.T) {
	counts := make(map[langref.Kind]int)
	for _, descriptor := range langref.All() {
		counts[descriptor.Kind]++
	}

	for _, tc := range []struct {
		path       string
		wantClaims int
	}{
		{path: "../../README.md", wantClaims: 1},
		{path: "../../QUICKSTART.md", wantClaims: 2},
	} {
		t.Run(tc.path, func(t *testing.T) {
			text := readDocForClaimTest(t, tc.path)
			for _, check := range []struct {
				name       string
				pattern    *regexp.Regexp
				kinds      []langref.Kind
				wantClaims int
			}{
				{"functions-methods", languageCountsPattern, []langref.Kind{langref.KindFunc, langref.KindMethod}, tc.wantClaims},
				{"all-kinds", fullLanguageCountsPattern, []langref.Kind{langref.KindFunc, langref.KindMethod, langref.KindKeyword, langref.KindQuery}, 1},
			} {
				t.Run(check.name, func(t *testing.T) {
					claims := check.pattern.FindAllStringSubmatch(text, -1)
					if len(claims) != check.wantClaims {
						t.Fatalf("найдено утверждений %s: %d, ожидалось %d; если формулировка изменилась, обновите проверку вместе с текстом", check.name, len(claims), check.wantClaims)
					}
					for _, claim := range claims {
						for i, kind := range check.kinds {
							got, err := strconv.Atoi(claim[i+1])
							if err != nil {
								t.Fatalf("разобрать число %s %q: %v", kind, claim[i+1], err)
							}
							if got != counts[kind] {
								t.Errorf("%s: указано %d, в реестре %d", kind, got, counts[kind])
							}
						}
					}
				})
			}
		})
	}
}

func TestReadmeInitAIGuideContract(t *testing.T) {
	readme := readDocForClaimTest(t, "../../README.md")
	const claim = "- **`onebase init` кладёт в проект `AGENTS.md`** — полное руководство, сгенерированное"
	if got := strings.Count(readme, claim); got != 1 {
		t.Fatalf("найдено утверждений о руководстве AGENTS.md: %d, ожидалось 1; если формулировка изменилась, обновите проверку вместе с текстом", got)
	}

	projectDir := filepath.Join(t.TempDir(), "проект")
	runInitInto(t, projectDir, "")
	guide := readDocForClaimTest(t, filepath.Join(projectDir, "AGENTS.md"))
	for _, section := range []string{
		"## Структура репозитория конфигурации",
		"## Рабочий цикл",
		"## Язык DSL",
		"## Схема метаданных",
		"## Безопасность",
	} {
		if !strings.Contains(guide, section) {
			t.Errorf("AGENTS.md из onebase init не содержит ключевой раздел %q", section)
		}
	}
}

func readDocForClaimTest(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("прочитать %s: %v", path, err)
	}
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}
