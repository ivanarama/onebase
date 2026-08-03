package launcher

import (
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

// ── Журналы документов в конфигураторе ───────────────────────────────────────
//
// Журнал (journals/<Имя>.yaml) — чисто декларативный объект без модуля .os,
// поэтому редактор показывает сырой YAML (как виджет). Сохранение пишет файл
// verbatim из textarea, предварительно проверив, что YAML парсится — иначе
// битый файл сломал бы project.Load всей базы. Для нового объекта имя файла
// создаётся в нижнем регистре, а при последующих правках точный путь исходного
// файла передаётся редактором. Это важно для существующих конфигураций с
// mixed-case именами файлов и с несовпадающими name/filename.
// saveConfigFile/deleteConfigFile прозрачно работают и с файловой
// конфигурацией, и с конфигурацией в БД.

// journalYAMLRelPath — относительный путь файла журнала (нижний регистр имени).
func journalYAMLRelPath(name string) string { return "journals/" + nameToFilename(name) + ".yaml" }

// journalYAMLRelPathFromForm возвращает исходный путь журнала из формы, если
// он является ровно одним .yaml-файлом внутри journals/. Пустой путь допустим
// для старой формы и новых объектов: тогда используется стандартное lower-имя.
func journalYAMLRelPathFromForm(name, submitted string) (string, bool) {
	if submitted == "" {
		return journalYAMLRelPath(name), true
	}
	clean := path.Clean(submitted)
	if clean != submitted || path.Dir(clean) != "journals" || path.Ext(clean) != ".yaml" {
		return "", false
	}
	base := strings.TrimSuffix(path.Base(clean), ".yaml")
	if !validObjectName(base) {
		return "", false
	}
	return clean, true
}

// configuratorSaveJournal пишет journals/<Имя>.yaml из raw-YAML редактора.
func (h *handler) configuratorSaveJournal(w http.ResponseWriter, r *http.Request) {
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
	name := strings.TrimSpace(r.FormValue("journal_name"))
	if name == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Имя журнала не задано")
		renderCfg(w, r, data)
		return
	}
	if !validObjectName(name) {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Недопустимое имя журнала")
		renderCfg(w, r, data)
		return
	}
	relPath, ok := journalYAMLRelPathFromForm(name, r.FormValue("journal_path"))
	if !ok {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Недопустимое имя журнала")
		renderCfg(w, r, data)
		return
	}
	source := r.FormValue("source")
	// YAML должен как минимум парситься — иначе битый файл journals/*.yaml
	// уронит загрузку всей конфигурации базы.
	var probe map[string]any
	if err := yaml.Unmarshal([]byte(source), &probe); err != nil {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Ошибка в YAML журнала") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}

	saveErr := saveConfigFiles(r, h, b, []configFileEntry{
		{relPath: relPath, content: []byte(source)},
	})

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = name
		data.SelectedTreeID = "journal-" + name
	}
	renderCfg(w, r, data)
}

// configuratorDeleteJournal удаляет journals/<Имя>.yaml.
func (h *handler) configuratorDeleteJournal(w http.ResponseWriter, r *http.Request) {
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
	name := strings.TrimSpace(r.FormValue("journal_name"))
	if name == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Имя журнала не задано")
		renderCfg(w, r, data)
		return
	}
	if !validObjectName(name) {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Недопустимое имя журнала")
		renderCfg(w, r, data)
		return
	}
	relPath, ok := journalYAMLRelPathFromForm(name, r.FormValue("journal_path"))
	if !ok {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Недопустимое имя журнала")
		renderCfg(w, r, data)
		return
	}
	delErr := deleteConfigFiles(r, h, b, []string{relPath})
	data := h.loadCfgData(r.Context(), b, "tree")
	if delErr != nil {
		data.Error = tr(lang, "Ошибка удаления") + ": " + delErr.Error()
	}
	renderCfg(w, r, data)
}
