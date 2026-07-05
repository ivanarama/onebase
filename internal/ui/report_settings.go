package ui

// Рантайм-настройки отчёта (план 70): чтение пользовательских настроек из
// запроса и вычисление эффективной компоновки. Источник правок — панель
// «Настройки» на форме отчёта, которая пишет скрытое поле __settings (JSON
// report.UserReportSettings).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/storage"
)

// maxUserSettingsBytes — потолок размера JSON пользовательских настроек отчёта
// (__settings), сохраняемого в _settings. Защищает от раздувания служебной
// таблицы произвольным вводом (issue #23). 64 КиБ с запасом покрывают любую
// разумную презентационную настройку (выбор/порядок колонок, отборы, сортировка).
const maxUserSettingsBytes = 64 * 1024

const standardReportPresetID = storage.StandardReportPresetID
const defaultReportPresetName = "Мой вариант"

// errSettingsTooLarge — отказ сохранить слишком большой блок __settings (issue #23).
var errSettingsTooLarge = errors.New("настройки отчёта слишком велики")

var errReportPresetNameRequired = errors.New("укажите название варианта отчёта")

type reportSettingsRequest struct {
	Settings       *reportpkg.UserReportSettings
	ActivePresetID string
}

// readReportSettings разбирает пользовательские настройки из поля __settings
// запроса (FormValue читает и POST-форму, и GET-query). Пустое или повреждённое
// значение → nil (поведение отчёта по умолчанию).
func readReportSettings(r *http.Request) *reportpkg.UserReportSettings {
	raw := r.FormValue("__settings")
	if raw == "" {
		return nil
	}
	s, err := reportpkg.ParseUserSettings(raw)
	if err != nil {
		return nil
	}
	return s
}

// reportSettingsWithRequestVariant накладывает явный __variant из запроса поверх
// настроек отчёта. Это важно для селектора вариантов: обычный submit формы шлёт
// только __variant, без __settings, но выбранный вариант всё равно должен менять
// доверенную базовую компоновку.
func reportSettingsWithRequestVariant(r *http.Request, s *reportpkg.UserReportSettings) *reportpkg.UserReportSettings {
	variant, ok := requestFormValue(r, "__variant")
	if !ok {
		return s
	}
	if s == nil {
		s = &reportpkg.UserReportSettings{}
	} else {
		cp := *s
		s = &cp
	}
	s.Variant = variant
	return s
}

func requestFormValue(r *http.Request, name string) (string, bool) {
	if err := r.ParseForm(); err != nil {
		return "", false
	}
	vs, ok := r.Form[name]
	if !ok {
		return "", false
	}
	if len(vs) == 0 {
		return "", true
	}
	return vs[0], true
}

func (s *Server) reportSettingsForRequest(r *http.Request, rep *reportpkg.Report) reportSettingsRequest {
	if !reportSupportsRuntimeSettings(rep) {
		return reportSettingsRequest{}
	}
	presetID, hasPreset := requestFormValue(r, "__preset")
	if settings := readReportSettings(r); settings != nil {
		settings = reportSettingsWithRequestVariant(r, settings)
		return reportSettingsRequest{Settings: settings, ActivePresetID: presetID}
	}
	if s.store == nil {
		return reportSettingsRequest{Settings: reportSettingsWithRequestVariant(r, nil), ActivePresetID: presetID}
	}

	user := currentUserLogin(r)
	if hasPreset {
		if presetID == "" || presetID == standardReportPresetID {
			return reportSettingsRequest{Settings: reportSettingsWithRequestVariant(r, nil), ActivePresetID: standardReportPresetID}
		}
		if p, err := s.store.GetReportPreset(r.Context(), rep.Name, user, presetID); err == nil && p != nil {
			if st, err := reportpkg.ParseUserSettings(p.SettingsJSON); err == nil {
				return reportSettingsRequest{Settings: st, ActivePresetID: p.ID}
			}
		}
		return reportSettingsRequest{Settings: reportSettingsWithRequestVariant(r, nil), ActivePresetID: standardReportPresetID}
	}

	if p, err := s.store.GetDefaultReportPreset(r.Context(), rep.Name, user); err == nil && p != nil {
		if st, err := reportpkg.ParseUserSettings(p.SettingsJSON); err == nil {
			return reportSettingsRequest{Settings: st, ActivePresetID: p.ID}
		}
	}

	// Fallback для баз, где до появления именованных пресетов уже была одна
	// сохранённая настройка в _settings.
	if settings := loadUserSettings(r.Context(), s.store, rep.Name, user); settings != nil {
		settings = reportSettingsWithRequestVariant(r, settings)
		return reportSettingsRequest{Settings: settings}
	}
	return reportSettingsRequest{Settings: reportSettingsWithRequestVariant(r, nil)}
}

func reportSupportsRuntimeSettings(rep *reportpkg.Report) bool {
	if rep == nil {
		return false
	}
	if rep.Composition != nil {
		return true
	}
	for i := range rep.Variants {
		if rep.Variants[i].Composition != nil {
			return true
		}
	}
	return false
}

func loadReportPresets(ctx context.Context, store *storage.DB, report, user string) []storage.ReportPreset {
	if store == nil {
		return nil
	}
	presets, err := store.ListReportPresets(ctx, report, user)
	if err != nil {
		return nil
	}
	out := presets[:0]
	for _, p := range presets {
		if p.ID == standardReportPresetID {
			continue
		}
		out = append(out, p)
	}
	return out
}

func activeReportPreset(presets []storage.ReportPreset, id string) *storage.ReportPreset {
	for i := range presets {
		if presets[i].ID == id {
			return &presets[i]
		}
	}
	return nil
}

// effectiveComposition вычисляет компоновку, по которой строится отчёт.
//
// БЕЗОПАСНОСТЬ (issue #1): база компоновки берётся ИСКЛЮЧИТЕЛЬНО из доверенной
// конфигурации (rep.ActiveComposition(variant)) — YAML отчёта. Пользовательские
// настройки (__settings, клиентский ввод) применяются ТОЛЬКО к презентационным
// аспектам через mergeUserComposition. Исполняемые выражения — Measures[].Expr и
// Conditional[].When — а также условное оформление (Conditional целиком) и
// навигация (DetailLink/DetailEntity, Chart) всегда остаются из доверенного
// блока. Без этого пользователь с правом report:run мог бы прислать произвольное
// DSL-выражение в Composition и исполнить его на сервере (файловые builtins,
// SSRF, при exec.enabled — запуск команд ОС).
func effectiveComposition(rep *reportpkg.Report, s *reportpkg.UserReportSettings) *reportpkg.Composition {
	variant := ""
	if s != nil {
		variant = s.Variant
	}
	base := rep.ActiveComposition(variant)
	if base == nil {
		return nil
	}
	if s == nil || s.Composition == nil {
		return base
	}
	return mergeUserComposition(base, s.Composition)
}

// reportSettingsPanelJSON возвращает JSON, которым панель «Настройка отчёта»
// инициализирует чекбоксы и скрытое поле __settings. Важно: это не индикатор
// пользовательской модификации. Если пользовательских настроек нет, но у отчёта
// есть базовая composition, панель всё равно должна показать её выбранной.
func reportSettingsPanelJSON(rep *reportpkg.Report, s *reportpkg.UserReportSettings) string {
	st := reportSettingsPanelState(rep, s)
	if st == nil {
		return ""
	}
	raw, err := st.JSON()
	if err != nil {
		return ""
	}
	return raw
}

func reportSettingsPanelState(rep *reportpkg.Report, s *reportpkg.UserReportSettings) *reportpkg.UserReportSettings {
	if !reportSupportsRuntimeSettings(rep) {
		return nil
	}
	comp := effectiveComposition(rep, s)
	if comp == nil {
		return nil
	}
	out := &reportpkg.UserReportSettings{}
	if s != nil {
		out.Variant = s.Variant
		out.Filters = append([]reportpkg.Filter(nil), s.Filters...)
	}
	if comp != nil {
		out.Composition = panelComposition(comp)
	}
	if out.Variant == "" && out.Composition == nil && len(out.Filters) == 0 {
		return nil
	}
	return out
}

func panelComposition(c *reportpkg.Composition) *reportpkg.Composition {
	if c == nil {
		return nil
	}
	out := &reportpkg.Composition{
		Groupings:  append([]string(nil), c.Groupings...),
		Columns:    append([]string(nil), c.Columns...),
		Measures:   panelMeasures(c.Measures),
		Totals:     c.Totals,
		Detail:     c.Detail,
		Sort:       append([]reportpkg.SortKey(nil), c.Sort...),
		Appearance: safeAppearance(c.Appearance),
	}
	return out
}

func panelMeasures(in []reportpkg.Measure) []reportpkg.Measure {
	if in == nil {
		return nil
	}
	out := make([]reportpkg.Measure, 0, len(in))
	for _, m := range in {
		// В панель уходит только презентация показателей. Expr не нужен для UI
		// и при обратной отправке всё равно берётся только из доверенной YAML-базы.
		out = append(out, reportpkg.Measure{
			Field:  m.Field,
			Agg:    m.Agg,
			Title:  m.Title,
			Align:  m.Align,
			Format: m.Format,
		})
	}
	return out
}

// mergeUserComposition строит итоговую компоновку на базе доверенной (base) и
// накладывает только БЕЗОПАСНЫЕ презентационные правки из пользовательской (u):
//
//   - набор/порядок группировок и колонок (Groupings, Columns) — данные, не код;
//   - набор/порядок и презентация показателей (Measures): для каждого показателя
//     пользователя берём презентационные поля (Agg/Title/Align/Format), но Expr
//     ВСЕГДА из доверенного показателя с тем же Field; показатель, которого нет
//     в доверенной компоновке, добавляется БЕЗ Expr (Expr обнуляется), чтобы
//     инъекция вычисляемого показателя не исполнялась;
//   - сортировка (Sort), итоги (Totals), детальные строки (Detail);
//   - общее оформление вывода (Appearance: линии сетки, зебра) — чистая
//     презентация без исполняемых выражений, поэтому берётся из пользовательского
//     ввода; Lines канонизируется safeAppearance (мусор → "").
//
// Из доверенной компоновки целиком наследуются ИСПОЛНЯЕМЫЕ и навигационные
// аспекты: Conditional (When+Style — When исполняется!), Chart, DetailLink,
// DetailEntity. Они НИКОГДА не берутся из пользовательского ввода.
func mergeUserComposition(base, u *reportpkg.Composition) *reportpkg.Composition {
	if base == nil {
		// Нет доверенной базы (отчёт без composition) — пользовательский ввод не
		// должен переводить плоский отчёт в режим СКД.
		return nil
	}
	// Доверенные показатели и поля по имени (регистронезависимо, как DSL).
	// Expr всегда наследуется только отсюда; презентационные поля служат
	// дефолтами, если пользовательский JSON пришёл частичным.
	trustedExpr := make(map[string]string, len(base.Measures))
	trustedMeasure := make(map[string]reportpkg.Measure, len(base.Measures))
	canonicalField := make(map[string]string)
	addCanonical := func(field string) {
		if field != "" {
			canonicalField[strings.ToLower(field)] = field
		}
	}
	for _, g := range base.Groupings {
		addCanonical(g)
	}
	for _, c := range base.Columns {
		addCanonical(c)
	}
	for _, m := range base.Measures {
		key := strings.ToLower(m.Field)
		trustedExpr[key] = m.Expr
		trustedMeasure[key] = m
		addCanonical(m.Field)
	}
	for _, sk := range base.Sort {
		addCanonical(sk.Field)
	}

	out := *base // копия: Conditional/Chart/DetailLink/DetailEntity — из доверенной.

	// Презентационные коллекции берём из пользовательского ввода, но только если
	// поле реально присутствовало. Старый UI отправлял частичный JSON и тем самым
	// случайно очищал Columns/Sort/Totals/Detail базовой СКД. Пустой набор
	// группировок или показателей для отчёта, где YAML задаёт их явно, считаем
	// повреждённой настройкой и не даём ей стереть стандартную компоновку.
	invalidEmptyGroupings := len(base.Groupings) > 0 && u.Groupings != nil && len(u.Groupings) == 0
	invalidEmptyMeasures := len(base.Measures) > 0 && u.Measures != nil && len(u.Measures) == 0
	invalidEmptyComposition := invalidEmptyGroupings || invalidEmptyMeasures
	if u.Groupings != nil && !invalidEmptyComposition {
		out.Groupings = canonicalStrings(u.Groupings, canonicalField)
	}
	if u.Columns != nil && !invalidEmptyComposition {
		out.Columns = canonicalStrings(u.Columns, canonicalField)
	}
	if u.Sort != nil {
		out.Sort = canonicalSortKeys(u.Sort, canonicalField)
	}
	// Totals/Detail пока не редактируются в runtime-панели, поэтому пустое
	// значение не должно стирать доверенную базу. Ненулевое значение применяем
	// для обратной совместимости с уже существующим JSON настроек.
	if u.Totals.Grand || u.Totals.Subtotals {
		out.Totals = u.Totals
	}
	if u.Detail {
		out.Detail = true
	}
	out.Appearance = safeAppearance(u.Appearance) // презентация: безопасно из пользователя

	// Показатели: презентация — из пользовательского ввода, Expr — только из
	// доверенной компоновки (по совпадению Field), иначе обнуляем.
	if u.Measures != nil && len(u.Measures) > 0 && !invalidEmptyComposition {
		measures := make([]reportpkg.Measure, 0, len(u.Measures))
		for _, m := range u.Measures {
			baseMeasure := trustedMeasure[strings.ToLower(m.Field)]
			field := m.Field
			if baseMeasure.Field != "" {
				field = baseMeasure.Field
			}
			safe := reportpkg.Measure{
				Field:  field,
				Agg:    firstNonEmpty(m.Agg, baseMeasure.Agg),
				Title:  firstNonEmpty(m.Title, baseMeasure.Title),
				Align:  firstNonEmpty(m.Align, baseMeasure.Align),
				Format: firstNonEmpty(m.Format, baseMeasure.Format),
				// Expr НЕ из пользовательского ввода: берём доверенное значение
				// по имени поля; если показателя нет в доверенной — Expr пуст.
				Expr: trustedExpr[strings.ToLower(m.Field)],
			}
			measures = append(measures, safe)
		}
		out.Measures = measures
	}
	return &out
}

func canonicalStrings(in []string, canonical map[string]string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if c := canonical[strings.ToLower(v)]; c != "" {
			out = append(out, c)
			continue
		}
		out = append(out, v)
	}
	return out
}

func canonicalSortKeys(in []reportpkg.SortKey, canonical map[string]string) []reportpkg.SortKey {
	if in == nil {
		return nil
	}
	out := make([]reportpkg.SortKey, 0, len(in))
	for _, sk := range in {
		if c := canonical[strings.ToLower(sk.Field)]; c != "" {
			sk.Field = c
		}
		out = append(out, sk)
	}
	return out
}

func firstNonEmpty(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

func reportGroupChecked(rep *reportpkg.Report, settings *reportpkg.UserReportSettings, field string) bool {
	state := reportSettingsPanelState(rep, settings)
	if state == nil || state.Composition == nil {
		return false
	}
	return containsFold(state.Composition.Groupings, field)
}

func reportMeasureChecked(rep *reportpkg.Report, settings *reportpkg.UserReportSettings, field string) bool {
	state := reportSettingsPanelState(rep, settings)
	if state == nil || state.Composition == nil {
		return false
	}
	for _, m := range state.Composition.Measures {
		if strings.EqualFold(m.Field, field) {
			return true
		}
	}
	return false
}

func containsFold(values []string, want string) bool {
	for _, v := range values {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}

// safeAppearance канонизирует оформление из пользовательского ввода (__settings —
// JSON, минует compform.normLines): Lines сводится к известным значениям, иначе
// "" (исторический вид). Рендер (appearanceClass) и так игнорирует неизвестный
// Lines, но канонизация не даёт мусору оседать в _settings. Zebra — bool, как есть.
func safeAppearance(a reportpkg.Appearance) reportpkg.Appearance {
	switch a.Lines {
	case "vertical", "both", "none": // допустимые
	default:
		a.Lines = ""
	}
	return a
}

// loadUserSettings загружает сохранённые рантайм-настройки отчёта пользователя
// из _settings. Нет настроек или повреждённый JSON → nil (стандартный вид).
func loadUserSettings(ctx context.Context, store *storage.DB, report, user string) *reportpkg.UserReportSettings {
	raw, err := store.GetReportUserSettings(ctx, report, user)
	if err != nil || raw == "" {
		return nil
	}
	st, err := reportpkg.ParseUserSettings(raw)
	if err != nil {
		return nil
	}
	return st
}

// currentUserLogin возвращает логин текущего пользователя или "" для анонимной/
// однопользовательской сессии (настройки хранятся под пустым пользователем).
func currentUserLogin(r *http.Request) string {
	if u := auth.UserFromContext(r.Context()); u != nil {
		return u.Login
	}
	return ""
}

// reportFormURL — путь формы отчёта для редиректа после save/reset.
func reportFormURL(name string) string {
	return "/ui/report/" + url.PathEscape(strings.ToLower(name))
}

func reportFormURLWithPreset(name, presetID string) string {
	u := reportFormURL(name)
	if presetID == "" {
		return u
	}
	return u + "?__preset=" + url.QueryEscape(presetID)
}

func reportRunURLAfterSettingsSave(name string, params []reportpkg.Param, r *http.Request, presetID string) string {
	u, err := url.Parse(reportFormURL(name))
	if err != nil {
		return reportFormURLWithPreset(name, presetID)
	}
	q := u.Query()
	q.Set("__run", "1")
	if presetID != "" {
		q.Set("__preset", presetID)
	}
	for _, p := range params {
		val, ok := requestFormValue(r, p.Name)
		if ok && val != "" {
			q.Set(p.Name, val)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func reportPresetNameOrDefault(ctx context.Context, store *storage.DB, report, user, name string) string {
	name = strings.TrimSpace(name)
	if name != "" {
		return name
	}
	if store == nil {
		return defaultReportPresetName
	}
	presets, err := store.ListReportPresets(ctx, report, user)
	if err != nil {
		return defaultReportPresetName
	}
	used := make(map[string]struct{}, len(presets))
	for _, p := range presets {
		if p.ID == standardReportPresetID {
			continue
		}
		if n := strings.TrimSpace(p.Name); n != "" {
			used[strings.ToLower(n)] = struct{}{}
		}
	}
	if _, ok := used[strings.ToLower(defaultReportPresetName)]; !ok {
		return defaultReportPresetName
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s %d", defaultReportPresetName, i)
		if _, ok := used[strings.ToLower(candidate)]; !ok {
			return candidate
		}
	}
}

// reportSettingsSave сохраняет рантайм-настройки текущего пользователя (POST
// поля __settings) и возвращает на форму отчёта.
func (s *Server) reportSettingsSave(w http.ResponseWriter, r *http.Request) {
	rep := s.getReport(w, r)
	if rep == nil {
		return
	}
	if !s.requirePerm(w, r, "report", rep.Name, "run") {
		return
	}
	action := r.FormValue("__preset_action")
	raw := r.FormValue("__settings")
	// Лимит размера: не пускаем в _settings произвольно большой ввод (issue #23).
	if len(raw) > maxUserSettingsBytes {
		http.Error(w, s.errText(r, errSettingsTooLarge), http.StatusRequestEntityTooLarge)
		return
	}
	// Валидируем JSON и сохраняем реканонизированный вид (issue #23): битый JSON
	// отвергаем, а каноничная сериализация отбрасывает мусорные/лишние поля.
	st, err := reportpkg.ParseUserSettings(raw)
	if err != nil {
		http.Error(w, s.errText(r, err), http.StatusBadRequest)
		return
	}
	st = reportSettingsWithRequestVariant(r, st)
	canon := ""
	if normalized := reportSettingsPanelState(rep, st); normalized != nil {
		if canon, err = normalized.JSON(); err != nil {
			http.Error(w, s.errText(r, err), http.StatusBadRequest)
			return
		}
	}
	if len(canon) > maxUserSettingsBytes {
		http.Error(w, s.errText(r, errSettingsTooLarge), http.StatusRequestEntityTooLarge)
		return
	}

	// Обратная совместимость: старые формы без __preset_action по-прежнему
	// сохраняют единственную настройку в _settings.
	if action == "" {
		_ = s.store.SaveReportUserSettings(r.Context(), rep.Name, currentUserLogin(r), canon)
		http.Redirect(w, r, reportRunURLAfterSettingsSave(rep.Name, rep.Params, r, ""), http.StatusSeeOther)
		return
	}

	user := currentUserLogin(r)
	presetID := r.FormValue("__preset")
	name := strings.TrimSpace(r.FormValue("__preset_name"))
	isDefault := r.FormValue("__preset_default") != ""

	if presetID == standardReportPresetID {
		presetID = ""
		_ = s.store.DeleteReportPreset(r.Context(), rep.Name, user, standardReportPresetID)
	}
	if action == "save" && presetID != "" {
		if p, err := s.store.GetReportPreset(r.Context(), rep.Name, user, presetID); err == nil && p != nil && p.ID != standardReportPresetID {
			if name == "" {
				name = p.Name
			}
		} else {
			presetID = ""
		}
	}
	if action == "save_as" {
		presetID = ""
	}
	name = reportPresetNameOrDefault(r.Context(), s.store, rep.Name, user, name)
	if name == "" {
		http.Error(w, s.errText(r, errReportPresetNameRequired), http.StatusBadRequest)
		return
	}
	id, err := s.store.SaveReportPreset(r.Context(), storage.ReportPreset{
		ID:           presetID,
		Report:       rep.Name,
		User:         user,
		Name:         name,
		SettingsJSON: canon,
		IsDefault:    isDefault,
	})
	if err != nil {
		if errors.Is(err, storage.ErrReportPresetNameExists) {
			http.Error(w, s.errText(r, err), http.StatusConflict)
			return
		}
		http.Error(w, s.errText(r, err), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, reportRunURLAfterSettingsSave(rep.Name, rep.Params, r, id), http.StatusSeeOther)
}

func (s *Server) reportPresetDelete(w http.ResponseWriter, r *http.Request) {
	rep := s.getReport(w, r)
	if rep == nil {
		return
	}
	if !s.requirePerm(w, r, "report", rep.Name, "run") {
		return
	}
	presetID := r.FormValue("__preset")
	if presetID != "" && presetID != standardReportPresetID {
		_ = s.store.DeleteReportPreset(r.Context(), rep.Name, currentUserLogin(r), presetID)
	}
	http.Redirect(w, r, reportFormURLWithPreset(rep.Name, standardReportPresetID), http.StatusSeeOther)
}

// reportSettingsReset удаляет рантайм-настройки текущего пользователя — возврат
// к стандартному виду из конфигурации — и возвращает на форму отчёта.
func (s *Server) reportSettingsReset(w http.ResponseWriter, r *http.Request) {
	rep := s.getReport(w, r)
	if rep == nil {
		return
	}
	if !s.requirePerm(w, r, "report", rep.Name, "run") {
		return
	}
	_ = s.store.DeleteReportUserSettings(r.Context(), rep.Name, currentUserLogin(r))
	http.Redirect(w, r, reportFormURLWithPreset(rep.Name, standardReportPresetID), http.StatusSeeOther)
}
