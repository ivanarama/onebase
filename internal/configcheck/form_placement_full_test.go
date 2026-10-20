package configcheck_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configcheck"
	"github.com/ivantit66/onebase/internal/project"
)

func fullPlacementProject(t *testing.T, kind, owner string, formPaths ...string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		kind + "/owner.yaml": fmt.Sprintf("name: %s\n", owner),
	}
	for _, rel := range formPaths {
		files["forms/"+rel] = fmt.Sprintf(`schema: onebase.form/v1
form:
  name: Главная
  kind: object
  entity: %s
`, owner)
	}
	for rel, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// RunFullWithOptions — публичный путь onebase check. Дополнительно проверяем,
// что отсутствие предупреждения относится к реально загруженной форме.
func TestRunFullFormPlacementLoadedDirectories(t *testing.T) {
	for _, tc := range []struct {
		kind, owner, folder string
	}{
		{"catalogs", "Инвентаризация", "Инвентаризация"},
		{"processors", "ЗагрузкаПрайса", "ЗагрузкаПрайса"},
		{"catalogs", "Sklad", "ſKLAD"},
		{"processors", "Sync", "ſYNC"},
	} {
		t.Run(tc.kind+"/"+tc.folder, func(t *testing.T) {
			rel := tc.folder + "/главная.form.yaml"
			dir := fullPlacementProject(t, tc.kind, tc.owner, rel)
			proj, err := project.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer proj.Close()
			if tc.kind == "catalogs" {
				if len(proj.Entities) != 1 || len(proj.Entities[0].Forms) != 1 || proj.Entities[0].Forms[0].SourcePath != "forms/"+rel {
					t.Fatalf("форма сущности не загружена из forms/%s", rel)
				}
			} else if len(proj.Processors) != 1 || len(proj.Processors[0].Forms) != 1 || proj.Processors[0].Forms[0].SourcePath != "forms/"+rel {
				t.Fatalf("форма обработки не загружена из forms/%s", rel)
			}

			res := configcheck.RunFullWithOptions(dir, configcheck.Options{})
			if !res.OK {
				t.Fatalf("check отклонил загруженную форму: %+v", res.Issues)
			}
			for _, warning := range res.Warnings {
				if warning.Code == "form.not-loaded" {
					t.Errorf("ложное предупреждение: %+v", warning)
				}
			}
		})
	}
}

// ToLower("I") == ToLower("İ"), но EqualFold различает эти имена.
// Оба владельца должны сохраниться при проверке реально загруженной формы.
func TestRunFullFormPlacementPreservesDistinctUnicodeOwners(t *testing.T) {
	dir := fullPlacementProject(t, "catalogs", "I", "i/главная.form.yaml")
	processorsDir := filepath.Join(dir, "processors")
	if err := os.MkdirAll(processorsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(processorsDir, "other.yaml"), []byte("name: İ\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer proj.Close()
	if len(proj.Entities) != 1 || proj.Entities[0].Name != "I" ||
		len(proj.Entities[0].Forms) != 1 || proj.Entities[0].Forms[0].SourcePath != "forms/i/главная.form.yaml" {
		t.Fatal("форма справочника I не загружена")
	}
	if len(proj.Processors) != 1 || proj.Processors[0].Name != "İ" || len(proj.Processors[0].Forms) != 0 {
		t.Fatal("обработка İ должна существовать без формы справочника I")
	}

	res := configcheck.RunFullWithOptions(dir, configcheck.Options{})
	if !res.OK {
		t.Fatalf("check отклонил загруженную форму: %+v", res.Issues)
	}
	for _, warning := range res.Warnings {
		if warning.Code == "form.not-loaded" {
			t.Errorf("ложное предупреждение при совпадающем ToLower разных владельцев: %+v", warning)
		}
	}
}

func placementWarnings(t *testing.T, dir string) []configcheck.Issue {
	t.Helper()
	res := configcheck.RunFullWithOptions(dir, configcheck.Options{})
	if !res.OK {
		t.Fatalf("check отклонил проект: %+v", res.Issues)
	}
	var warnings []configcheck.Issue
	for _, warning := range res.Warnings {
		if warning.Code == "form.not-loaded" {
			warnings = append(warnings, warning)
		}
	}
	return warnings
}

// Подсказанный каталог должен действительно загружаться тем же публичным путём.
func TestRunFullFormPlacementUnicodeSuggestionLoads(t *testing.T) {
	for _, kind := range []string{"catalogs", "processors"} {
		t.Run(kind, func(t *testing.T) {
			dir := fullPlacementProject(t, kind, "İ", "other/главная.form.yaml")
			warnings := placementWarnings(t, dir)
			if len(warnings) != 1 {
				t.Fatalf("ожидалось одно предупреждение: %+v", warnings)
			}
			const prefix = "Доступные каталоги: "
			_, suggestion, ok := strings.Cut(warnings[0].SuggestedFix, prefix)
			if !ok || suggestion != "İ." {
				t.Fatalf("подсказка потеряла имя владельца İ: %q", warnings[0].SuggestedFix)
			}
			// Прежняя подсказка i не совпадает с İ даже без учёта регистра.
			forms := filepath.Join(dir, "forms")
			if err := os.Rename(filepath.Join(forms, "other"), filepath.Join(forms, "i")); err != nil {
				t.Fatal(err)
			}
			if warnings := placementWarnings(t, dir); len(warnings) != 1 {
				t.Fatalf("каталог i не должен загружаться для İ: %+v", warnings)
			}
			folder := strings.TrimSuffix(suggestion, ".")
			if err := os.Rename(filepath.Join(forms, "i"), filepath.Join(forms, folder)); err != nil {
				t.Fatal(err)
			}
			if warnings := placementWarnings(t, dir); len(warnings) != 0 {
				t.Fatalf("подсказанный каталог не загружается: %+v", warnings)
			}
			proj, err := project.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer proj.Close()
			if kind == "catalogs" {
				if len(proj.Entities[0].Forms) != 1 {
					t.Fatal("подсказанная форма сущности не загружена")
				}
			} else if len(proj.Processors[0].Forms) != 1 {
				t.Fatal("подсказанная форма обработки не загружена")
			}
		})
	}
}

func TestRunFullFormPlacementSuggestionsPreserveDistinctUnicodeOwners(t *testing.T) {
	dir := fullPlacementProject(t, "catalogs", "I", "other/главная.form.yaml")
	processorsDir := filepath.Join(dir, "processors")
	if err := os.MkdirAll(processorsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(processorsDir, "other.yaml"), []byte("name: İ\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	warnings := placementWarnings(t, dir)
	if len(warnings) != 1 || !strings.HasSuffix(warnings[0].SuggestedFix, "Доступные каталоги: i, İ.") {
		t.Fatalf("подсказка объединила различных владельцев I и İ: %+v", warnings)
	}
}

func TestRunFullFormPlacementSuggestionsSortedAndLimited(t *testing.T) {
	dir := fullPlacementProject(t, "catalogs", "İ", "other/главная.form.yaml")
	for i, name := range []string{"Zulu", "Gamma", "Beta", "Alpha", "Theta", "Eta", "Delta", "Iota", "Kappa"} {
		path := filepath.Join(dir, "catalogs", fmt.Sprintf("owner%d.yaml", i))
		if err := os.WriteFile(path, []byte("name: "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const want = "Доступные каталоги: alpha, beta, delta, eta, gamma, iota, kappa, theta."
	for range 3 {
		warnings := placementWarnings(t, dir)
		if len(warnings) != 1 || !strings.HasSuffix(warnings[0].SuggestedFix, want) {
			t.Fatalf("ожидались восемь отсортированных примеров: %+v", warnings)
		}
	}
}

func TestRunFullFormPlacementUnloadedDirectories(t *testing.T) {
	for _, rel := range []string{
		"главная.form.yaml",
		"ЧужойКаталог/главная.form.yaml",
		"Инвентаризация/вложено/главная.form.yaml",
	} {
		t.Run(rel, func(t *testing.T) {
			dir := fullPlacementProject(t, "catalogs", "Инвентаризация", rel)
			res := configcheck.RunFullWithOptions(dir, configcheck.Options{})
			if !res.OK {
				t.Fatalf("предупреждение размещения не должно ронять check: %+v", res.Issues)
			}
			var placements []configcheck.Issue
			for _, warning := range res.Warnings {
				if warning.Code == "form.not-loaded" {
					placements = append(placements, warning)
				}
			}
			if len(placements) != 1 || placements[0].File != "forms/"+rel {
				t.Fatalf("ожидалось одно предупреждение для forms/%s: %+v", rel, placements)
			}
		})
	}
}

func TestRunFullFormPlacementRejectsAmbiguousDirectories(t *testing.T) {
	for _, kind := range []string{"catalogs", "processors"} {
		t.Run(kind, func(t *testing.T) {
			dir := fullPlacementProject(t, kind, "Инвентаризация",
				"Инвентаризация/главная.form.yaml", "иНВЕНТАРИЗАЦИЯ/главная.form.yaml")
			entries, err := os.ReadDir(filepath.Join(dir, "forms"))
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) < 2 {
				t.Skip("filesystem does not support case-distinct directory names")
			}
			res := configcheck.RunFullWithOptions(dir, configcheck.Options{})
			if res.OK {
				t.Fatal("check принял неоднозначные каталоги форм")
			}
			for _, issue := range res.Issues {
				if strings.Contains(issue.Message, "ambiguous forms directories") &&
					strings.Contains(issue.Message, "Инвентаризация") && strings.Contains(issue.Message, "иНВЕНТАРИЗАЦИЯ") {
					return
				}
			}
			t.Fatalf("ошибка не называет неоднозначные каталоги: %+v", res.Issues)
		})
	}
}
