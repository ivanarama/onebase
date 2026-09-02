package cli

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/langref"
)

var (
	languageCountsPattern = regexp.MustCompile(`([0-9]+) функций, ([0-9]+) методов`)
	aiGuideLinesPattern   = regexp.MustCompile("(?m)^- \\*\\*.*`AGENTS\\.md`\\*\\* — ([0-9]+) строк(?:а|и)?(?:,|$)")
)

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

func TestReadmeAIGuideLineCountUpToDate(t *testing.T) {
	readme := readDocForClaimTest(t, "../../README.md")
	claims := aiGuideLinesPattern.FindAllStringSubmatch(readme, -1)
	if len(claims) != 1 {
		t.Fatalf("найдено утверждений о числе строк AGENTS.md: %d, ожидалось 1; если формулировка изменилась, обновите проверку вместе с текстом", len(claims))
	}

	got, err := strconv.Atoi(claims[0][1])
	if err != nil {
		t.Fatalf("разобрать число строк AGENTS.md %q: %v", claims[0][1], err)
	}
	want := lineCount(generateAIGuide(""))
	if got != want {
		t.Errorf("README.md обещает %d строк в AGENTS.md, генератор выдаёт %d", got, want)
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

func lineCount(text string) int {
	if text == "" {
		return 0
	}
	lines := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		lines++
	}
	return lines
}
