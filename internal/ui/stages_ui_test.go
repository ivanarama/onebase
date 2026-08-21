package ui

// Интерфейс этапов (план 121): схема маршрута на карточке, история переходов и
// отчёт «где застряло».

import (
	"context"
	"encoding/json"
	"html"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func stagesUIEntity() *metadata.Entity {
	return &metadata.Entity{
		Name: "Заявка",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Состояние", Type: "enum:СостояниеЗаявки", EnumName: "СостояниеЗаявки"},
		},
		Stages: &metadata.Stages{
			Field: "Состояние",
			Order: []string{"Черновик", "НаСогласовании", "Утверждена"},
			Transitions: []metadata.StageTransition{
				{From: "Черновик", To: []string{"НаСогласовании"}},
				{From: "НаСогласовании", To: []string{"Утверждена"}},
			},
			DeadlineDays: map[string]int{"НаСогласовании": 2},
			Enforce:      metadata.StageEnforceStrict,
		},
	}
}

func stagesUIServer(t *testing.T) (*Server, *metadata.Entity, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "stages.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := stagesUIEntity()
	if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{e},
		Enums: []*metadata.Enum{{
			Name:   "СостояниеЗаявки",
			Values: []string{"Черновик", "НаСогласовании", "Утверждена"},
		}},
	})
	s := &Server{store: db, reg: reg, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	return s, e, ctx
}

// Схема маршрута отдаётся серией graph уже вендоренного ECharts: узлы из order,
// рёбра из transitions, текущий этап подсвечен.
func TestStageChartJSONBuildsGraphSeries(t *testing.T) {
	s, e, _ := stagesUIServer(t)
	r := httptest.NewRequest(http.MethodGet, "/ui/catalog/заявка/x", nil)
	view := s.buildStageRoute(r, e, "НаСогласовании")
	if view == nil {
		t.Fatal("маршрут не построен")
	}
	if view.Current != "НаСогласовании" || view.OffRoute {
		t.Fatalf("текущий этап %q offRoute=%v", view.Current, view.OffRoute)
	}

	var opt struct {
		Series []struct {
			Type string `json:"type"`
			Data []struct {
				Name      string `json:"name"`
				ItemStyle struct {
					Color string `json:"color"`
				} `json:"itemStyle"`
			} `json:"data"`
			Links []struct {
				Source string `json:"source"`
				Target string `json:"target"`
			} `json:"links"`
		} `json:"series"`
	}
	if err := json.Unmarshal([]byte(stageChartJSON(view)), &opt); err != nil {
		t.Fatalf("option не разбирается: %v", err)
	}
	if len(opt.Series) != 1 || opt.Series[0].Type != "graph" {
		t.Fatalf("серия %+v, ожидалась graph", opt.Series)
	}
	if len(opt.Series[0].Data) != 3 {
		t.Fatalf("узлов %d, ожидалось 3 (по этапу из order)", len(opt.Series[0].Data))
	}
	if len(opt.Series[0].Links) != 2 {
		t.Fatalf("рёбер %d, ожидалось 2 (по объявленным переходам)", len(opt.Series[0].Links))
	}
	var highlighted int
	for _, n := range opt.Series[0].Data {
		if n.ItemStyle.Color == "#2563eb" {
			highlighted++
			if n.Name != "НаСогласовании" {
				t.Fatalf("подсвечен узел %q", n.Name)
			}
		}
	}
	if highlighted != 1 {
		t.Fatalf("подсвечено узлов: %d, ожидался ровно 1", highlighted)
	}
}

// Длинные и обратные переходы не должны получать постоянную большую кривизну:
// ECharts масштабирует её вместе с длиной ребра, и дуга уходит за canvas.
func TestStageChartJSONNormalizesCurvenessBySpan(t *testing.T) {
	view := &stageRouteView{
		Nodes: []stageNodeView{
			{Name: "draft", Label: "Черновик"},
			{Name: "review", Label: "На согласовании"},
			{Name: "approved", Label: "Утверждена"},
			{Name: "rejected", Label: "Отклонена"},
		},
		Edges: [][2]string{
			{"draft", "review"},
			{"review", "rejected"},
			{"rejected", "draft"},
		},
	}
	var opt struct {
		Series []struct {
			Links []struct {
				Source    string `json:"source"`
				Target    string `json:"target"`
				LineStyle struct {
					Curveness float64 `json:"curveness"`
				} `json:"lineStyle"`
			} `json:"links"`
		} `json:"series"`
	}
	if err := json.Unmarshal([]byte(stageChartJSON(view)), &opt); err != nil {
		t.Fatalf("option не разбирается: %v", err)
	}
	if len(opt.Series) != 1 || len(opt.Series[0].Links) != 3 {
		t.Fatalf("неожиданные серии/рёбра: %+v", opt.Series)
	}
	want := []float64{0, 0.075, -0.06}
	for i, link := range opt.Series[0].Links {
		if math.Abs(link.LineStyle.Curveness-want[i]) > 1e-9 {
			t.Errorf("ребро %s → %s: curveness=%v, ожидалось %v", link.Source, link.Target, link.LineStyle.Curveness, want[i])
		}
	}
}

func TestStageCurvenessCapsShortDetours(t *testing.T) {
	tests := []struct {
		name     string
		from, to int
		total    int
		want     float64
	}{
		{name: "same node", from: 1, to: 1, total: 4, want: 0},
		{name: "invalid total", from: 1, to: 0, total: 1, want: 0},
		{name: "adjacent forward", from: 1, to: 2, total: 4, want: 0},
		{name: "adjacent reverse capped", from: 10, to: 9, total: 20, want: -0.35},
		{name: "short forward jump capped", from: 0, to: 2, total: 20, want: 0.25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := curvenessFor(tt.from, tt.to, tt.total)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("curvenessFor(%d, %d, %d)=%v, ожидалось %v", tt.from, tt.to, tt.total, got, tt.want)
			}
		})
	}
}

// Значение вне order — объект показывается «вне маршрута», а не с молча
// неподсвеченной схемой: так выглядят данные, накопленные до объявления этапов.
func TestStageRouteMarksOffRouteValue(t *testing.T) {
	s, e, _ := stagesUIServer(t)
	r := httptest.NewRequest(http.MethodGet, "/ui/catalog/заявка/x", nil)
	view := s.buildStageRoute(r, e, "Аннулирована")
	if view == nil {
		t.Fatal("маршрут не построен")
	}
	if !view.OffRoute {
		t.Fatal("значение вне order не помечено как «вне маршрута»")
	}
	if view.Current != "" {
		t.Fatalf("текущим считается %q, а такого этапа в маршруте нет", view.Current)
	}
}

// Отчёт «где застряло» считает объекты по этапам, показывает просрочку и
// отдельно — объекты вне маршрута.
func TestStagesReportPageCountsObjects(t *testing.T) {
	s, e, ctx := stagesUIServer(t)

	// Один объект прошёл маршрут штатно.
	id := uuid.New()
	if err := s.store.Upsert(ctx, e.Name, id, map[string]any{
		"Наименование": "Заявка 1", "Состояние": "Черновик"}, e); err != nil {
		t.Fatal(err)
	}
	var v int64 = 1
	if err := s.store.UpsertVersioned(ctx, e.Name, id, map[string]any{
		"Наименование": "Заявка 1", "Состояние": "НаСогласовании"}, e, &v); err != nil {
		t.Fatal(err)
	}

	// Объект со значением вне объявленного маршрута — пишем как «данные до
	// объявления этапов»: та же сущность, но без блока stages.
	legacy := stagesUIEntity()
	legacy.Stages = nil
	if err := s.store.Upsert(ctx, legacy.Name, uuid.New(), map[string]any{
		"Наименование": "Старая", "Состояние": "Аннулирована"}, legacy); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/stages", nil)
	s.stagesReport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("код ответа %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Заявка") {
		t.Fatal("в отчёте нет сущности с этапами")
	}
	if !strings.Contains(body, "Вне маршрута") {
		t.Fatal("объект со значением вне order не показан отдельной строкой")
	}
	if !strings.Contains(body, `"type":"graph"`) {
		t.Fatal("схема маршрута на странице отчёта не построена")
	}
}

// Схема маршрута рисуется и в управляемой форме, а не только в автоформе:
// иначе объявление stages «работает» ровно до того момента, когда у сущности
// появляется своя форма.
func TestStageRoutePartialRendersInBothForms(t *testing.T) {
	s, e, _ := stagesUIServer(t)
	r := httptest.NewRequest(http.MethodGet, "/ui/catalog/заявка/x", nil)
	view := s.buildStageRoute(r, e, "НаСогласовании")
	data := map[string]any{
		"Entity":     e,
		"IsNew":      false,
		"StageRoute": view,
		"Lang":       "",
		"Cfg":        s.cfg,
	}
	for _, tplName := range []string{"stage-route"} {
		var buf strings.Builder
		if err := tmpl.ExecuteTemplate(&buf, tplName, data); err != nil {
			t.Fatalf("%s: %v", tplName, err)
		}
		// Опция уезжает в data-атрибут, поэтому кавычки экранированы шаблонизатором.
		out := html.UnescapeString(buf.String())
		if !strings.Contains(out, `"type":"graph"`) {
			t.Fatalf("%s: схема не построена: %s", tplName, out)
		}
		if !strings.Contains(out, "ob-stage-route") {
			t.Fatalf("%s: нет контейнера графика", tplName)
		}
		// Инициализатор обязан быть идемпотентным: тот же блок может приехать
		// повторно при замене части DOM.
		if !strings.Contains(out, "obStageDrawn") {
			t.Fatalf("%s: инициализатор не защищён от повторного запуска", tplName)
		}
	}
	// Оба шаблона формы обязаны звать общий блок, иначе он снова разъедется.
	for _, name := range []string{"page-form", "page-managed-form"} {
		if !strings.Contains(templateSource(), `{{template "stage-route" .}}`) {
			t.Fatalf("шаблон %s не подключает общий блок маршрута", name)
		}
	}
}
