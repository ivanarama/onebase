// i18ncheck — проверка покрытия i18n: собирает все {{t $.Lang "..."}}
// из шаблонов в internal/ui и internal/launcher, а также Go-ключи
// i18nerr.New/Errorf/Wrapf и tr()/s.tr() в нескольких пакетах движка;
// сверяет с JSON-словарями в internal/i18n/locales и сообщает о ключах,
// которых нет ни в одной локали (кроме ru.json, который служит индексом
// языков — T() для ru возвращает ключ как есть).
//
// Запуск: go run ./tools/i18ncheck
// Exit 1 — если найдены непереведённые ключи (используется pre-commit-хуком).
package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/i18n"
)

// keyPatterns — список регулярных выражений для извлечения i18n-ключей.
//
// 1. Шаблонные ключи: {{t $.Lang "..."}} и {{t .Lang "..."}}
// 2. i18nerr.New("ключ") и i18nerr.Errorf("ключ", args...)
// 3. i18nerr.Wrapf(err, "ключ", args...)
// 4. tr(lang, "ключ") и s.tr(lang, "ключ")
//
// Ограничение: ключи с Go-экранированием (например \n или \") не раскодируются —
// такие строки редки в UI-ключах; при необходимости добавьте strconv.Unquote.
var keyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\{\{t\s+\$[.\w]*\s+"((?:[^"\\]|\\.)*)"\s*\}\}`),
	regexp.MustCompile(`i18nerr\.(?:New|Errorf)\(\s*"((?:[^"\\]|\\.)*)"`),
	// (?s).*? вместо [^,]+: err-аргумент часто содержит запятые
	// (Wrapf(load(a, b), "ключ")) — жадность до первой запятой молча
	// теряла ключ из проверки.
	regexp.MustCompile(`(?s)i18nerr\.Wrapf\(.*?,\s*"((?:[^"\\]|\\.)*)"`),
	regexp.MustCompile(`\btr\(\s*\w+,\s*"((?:[^"\\]|\\.)*)"\s*\)`),
}

// jsKeyPatterns — ключи из клиентских скриптов: там перевод берётся хелпером
// T("ключ") из словаря, отданного сервером (Bundle.Dict).
//
// Без этого обхода отчёт врал в самую опасную сторону: строки конструктора
// макетов в список ключей не попадали, и инструмент печатал «0 останется
// по-английски», хотя по-английски оставались десятки надписей. Контрибьютор,
// добавивший строку в скрипт, получал явное «всё в порядке».
var jsKeyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bT\(\s*"((?:[^"\\]|\\.)*)"\s*\)`),
}

func main() {
	root, err := repoRoot()
	if err != nil {
		fail(err.Error())
	}
	keys, err := collectKeys(root, []string{
		"internal/ui",
		"internal/launcher",
		"internal/storage",
		"internal/dsl",
		"internal/query",
		"internal/entityservice",
	})
	if err != nil {
		fail(err.Error())
	}
	localesDir := filepath.Join(root, "internal", "i18n", "locales")
	human, err := loadDicts(localesDir)
	if err != nil {
		fail(err.Error())
	}
	machine, err := loadDicts(filepath.Join(localesDir, i18n.MachineDir))
	if err != nil {
		fail(err.Error())
	}
	delete(human, i18n.BaseLang) // ru.json — индекс, без значений

	// Гейт — только английский: он запасной вариант для всех прочих языков
	// (i18n.Bundle.T), поэтому дырка именно в en.json утекает в интерфейс
	// русской строкой. Раньше блокировал ключ, которого нет НИ В ОДНОЙ
	// локали: ключ, доехавший до одного лишь de, гейт проходил, а показывался
	// по-русски всем остальным.
	var missingEn []string
	for _, k := range keys {
		if _, ok := human[i18n.FallbackLang][k]; !ok {
			missingEn = append(missingEn, k)
		}
	}
	sort.Strings(missingEn)

	fmt.Printf("i18ncheck: %d keys in templates, %d locales\n", len(keys), len(human))
	var langs []string
	for l := range human {
		if l != i18n.FallbackLang {
			langs = append(langs, l)
		}
	}
	sort.Strings(langs)
	for _, l := range langs {
		var missing, byMachine int
		for _, k := range keys {
			if _, ok := human[l][k]; ok {
				continue
			}
			missing++
			if _, ok := machine[l][k]; ok {
				byMachine++
			}
		}
		if missing == 0 {
			continue
		}
		// Остаток после машинного яруса и есть то, что покажется
		// по-английски; это норма, принятая по #960, а не долг.
		fmt.Printf("  %s: %d не переведено человеком (%d закрыто машинным ярусом, %d останется по-английски)\n",
			l, missing, byMachine, missing-byMachine)
	}
	if len(missingEn) == 0 {
		fmt.Printf("OK — все ключи есть в %s.json (запасной язык интерфейса)\n", i18n.FallbackLang)
		return
	}
	fmt.Printf("\nFAIL — %d ключей нет в %s.json:\n", len(missingEn), i18n.FallbackLang)
	for _, k := range missingEn {
		fmt.Printf("  %q\n", k)
	}
	fmt.Printf("\nДобавьте переводы в internal/i18n/locales/%s.json — без них строка покажется по-русски во всех языках.\n", i18n.FallbackLang)
	os.Exit(1)
}

func repoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", cwd)
		}
		dir = parent
	}
}

func collectKeys(root string, subdirs []string) ([]string, error) {
	seen := map[string]struct{}{}
	for _, sub := range subdirs {
		base := filepath.Join(root, filepath.FromSlash(sub))
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			isGo := strings.HasSuffix(path, ".go")
			// Клиентские скрипты переводятся тем же словарём, что и шаблоны, —
			// значит и в чек-лист ключей обязаны попадать. Минифицированный
			// вендоринг пропускаем: своих ключей там нет, а регулярка по
			// сжатому файлу даёт мусор.
			isJS := strings.HasSuffix(path, ".js") &&
				!strings.HasSuffix(path, ".min.js") &&
				!strings.HasSuffix(path, "_behavior_test.js")
			if !isGo && !isJS {
				return nil
			}
			// Пропускаем тестовые файлы: i18nerr-вызовы в *_test.go
			// не требуют переводов (тесты проверяют внутреннее поведение,
			// а не пользовательский интерфейс).
			if isGo && strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path) //nolint:gosec // G122: обход идёт по каталогу проекта или по временному каталогу, который мы сами распаковали; переход на os.Root — отдельная задача, он меняет поведение
			if err != nil {
				return err
			}
			pats := keyPatterns
			if isJS {
				pats = jsKeyPatterns
			}
			for _, p := range pats {
				for _, m := range p.FindAllSubmatch(data, -1) {
					seen[string(m[1])] = struct{}{}
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func loadDicts(dir string) (map[string]map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var m map[string]string
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		lang := strings.TrimSuffix(e.Name(), ".json")
		out[lang] = m
	}
	return out, nil
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "i18ncheck:", msg)
	os.Exit(2)
}
