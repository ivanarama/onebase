package configcheck

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/project"
)

// CheckFormPlacement возвращает НЕблокирующие предупреждения о файлах управляемых
// форм, которые платформа не загрузит из-за размещения.
//
// Загрузчик (internal/dsl/loader/managed_form_loader.go) ищет формы в
// forms/<имя-сущности>/*.form.yaml без учёта регистра каталога и возвращает пусто,
// если каталога нет. Поэтому файл, положенный плоско в forms/ или в каталог с
// именем, не совпадающим ни с одной сущностью, становится мёртвой конфигурацией:
// он существует, читается человеком как рабочий, проходит `onebase check` — а в
// браузере сущность открывается авто-генерируемой формой.
//
// Именно так в поставке оказались 15 из 24 файлов форм (весь examples/trade и
// весь examples/finance), и это годами скрывало дефекты рендера управляемых форм.
func CheckFormPlacement(dir string, proj *project.Project) []Issue {
	formsRoot := filepath.Join(dir, "forms")
	info, err := os.Stat(formsRoot)
	if err != nil || !info.IsDir() {
		return nil
	}

	// Каталоги, которые загрузчик реально просматривает: по одному на сущность
	// И на обработку — у обработок тоже бывают управляемые формы
	// (ui.handleProcessorFormEvent, шаблон с forms/<имя обработки>/).
	// Исходные имена нельзя объединять через ToLower: I и İ дают один
	// ключ, хотя EqualFold считает их разными владельцами.
	known := make(map[string]struct{})
	for _, ent := range proj.Entities {
		if ent == nil || ent.Name == "" {
			continue
		}
		known[ent.Name] = struct{}{}
	}
	for _, pr := range proj.Processors {
		if pr == nil || pr.Name == "" {
			continue
		}
		known[pr.Name] = struct{}{}
	}

	var warns []Issue
	_ = filepath.WalkDir(formsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".form.yaml") {
			return nil
		}
		rel, relErr := filepath.Rel(formsRoot, path)
		if relErr != nil {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		label := filepath.ToSlash(filepath.Join("forms", rel))

		switch {
		case len(parts) == 1:
			warns = append(warns, Issue{
				File: label, Kind: "Управляемая форма", Code: "form.not-loaded",
				Message: fmt.Sprintf("файл %q лежит прямо в forms/ и НЕ загружается: "+
					"формы читаются только из forms/<имя-сущности>/", label),
				SuggestedFix: "Перенесите файл в forms/<имя-сущности>/ " +
					"(например forms/реализациятоваров/объекта.form.yaml). Сейчас сущность " +
					"открывается авто-генерируемой формой, а этот файл не влияет ни на что.",
			})
		case len(parts) > 2:
			warns = append(warns, Issue{
				File: label, Kind: "Управляемая форма", Code: "form.not-loaded",
				Message: fmt.Sprintf("файл %q лежит во вложенном каталоге и НЕ загружается: "+
					"просматривается только forms/<сущность>/ на один уровень", label),
				SuggestedFix: "Положите файл непосредственно в forms/<имя-сущности>/.",
			})
		default:
			// То же Unicode-сравнение, что у managed-загрузчика. ToLower
			// недостаточно: например, EqualFold("ſ", "S") == true.
			matched := false
			for name := range known {
				if strings.EqualFold(parts[0], name) {
					matched = true
					break
				}
			}
			// Неоднозначные каталоги отклоняет загрузчик в project.Load,
			// до запуска проверок размещения в RunFullWithOptions.
			if !matched {
				warns = append(warns, Issue{
					File: label, Kind: "Управляемая форма", Code: "form.not-loaded",
					Message: fmt.Sprintf("каталог forms/%s/ не соответствует ни одной сущности "+
						"конфигурации — файл %q НЕ загружается", parts[0], label),
					SuggestedFix: "Имя каталога должно совпадать с именем сущности или обработки без " +
						"учёта регистра — это ИМЯ из YAML (поле name), а не имя файла. " +
						suggestFormDirectories(known),
				})
			}
		}
		return nil
	})

	sort.Slice(warns, func(i, j int) bool { return warns[i].File < warns[j].File })
	return warns
}

// suggestFormDirectories перечисляет примеры имён каталогов в стабильном порядке.
func suggestFormDirectories(known map[string]struct{}) string {
	// Нижний регистр нужен только для примеров, не для сопоставления владельцев.
	examples := make(map[string]struct{})
	for name := range known {
		examples[strings.ToLower(name)] = struct{}{}
	}
	var names []string
	for name := range examples {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 8 {
		names = names[:8]
	}
	if len(names) == 0 {
		return ""
	}
	return "Доступные каталоги: " + strings.Join(names, ", ") + "."
}
