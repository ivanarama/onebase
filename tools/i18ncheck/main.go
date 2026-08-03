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
	dicts, err := loadDicts(filepath.Join(root, "internal", "i18n", "locales"))
	if err != nil {
		fail(err.Error())
	}
	delete(dicts, "ru") // ru.json — индекс, без значений

	// Ключи, которых нет НИ В ОДНОЙ нерусской локали = «забытые» (блокируют коммит).
	// Ключи, которых нет в части локалей = предупреждение (не блокирует).
	missingAll := []string{}
	partialMissing := map[string][]string{} // lang -> keys
	for _, k := range keys {
		inAny := false
		for _, d := range dicts {
			if _, ok := d[k]; ok {
				inAny = true
				break
			}
		}
		if !inAny {
			missingAll = append(missingAll, k)
		}
		for lang, d := range dicts {
			if _, ok := d[k]; !ok {
				partialMissing[lang] = append(partialMissing[lang], k)
			}
		}
	}
	sort.Strings(missingAll)

	fmt.Printf("i18ncheck: %d keys in templates, %d locales\n", len(keys), len(dicts))
	if len(partialMissing) > 0 {
		var langs []string
		for l := range partialMissing {
			langs = append(langs, l)
		}
		sort.Strings(langs)
		for _, l := range langs {
			n := len(partialMissing[l])
			// Не дублируем «common missing» в per-locale, ибо там и так все языки.
			extra := n - len(missingAll)
			if extra > 0 {
				fmt.Printf("  %s: missing %d (extra over common)\n", l, extra)
			}
		}
	}
	if len(missingAll) == 0 {
		fmt.Println("OK — все ключи переведены хотя бы в одной локали")
		return
	}
	fmt.Printf("\nFAIL — %d ключей нет ни в одной локали:\n", len(missingAll))
	for _, k := range missingAll {
		fmt.Printf("  %q\n", k)
	}
	fmt.Println("\nДобавьте переводы в internal/i18n/locales/<lang>.json.")
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
			if d.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			// Пропускаем тестовые файлы: i18nerr-вызовы в *_test.go
			// не требуют переводов (тесты проверяют внутреннее поведение,
			// а не пользовательский интерфейс).
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path) //nolint:gosec // G122: обход идёт по каталогу проекта или по временному каталогу, который мы сами распаковали; переход на os.Root — отдельная задача, он меняет поведение
			if err != nil {
				return err
			}
			for _, p := range keyPatterns {
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
