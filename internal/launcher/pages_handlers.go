package launcher

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/page"
	"github.com/ivantit66/onebase/internal/ui"
)

// ── Страницы в конфигураторе (план 66, доработка 2) ──────────────────────────
//
// Объект «страница» = метаданные pages/<Имя>.yaml + обработчик src/<Имя>.page.os
// (Процедура ПриФормировании). Структурно — близнец обработки (processors/*.yaml
// + src/*.proc.os), поэтому редактор устроен так же: метаданные + код + кнопки
// Сохранить/Проверить/Удалить с перерисовкой дерева через renderCfg.
//
// Имена файлов сохраняем в исходном регистре имени страницы (как демо
// pages/Панель.yaml), чтобы правка перезаписывала тот же файл на регистро-
// зависимых ФС, а не плодила дубль. saveConfigFile/deleteConfigFile прозрачно
// работают и с файловой конфигурацией, и с конфигурацией в БД.

// configuratorSavePage пишет pages/<Имя>.yaml (метаданные) и src/<Имя>.page.os
// (обработчик). Переводы (titles): если форма содержит блок titles.* — берутся
// из формы; иначе переносятся из существующего файла (не перезаписываются).
func (h *handler) configuratorSavePage(w http.ResponseWriter, r *http.Request) {
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
	name := strings.TrimSpace(r.FormValue("page_name"))
	if name == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Имя страницы не задано")
		renderCfg(w, r, data)
		return
	}
	if !validObjectName(name) {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Недопустимое имя страницы")
		renderCfg(w, r, data)
		return
	}

	pg := &page.Page{
		Name:   name,
		Title:  strings.TrimSpace(r.FormValue("title")),
		Icon:   ui.NormalizeIconName(r.FormValue("icon")),
		Roles:  splitConfigList(r.FormValue("roles")),
		Params: splitConfigList(r.FormValue("params")),
	}
	// Titles: если форма содержит блок titles.* — берём из формы (редактирование
	// переводов), иначе переносим из существующей конфигурации (не трогаем).
	if formHasMapField(r, "titles") {
		pg.Titles = parseMapForm(r, "titles")
	} else if proj, lerr := h.loadProjectFor(r.Context(), b); lerr == nil && proj != nil {
		for _, ex := range proj.Pages {
			if strings.EqualFold(ex.Name, name) {
				pg.Titles = ex.Titles
				break
			}
		}
		proj.Close()
	}

	yamlBody, merr := page.Marshal(pg)
	if merr != nil {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Ошибка сохранения") + ": " + merr.Error()
		renderCfg(w, r, data)
		return
	}

	// Атомарно: исходник ПЕРВЫМ, метаданные ПОСЛЕДНИМИ — чтобы hot-reload,
	// сработавший по записи pages/<имя>.yaml, уже видел готовый src/<имя>.page.os
	// (а не страницу с новыми метаданными и старым/отсутствующим обработчиком).
	saveErr := saveConfigFiles(r, h, b, []configFileEntry{
		{relPath: pageSrcRelPath(name), content: []byte(r.FormValue("source"))},
		{relPath: pageYAMLRelPath(name), content: yamlBody},
	})

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = name
	}
	renderCfg(w, r, data)
}

// configuratorDeletePage удаляет pages/<Имя>.yaml и src/<Имя>.page.os.
func (h *handler) configuratorDeletePage(w http.ResponseWriter, r *http.Request) {
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
	name := strings.TrimSpace(r.FormValue("page_name"))
	if name == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Имя страницы не задано")
		renderCfg(w, r, data)
		return
	}
	if !validObjectName(name) {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Недопустимое имя страницы")
		renderCfg(w, r, data)
		return
	}

	// Метаданные ПЕРВЫМИ — чтобы reload, сработавший по удалению pages/<имя>.yaml,
	// перестал видеть страницу до исчезновения её обработчика (а не наоборот).
	delErr := deleteConfigFiles(r, h, b, []string{
		pageYAMLRelPath(name),
		pageSrcRelPath(name),
	})

	data := h.loadCfgData(r.Context(), b, "tree")
	if delErr != nil {
		data.Error = tr(lang, "Ошибка удаления") + ": " + delErr.Error()
	}
	renderCfg(w, r, data)
}

// pageYAMLRelPath / pageSrcRelPath — относительные пути файлов страницы.
// Регистр имени сохраняется намеренно (см. комментарий к файлу).
func pageYAMLRelPath(name string) string { return "pages/" + name + ".yaml" }
func pageSrcRelPath(name string) string  { return "src/" + name + ".page.os" }

// validObjectName проверяет, что имя объекта пригодно для построения пути файла
// без обхода каталога. Запрещает разделители путей, "..", NUL и управляющие
// символы. Имена объектов конфигурации — идентификаторы (буквы/цифры/_),
// поэтому проверка не сужает легальные имена, но закрывает path traversal в
// обработчиках, строящих путь как «<подкаталог>/<имя>.<ext>».
func validObjectName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return false
	}
	for _, c := range name {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// splitConfigList разбирает список значений из текстового поля редактора:
// разделители — запятая, точка с запятой или перевод строки; пустые элементы
// и пробелы по краям отбрасываются. Пустой ввод → nil (поле опускается в YAML).
func splitConfigList(s string) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	})
	var out []string
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}
