package launcher

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/printform"
	"github.com/ivantit66/onebase/internal/sheet"
	"gopkg.in/yaml.v3"
)

// ── Создание макета с нуля (план 64, этап 5a, пункт 6.4) ─────────────────────
//
// Два сценария:
//   1. «+ Печатная форма (макет)» у сущности — скелет с областями
//      Заголовок/ШапкаТаблицы/Строка/Итоги и binding по первой ТЧ.
//   2. «Создать макет» у DSL-формы (.os) без макета — пустой макет 3×3 рядом
//      с .os, который подхватывается как парный (HasLayout).
//
// Запись: file-mode пишет на диск; configdb-режим пишет в _onebase_config
// (как configuratorSaveLayout). validLayoutName отсекает ../ и пустые имена.

// validLayoutName проверяет имя файла макета: непустое, без разделителей пути
// и без перехода вверх по дереву.
func validLayoutName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return false
	}
	return true
}

// buildEntityLayoutSkeleton строит скелет макета печатной формы для сущности.
// Области: Заголовок (1 ячейка bold по центру с именем сущности), ШапкаТаблицы,
// Строка, Итоги. columns — по числу полей первой ТЧ (или 3 по умолчанию).
// binding.repeat связывает область Строка с первой табличной частью.
func buildEntityLayoutSkeleton(name string, ent *metadata.Entity) *printform.LayoutTemplate {
	var firstTP *metadata.TablePart
	if ent != nil && len(ent.TableParts) > 0 {
		firstTP = &ent.TableParts[0]
	}

	nCols := 3
	if firstTP != nil && len(firstTP.Fields) > 0 {
		nCols = len(firstTP.Fields)
	}

	cols := make([]printform.LayoutColumn, nCols)

	title := name
	if ent != nil {
		title = ent.Name
	}

	// Заголовок: одна ячейка bold по центру, растянутая на все колонки.
	header := &printform.LayoutArea{
		Name: "Заголовок",
		Rows: []printform.LayoutRow{{
			Cells: []printform.LayoutCell{{
				Text:    title,
				Bold:    true,
				Align:   "center",
				ColSpan: nCols,
			}},
		}},
	}

	// ШапкаТаблицы: заголовки колонок (имена полей ТЧ или «Колонка N»).
	headerCells := make([]printform.LayoutCell, nCols)
	rowCells := make([]printform.LayoutCell, nCols)
	for i := 0; i < nCols; i++ {
		colTitle := ""
		colParam := ""
		if firstTP != nil && i < len(firstTP.Fields) {
			colTitle = firstTP.Fields[i].Name
			colParam = firstTP.Fields[i].Name
		} else {
			colTitle = "Колонка"
		}
		headerCells[i] = printform.LayoutCell{
			Text:    colTitle,
			Bold:    true,
			Align:   "center",
			Borders: &printform.CellBorders{Left: "thin", Top: "thin", Right: "thin", Bottom: "thin"},
		}
		rowCells[i] = printform.LayoutCell{
			Parameter: colParam,
			Borders:   &printform.CellBorders{Left: "thin", Top: "thin", Right: "thin", Bottom: "thin"},
		}
	}
	tableHeader := &printform.LayoutArea{
		Name: "ШапкаТаблицы",
		Rows: []printform.LayoutRow{{Cells: headerCells}},
	}
	rowArea := &printform.LayoutArea{
		Name: "Строка",
		Rows: []printform.LayoutRow{{Cells: rowCells}},
	}

	// Итоги: подпись «Итого» + пустая ячейка под сумму.
	totalsCells := make([]printform.LayoutCell, nCols)
	for i := range totalsCells {
		totalsCells[i] = printform.LayoutCell{}
	}
	if nCols >= 2 {
		totalsCells[0] = printform.LayoutCell{Text: "Итого", Bold: true, Align: "right", ColSpan: nCols - 1}
		totalsCells = totalsCells[:2] // "Итого" (spanned) + одна ячейка под итог
		totalsCells[1] = printform.LayoutCell{Bold: true}
	} else {
		totalsCells[0] = printform.LayoutCell{Text: "Итого", Bold: true}
	}
	totals := &printform.LayoutArea{
		Name: "Итоги",
		Rows: []printform.LayoutRow{{Cells: totalsCells}},
	}

	lt := &printform.LayoutTemplate{
		Name:     name,
		Document: title,
		Page:     ptrPageSetup(sheet.DefaultPageSetup()),
		Columns:  cols,
		Areas:    []*printform.LayoutArea{header, tableHeader, rowArea, totals},
	}
	if firstTP != nil {
		lt.Binding = &printform.Binding{
			Repeat: []printform.RepeatBinding{{Area: "Строка", Source: firstTP.Name}},
		}
	}
	return lt
}

func ptrPageSetup(p sheet.PageSetup) *sheet.PageSetup { return &p }

// buildEmptyLayoutSkeleton строит пустой макет с одной областью «Макет» 3×3 —
// для DSL-формы без привязанного макета.
func buildEmptyLayoutSkeleton(name string) *printform.LayoutTemplate {
	var rows []printform.LayoutRow
	for r := 0; r < 3; r++ {
		cells := make([]printform.LayoutCell, 3)
		rows = append(rows, printform.LayoutRow{Cells: cells})
	}
	return &printform.LayoutTemplate{
		Name:    name,
		Columns: make([]printform.LayoutColumn, 3),
		Areas:   []*printform.LayoutArea{{Name: "Макет", Rows: rows}},
	}
}

// marshalLayout сериализует макет в YAML (areas — sequence, v2).
func marshalLayout(lt *printform.LayoutTemplate) ([]byte, error) {
	return yaml.Marshal(lt)
}

// configuratorNewLayout создаёт макет печатной формы. Параметры формы:
//
//	entity — имя сущности (для скелета с binding по первой ТЧ); опционально.
//	osform — имя DSL-формы (.os), для которой создаётся парный пустой макет.
//	name   — имя новой формы/макета (для сценария сущности).
//
// Ровно один из entity/osform определяет сценарий.
func (h *handler) configuratorNewLayout(w http.ResponseWriter, r *http.Request) {
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

	entityName := strings.TrimSpace(r.FormValue("entity"))
	osForm := strings.TrimSpace(r.FormValue("osform"))

	var layoutName, document string
	var lt *printform.LayoutTemplate
	var subdir string // подпапка внутри printforms (для парного макета .os)

	switch {
	case osForm != "":
		// Сценарий 2: парный макет для .os-формы без макета.
		if !validLayoutName(osForm) {
			h.layoutCreateError(w, r, b, lang, tr(lang, "Недопустимое имя файла"))
			return
		}
		layoutName = osForm
		lt = buildEmptyLayoutSkeleton(layoutName)
		// определим подпапку, в которой лежит .os (если в подпапке сущности).
		subdir = h.findOSFormSubdir(r, b, osForm)
	default:
		// Сценарий 1: новая декларативная форма у сущности.
		layoutName = strings.TrimSpace(r.FormValue("name"))
		if layoutName == "" {
			h.layoutCreateError(w, r, b, lang, tr(lang, "Имя макета обязательно"))
			return
		}
		if !validLayoutName(layoutName) {
			h.layoutCreateError(w, r, b, lang, tr(lang, "Недопустимое имя файла"))
			return
		}
		ent := h.findEntity(r, b, entityName)
		lt = buildEntityLayoutSkeleton(layoutName, ent)
		document = lt.Document
		_ = document
	}

	src, err := marshalLayout(lt)
	if err != nil {
		h.layoutCreateError(w, r, b, lang, tr(lang, "Ошибка создания макета")+": "+err.Error())
		return
	}

	filename := layoutName + ".layout.yaml"
	relPath := "printforms/" + filename
	if subdir != "" {
		relPath = "printforms/" + subdir + "/" + filename
	}

	if werr := h.writeLayoutFile(r.Context(), b, relPath, src); werr != nil {
		h.layoutCreateError(w, r, b, lang, layoutWriteMessage(lang, werr))
		return
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	data.FieldsSaved = true
	data.FieldsSavedEntity = layoutName
	data.SavedMessage = tr(lang, "✓ Макет") + " «" + layoutName + "» " + tr(lang, "создан. Перезапустите базу, чтобы форма появилась в списке печати.")
	data.SelectedTreeID = "mkt-" + layoutName
	renderCfg(w, r, data)
}

// layoutCreateError перерисовывает конфигуратор с сообщением об ошибке.
func (h *handler) layoutCreateError(w http.ResponseWriter, r *http.Request, b *Base, lang, msg string) {
	data := h.loadCfgData(r.Context(), b, "tree")
	data.Error = msg
	renderCfg(w, r, data)
}

// findEntity загружает метаданные сущности по имени (регистронезависимо) или nil.
func (h *handler) findEntity(r *http.Request, b *Base, name string) *metadata.Entity {
	if name == "" {
		return nil
	}
	proj, err := h.loadProjectFor(r.Context(), b)
	if err != nil {
		return nil
	}
	defer proj.Close()
	for _, e := range proj.Entities {
		if strings.EqualFold(e.Name, name) {
			return e
		}
	}
	return nil
}

// findOSFormSubdir возвращает имя подпапки printforms/, в которой лежит .os-форма
// osForm (пусто, если форма в корне printforms или не найдена). Нужна, чтобы
// положить парный .layout.yaml рядом с .os.
func (h *handler) findOSFormSubdir(r *http.Request, b *Base, osForm string) string {
	if b.ConfigSource == "database" {
		return "" // в БД парность определяется по совпадению basename без подпапки
	}
	pfDir := filepath.Join(b.Path, "printforms")
	found := ""
	walkErr := filepath.Walk(pfDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.EqualFold(info.Name(), osForm+".os") {
			rel, rerr := filepath.Rel(pfDir, filepath.Dir(path))
			if rerr == nil && rel != "." {
				found = filepath.ToSlash(rel)
			}
		}
		return nil
	})
	bestEffort("обойти каталог печатных форм", walkErr)
	return found
}
