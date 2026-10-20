package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Варианты ссылочных полей панели «Отбор» грузятся только полям панели. У
// документа с list_filters из трёх полей список открывался секунды: варианты
// загружались всем ссылочным реквизитам, и справочник адресов в 600 тыс. строк
// сортировался ради поля, которого на экране нет.
func TestUI_EntityList_FilterOptionsOnlyForPanelFields(t *testing.T) {
	бренд := &metadata.Entity{Name: "Бренд", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
	город := &metadata.Entity{Name: "Город", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
	заявка := &metadata.Entity{Name: "Заявка", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Бренд", Type: "reference:Бренд", RefEntity: "Бренд"},
			{Name: "Город", Type: "reference:Город", RefEntity: "Город"},
		},
		ListFilters: []string{"Наименование", "Бренд"},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{бренд, город, заявка})
	if err := s.store.Upsert(ctx, бренд.Name, uuid.New(), map[string]any{"Наименование": "Атлант"}, бренд); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Upsert(ctx, город.Name, uuid.New(), map[string]any{"Наименование": "Москва"}, город); err != nil {
		t.Fatal(err)
	}

	// Через страницу списка: у поля панели варианты есть, как раньше.
	req := reqWithChi(http.MethodGet, "/ui/catalog/"+заявка.Name, nil, map[string]string{"kind": "catalog", "entity": заявка.Name})
	req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{ID: "root", Login: "admin", IsAdmin: true}))
	rec := httptest.NewRecorder()
	s.list(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Атлант") {
		t.Fatal("у поля панели «Бренд» нет вариантов")
	}

	// Варианты — только переданным полям панели.
	panel := listFilterPanelFields(map[string]any{"Entity": заявка, "Params": storage.ListParams{}})
	opts, err := s.loadInitialRefFilterOptions(ctx, заявка, storage.ListParams{}, panel)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := opts["Бренд"]; !ok {
		t.Fatal("нет вариантов поля панели «Бренд»")
	}
	if _, ok := opts["Город"]; ok {
		t.Fatal("варианты загружены полю «Город», которого нет в панели")
	}
}
