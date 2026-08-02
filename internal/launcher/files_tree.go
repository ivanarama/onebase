package launcher

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// ── Дерево файлов конфигурации (issue #132) ─────────────────────────────────
//
// Виртуальный обозреватель: файлы конфигурации сгруппированы как дерево
// конфигурации (Категория → Объект → его файлы), хотя физически части объекта
// разбросаны по папкам (catalogs/X.yaml, src/X.module.os, forms/x/*). Это
// логическое представление поверх файлов — физическая раскладка на диске не
// меняется. Работает и в файловом режиме, и в режиме БД (пути виртуальные).

// fileTreeFile — лист дерева: один файл с подписью-ролью.
type fileTreeFile struct {
	Label string // «Метаданные», «Модуль объекта», «Форма: …»
	Path  string // относительный путь (слешами) — для просмотра/редактора
}

// fileTreeObject — объект конфигурации с его файлами. Name пустой → файлы
// рендерятся прямо под категорией (config-уровень, «Прочее»).
type fileTreeObject struct {
	Name   string
	NodeID string // data-id узла дерева конфигурации — для «открыть в редакторе» (issue #132 фаза 2); пусто → редактора нет
	Files  []fileTreeFile
}

// categoryNodePrefix — раздел → префикс data-id узла в дереве конфигурации
// (должен совпадать с разметкой configurator_tmpl.go).
var categoryNodePrefix = map[string]string{
	"Справочники":          "e-",
	"Документы":            "e-",
	"Перечисления":         "en-",
	"Регистры накопления":  "r-",
	"Регистры сведений":    "ir-",
	"Регистры бухгалтерии": "ar-",
	"Отчёты":               "rep-",
	"Обработки":            "proc-",
	"Страницы":             "page-",
	"Журналы":              "journal-",
	"Подсистемы":           "sub-",
	"Виджеты":              "wdg-",
}

// fileTreeCategory — раздел дерева (Справочники, Документы, …).
type fileTreeCategory struct {
	Name    string
	Objects []fileTreeObject
}

// порядок и иконки разделов — как в дереве конфигурации.
var fileTreeCatOrder = []string{
	"Справочники", "Документы", "Перечисления",
	"Регистры накопления", "Регистры сведений", "Регистры бухгалтерии",
	"Отчёты", "Обработки", "Страницы", "Журналы", "Подсистемы", "Виджеты",
	"Конфигурация", "Прочее",
}

// folderCategory — метаданные верхнего уровня: папка → раздел.
var folderCategory = map[string]string{
	"catalogs":    "Справочники",
	"documents":   "Документы",
	"enums":       "Перечисления",
	"registers":   "Регистры накопления",
	"inforegs":    "Регистры сведений",
	"accountregs": "Регистры бухгалтерии",
	"reports":     "Отчёты",
	"processors":  "Обработки",
	"pages":       "Страницы",
	"journals":    "Журналы",
	"subsystems":  "Подсистемы",
	"widgets":     "Виджеты",
}

// srcSuffix — расширение исходника в src/ → подпись-роль и запасной раздел
// (если объект не найден в индексе).
type srcRole struct{ label, fallbackCat string }

var srcRoles = []struct {
	suffix string
	role   srcRole
}{
	{".module.os", srcRole{"Модуль объекта", "Прочее"}},
	{".manager.os", srcRole{"Модуль менеджера", "Прочее"}},
	{".posting.os", srcRole{"Обработка проведения", "Документы"}},
	{".proc.os", srcRole{"Модуль обработки", "Обработки"}},
	{".rep.os", srcRole{"Модуль отчёта", "Отчёты"}},
	{".page.os", srcRole{"Модуль страницы", "Страницы"}},
	{".service.os", srcRole{"Модуль сервиса", "Прочее"}},
}

type objMeta struct{ Category, Display string }

// buildObjectIndex строит lower(имя) → {раздел, отображаемое имя} из модели
// проекта — чтобы привязать src/* и forms/* к правильному объекту и разделу.
func buildObjectIndex(proj *project.Project) map[string]objMeta {
	idx := map[string]objMeta{}
	if proj == nil {
		return idx
	}
	put := func(name, cat string) {
		if name != "" {
			idx[strings.ToLower(name)] = objMeta{cat, name}
		}
	}
	for _, e := range proj.Entities {
		cat := "Документы"
		if e.Kind == metadata.KindCatalog {
			cat = "Справочники"
		}
		put(e.Name, cat)
	}
	for _, p := range proj.Processors {
		put(p.Name, "Обработки")
	}
	for _, r := range proj.Reports {
		put(r.Name, "Отчёты")
	}
	for _, p := range proj.Pages {
		put(p.Name, "Страницы")
	}
	for _, j := range proj.Journals {
		put(j.Name, "Журналы")
	}
	for _, e := range proj.Enums {
		put(e.Name, "Перечисления")
	}
	for _, r := range proj.Registers {
		put(r.Name, "Регистры накопления")
	}
	for _, r := range proj.InfoRegisters {
		put(r.Name, "Регистры сведений")
	}
	for _, r := range proj.AccountRegisters {
		put(r.Name, "Регистры бухгалтерии")
	}
	for _, s := range proj.Subsystems {
		put(s.Name, "Подсистемы")
	}
	for _, w := range proj.Widgets {
		put(w.Name, "Виджеты")
	}
	return idx
}

// classifyConfigFile определяет (раздел, объект, подпись) для относительного
// пути файла конфигурации. Пустой объект → файл рендерится прямо под разделом.
func classifyConfigFile(path string, idx map[string]objMeta) (category, object, label string) {
	path = filepath.ToSlash(path)
	parts := strings.Split(path, "/")
	base := parts[len(parts)-1]
	top := parts[0]

	// Исходники в src/ — привязываем к объекту по имени.
	if top == "src" && len(parts) == 2 {
		for _, sr := range srcRoles {
			if strings.HasSuffix(base, sr.suffix) {
				name := strings.TrimSuffix(base, sr.suffix)
				if m, ok := idx[strings.ToLower(name)]; ok {
					return m.Category, m.Display, sr.role.label
				}
				return sr.role.fallbackCat, name, sr.role.label
			}
		}
		return "Прочее", "", base
	}

	// Формы: forms/<объект>/<имя>.form.(yaml|os).
	if top == "forms" && len(parts) >= 3 {
		objLower := parts[1]
		var label string
		if strings.HasSuffix(base, ".form.os") {
			label = "Модуль формы: " + strings.TrimSuffix(base, ".form.os")
		} else {
			label = "Форма: " + strings.TrimSuffix(strings.TrimSuffix(base, ".form.yaml"), ".form.yml")
		}
		if m, ok := idx[objLower]; ok {
			return m.Category, m.Display, label
		}
		return "Прочее", objLower, label
	}

	// Метаданные объекта верхнего уровня: <папка>/<Имя>.yaml.
	if cat, ok := folderCategory[top]; ok && len(parts) == 2 {
		name := strings.TrimSuffix(strings.TrimSuffix(base, ".yaml"), ".yml")
		disp := name
		if m, ok := idx[strings.ToLower(name)]; ok {
			disp = m.Display
		}
		return cat, disp, "Метаданные"
	}

	// Конфигурация приложения.
	if top == "config" || base == "app.yaml" || base == "home_page.yaml" {
		return "Конфигурация", "", base
	}

	return "Прочее", "", path
}

// buildConfigFileTreeFrom собирает дерево из списка путей и модели проекта.
// Чистая функция (без I/O) — удобно тестировать.
func buildConfigFileTreeFrom(proj *project.Project, paths []string) []fileTreeCategory {
	idx := buildObjectIndex(proj)
	// cat → obj → []file
	cats := map[string]map[string][]fileTreeFile{}
	for _, p := range paths {
		p = filepath.ToSlash(p)
		cat, obj, label := classifyConfigFile(p, idx)
		if cats[cat] == nil {
			cats[cat] = map[string][]fileTreeFile{}
		}
		cats[cat][obj] = append(cats[cat][obj], fileTreeFile{Label: label, Path: p})
	}

	catRank := map[string]int{}
	for i, c := range fileTreeCatOrder {
		catRank[c] = i
	}
	var out []fileTreeCategory
	for catName, objs := range cats {
		var objList []fileTreeObject
		for objName, files := range objs {
			sort.Slice(files, func(i, j int) bool { return files[i].Label < files[j].Label })
			obj := fileTreeObject{Name: objName, Files: files}
			if objName != "" {
				if p := categoryNodePrefix[catName]; p != "" {
					obj.NodeID = p + objName // issue #132 фаза 2
				}
			}
			objList = append(objList, obj)
		}
		// объекты по имени; безымянная группа («Прочее»/config) — в конец.
		sort.Slice(objList, func(i, j int) bool {
			if (objList[i].Name == "") != (objList[j].Name == "") {
				return objList[j].Name == ""
			}
			return objList[i].Name < objList[j].Name
		})
		out = append(out, fileTreeCategory{Name: catName, Objects: objList})
	}
	sort.Slice(out, func(i, j int) bool {
		ri, oki := catRank[out[i].Name]
		rj, okj := catRank[out[j].Name]
		if oki && okj {
			return ri < rj
		}
		if oki != okj {
			return oki // известные разделы раньше неизвестных
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// configFilePaths возвращает относительные (слешами) пути всех файлов
// конфигурации — из файлов проекта или из БД.
func (h *handler) configFilePaths(ctx context.Context, b *Base) []string {
	if b.ConfigSource == "database" {
		db, err := OpenDB(ctx, b)
		if err != nil {
			return nil
		}
		defer db.Close()
		files, err := configdb.New(db).ListByPrefix(ctx, "")
		if err != nil {
			return nil
		}
		out := make([]string, 0, len(files))
		for _, f := range files {
			out = append(out, f.Path)
		}
		return out
	}
	return walkConfigFiles(b.Path)
}

// walkConfigFiles обходит каталог проекта и собирает .yaml/.yml/.os/.json,
// пропуская служебные папки.
func walkConfigFiles(root string) []string {
	var out []string
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch filepath.Base(p) {
			case "backups", ".git", "node_modules", "workspace", ".onebase":
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".yaml", ".yml", ".os", ".json":
			rel, rerr := filepath.Rel(root, p)
			if rerr == nil {
				out = append(out, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	bestEffort("обойти каталог конфигурации", walkErr)
	return out
}

// buildConfigFileTree — дерево файлов для вкладки «Файлы».
func (h *handler) buildConfigFileTree(ctx context.Context, b *Base, proj *project.Project) []fileTreeCategory {
	return buildConfigFileTreeFrom(proj, h.configFilePaths(ctx, b))
}

// configuratorFileRaw отдаёт сырое содержимое файла конфигурации для просмотра
// (read-only). Защита от traversal: путь нормализуется и обязан остаться внутри
// конфигурации, расширение — из белого списка.
func (h *handler) configuratorFileRaw(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rel := filepath.ToSlash(strings.TrimSpace(r.URL.Query().Get("path")))
	if rel == "" || strings.Contains(rel, "..") || strings.HasPrefix(rel, "/") {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".yaml", ".yml", ".os", ".json":
	default:
		http.Error(w, "unsupported file", http.StatusBadRequest)
		return
	}

	var content []byte
	if b.ConfigSource == "database" {
		db, derr := OpenDB(r.Context(), b)
		if derr != nil {
			http.Error(w, derr.Error(), 500)
			return
		}
		defer db.Close()
		files, lerr := configdb.New(db).ListByPrefix(r.Context(), rel)
		if lerr != nil {
			http.Error(w, lerr.Error(), 500)
			return
		}
		for _, f := range files {
			if f.Path == rel {
				content = f.Content
				break
			}
		}
		if content == nil {
			http.NotFound(w, r)
			return
		}
	} else {
		abs := filepath.Join(b.Path, filepath.FromSlash(rel))
		// Подтверждаем, что после очистки путь всё ещё внутри проекта.
		if !strings.HasPrefix(filepath.Clean(abs)+string(os.PathSeparator), filepath.Clean(b.Path)+string(os.PathSeparator)) {
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		if !realPathInsideBase(abs, b.Path) {
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		content, err = os.ReadFile(abs)
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	writeDownload(w, rel, content)
}

func realPathInsideBase(path, base string) bool {
	baseReal, err := filepath.EvalSymlinks(base)
	if err != nil {
		return false
	}
	pathReal, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(baseReal, pathReal)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".." && !filepath.IsAbs(rel))
}
