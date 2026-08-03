package launcher

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/converter"
)

func (h *handler) configuratorPage(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	tab := r.URL.Query().Get("tab")
	if tab == "" {
		tab = "tree"
	}
	data := h.loadCfgData(r.Context(), b, tab)
	// «Открыть в редакторе» из дерева файлов (issue #132, фаза 2): ?select=<data-id>
	// узла → выделить объект в дереве (через SelectedTreeID/bootstrap).
	if sel := strings.TrimSpace(r.URL.Query().Get("select")); sel != "" && data.SelectedTreeID == "" {
		data.SelectedTreeID = sel
	}
	if cookie, cerr := r.Cookie("onebase_session"); cerr == nil {
		data.SessionToken = cookie.Value
	}
	renderCfg(w, r, data)
}

func (h *handler) configuratorConvert(w http.ResponseWriter, r *http.Request) {
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
	srcDir := strings.TrimSpace(r.FormValue("src_dir"))
	apply := r.FormValue("apply") == "1"

	data := h.loadCfgData(r.Context(), b, "convert")
	data.ConvertSrcDir = srcDir

	if srcDir == "" {
		data.Error = tr(lang, "Укажите путь к папке XML-выгрузки конфигурации")
		renderCfg(w, r, data)
		return
	}

	outDir, err := workspacePath(b.ID + "-convert")
	if err != nil {
		data.Error = err.Error()
		renderCfg(w, r, data)
		return
	}
	// clean previous conversion
	removeTemp(outDir)

	rep, err := converter.Convert(converter.Options{SourceDir: srcDir, OutDir: outDir})
	if err != nil {
		data.Error = tr(lang, "Ошибка конвертации") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}
	data.ConvertResult = rep.String()

	if apply {
		if b.ConfigSource == "database" {
			db, cerr := OpenDB(r.Context(), b)
			if cerr != nil {
				data.Error = tr(lang, "Ошибка подключения к БД") + ": " + cerr.Error()
				renderCfg(w, r, data)
				return
			}
			defer db.Close()
			repo := configdb.New(db)
			// Без схемы импорт всё равно не пройдёт; сообщаем в том же стиле,
			// что и соседние отказы, вместо того чтобы падать на ImportFromDir
			// с менее понятной ошибкой.
			if cerr := repo.EnsureSchema(r.Context()); cerr != nil {
				data.Error = tr(lang, "Ошибка подготовки схемы конфигурации") + ": " + cerr.Error()
				renderCfg(w, r, data)
				return
			}
			if cerr := repo.ImportFromDir(r.Context(), outDir); cerr != nil {
				data.Error = tr(lang, "Ошибка импорта") + ": " + cerr.Error()
				renderCfg(w, r, data)
				return
			}
		} else {
			// file mode — copy files into base path
			if cerr := copyDir(outDir, b.Path); cerr != nil {
				data.Error = tr(lang, "Ошибка копирования") + ": " + cerr.Error()
				renderCfg(w, r, data)
				return
			}
		}
		data.ConvertApplied = true
		// reload tree with new data
		fresh := h.loadCfgData(r.Context(), b, "convert")
		fresh.ConvertSrcDir = srcDir
		fresh.ConvertResult = data.ConvertResult
		fresh.ConvertApplied = true
		data = fresh
	}

	renderCfg(w, r, data)
}

// ── data loading ──────────────────────────────────────────────────────────────

// configDirtyAfter возвращает true, если в rootDir есть .os/.yaml/.yml файл
// с mtime новее threshold. Используется для отображения «звёздочки» в дереве

func renderCfg(w http.ResponseWriter, r *http.Request, data *configuratorData) {
	if data.Lang == "" {
		data.Lang = resolveLang(r)
	}
	// AJAX-сохранение форм редактирования объектов: вместо полной перерисовки
	// страницы возвращаем компактный JSON, который клиент показывает тостом. Это
	// убирает полностраничную перезагрузку (и связанный с ней «разрыв кадра» в
	// WebView2) и позволяет иметь единую кнопку «Сохранить» в шапке.
	if r != nil && r.Header.Get("X-Onebase-Ajax") == "1" {
		entity := data.FieldsSavedEntity
		if entity == "" {
			entity = data.ModuleSavedEntity
		}
		msg := tr(data.Lang, "Сохранено")
		if entity != "" && entity != "panel-backup" && entity != "__app__" {
			msg = "✓ " + entity + " — " + tr(data.Lang, "сохранено")
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      data.Error == "",
			"error":   data.Error,
			"message": msg,
			"running": data.IsRunning,
		})
		return
	}
	// Bootstrap-блоб window.__cfg + словарь window.__cfgI18n собираем здесь, а не
	// в loadCfgData: флаги сохранения (FieldsSavedEntity/ModuleSavedEntity) и
	// SelectedTreeID проставляются вызывающими ПОСЛЕ loadCfgData, поэтому их
	// финальные значения известны лишь к моменту рендера (план 55, фаза 2b-1).
	populateBootstrap(data, data.Lang)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := cfgTmpl.ExecuteTemplate(w, "cfg-main", data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

// ── Register field save ───────────────────────────────────────────────────────
