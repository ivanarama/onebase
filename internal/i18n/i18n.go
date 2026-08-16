package i18n

import (
	"encoding/json"
	"io/fs"
	"os"
	gopath "path"
	"path/filepath"
	"sort"
	"strings"
)

// MachineDir — имя каталога с машинными переводами внутри каталога локалей
// (как встроенного, так и внешнего). См. комментарий к EmbeddedLocales.
const MachineDir = "machine"

// BaseLang — язык ключей: ключи словарей это и есть исходные русские строки,
// поэтому для ru словарь не нужен, а T() возвращает ключ как есть.
const BaseLang = "ru"

// FallbackLang — язык, на который откатывается T(), если перевода нет.
// Без него непереведённый ключ показывался бы по-русски: в армянском или
// китайском интерфейсе это нечитаемо, тогда как английский читаем почти
// везде (#960).
const FallbackLang = "en"

// Lang describes an available language.
type Lang struct {
	Code   string // e.g. "en", "sr"
	Native string // e.g. "English", "Српски"
}

// Bundle holds translations for all languages.
type Bundle struct {
	dicts   map[string]map[string]string // lang → key → translation
	sorted  []Lang                       // available languages sorted by code
	natives map[string]string            // lang code → native name
}

// Load reads built-in dictionaries from embedded FS and merges external
// dictionaries from externalDir (file-based overrides/additions).
func Load(embedded fs.FS, externalDir string) (*Bundle, error) {
	b := &Bundle{
		dicts:   make(map[string]map[string]string),
		natives: map[string]string{},
	}
	// load embedded
	if err := b.loadFS(embedded); err != nil {
		return nil, err
	}
	// load external (may not exist)
	if externalDir != "" {
		_ = b.loadDir(externalDir)
	}
	b.buildSorted()
	return b, nil
}

// loadFS грузит словари в два прохода: сначала машинный ярус
// (locales/machine/<lang>.json), затем человеческий (locales/<lang>.json).
// Порядок задан явно, а не порядком обхода: fs.WalkDir идёт лексически, и
// подкаталог «machine» оказался бы то раньше, то позже соседних файлов —
// часть языков получила бы машинный перевод поверх человеческого.
func (b *Bundle) loadFS(fsys fs.FS) error {
	if err := b.walkJSON(fsys, true); err != nil {
		return err
	}
	return b.walkJSON(fsys, false)
}

// walkJSON обходит fsys и грузит .json-файлы одного яруса: machine == true —
// только лежащие в каталоге «machine», иначе — только все остальные.
func (b *Bundle) walkJSON(fsys fs.FS, machine bool) error {
	return fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if filepath.Ext(path) != ".json" {
			return nil
		}
		if isMachinePath(path) != machine {
			return nil
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		lang := strings.TrimSuffix(filepath.Base(path), ".json")
		return b.merge(lang, data)
	})
}

// isMachinePath сообщает, лежит ли файл в каталоге машинных переводов.
// Пути в io/fs всегда со слэшем, независимо от ОС.
func isMachinePath(path string) bool {
	return gopath.Base(gopath.Dir(path)) == MachineDir
}

func (b *Bundle) loadDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err // caller ignores
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		lang := strings.TrimSuffix(e.Name(), ".json")
		_ = b.merge(lang, data)
	}
	return nil
}

func (b *Bundle) merge(lang string, data []byte) error {
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	if b.dicts[lang] == nil {
		b.dicts[lang] = make(map[string]string, len(m))
	}
	for k, v := range m {
		b.dicts[lang][k] = v
	}
	// extract native name if provided
	if n, ok := m["__native__"]; ok {
		b.natives[lang] = n
		delete(b.dicts[lang], "__native__")
	} else {
		b.natives[lang] = langName(lang)
	}
	return nil
}

func (b *Bundle) buildSorted() {
	seen := map[string]bool{}
	for lang := range b.dicts {
		seen[lang] = true
	}
	b.sorted = make([]Lang, 0, len(seen))
	for lang := range seen {
		b.sorted = append(b.sorted, Lang{
			Code:   lang,
			Native: b.natives[lang],
		})
	}
	sort.Slice(b.sorted, func(i, j int) bool {
		return b.sorted[i].Code < b.sorted[j].Code
	})
}

// T returns the translation for key in the given language, falling back to
// English and then to the key itself (the Russian source string).
//
// Порядок важен: до #960 непереведённый ключ отдавался как есть, то есть
// по-русски. Для ru это правильный ответ, для остальных языков — нет: у
// тринадцати локалей переведена половина интерфейса, и пользователь получал
// экран, наполовину набранный чужим письмом. Английский промежуточный шаг
// делает пропуск перевода читаемым, а не сломанным.
func (b *Bundle) T(lang, key string) string {
	if d, ok := b.dicts[lang]; ok {
		if v, ok := d[key]; ok {
			return v
		}
	}
	// Региональный вариант («de-at») сначала пробует свой базовый язык.
	if base := baseSubtag(lang); base != lang {
		if d, ok := b.dicts[base]; ok {
			if v, ok := d[key]; ok {
				return v
			}
		}
	}
	if !IsBaseLang(lang) {
		if d, ok := b.dicts[FallbackLang]; ok {
			if v, ok := d[key]; ok {
				return v
			}
		}
	}
	return key
}

// IsBaseLang сообщает, что язык — русский, то есть язык самих ключей: для него
// ключ и есть готовый ответ, английский откат тут был бы регрессией. Проверка
// идёт по базовому субтегу, потому что Resolve пропускает явный выбор
// пользователя как есть, включая «ru-RU».
func IsBaseLang(lang string) bool {
	return baseSubtag(lang) == BaseLang
}

// baseSubtag отрезает регион: «pt-br» → «pt».
func baseSubtag(lang string) string {
	if i := strings.Index(lang, "-"); i > 0 {
		return lang[:i]
	}
	return lang
}

// Available returns the list of languages with translations loaded.
func (b *Bundle) Available() []Lang {
	return b.sorted
}

// Dict returns a copy of the translation dictionary for lang (key→translation),
// or nil if the language has no dictionary loaded. The base language (ru) has
// no dictionary — callers fall back to the key itself (front-end T(k)=dict[k]||k).
//
// Для прочих языков в словарь подмешивается английский: у фронтенда нет
// доступа к Bundle, он умеет только dict[k] || k, — значит цепочку отката
// (см. T) надо уложить в сам словарь, иначе клиентские строки останутся
// русскими там, где серверные уже английские.
func (b *Bundle) Dict(lang string) map[string]string {
	base, hasBase := b.dicts[baseSubtag(lang)]
	exact, hasExact := b.dicts[lang]
	if !hasBase && !hasExact {
		return nil
	}
	out := map[string]string{}
	if !IsBaseLang(lang) {
		for k, v := range b.dicts[FallbackLang] {
			out[k] = v
		}
	}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range exact {
		out[k] = v
	}
	return out
}

// Resolve determines the effective language from the priority chain:
// user preference → base default → Accept-Language header → "ru".
func Resolve(userLang, baseLang, acceptHeader string, b *Bundle) string {
	// Explicit user/base choices are accepted even without a dictionary:
	// the key language (Russian) has no dict but T() returns keys directly.
	for _, c := range []string{userLang, baseLang} {
		norm := normalizeLang(c)
		if norm != "" {
			return norm
		}
	}
	// Accept-Language: only pick languages that have a loaded dictionary.
	for _, part := range strings.Split(acceptHeader, ",") {
		seg := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		norm := normalizeLang(seg)
		if norm == "" {
			continue
		}
		if _, ok := b.dicts[norm]; ok {
			return norm
		}
		if idx := strings.Index(norm, "-"); idx > 0 {
			base := norm[:idx]
			if _, ok := b.dicts[base]; ok {
				return base
			}
		}
	}
	return "ru"
}

// normalizeLang lowercases and strips whitespace.
func normalizeLang(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	if s == "" {
		return ""
	}
	return s
}

// langName returns a human-friendly name for well-known language codes.
func langName(code string) string {
	switch strings.ToLower(code) {
	case "en":
		return "English"
	case "ru":
		return "Русский"
	case "sr":
		return "Српски"
	case "de":
		return "Deutsch"
	case "fr":
		return "Français"
	case "es":
		return "Español"
	case "ka":
		return "ქართული"
	case "hy":
		return "Հայերեն"
	case "kk":
		return "Қазақша"
	case "az":
		return "Azərbaycan"
	case "uz":
		return "O'zbekcha"
	case "uk":
		return "Українська"
	case "tr":
		return "Türkçe"
	case "ro":
		return "Română"
	case "pt":
		return "Português"
	case "zh":
		return "中文"
	default:
		return code
	}
}
