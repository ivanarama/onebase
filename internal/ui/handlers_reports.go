package ui

// HTTP-обработчики отчётов: форма параметров, запуск, экспорт в Excel.
// Выделено из handlers.go (план 55, этап 1) — перенос as-is.

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/excel"
	"github.com/ivantit66/onebase/internal/query"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func (s *Server) reportForm(w http.ResponseWriter, r *http.Request) {
	rep := s.getReport(w, r)
	if rep == nil {
		return
	}
	if !s.requirePerm(w, r, "report", rep.Name, "run") {
		return
	}
	if r.URL.Query().Get("__run") == "1" {
		if err := r.ParseForm(); err != nil {
			http.Error(w, s.errText(r, err), 400)
			return
		}
		s.runReport(w, r, rep, reportParamValuesFromRequest(r, rep))
		return
	}
	user := currentUserLogin(r)
	supportsSettings := reportSupportsRuntimeSettings(rep)
	var presets []storage.ReportPreset
	if supportsSettings {
		presets = loadReportPresets(r.Context(), s.store, rep.Name, user)
	}
	activePresetID := r.FormValue("__preset")
	if supportsSettings && s.store != nil && activePresetID == "" {
		if p, err := s.store.GetDefaultReportPreset(r.Context(), rep.Name, user); err == nil && p != nil {
			activePresetID = p.ID
		}
	}
	activePreset := activeReportPreset(presets, activePresetID)
	// Если у отчёта нет ни параметров, ни вариантов компоновки, ни пользовательских
	// пресетов — строим сразу. Варианты/пресеты требуют формы с выбором.
	if len(rep.Params) == 0 && len(rep.Variants) == 0 && len(presets) == 0 {
		s.runReport(w, r, rep, map[string]any{})
		return
	}
	s.render(w, r, "page-report", map[string]any{
		"Report":         rep,
		"ParamValues":    map[string]any{},
		"ReportParams":   s.buildReportParams(r.Context(), s.resolveLang(r), rep.Params),
		"ActiveVariant":  r.FormValue("__variant"),
		"ReportPresets":  presets,
		"ActivePresetID": activePresetID,
		"ActivePreset":   activePreset,
	})
}

func (s *Server) reportRun(w http.ResponseWriter, r *http.Request) {
	rep := s.getReport(w, r)
	if rep == nil {
		return
	}
	if !s.requirePerm(w, r, "report", rep.Name, "run") {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, s.errText(r, err), 400)
		return
	}
	s.runReport(w, r, rep, reportParamValuesFromRequest(r, rep))
}

func reportParamValuesFromRequest(r *http.Request, rep *reportpkg.Report) map[string]any {
	paramValues := make(map[string]any, len(rep.Params))
	for _, p := range rep.Params {
		val := r.FormValue(p.Name)
		if val == "" {
			paramValues[p.Name] = nil
		} else {
			paramValues[p.Name] = val
		}
	}
	return paramValues
}

func (s *Server) getReport(w http.ResponseWriter, r *http.Request) *reportpkg.Report {
	name := chi.URLParam(r, "name")
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	rep := s.reg.GetReport(name)
	if rep == nil {
		http.Error(w, "unknown report: "+name, 404)
		return nil
	}
	return rep
}

func (s *Server) runReport(w http.ResponseWriter, r *http.Request, rep *reportpkg.Report, paramValues map[string]any) {
	// Выбранный вариант компоновки (параметр __variant); пусто → основной.
	variant := r.FormValue("__variant")
	user := currentUserLogin(r)
	var presets []storage.ReportPreset
	if reportSupportsRuntimeSettings(rep) {
		presets = loadReportPresets(r.Context(), s.store, rep.Name, user)
	}
	rs := s.reportSettingsForRequest(r, rep)
	settings := rs.Settings
	activePreset := activeReportPreset(presets, rs.ActivePresetID)
	if settings != nil {
		variant = settings.Variant
	}
	comp := effectiveComposition(rep, settings)
	settingsJSON := reportSettingsPanelJSON(rep, settings)
	// Build query params: convert date strings to time.Time for proper PG type inference.
	// Keep paramValues unchanged so the form repopulates with the original strings.
	queryValues := make(map[string]any, len(paramValues))
	for k, v := range paramValues {
		queryValues[k] = v
	}
	for _, p := range rep.Params {
		switch p.Type {
		case "date":
			if str, ok := queryValues[p.Name].(string); ok && str != "" {
				if t, err2 := time.ParseInLocation("2006-01-02", str, time.Local); err2 == nil {
					queryValues[p.Name] = t
				}
			}
		case "bool":
			str, _ := queryValues[p.Name].(string)
			queryValues[p.Name] = parseParamValue(str, "bool")
		}
	}
	compiled, err := query.Compile(rep.Query, query.CompileOpts{
		Entities:    s.reg.Entities(),
		Params:      queryValues,
		Registers:   s.reg.Registers(),
		InfoRegs:    s.reg.InfoRegisters(),
		AccountRegs: s.reg.AccountRegisters(),
		Dialect:     s.store.Dialect(),
	})
	reportParams := s.buildReportParams(r.Context(), s.resolveLang(r), rep.Params)
	if err != nil {
		s.render(w, r, "page-report", map[string]any{
			"Report":             rep,
			"QueryError":         err.Error(),
			"ParamValues":        paramValues,
			"ReportParams":       reportParams,
			"ActiveVariant":      variant,
			"UserSettings":       settings,
			"ReportSettingsJSON": settingsJSON,
			"ReportPresets":      presets,
			"ActivePresetID":     rs.ActivePresetID,
			"ActivePreset":       activePreset,
		})
		return
	}
	rows, cols, err := s.store.RunQuery(r.Context(), compiled.SQL, compiled.Args)
	if err != nil {
		s.render(w, r, "page-report", map[string]any{
			"Report":             rep,
			"QueryError":         err.Error(),
			"ParamValues":        paramValues,
			"ReportParams":       reportParams,
			"ActiveVariant":      variant,
			"UserSettings":       settings,
			"ReportSettingsJSON": settingsJSON,
			"ReportPresets":      presets,
			"ActivePresetID":     rs.ActivePresetID,
			"ActivePreset":       activePreset,
		})
		return
	}
	detailLinkCol := ""
	if comp != nil {
		detailLinkCol = comp.DetailLink
	}
	s.resolveUUIDsInReport(r.Context(), rows, detailLinkCol)

	// Пользовательские отборы применяются к строкам до компоновки (план 70).
	if comp != nil && settings != nil && len(settings.Filters) > 0 {
		rows = compose.ApplyFilters(rows, settings.Filters)
	}

	if comp != nil {
		ev := newInterpEvaluator(s.interp)
		// Режим кросс-таблицы (pivot): измерения уходят в колонки. График в этом
		// режиме не строится (категории — это столбцы таблицы).
		if len(comp.Columns) > 0 {
			cr, cerr := compose.ComposeCross(rows, *comp, ev)
			if cerr != nil {
				s.render(w, r, "page-report", map[string]any{
					"Report": rep, "QueryError": cerr.Error(),
					"ParamValues": paramValues, "ReportParams": reportParams,
					"ActiveVariant":      variant,
					"UserSettings":       settings,
					"ReportSettingsJSON": settingsJSON,
					"ReportPresets":      presets,
					"ActivePresetID":     rs.ActivePresetID,
					"ActivePreset":       activePreset,
				})
				return
			}
			s.render(w, r, "page-report", map[string]any{
				"Report":             rep,
				"ComposedHTML":       renderCrossTable(cr, comp),
				"Capped":             cr.Capped,
				"ComposeWarnings":    cr.Warnings,
				"ParamValues":        paramValues,
				"ReportParams":       reportParams,
				"ActiveVariant":      variant,
				"UserSettings":       settings,
				"ReportSettingsJSON": settingsJSON,
				"ReportPresets":      presets,
				"ActivePresetID":     rs.ActivePresetID,
				"ActivePreset":       activePreset,
				"ReportCols":         cols,
			})
			return
		}
		res, cerr := compose.Compose(rows, *comp, ev)
		if cerr != nil {
			s.render(w, r, "page-report", map[string]any{
				"Report": rep, "QueryError": cerr.Error(),
				"ParamValues": paramValues, "ReportParams": reportParams,
				"ActiveVariant":      variant,
				"UserSettings":       settings,
				"ReportSettingsJSON": settingsJSON,
				"ReportPresets":      presets,
				"ActivePresetID":     rs.ActivePresetID,
				"ActivePreset":       activePreset,
			})
			return
		}
		var chartOption map[string]any
		if comp.Chart != nil {
			chartOption = buildComposedChart(res, comp.Chart, rows, *comp, ev)
		}
		s.render(w, r, "page-report", map[string]any{
			"Report":             rep,
			"ComposedHTML":       renderComposedTable(res, comp),
			"Capped":             res.Capped,
			"ComposeWarnings":    res.Warnings,
			"ChartOption":        chartOption,
			"ParamValues":        paramValues,
			"ReportParams":       reportParams,
			"ActiveVariant":      variant,
			"UserSettings":       settings,
			"ReportSettingsJSON": settingsJSON,
			"ReportPresets":      presets,
			"ActivePresetID":     rs.ActivePresetID,
			"ActivePreset":       activePreset,
			"ReportCols":         cols,
		})
		return
	}

	var chartOption map[string]any
	if rep.ChartProc != "" {
		chartOption = s.runChartProc(r.Context(), rep, rows, paramValues)
	}

	s.render(w, r, "page-report", map[string]any{
		"Report":             rep,
		"Cols":               cols,
		"Rows":               rows,
		"ParamValues":        paramValues,
		"ChartOption":        chartOption,
		"ReportParams":       reportParams,
		"ActiveVariant":      variant,
		"UserSettings":       settings,
		"ReportSettingsJSON": settingsJSON,
		"ReportPresets":      presets,
		"ActivePresetID":     rs.ActivePresetID,
		"ActivePreset":       activePreset,
		"ReportCols":         cols,
	})
}

func (s *Server) runChartProc(ctx context.Context, rep *reportpkg.Report, rows []map[string]any, paramValues map[string]any) map[string]any {
	procDecl := s.reg.GetProcedure(rep.Name, rep.ChartProc)
	if procDecl == nil {
		procDecl = s.reg.GetModuleProc(rep.ChartProc)
	}
	if procDecl == nil {
		return nil
	}

	mc := runtime.NewMovementsCollector("report", uuid.Nil)
	dslVars := s.buildDSLVars(ctx, mc)

	resultArray := &interpreter.Array{}
	for _, row := range rows {
		st := interpreter.NewStructFromMap(row)
		resultArray.CallMethod("добавить", []any{st})
	}
	dslVars["Результат"] = resultArray
	dslVars["Result"] = resultArray
	dslVars["Параметры"] = &interpreter.MapThis{M: paramValues}

	var result any
	if err := s.interp.RunWithResult(procDecl, &interpreter.MapThis{M: paramValues}, &result, dslVars); err != nil {
		return nil
	}

	chart, ok := result.(*interpreter.Chart)
	if !ok {
		return nil
	}
	return chart.ToEChartsOption()
}

// resolveUUIDsInReport replaces UUID-looking strings in report rows with entity
// display names. skipCol (если непустой) исключается из подстановки — нужен для
// колонки detail_link, где UUID должен остаться для ссылки на документ (issue #87).
func (s *Server) resolveUUIDsInReport(ctx context.Context, rows []map[string]any, skipCol string) {
	uuidToLabel := make(map[string]string)
	for _, row := range rows {
		for _, v := range row {
			if str, ok := v.(string); ok {
				if _, err := uuid.Parse(str); err == nil {
					uuidToLabel[str] = ""
				}
			}
		}
	}
	if len(uuidToLabel) == 0 {
		return
	}
	for _, entity := range s.reg.Entities() {
		for idStr, label := range uuidToLabel {
			if label != "" {
				continue
			}
			id, _ := uuid.Parse(idStr)
			if refRow, err := s.store.GetByID(ctx, entity.Name, id, entity); err == nil {
				uuidToLabel[idStr] = firstStringField(refRow, entity)
			}
		}
	}
	applyResolvedLabels(rows, uuidToLabel, skipCol)
}

// applyResolvedLabels подставляет наименования вместо UUID в строках отчёта,
// пропуская колонку skipCol. Колонка detail_link хранит UUID регистратора для
// ссылки на документ — его нельзя превращать в имя, иначе drill-down строит
// /ui/.../<имя> вместо /<uuid> и ломается (issue #87). Имя колонки сравнивается
// регистронезависимо: колонки запроса приходят в нижнем регистре, а detail_link
// в composition — в исходном.
func applyResolvedLabels(rows []map[string]any, uuidToLabel map[string]string, skipCol string) {
	for _, row := range rows {
		for col, v := range row {
			if skipCol != "" && strings.EqualFold(col, skipCol) {
				continue
			}
			if str, ok := v.(string); ok {
				if label, found := uuidToLabel[str]; found && label != "" {
					row[col] = label
				}
			}
		}
	}
}

// reportParamUI is a template-friendly wrapper around a report parameter.
type reportParamUI struct {
	Name    string
	Label   string
	Type    string // raw type string
	IsDate  bool
	IsNum   bool
	IsBool  bool
	IsSel   bool
	IsRef   bool
	Options []string         // for IsSel
	Opts    []map[string]any // for IsRef: [{id, _label}]
	// RefEntity — имя сущности (для IsRef), используется на UI для лупы
	// в picker'е (открытие карточки через /ui/_ref-open/<entity>/<id>).
	RefEntity string
}

// buildReportParams builds UI-ready param descriptors, loading reference options inline.
func (s *Server) buildReportParams(ctx context.Context, lang string, params []reportpkg.Param) []reportParamUI {
	out := make([]reportParamUI, 0, len(params))
	for _, p := range params {
		ui := reportParamUI{
			Name:  p.Name,
			Label: p.DisplayLabel(lang),
			Type:  p.Type,
		}
		switch {
		case p.Type == "date":
			ui.IsDate = true
		case p.Type == "number":
			ui.IsNum = true
		case p.Type == "bool":
			ui.IsBool = true
		case p.Type == "select":
			ui.IsSel = true
			ui.Options = p.Options
		case strings.HasPrefix(p.Type, "reference:"):
			ui.IsRef = true
			entityName := strings.TrimPrefix(p.Type, "reference:")
			ui.RefEntity = entityName
			if entity := s.reg.GetEntity(entityName); entity != nil {
				rows, _ := s.store.List(ctx, entity.Name, entity, storage.ListParams{})
				rows = filterOutFolders(rows)
				for _, row := range rows {
					row["_label"] = firstStringField(row, entity)
				}
				ui.Opts = rows
			}
		}
		out = append(out, ui)
	}
	return out
}

// loadReportRefOpts returns select options for report params with type "reference:EntityName".
func (s *Server) loadReportRefOpts(ctx context.Context, params []reportpkg.Param) map[string][]map[string]any {
	opts := make(map[string][]map[string]any)
	for _, p := range params {
		if !strings.HasPrefix(p.Type, "reference:") {
			continue
		}
		entityName := strings.TrimPrefix(p.Type, "reference:")
		entity := s.reg.GetEntity(entityName)
		if entity == nil {
			continue
		}
		rows, err := s.store.List(ctx, entity.Name, entity, storage.ListParams{})
		if err != nil {
			continue
		}
		rows = filterOutFolders(rows)
		for _, row := range rows {
			row["_label"] = firstStringField(row, entity)
		}
		opts[p.Name] = rows
	}
	return opts
}

type reportExportError struct {
	status int
	prefix string
	err    error
}

func (e *reportExportError) Error() string {
	return e.err.Error()
}

func newReportExportError(status int, prefix string, err error) error {
	return &reportExportError{status: status, prefix: prefix, err: err}
}

func (s *Server) writeReportExportError(w http.ResponseWriter, r *http.Request, err error) {
	if ee, ok := err.(*reportExportError); ok {
		http.Error(w, ee.prefix+": "+s.errText(r, ee.err), ee.status)
		return
	}
	http.Error(w, "report export error: "+s.errText(r, err), http.StatusInternalServerError)
}

// reportExcel runs a report query with GET params and returns XLSX.
// reportExportRows вычисляет табличные данные отчёта для выгрузки (Excel/PDF):
// заголовки и строки с учётом эффективной компоновки (план 70), кросс-таблицы
// (pivot) или плоского результата. Параметры берутся из query-строки (экспорт —
// GET). HTTP-ответ об ошибке пишет вызывающий. Общий код reportExcel и reportPDF
// (issue #218) — чтобы обе выгрузки шли из одних и тех же данных и не расходились
// с экраном (runReport использует те же crossSheetRows/composedRows).
func (s *Server) reportExportRows(r *http.Request, rep *reportpkg.Report) (headers []string, rows [][]any, err error) {
	settings := s.reportSettingsForRequest(r, rep).Settings
	comp := effectiveComposition(rep, settings)
	paramValues := make(map[string]any, len(rep.Params))
	for _, p := range rep.Params {
		val := r.URL.Query().Get(p.Name)
		if p.Type == "bool" {
			paramValues[p.Name] = parseParamValue(val, "bool")
			continue
		}
		if val == "" {
			paramValues[p.Name] = nil
		} else if p.Type == "date" {
			if t, perr := time.ParseInLocation("2006-01-02", val, time.Local); perr == nil {
				paramValues[p.Name] = t
			} else {
				paramValues[p.Name] = val
			}
		} else {
			paramValues[p.Name] = val
		}
	}
	compiled, err := query.Compile(rep.Query, query.CompileOpts{
		Entities:    s.reg.Entities(),
		Params:      paramValues,
		Registers:   s.reg.Registers(),
		InfoRegs:    s.reg.InfoRegisters(),
		AccountRegs: s.reg.AccountRegisters(),
		Dialect:     s.store.Dialect(),
	})
	if err != nil {
		return nil, nil, newReportExportError(http.StatusBadRequest, "query compile error", err)
	}
	data, cols, err := s.store.RunQuery(r.Context(), compiled.SQL, compiled.Args)
	if err != nil {
		return nil, nil, newReportExportError(http.StatusInternalServerError, "query error", err)
	}
	detailLinkCol := ""
	if comp != nil {
		detailLinkCol = comp.DetailLink
	}
	s.resolveUUIDsInReport(r.Context(), data, detailLinkCol)

	// Пользовательские отборы (план 70) — до компоновки, как в runReport.
	if comp != nil && settings != nil && len(settings.Filters) > 0 {
		data = compose.ApplyFilters(data, settings.Filters)
	}

	// Если отчёт использует компоновщик — строим дерево групп/итогов.
	if comp != nil {
		ev := newInterpEvaluator(s.interp)
		// Кросс-таблица (pivot): измерения-колонки выгружаются в столбцы листа.
		if len(comp.Columns) > 0 {
			cr, cerr := compose.ComposeCross(data, *comp, ev)
			if cerr != nil {
				return nil, nil, newReportExportError(http.StatusInternalServerError, "compose error", cerr)
			}
			h, xr := crossSheetRows(cr, comp)
			return h, xr, nil
		}
		res, cerr := compose.Compose(data, *comp, ev)
		if cerr != nil {
			return nil, nil, newReportExportError(http.StatusInternalServerError, "compose error", cerr)
		}
		h, xr := composedRows(res, comp)
		return h, xr, nil
	}

	// Плоский путь (без компоновки) — обратная совместимость.
	flat := make([][]any, len(data))
	for i, row := range data {
		cells := make([]any, len(cols))
		for j, col := range cols {
			cells[j] = row[col]
		}
		flat[i] = cells
	}
	return cols, flat, nil
}

func (s *Server) reportExcel(w http.ResponseWriter, r *http.Request) {
	rep := s.getReport(w, r)
	if rep == nil {
		return
	}
	if !s.requirePerm(w, r, "report", rep.Name, "run") {
		return
	}
	headers, rows, err := s.reportExportRows(r, rep)
	if err != nil {
		s.writeReportExportError(w, r, err)
		return
	}
	data, err := excel.ExportList(headers, rows)
	if err != nil {
		http.Error(w, "Excel error: "+s.errText(r, err), 500)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", contentDisposition(rep.Name+".xlsx"))
	w.Write(data)
}
