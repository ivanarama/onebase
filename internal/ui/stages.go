package ui

// Этапы объекта в интерфейсе (план 121): схема маршрута на карточке, история
// переходов и отчёт «где застряло».
//
// Схема рисуется серией graph уже вендоренного ECharts (webassets/echarts) —
// новой зависимости не появляется.

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// stageNodeView — узел схемы маршрута.
type stageNodeView struct {
	Name    string
	Label   string // представление значения перечисления на языке пользователя
	Current bool
	Count   int // сколько объектов стоит на этапе (для отчёта; на карточке 0)
}

// stageRouteView — схема маршрута для шаблона.
type stageRouteView struct {
	Entity  string
	Field   string
	Current string
	Label   string // представление текущего этапа
	Nodes   []stageNodeView
	Edges   [][2]string
	// OffRoute — текущее значение объекта не объявлено в order. Так выглядят
	// данные, накопленные до объявления этапов: показать их «вне маршрута»
	// честнее, чем молча не подсветить ни один узел.
	OffRoute bool
}

// buildStageRoute собирает схему маршрута для карточки объекта. nil означает
// «показывать нечего»: этапы не объявлены, поля нет или оно под маской ПДн
// (план 88) — в последнем случае этап не показывается вовсе, а не показывается
// пустым, иначе схема стала бы обходным каналом чтения закрытого реквизита.
func (s *Server) buildStageRoute(r *http.Request, entity *metadata.Entity, current string) *stageRouteView {
	if entity == nil || entity.Stages == nil {
		return nil
	}
	f := entity.StageField()
	if f == nil {
		return nil
	}
	if dec, ok := s.fieldDecisions(r.Context(), entity)[f.Name]; ok && dec.Masked() {
		return nil
	}
	st := entity.Stages
	lang := s.resolveLang(r)
	enum := s.reg.GetEnum(f.EnumName)
	label := func(v string) string {
		if enum != nil {
			return enum.ValueTitle(v, lang)
		}
		return v
	}
	canon := st.Canonical(current)
	view := &stageRouteView{
		Entity:   entity.Name,
		Field:    f.Name,
		Current:  canon,
		Label:    label(canon),
		OffRoute: strings.TrimSpace(current) != "" && canon == "",
	}
	for _, stage := range st.Order {
		view.Nodes = append(view.Nodes, stageNodeView{
			Name:    stage,
			Label:   label(stage),
			Current: stage == canon,
		})
	}
	for _, tr := range st.Transitions {
		from := st.Canonical(tr.From)
		for _, to := range tr.To {
			if t := st.Canonical(to); from != "" && t != "" {
				view.Edges = append(view.Edges, [2]string{from, t})
			}
		}
	}
	return view
}

// hasStages сообщает, объявлен ли блок `stages` хоть у одной сущности.
func (s *Server) hasStages() bool {
	for _, e := range s.reg.Entities() {
		if e != nil && e.Stages != nil && e.StageField() != nil {
			return true
		}
	}
	return false
}

// stageCurrentValue достаёт текущее значение поля-этапа из значений формы.
func stageCurrentValue(entity *metadata.Entity, vals map[string]string) string {
	if entity == nil || entity.Stages == nil {
		return ""
	}
	f := entity.StageField()
	if f == nil {
		return ""
	}
	return vals[f.Name]
}

// stageChartJSON — option серии graph для схемы маршрута.
//
// Раскладка задаётся координатами, а не силовым алгоритмом: маршрут читается
// слева направо, и «живая» физика каждый раз ставила бы этапы в новом порядке.
func stageChartJSON(v any) template.JS {
	view, ok := v.(*stageRouteView)
	if !ok || view == nil || len(view.Nodes) == 0 {
		return template.JS("null")
	}
	index := make(map[string]int, len(view.Nodes))
	nodes := make([]map[string]any, 0, len(view.Nodes))
	for i, n := range view.Nodes {
		index[n.Name] = i
		label := n.Label
		if n.Count > 0 {
			label = fmt.Sprintf("%s (%d)", label, n.Count)
		}
		color := "#e2e8f0"
		textColor := "#475569"
		if n.Current {
			color = "#2563eb"
			textColor = "#ffffff"
		}
		nodes = append(nodes, map[string]any{
			"name":       n.Label,
			"x":          i * 180,
			"y":          0,
			"symbol":     "roundRect",
			"symbolSize": []int{140, 44},
			"itemStyle":  map[string]any{"color": color, "borderColor": "#cbd5e1"},
			"label": map[string]any{
				"show":      true,
				"color":     textColor,
				"fontSize":  12,
				"width":     124,
				"overflow":  "truncate",
				"formatter": label,
			},
		})
	}
	links := make([]map[string]any, 0, len(view.Edges))
	for _, e := range view.Edges {
		fi, okFrom := index[e[0]]
		ti, okTo := index[e[1]]
		if !okFrom || !okTo {
			continue
		}
		links = append(links, map[string]any{
			"source":    view.Nodes[fi].Label,
			"target":    view.Nodes[ti].Label,
			"lineStyle": map[string]any{"color": "#94a3b8", "curveness": curvenessFor(fi, ti)},
		})
	}
	opt := map[string]any{
		"tooltip":   map[string]any{"show": false},
		"animation": false,
		"series": []map[string]any{{
			"type":           "graph",
			"layout":         "none",
			"roam":           false,
			"edgeSymbol":     []string{"none", "arrow"},
			"edgeSymbolSize": 8,
			"data":           nodes,
			"links":          links,
		}},
	}
	b, err := json.Marshal(opt)
	if err != nil {
		return template.JS("null")
	}
	return template.JS(b) //nolint:gosec // G203: значение получено json.Marshal — он экранирует < > & в \u-последовательности
}

// curvenessFor выгибает обратные переходы (возврат «Отклонена → Черновик»),
// чтобы они не ложились поверх прямой стрелки.
func curvenessFor(from, to int) float64 {
	if to < from {
		return -0.35
	}
	if to-from > 1 {
		return 0.25
	}
	return 0
}

// stageSummaryView — одна строка отчёта «где застряло».
type stageSummaryView struct {
	Stage        string
	Label        string
	Count        int
	Unknown      int
	Overdue      int
	DeadlineDays int
	// Since — самый давний момент попадания на этап среди объектов с историей.
	Since time.Time
}

// stageEntityReport — отчёт по одной сущности.
type stageEntityReport struct {
	Entity   *metadata.Entity
	Field    string
	Rows     []stageSummaryView
	Total    int
	OffRoute int // объекты, чьё состояние не объявлено в order
	Route    *stageRouteView
	ListURL  string
}

// stagesReport — страница «Этапы: где застряло».
func (s *Server) stagesReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lang := s.resolveLang(r)
	wanted := strings.TrimSpace(chi.URLParam(r, "entity"))

	var reports []*stageEntityReport
	for _, e := range s.reg.Entities() {
		if e == nil || e.Stages == nil {
			continue
		}
		if wanted != "" && !strings.EqualFold(e.Name, wanted) {
			continue
		}
		f := e.StageField()
		if f == nil {
			continue
		}
		// Поле-этап под маской ПДн — сводка по нему тоже закрыта.
		if dec, ok := s.fieldDecisions(ctx, e)[f.Name]; ok && dec.Masked() {
			continue
		}
		// Построчный доступ (план 79): отчёт считает объекты, поэтому без
		// фильтра он выдавал бы количество чужих записей тому, кому они не
		// видны. Сущность, к которой чтение запрещено целиком, просто выпадает
		// из отчёта.
		params, err := s.rowFilterFor(ctx, e, "read", storage.ListParams{})
		if err != nil {
			continue
		}
		buckets, err := s.store.StageSummary(ctx, e, params.RowFilter)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		enum := s.reg.GetEnum(f.EnumName)
		rep := &stageEntityReport{
			Entity:  e,
			Field:   f.Name,
			Route:   &stageRouteView{Entity: e.Name, Field: f.Name},
			ListURL: "/ui/" + strings.ToLower(string(e.Kind)) + "/" + strings.ToLower(e.Name),
		}
		for _, b := range buckets {
			label := b.Stage
			if enum != nil {
				label = enum.ValueTitle(b.Stage, lang)
			}
			rep.Rows = append(rep.Rows, stageSummaryView{
				Stage: b.Stage, Label: label, Count: b.Count, Unknown: b.Unknown,
				Overdue: b.Overdue, DeadlineDays: b.DeadlineDays, Since: b.Since,
			})
			rep.Total += b.Count
			rep.Route.Nodes = append(rep.Route.Nodes, stageNodeView{
				Name: b.Stage, Label: label, Count: b.Count,
			})
		}
		for _, tr := range e.Stages.Transitions {
			from := e.Stages.Canonical(tr.From)
			for _, to := range tr.To {
				if t := e.Stages.Canonical(to); from != "" && t != "" {
					rep.Route.Edges = append(rep.Route.Edges, [2]string{from, t})
				}
			}
		}
		off, err := s.store.StageOffRouteCount(ctx, e, params.RowFilter)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		rep.OffRoute = off
		reports = append(reports, rep)
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].Entity.Name < reports[j].Entity.Name })

	s.render(w, r, "page-stages", map[string]any{
		"Reports": reports,
		"Wanted":  wanted,
	})
}

// stageHistoryRow — строка истории переходов для шаблона.
type stageHistoryRow struct {
	At        time.Time
	UserLogin string
	From      string
	To        string
	Source    string
	// Violation — переход был недопустим и прошёл только потому, что маршрут
	// объявлен в режиме warn. Показывать это обязательно: объект стоит не там,
	// где предполагает маршрут, и другого следа этому нет.
	Violation bool
}

// loadStageHistory читает историю переходов объекта для карточки. Пустой
// результат — этапы не объявлены, поле под маской или переходов ещё не было.
func (s *Server) loadStageHistory(r *http.Request, entity *metadata.Entity, id uuid.UUID) []stageHistoryRow {
	if entity == nil || entity.Stages == nil {
		return nil
	}
	f := entity.StageField()
	if f == nil {
		return nil
	}
	if dec, ok := s.fieldDecisions(r.Context(), entity)[f.Name]; ok && dec.Masked() {
		return nil
	}
	changes, err := s.store.StageHistory(r.Context(), entity.Name, id)
	if err != nil {
		uiLog().Warn("история этапов не прочитана", "сущность", entity.Name, "err", err)
		return nil
	}
	lang := s.resolveLang(r)
	enum := s.reg.GetEnum(f.EnumName)
	label := func(v string) string {
		if v == "" {
			return ""
		}
		if enum != nil {
			return enum.ValueTitle(v, lang)
		}
		return v
	}
	rows := make([]stageHistoryRow, 0, len(changes))
	for _, ch := range changes {
		rows = append(rows, stageHistoryRow{
			At: ch.At, UserLogin: ch.UserLogin,
			From: label(ch.FromStage), To: label(ch.ToStage), Source: ch.Source,
			Violation: ch.Violation,
		})
	}
	return rows
}

// stageSourceLabel — человекочитаемый источник записи истории. Обычная запись
// метки не получает: подпись «локально» у каждой строки была бы шумом.
func stageSourceLabel(src string) string {
	switch src {
	case storage.StageSourceExchange:
		return "обмен"
	case storage.StageSourceMigration:
		return "конфигурация"
	default:
		return ""
	}
}
