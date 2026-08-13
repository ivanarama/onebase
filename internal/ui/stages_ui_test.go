package ui

// Интерфейс этапов (план 121): схема маршрута на карточке, история переходов и
// отчёт «где застряло».

import (
	"context"
	"encoding/json"
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
