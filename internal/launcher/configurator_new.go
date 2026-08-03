package launcher

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (h *handler) configuratorNewObject(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if failForm(w, r) {
		return
	}

	kind := r.FormValue("kind")
	name := strings.TrimSpace(r.FormValue("name"))

	if name == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Укажите имя объекта")
		renderCfg(w, r, data)
		return
	}
	if !validObjectName(name) {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Недопустимое имя объекта")
		renderCfg(w, r, data)
		return
	}

	subdir, content := newObjectContent(kind, name)
	if subdir == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Неизвестный тип объекта") + ": " + kind
		renderCfg(w, r, data)
		return
	}

	filename := nameToFilename(name) + ".yaml"
	if kind == "module" {
		// subdir/content приходят из newObjectContent; здесь только расширение.
		filename = nameToFilename(name) + ".module.os"
	}
	if kind == "page" {
		// Имя файла страницы — в исходном регистре (как демо pages/Панель.yaml),
		// чтобы правка существующей страницы перезаписывала тот же файл, а не
		// плодила дубль на регистрозависимых ФС.
		filename = name + ".yaml"
	}

	path := subdir + "/" + filename
	files := []configFileEntry{{relPath: path, content: []byte(content)}}
	if kind == "page" {
		// Страница — это пара файлов: сначала обработчик, затем метаданные, чтобы
		// файловый watcher при reload по .yaml уже видел готовый .os.
		files = []configFileEntry{
			{relPath: pageSrcRelPath(name), content: []byte(newPageOSSkeleton(name))},
			{relPath: path, content: []byte(content)},
		}
	}
	saveErr := saveConfigFiles(r, h, b, files)

	if saveErr != nil {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Ошибка создания") + ": " + saveErr.Error()
		renderCfg(w, r, data)
		return
	}
	data := h.loadCfgData(r.Context(), b, "tree")
	data.FieldsSavedEntity = name
	data.FieldsSaved = true
	// issue #127: спозиционировать дерево на только что созданном объекте —
	// передаём точный data-id его узла, дальше клиент раскроет группу,
	// проскроллит и выделит его.
	data.SelectedTreeID = treeNodeID(kind, name)
	renderCfg(w, r, data)
}

// treeNodeID возвращает data-id узла дерева конфигуратора для (вид, имя). Набор
// префиксов обязан совпадать с разметкой дерева (configurator_tmpl.go) и
// перечнем видов в newObjectContent. Пустая строка — для видов без узла в дереве.
func treeNodeID(kind, name string) string {
	prefix := map[string]string{
		"catalog":    "e-",
		"document":   "e-",
		"register":   "r-",
		"inforeg":    "ir-",
		"accountreg": "ar-",
		"enum":       "en-",
		"subsystem":  "sub-",
		"widget":     "wdg-",
		"processor":  "proc-",
		"page":       "page-",
		"journal":    "journal-",
		"module":     "mod-",
	}[kind]
	if prefix == "" {
		return ""
	}
	return prefix + name
}

func newObjectContent(kind, name string) (subdir, content string) {
	switch kind {
	case "catalog":
		return "catalogs", "name: " + name + "\nfields:\n  - name: Наименование\n    type: string\n"
	case "document":
		return "documents", "name: " + name + "\nfields:\n  - name: Дата\n    type: date\n"
	case "register":
		return "registers", "name: " + name + "\ndimensions:\n  - name: Измерение1\n    type: string\nresources:\n  - name: Ресурс1\n    type: number\n"
	case "inforeg":
		return "inforegs", "name: " + name + "\nperiodic: false\ndimensions:\n  - name: Ключ\n    type: string\nresources:\n  - name: Значение\n    type: string\n"
	case "enum":
		return "enums", "name: " + name + "\nvalues:\n  - Значение1\n  - Значение2\n"
	case "subsystem":
		return "subsystems", "name: " + name + "\ntitle: " + name + "\norder: 10\ncontents:\n  catalogs: []\n  documents: []\n  registers: []\n"
	case "widget":
		return "widgets", "name: " + name + "\ntype: kpi\ntitle: " + name + "\nformat: number\nquery: |\n  ВЫБРАТЬ КОЛИЧЕСТВО(*) КАК Значение ИЗ Документ.ИмяДокумента\n"
	case "accountreg":
		return "accountregs", "name: " + name + "\ntitle: " + name + "\naccounts: ПланСчетов\nresources:\n  - name: Сумма\n    type: number\n"
	case "processor":
		return "processors", "name: " + name + "\ntitle: " + name + "\nparams: []\n"
	case "page":
		return "pages", "name: " + name + "\ntitle: " + name + "\n"
	case "journal":
		// Пустой, но проходящий check каркас журнала (документы/колонки
		// заполняет автор). Пример формата — в комментарии.
		return "journals", "name: " + name + "\ntitle: " + name + "\ndocuments: []\ncolumns: []\n" +
			"# Пример: documents: [Заказ]\n" +
			"# columns: [{field: Дата, label: Дата, format: date}, {field: Сумма, label: Сумма}]\n" +
			"# filters: [{field: Дата, label: Дата, type: date_range}]\n" +
			"# conditional: [{when: 'Статус = \"Проведён\"', field: Статус, style: {color: \"#0b6e2d\", bold: true}}]\n"
	case "module":
		// Общий модуль — файл src/<имя>.module.os со стартовой экспортной
		// процедурой (расширение файла проставляется в configuratorNewObject).
		return "src", "// " + name + "\n// Общий модуль\n\nПроцедура Главная() Экспорт\nКонецПроцедуры\n"
	}
	return "", ""
}

// newPageOSSkeleton — стартовый обработчик src/<имя>.page.os для новой страницы
// (план 66): пустая процедура ПриФормировании с парой блоков-примеров, чтобы
// onebase check сразу проходил, а автор видел, куда писать.
func newPageOSSkeleton(name string) string {
	return "// Страница " + name + " (план 66) — произвольное представление на встроенном языке.\n" +
		"// Обработчик наполняет построитель «Страница» структурными блоками.\n\n" +
		"Процедура ПриФормировании(Страница, Параметры) Экспорт\n" +
		"    Страница.Заголовок(\"" + name + "\");\n" +
		"    Страница.Абзац(\"Произвольное представление на встроенном языке.\");\n" +
		"КонецПроцедуры\n"
}

func nameToFilename(name string) string {
	return strings.ToLower(name)
}

// ── Enum save ─────────────────────────────────────────────────────────────────
