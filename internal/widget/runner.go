// Package widget runs dashboard widgets defined as metadata.Widget on top of
// the existing Query Language pipeline.
package widget

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/shopspring/decimal"
)

// Result is the rendered output of a single widget. Type mirrors metadata.WidgetType
// so templates can switch on a single field; other fields are populated based on type.
type Result struct {
	Name  string
	Type  string
	Title string
	Span  int
	Error string
	// AccessDenied — у пользователя нет прав на источник данных виджета (или на
	// все его кнопки-действия). Дашборд такие карточки не рендерит вовсе, в
	// отличие от настоящих ошибок (compile/SQL), которые остаются видимыми.
	AccessDenied bool

	// kpi
	KPI *KPIResult

	// list / recent
	Rows    []map[string]any
	Columns []ColumnSpec

	// chart
	Chart *ChartData

	// actions
	Actions []ActionLink

	// Link — внутренний адрес, на который ведёт клик по карточке (см.
	// metadata.Widget.Link). Пусто — карточка не кликабельна, как раньше.
	Link string
}

// KPIResult holds the single numeric value rendered by a KPI widget.
type KPIResult struct {
	Value   any    // primary number (or string if textual)
	Display string // pre-formatted text for direct rendering
}

// ColumnSpec describes how to render a column for list/recent widgets.
type ColumnSpec struct {
	Field  string
	Label  string
	Format string
	Align  string
}

// ChartData carries pre-shaped data for the chart widget. The UI layer turns
// this into an ECharts options object client-side.
type ChartData struct {
	Kind   string   // bar | line | pie
	XAxis  []string // category labels (bar/line) or slice labels (pie)
	Series []ChartSeries
}

// ChartSeries holds one series of values aligned with ChartData.XAxis.
type ChartSeries struct {
	Name string
	Data []float64
}

// EChartsOption builds the ECharts option map for a chart widget. It is the
// single source of truth for how dashboard charts look: both the base UI
// (рабочий стол) and the launcher's configurator (предпросмотр виджета)
// serialize this same map, so the preview matches what the user will see —
// включая сглаживание линий (smooth) и круговую диаграмму.
func EChartsOption(chart *ChartData) map[string]any {
	if chart == nil {
		return nil
	}
	opt := map[string]any{
		"tooltip": map[string]any{"trigger": "axis"},
		"grid":    map[string]any{"left": 56, "right": 16, "top": 24, "bottom": 30},
	}
	switch strings.ToLower(chart.Kind) {
	case "pie":
		var data []map[string]any
		if len(chart.Series) > 0 {
			s := chart.Series[0]
			for i, label := range chart.XAxis {
				if i >= len(s.Data) {
					break
				}
				data = append(data, map[string]any{"name": label, "value": s.Data[i]})
			}
		}
		opt["tooltip"] = map[string]any{"trigger": "item"}
		opt["series"] = []map[string]any{{
			"type":   "pie",
			"radius": []string{"40%", "70%"},
			"data":   data,
		}}
	default:
		seriesType := "bar"
		if strings.EqualFold(chart.Kind, "line") {
			seriesType = "line"
		}
		var series []map[string]any
		for _, s := range chart.Series {
			series = append(series, map[string]any{
				"name":   s.Name,
				"type":   seriesType,
				"data":   s.Data,
				"smooth": seriesType == "line",
			})
		}
		opt["xAxis"] = map[string]any{"type": "category", "data": chart.XAxis}
		opt["yAxis"] = map[string]any{"type": "value"}
		opt["series"] = series
	}
	return opt
}

// ActionLink is a single rendered button on the actions widget.
type ActionLink struct {
	Label  string
	URL    string
	NewTab bool
}

// Runner executes widgets against the database. It holds references to the
// shared registry and storage; it is safe to reuse per request.
type Runner struct {
	Reg         *runtime.Registry
	Store       *storage.DB
	CurrentUser string // login of the user looking at the dashboard (for recent.scope=current_user)
	User        *auth.User
	Cache       *Cache // optional — key includes widget, user and authorization fingerprint
}

// New creates a Runner. The Resolve hook is optional — when non-nil it is
// invoked on every row of list/chart widgets to map raw UUIDs back to display
// names, similar to what reports do.
func New(reg *runtime.Registry, store *storage.DB) *Runner {
	return &Runner{Reg: reg, Store: store}
}

// Run executes a single widget. It never returns an error: any failure is
// captured in Result.Error so the dashboard keeps rendering other widgets.
// When a Cache is configured, results are reused inside its TTL window. The
// "actions" widget type is purely declarative so it skips the cache.
func (r *Runner) Run(ctx context.Context, w *metadata.Widget) Result {
	if r.Cache != nil && w.Type != metadata.WidgetTypeActions {
		security, cacheable := securityFingerprint(r.User)
		if !cacheable {
			return r.runOnce(ctx, w)
		}
		key := cacheKey(w.Name, r.CurrentUser, security)
		if cached, ok := r.Cache.get(key); ok {
			return cached
		}
		res := r.runOnce(ctx, w)
		// Don't cache transient errors — they're often "compile" errors during
		// the editing loop, and a stale failure looks worse than a fresh retry.
		if res.Error == "" {
			r.Cache.put(key, res)
		}
		return res
	}
	return r.runOnce(ctx, w)
}

func (r *Runner) runOnce(ctx context.Context, w *metadata.Widget) Result {
	res := Result{Name: w.Name, Type: string(w.Type), Title: w.Title, Link: safeWidgetLink(w.Link)}
	switch w.Type {
	case metadata.WidgetTypeKPI:
		r.runKPI(ctx, w, &res)
	case metadata.WidgetTypeList:
		r.runList(ctx, w, &res)
	case metadata.WidgetTypeChart:
		r.runChart(ctx, w, &res)
	case metadata.WidgetTypeActions:
		r.runActions(w, &res)
	case metadata.WidgetTypeRecent:
		r.runRecent(ctx, w, &res)
	default:
		res.Error = "неизвестный тип виджета: " + string(w.Type)
	}
	return res
}

func (r *Runner) runKPI(ctx context.Context, w *metadata.Widget, res *Result) {
	rows, _, err := r.runQuery(ctx, w)
	if err != nil {
		setResultError(res, err)
		return
	}
	if len(rows) == 0 {
		res.KPI = &KPIResult{Display: formatKPI(0, w.Format)}
		return
	}
	val := firstScalar(rows[0])
	res.KPI = &KPIResult{Value: val, Display: formatKPI(val, w.Format)}
}

func (r *Runner) runList(ctx context.Context, w *metadata.Widget, res *Result) {
	rows, cols, err := r.runQuery(ctx, w)
	if err != nil {
		setResultError(res, err)
		return
	}
	if w.Limit > 0 && len(rows) > w.Limit {
		rows = rows[:w.Limit]
	}
	r.resolveUUIDs(ctx, rows)
	res.Rows = rows
	res.Columns = columnsForList(w, cols)
}

func (r *Runner) runChart(ctx context.Context, w *metadata.Widget, res *Result) {
	rows, cols, err := r.runQuery(ctx, w)
	if err != nil {
		setResultError(res, err)
		return
	}
	x := resolveFieldName(cols, w.XField)
	if x == "" && len(cols) > 0 {
		x = cols[0]
	}
	// Для каждой серии храним подпись (label — как объявлено в y_fields, с
	// исходным регистром) отдельно от ключа данных (key — фактическое имя
	// колонки, которое SQLite мог привести к нижнему регистру). Иначе в легенде
	// «Приход» превращался бы в «приход».
	type chartY struct{ label, key string }
	yFields := make([]chartY, 0, len(w.YFields))
	if len(w.YFields) > 0 {
		for _, yf := range w.YFields {
			if resolved := resolveFieldName(cols, yf); resolved != "" {
				yFields = append(yFields, chartY{label: yf, key: resolved})
			}
		}
	}
	if len(yFields) == 0 {
		for _, c := range cols {
			if !strings.EqualFold(c, x) {
				yFields = append(yFields, chartY{label: c, key: c})
			}
		}
	}
	r.resolveUUIDs(ctx, rows)
	chart := &ChartData{Kind: w.ChartKind}
	for _, row := range rows {
		v := row[x]
		label := fmt.Sprintf("%v", v)
		if t, ok := v.(time.Time); ok {
			label = t.Format("02.01")
		} else if v == nil {
			label = ""
		}
		chart.XAxis = append(chart.XAxis, label)
	}
	for _, yf := range yFields {
		s := ChartSeries{Name: yf.label}
		for _, row := range rows {
			s.Data = append(s.Data, toFloat(row[yf.key]))
		}
		chart.Series = append(chart.Series, s)
	}
	res.Chart = chart
}

// resolveFieldName matches a YAML-declared field name against the columns
// actually returned by the query, case-insensitively. SQL backends often
// lowercase identifiers (PostgreSQL most aggressively), so a widget declaring
// `x_field: Дата` may have to look up "дата" in the row map.
func resolveFieldName(cols []string, declared string) string {
	if declared == "" {
		return ""
	}
	for _, c := range cols {
		if c == declared {
			return c
		}
	}
	lower := strings.ToLower(declared)
	for _, c := range cols {
		if strings.ToLower(c) == lower {
			return c
		}
	}
	return ""
}

func (r *Runner) runActions(w *metadata.Widget, res *Result) {
	deniedSome := false
	for _, item := range w.Items {
		link := ActionLink{Label: item.Label, NewTab: item.NewTab}
		switch {
		case item.URL != "":
			link.URL = item.URL
		case item.Entity != "":
			ent := r.Reg.GetEntity(item.Entity)
			if ent == nil {
				continue
			}
			// Кнопка создания видна только тем, кто может записать сущность.
			if !r.canWrite(string(ent.Kind), ent.Name) {
				deniedSome = true
				continue
			}
			link.URL = "/ui/" + strings.ToLower(string(ent.Kind)) + "/" + ent.Name + "/new"
		default:
			continue
		}
		res.Actions = append(res.Actions, link)
	}
	// Все кнопки отфильтрованы правами → карточку целиком прячем с дашборда.
	if len(res.Actions) == 0 && deniedSome {
		res.AccessDenied = true
	}
}

func (r *Runner) runRecent(ctx context.Context, w *metadata.Widget, res *Result) {
	// "Recent" is a platform widget: it reads the global _audit log to find the
	// most-recently-touched records across the chosen entities. This works for
	// any entity (documents and catalogs) without requiring a specific column
	// like дата or updated_at.
	limit := w.Limit
	if limit <= 0 {
		limit = 8
	}

	d := r.Store.Dialect()
	var where []string
	var args []any
	idx := 1

	if len(w.Entities) > 0 {
		placeholders := make([]string, len(w.Entities))
		for i, name := range w.Entities {
			placeholders[i] = d.Placeholder(idx)
			args = append(args, name)
			idx++
		}
		where = append(where, "entity_name IN ("+strings.Join(placeholders, ", ")+")")
	} else {
		where = append(where, "entity_kind = "+d.Placeholder(idx))
		args = append(args, "document")
		idx++
	}

	if strings.EqualFold(w.Scope, "current_user") && r.CurrentUser != "" {
		where = append(where, "user_login = "+d.Placeholder(idx))
		args = append(args, r.CurrentUser)
	}

	sql := `SELECT entity_kind, entity_name, record_id, MAX(at) AS _ts
		FROM _audit
		WHERE ` + strings.Join(where, " AND ") + ` AND record_id IS NOT NULL
		GROUP BY entity_kind, entity_name, record_id
		ORDER BY _ts DESC
		LIMIT ` + fmt.Sprint(limit*3)

	rows, _, err := r.Store.RunQuery(ctx, sql, args)
	if err != nil {
		res.Error = err.Error()
		return
	}
	var kept []map[string]any
	for _, row := range rows {
		entName, _ := row["entity_name"].(string)
		kind, _ := row["entity_kind"].(string)
		id := fmt.Sprintf("%v", row["record_id"])
		entity := r.Reg.GetEntity(entName)
		if entity == nil || !r.canRead(kind, entName) {
			continue
		}
		uid, err := uuid.Parse(id)
		if err != nil || !r.rowAllowedID(ctx, entity, "read", uid) {
			continue
		}
		title := recordPresentation(ctx, r, entName, id)
		if title == shortID(id) {
			continue
		}
		row["_url"] = "/ui/" + kind + "/" + entName + "/" + id
		row["_label"] = entName
		row["_title"] = title
		kept = append(kept, row)
		if len(kept) >= limit {
			break
		}
	}
	res.Rows = kept
	res.Columns = []ColumnSpec{
		{Field: "entity_name", Label: "Объект"},
		{Field: "_ts", Label: "Когда", Format: "date"},
	}
}

// resolveUUIDs walks rows and replaces any UUID-shaped string with the
// display name of the corresponding entity. Same idea as
// ui.resolveUUIDsInReport but kept inside the widget package to avoid a
// circular import.
func (r *Runner) resolveUUIDs(ctx context.Context, rows []map[string]any) {
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
	for _, entity := range r.Reg.Entities() {
		if !r.canRead(string(entity.Kind), entity.Name) {
			continue
		}
		for idStr, label := range uuidToLabel {
			if label != "" {
				continue
			}
			id, perr := uuid.Parse(idStr)
			if perr != nil {
				continue
			}
			if !r.rowAllowedID(ctx, entity, "read", id) {
				continue
			}
			if refRow, err := r.Store.GetByID(ctx, entity.Name, id, entity); err == nil {
				r.maskRecord(entity, refRow)
				if len(entity.Presentation) > 0 {
					uuidToLabel[idStr] = explicitRecordPresentation(entity, refRow)
					if uuidToLabel[idStr] == "" {
						uuidToLabel[idStr] = shortID(idStr)
					}
					continue
				}
				for _, f := range entity.Fields {
					if s, ok := refRow[f.Name].(string); ok && strings.TrimSpace(s) != "" {
						uuidToLabel[idStr] = s
						break
					}
				}
				if uuidToLabel[idStr] == "" {
					uuidToLabel[idStr] = shortID(idStr)
				}
			}
		}
	}
	for _, row := range rows {
		for col, v := range row {
			if str, ok := v.(string); ok {
				if label, found := uuidToLabel[str]; found && label != "" {
					row[col] = label
				}
			}
		}
	}
}

// runQuery is the shared back-end for kpi/list/chart widgets.
func (r *Runner) runQuery(ctx context.Context, w *metadata.Widget) ([]map[string]any, []string, error) {
	params := make(map[string]any, len(w.Params))
	for k, v := range w.Params {
		params[k] = v
	}
	params = scheduler.ResolveParamTemplates(params)

	rowFilters, err := access.QueryRowFiltersWithLookup(r.User, r.Reg.Entities(), r.Reg.Registers(), r.Reg.InfoRegisters(), r.Reg.AccountRegisters(), r.Reg)
	if err != nil {
		return nil, nil, err
	}
	compiled, err := query.Compile(w.Query, query.CompileOpts{
		Params:      params,
		Entities:    r.Reg.Entities(),
		Registers:   r.Reg.Registers(),
		InfoRegs:    r.Reg.InfoRegisters(),
		AccountRegs: r.Reg.AccountRegisters(),
		RowFilters:  rowFilters,
		Dialect:     r.Store.Dialect(),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("compile: %w", err)
	}
	if denied := r.deniedQuerySource(compiled.Sources); denied != "" {
		return nil, nil, &accessDeniedError{object: denied}
	}
	// План 88E: защищённое поле в простой колонке виджета маскируется, в отборе
	// или агрегате — по-прежнему закрывает виджет целиком.
	maskPlan := access.QueryMaskPlanFor(r.User, compiled, r.sourceMeta)
	if maskPlan.Denied != "" {
		return nil, nil, &accessDeniedError{object: "поле «" + maskPlan.Denied + "»"}
	}
	rows, cols, err := query.Run(ctx, r.Store, &compiled)
	if err != nil {
		return rows, cols, err
	}
	if err := maskPlan.Apply(rows); err != nil {
		return nil, nil, &accessDeniedError{object: err.Error()}
	}
	return rows, cols, nil
}

func (r *Runner) sourceMeta(kind, name string) *metadata.Entity {
	if e := r.Reg.GetEntity(name); e != nil {
		return e
	}
	if reg := r.Reg.GetRegister(name); reg != nil {
		return storage.RegisterPredicateEntity(reg)
	}
	if ir := r.Reg.GetInfoRegister(name); ir != nil {
		return storage.InfoRegisterPredicateEntity(ir)
	}
	if ar := r.Reg.GetAccountRegister(name); ar != nil {
		return storage.AccountRegisterPredicateEntity(ar)
	}
	return nil
}

// accessDeniedError сигналит, что источник запроса недоступен пользователю —
// setResultError переводит его в Result.AccessDenied.
type accessDeniedError struct{ object string }

func (e *accessDeniedError) Error() string {
	return "нет доступа к объекту: " + e.object
}

// setResultError записывает ошибку в Result, помечая отказ в доступе.
func setResultError(res *Result, err error) {
	res.Error = err.Error()
	var denied *accessDeniedError
	res.AccessDenied = errors.As(err, &denied)
}

func (r *Runner) canRead(kind, name string) bool {
	return r.User == nil || r.User.Has(kind, name, "read")
}

func (r *Runner) canWrite(kind, name string) bool {
	return r.User == nil || r.User.Has(kind, name, "write")
}

func (r *Runner) rowAllowedID(ctx context.Context, entity *metadata.Entity, op string, id uuid.UUID) bool {
	dec, err := access.DecideWithLookup(r.User, string(entity.Kind), entity.Name, op, entity, r.Reg)
	if err != nil || !dec.Allowed {
		return false
	}
	if dec.Unrestricted {
		return true
	}
	row, err := r.Store.GetByID(ctx, entity.Name, id, entity)
	return err == nil && storage.MatchPredicateWithRefs(row, dec.Predicate, func(entity *metadata.Entity, id uuid.UUID) (map[string]any, bool) {
		if entity == nil {
			return nil, false
		}
		refRow, err := r.Store.GetByID(ctx, entity.Name, id, entity)
		return refRow, err == nil
	})
}

func (r *Runner) deniedQuerySource(sources []query.SourceRef) string {
	for _, src := range sources {
		if !r.canRead(src.Kind, src.Name) {
			return src.Name
		}
	}
	return ""
}

func columnsForList(w *metadata.Widget, cols []string) []ColumnSpec {
	if len(w.Columns) > 0 {
		out := make([]ColumnSpec, len(w.Columns))
		for i, c := range w.Columns {
			actual := resolveFieldName(cols, c.Field)
			if actual == "" {
				actual = c.Field
			}
			out[i] = ColumnSpec{Field: actual, Label: c.Label, Format: c.Format, Align: c.Align}
			if out[i].Label == "" {
				out[i].Label = c.Field
			}
		}
		return out
	}
	out := make([]ColumnSpec, len(cols))
	for i, c := range cols {
		out[i] = ColumnSpec{Field: c, Label: c}
	}
	return out
}

func firstScalar(row map[string]any) any {
	for _, v := range row {
		return v
	}
	return nil
}

func toFloat(v any) float64 {
	switch t := v.(type) {
	case decimal.Decimal:
		return t.InexactFloat64()
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int32:
		return float64(t)
	case int64:
		return float64(t)
	case string:
		// Нечисловая строка даёт 0 — прежний контракт: функция приводит
		// произвольное значение виджета к числу, и нечисловое здесь штатно.
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return 0
		}
		return f
	case nil:
		return 0
	}
	return 0
}

// safeWidgetLink пропускает только ВНУТРЕННИЙ путь приложения. Карточку
// дашборда настраивает конфигуратор, но пускать оттуда произвольный адрес
// (внешний хост, javascript:, protocol-relative «//host» и «/\host») незачем: это ссылка
// на свой же список или отчёт. Всё остальное отбрасывается — карточка просто
// остаётся некликабельной.
func safeWidgetLink(link string) string {
	link = strings.TrimSpace(link)
	if link == "" {
		return ""
	}
	// Обратный слэш браузер для http(s) нормализует в «/», поэтому «/\host»,
	// «\\host» и «\/host» — тот же протокол-относительный переход на чужой
	// хост, что и «//host», хотя проверку ниже они проходят. Путь приложения
	// обратных слэшей не содержит, так что отбрасываем их все: нормализовать и
	// вернуть уже не ту строку, которую задал конфигуратор, — хуже.
	if strings.Contains(link, `\`) {
		return ""
	}
	if !strings.HasPrefix(link, "/") || strings.HasPrefix(link, "//") {
		return ""
	}
	return link
}

func formatKPI(v any, format string) string {
	f := toFloat(v)
	switch strings.ToLower(format) {
	case "money":
		return formatMoney(f)
	case "percent":
		return fmt.Sprintf("%.1f%%", f)
	case "number":
		return formatInt(f)
	}
	if isInteger(f) {
		return formatInt(f)
	}
	return fmt.Sprintf("%.2f", f)
}

func formatMoney(f float64) string {
	// Russian convention: thousands separator U+00A0, comma decimal.
	neg := f < 0
	if neg {
		f = -f
	}
	whole := int64(f)
	frac := int64((f - float64(whole)) * 100)
	if frac < 0 {
		frac = -frac
	}
	s := groupDigits(whole)
	out := fmt.Sprintf("%s,%02d ₽", s, frac)
	if neg {
		out = "-" + out
	}
	return out
}

func formatInt(f float64) string {
	neg := f < 0
	if neg {
		f = -f
	}
	whole := int64(f + 0.5)
	s := groupDigits(whole)
	if neg {
		return "-" + s
	}
	return s
}

func groupDigits(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	rem := len(s) % 3
	if rem > 0 {
		b.WriteString(s[:rem])
		if len(s) > rem {
			b.WriteRune(' ')
		}
	}
	for i := rem; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteRune(' ')
		}
	}
	return b.String()
}

func isInteger(f float64) bool {
	return f == float64(int64(f))
}

// recordPresentation returns a human-readable title for a record referenced
// by the recent-widget. For documents we prefer "Номер · Дата"; for catalogs
// we fall back to the first string-typed field (usually Наименование). On any
// lookup failure we return a shortened UUID so the row still renders.
func recordPresentation(ctx context.Context, r *Runner, entityName, idStr string) string {
	ent := r.Reg.GetEntity(entityName)
	if ent == nil {
		return shortID(idStr)
	}
	id, err := parseUUID(idStr)
	if err != nil {
		return shortID(idStr)
	}
	row, err := r.Store.GetByID(ctx, ent.Name, id, ent)
	if err != nil || row == nil {
		return shortID(idStr)
	}
	r.maskRecord(ent, row)
	if len(ent.Presentation) > 0 {
		if label := explicitRecordPresentation(ent, row); label != "" {
			return label
		}
		return shortID(idStr)
	}
	if ent.Kind == metadata.KindDocument {
		num := fmt.Sprintf("%v", firstNonEmpty(row, "Номер", "Number"))
		dateRaw := firstNonEmpty(row, "Дата", "Date")
		if dateRaw != nil {
			if t, ok := dateRaw.(time.Time); ok {
				return strings.TrimSpace(num) + " от " + t.Format("02.01.2006")
			}
		}
		if num != "<nil>" && num != "" {
			return num
		}
	}
	// catalogs and fallbacks: first non-empty string field
	for _, f := range ent.Fields {
		if v, ok := row[f.Name]; ok && v != nil {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return shortID(idStr)
}

func explicitRecordPresentation(entity *metadata.Entity, row map[string]any) string {
	for _, field := range metadata.LabelFields(entity) {
		for key, value := range row {
			if !strings.EqualFold(key, field.Name) || value == nil {
				continue
			}
			if label := strings.TrimSpace(fmt.Sprint(value)); label != "" {
				return label
			}
			break
		}
	}
	return ""
}

// maskRecord applies the same field-level policy as UI read paths before a
// UUID-derived label enters a widget result (and potentially its cache).
func (r *Runner) maskRecord(entity *metadata.Entity, row map[string]any) {
	if entity == nil {
		return
	}
	access.MaskRecord(access.FieldDecisions(r.User, string(entity.Kind), entity.Name, entity), row)
}

func firstNonEmpty(row map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := row[k]; ok && v != nil {
			if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
				continue
			}
			return v
		}
	}
	return nil
}

func shortID(idStr string) string {
	if len(idStr) > 8 {
		return idStr[:8]
	}
	return idStr
}

// parseUUID isolates the google/uuid import to a single helper so the rest of
// the runner stays driver-agnostic.
func parseUUID(s string) (uuid.UUID, error) { return uuid.Parse(s) }
