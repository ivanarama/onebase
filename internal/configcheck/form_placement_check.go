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
// Загрузчик (internal/dsl/loader/managed_form_loader.go) ищет формы строго в
// forms/<имя-сущности-в-нижнем-регистре>/*.form.yaml и молча возвращает пусто,
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
	known := make(map[string]string) // нижний регистр → оригинальное имя
	for _, ent := range proj.Entities {
		if ent == nil || ent.Name == "" {
			continue
		}
		known[strings.ToLower(ent.Name)] = ent.Name
	}
	for _, pr := range proj.Processors {
		if pr == nil || pr.Name == "" {
			continue
		}
		known[strings.ToLower(pr.Name)] = pr.Name
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
					"формы читаются только из forms/<имя-сущности-в-нижнем-регистре>/", label),
				SuggestedFix: "Перенесите файл в forms/<имя-сущности-в-нижнем-регистре>/ " +
					"(например forms/реализациятоваров/объекта.form.yaml). Сейчас сущность " +
					"открывается авто-генерируемой формой, а этот файл не влияет ни на что.",
			})
		case len(parts) > 2:
			warns = append(warns, Issue{
				File: label, Kind: "Управляемая форма", Code: "form.not-loaded",
				Message: fmt.Sprintf("файл %q лежит во вложенном каталоге и НЕ загружается: "+
					"просматривается только forms/<сущность>/ на один уровень", label),
				SuggestedFix: "Положите файл непосредственно в forms/<имя-сущности-в-нижнем-регистре>/.",
			})
		default:
			// Сравнение регистрозависимое: загрузчик собирает путь как
			// strings.ToLower(entityName), поэтому на регистрозависимой ФС
			// каталог «РеализацияТоваров» не найдётся, хотя выглядит правильным.
			if _, ok := known[parts[0]]; !ok {
				warns = append(warns, Issue{
					File: label, Kind: "Управляемая форма", Code: "form.not-loaded",
					Message: fmt.Sprintf("каталог forms/%s/ не соответствует ни одной сущности "+
						"конфигурации — файл %q НЕ загружается", parts[0], label),
					SuggestedFix: "Имя каталога должно точно совпадать с именем сущности в нижнем " +
						"регистре — это ИМЯ из YAML (поле name), а не имя файла. " +
						suggestClosestEntity(parts[0], known),
				})
			}
		}
		return nil
	})

	sort.Slice(warns, func(i, j int) bool { return warns[i].File < warns[j].File })
	return warns
}

// suggestClosestEntity подсказывает подходящее имя каталога, если оно отличается
// от имени сущности только регистром — самая частая причина промаха.
func suggestClosestEntity(dirName string, known map[string]string) string {
	lower := strings.ToLower(dirName)
	if orig, ok := known[lower]; ok {
		return fmt.Sprintf("Похоже, имелась в виду сущность %q → каталог forms/%s/.", orig, lower)
	}
	var names []string
	for low := range known {
		names = append(names, low)
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
