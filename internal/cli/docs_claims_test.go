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

var languageCountsPattern = regexp.MustCompile(`([0-9]+) функций, ([0-9]+) методов`)

func TestPublicDocsLanguageCountsUpToDate(t *testing.T) {
	functions, methods := 0, 0
	for _, descriptor := range langref.All() {
		switch descriptor.Kind {
		case langref.KindFunc:
			functions++
		case langref.KindMethod:
			methods++
		}
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
			claims := languageCountsPattern.FindAllStringSubmatch(text, -1)
			if len(claims) != tc.wantClaims {
				t.Fatalf("найдено утверждений вида «N функций, M методов»: %d, ожидалось %d; если формулировка изменилась, обновите проверку вместе с текстом", len(claims), tc.wantClaims)
			}
			for _, claim := range claims {
				gotFunctions, err := strconv.Atoi(claim[1])
				if err != nil {
					t.Fatalf("разобрать число функций %q: %v", claim[1], err)
				}
				gotMethods, err := strconv.Atoi(claim[2])
				if err != nil {
					t.Fatalf("разобрать число методов %q: %v", claim[2], err)
				}
				if gotFunctions != functions || gotMethods != methods {
					t.Errorf("указано %d функций и %d методов, в реестре %d и %d", gotFunctions, gotMethods, functions, methods)
				}
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
