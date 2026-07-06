package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestReferenceOptions_HidesInactiveOnlyForChoices(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ent := activityCatalogEntity()
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{"Наименование": "Показывать", "Активный": true}, ent); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{"Наименование": "Скрыть", "Активный": false}, ent); err != nil {
		t.Fatal(err)
	}

	srv := &Server{store: db}
	choiceRows, err := srv.referenceOptions(ctx, ent, refOptionsChoice)
	if err != nil {
		t.Fatalf("referenceOptions choice: %v", err)
	}
	if labels := optionLabels(choiceRows); !sameStringSet(labels, []string{"Показывать"}) {
		t.Fatalf("choice labels = %v, want only active", labels)
	}

	filterRows, err := srv.referenceOptions(ctx, ent, refOptionsFilter)
	if err != nil {
		t.Fatalf("referenceOptions filter: %v", err)
	}
	if labels := optionLabels(filterRows); !sameStringSet(labels, []string{"Показывать", "Скрыть"}) {
		t.Fatalf("filter labels = %v, want active and inactive", labels)
	}
}

func TestPageList_ActivityControlsAndActions(t *testing.T) {
	ent := activityCatalogEntity()
	html := renderPageList(t, map[string]any{
		"Entity": ent,
		"Rows": []map[string]any{{
			"id":                 "11111111-1111-1111-1111-111111111111",
			"Наименование":       "Скрыть",
			"Активный":           false,
			"_activity_inactive": true,
		}},
		"Params":           storage.ListParams{ActivityScope: metadata.ActivityScopeInactive},
		"RefFilterOptions": map[string]any{},
		"CanWrite":         true,
		"Lang":             "ru",
		"Total":            1,
		"Page":             1,
		"TotalPages":       1,
	})

	for _, want := range []string{
		"Активные",
		"Скрытые",
		"Все",
		`href="?activity=active"`,
		`href="?activity=inactive"`,
		`href="?activity=all"`,
		`name="activity" value="inactive"`,
		`data-activity-enabled="1"`,
		`data-activity-inactive="1"`,
		`data-activity-hide-url=`,
		`/11111111-1111-1111-1111-111111111111/activity?active=0`,
		"Скрыть из выбора",
		"Вернуть в выбор",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("activity list HTML does not contain %q", want)
		}
	}
	if strings.Contains(html, "activity%3d") {
		t.Errorf("activity list HTML contains escaped query separator: %s", "activity%3d")
	}
}

func TestRefOptionsJSON_SearchLimitAndExcludeFolders(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ent := &metadata.Entity{
		Name:         "Товары",
		Kind:         metadata.KindCatalog,
		Hierarchical: true,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		name     string
		isFolder bool
	}{
		{"Группа Альфа", true},
		{"Альфа", false},
		{"Альбатрос", false},
		{"Бета", false},
	}
	for _, row := range rows {
		if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{"Наименование": row.name, "is_folder": row.isFolder}, ent); err != nil {
			t.Fatal(err)
		}
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
	s := &Server{reg: reg, store: db}

	rec := serveRefOptions(t, s, ent.Name, "q=Аль&limit=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Items  []map[string]any `json:"items"`
		Total  int              `json:"total"`
		Limit  int              `json:"limit"`
		Offset int              `json:"offset"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Total != 2 {
		t.Fatalf("total = %d, want 2 elements without folders", got.Total)
	}
	if got.Limit != 1 || len(got.Items) != 1 {
		t.Fatalf("limit/items = %d/%d, want 1/1", got.Limit, len(got.Items))
	}
	if label, _ := got.Items[0]["_label"].(string); strings.Contains(label, "Группа") {
		t.Fatalf("folder leaked into ref options: %#v", got.Items[0])
	}
}

func TestRefOptionsJSON_RBACRequiresRead(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ent := &metadata.Entity{Name: "Контрагенты", Kind: metadata.KindCatalog}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
	s := &Server{reg: reg, store: db}

	rec := serveRefOptions(t, s, ent.Name, "", &auth.User{Roles: []*auth.Role{{
		Permissions: auth.Permission{Catalogs: map[string][]string{"Другое": {"read"}}},
	}}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
}

func TestRefOptionsRouteWinsOverEntityCatchAll(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ent := &metadata.Entity{Name: "Контрагенты", Kind: metadata.KindCatalog}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
	s := &Server{reg: reg, store: db}
	router := chi.NewRouter()
	s.Mount(router)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/_ref-options/"+url.PathEscape(ent.Name), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q, want JSON; route likely hit catch-all", ct)
	}
}

func TestLoadInitialRefOptionsIncludesSelectedOutsideFirstPage(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	refEnt := &metadata.Entity{
		Name: "Товары",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	doc := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Товар", Type: metadata.FieldType("reference:Товары"), RefEntity: "Товары"},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{refEnt, doc}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= refPickerDefaultLimit; i++ {
		id := uuid.MustParse("00000000-0000-0000-0000-" + fmt12(i))
		if err := db.Upsert(ctx, refEnt.Name, id, map[string]any{"Наименование": "Товар"}, refEnt); err != nil {
			t.Fatal(err)
		}
	}
	selected := uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err := db.Upsert(ctx, refEnt.Name, selected, map[string]any{"Наименование": "Выбранный"}, refEnt); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{refEnt, doc}})
	s := &Server{reg: reg, store: db}

	opts, err := s.loadInitialRefOptions(ctx, doc, map[string]string{"Товар": selected.String()})
	if err != nil {
		t.Fatal(err)
	}
	if !hasOptionWithLabel(opts["Товар"], selected.String(), "Выбранный") {
		t.Fatalf("selected ref %s was not added to initial options: %#v", selected, opts["Товар"])
	}
}

func TestLoadRefOptionsCapsLegacyHelper(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	refEnt := &metadata.Entity{
		Name: "Товары",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	doc := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Товар", Type: metadata.FieldType("reference:Товары"), RefEntity: "Товары"},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{refEnt, doc}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= refPickerDefaultLimit+5; i++ {
		id := uuid.MustParse("00000000-0000-0000-0000-" + fmt12(i))
		if err := db.Upsert(ctx, refEnt.Name, id, map[string]any{"Наименование": fmt.Sprintf("Товар %03d", i)}, refEnt); err != nil {
			t.Fatal(err)
		}
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{refEnt, doc}})
	s := &Server{reg: reg, store: db}

	opts, err := s.loadRefOptions(ctx, doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(opts["Товар"]); got != refPickerDefaultLimit {
		t.Fatalf("loadRefOptions loaded %d rows, want capped %d", got, refPickerDefaultLimit)
	}
}

func TestLoadFolderOptionsCapsAndIncludesSelectedOutsideFirstPage(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ent := &metadata.Entity{
		Name:         "Группы",
		Kind:         metadata.KindCatalog,
		Hierarchical: true,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= refPickerDefaultLimit; i++ {
		id := uuid.MustParse("00000000-0000-0000-0000-" + fmt12(i))
		if err := db.Upsert(ctx, ent.Name, id, map[string]any{
			"Наименование": fmt.Sprintf("Папка %03d", i),
			"is_folder":    true,
		}, ent); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 5; i++ {
		id := uuid.MustParse("10000000-0000-0000-0000-" + fmt12(i))
		if err := db.Upsert(ctx, ent.Name, id, map[string]any{
			"Наименование": fmt.Sprintf("Элемент %03d", i),
			"is_folder":    false,
		}, ent); err != nil {
			t.Fatal(err)
		}
	}
	selected := uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err := db.Upsert(ctx, ent.Name, selected, map[string]any{
		"Наименование": "Папка 999",
		"is_folder":    true,
	}, ent); err != nil {
		t.Fatal(err)
	}

	s := &Server{store: db}
	opts := s.loadFolderOptions(ctx, ent, selected.String())

	if got := len(opts); got != refPickerDefaultLimit+1 {
		t.Fatalf("loadFolderOptions loaded %d rows, want capped %d plus selected", got, refPickerDefaultLimit)
	}
	if !hasOptionWithLabel(opts, selected.String(), "Папка 999") {
		t.Fatalf("selected folder %s was not added to options: %#v", selected, opts)
	}
	for _, row := range opts {
		if !asBool(row["is_folder"]) {
			t.Fatalf("non-folder leaked into folder options: %#v", row)
		}
	}
}

func TestLoadInitialTPRefOptionsIncludesSelectedOutsideFirstPage(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	refEnt := &metadata.Entity{
		Name: "Товары",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	doc := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		TableParts: []metadata.TablePart{{
			Name: "Товары",
			Fields: []metadata.Field{
				{Name: "Товар", Type: metadata.FieldType("reference:Товары"), RefEntity: "Товары"},
			},
		}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{refEnt, doc}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= refPickerDefaultLimit; i++ {
		id := uuid.MustParse("00000000-0000-0000-0000-" + fmt12(i))
		if err := db.Upsert(ctx, refEnt.Name, id, map[string]any{"Наименование": "Товар"}, refEnt); err != nil {
			t.Fatal(err)
		}
	}
	selected := uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err := db.Upsert(ctx, refEnt.Name, selected, map[string]any{"Наименование": "Выбранный ТЧ"}, refEnt); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{refEnt, doc}})
	s := &Server{reg: reg, store: db}

	opts, err := s.loadInitialTPRefOptions(ctx, doc, map[string][]map[string]any{
		"Товары": {{"Товар": selected.String()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := opts["Товары"]["Товар"]
	if !hasOptionWithLabel(rows, selected.String(), "Выбранный ТЧ") {
		t.Fatalf("selected TP ref %s was not added to initial options: %#v", selected, rows)
	}
}

func TestTreeChildrenJSON_ReturnsDirectChildren(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ent := &metadata.Entity{
		Name:         "Группы",
		Kind:         metadata.KindCatalog,
		Hierarchical: true,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	rootID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	childID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	grandchildID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	if err := db.Upsert(ctx, ent.Name, rootID, map[string]any{"Наименование": "Корень", "is_folder": true}, ent); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, ent.Name, childID, map[string]any{"Наименование": "Ребёнок", "parent_id": rootID, "is_folder": false}, ent); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, ent.Name, grandchildID, map[string]any{"Наименование": "Внук", "parent_id": childID, "is_folder": false}, ent); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
	s := &Server{reg: reg, store: db}

	rec := serveTreeChildren(t, s, ent.Name, "parent="+url.QueryEscape(rootID.String())+"&depth=0", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q, want JSON", ct)
	}
	var got treeChildrenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("rows = %#v, want one direct child", got.Rows)
	}
	row := got.Rows[0]
	if row.ID != childID.String() || row.ParentID != rootID.String() || row.Depth != 1 {
		t.Fatalf("row = %#v, want child of root at depth 1", row)
	}
	if len(row.Cells) == 0 || row.Cells[0] != "Ребёнок" {
		t.Fatalf("cells = %#v, want child label", row.Cells)
	}
}

func serveRefOptions(t *testing.T, s *Server, entity, query string, user *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	target := "/ui/_ref-options/" + entity
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("entity", entity)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	if user != nil {
		ctx = auth.ContextWithUser(ctx, user)
	}
	rec := httptest.NewRecorder()
	s.refOptionsJSON(rec, req.WithContext(ctx))
	return rec
}

func serveTreeChildren(t *testing.T, s *Server, entity, query string, user *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	target := "/ui/_tree-children/" + entity
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("entity", entity)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	if user != nil {
		ctx = auth.ContextWithUser(ctx, user)
	}
	rec := httptest.NewRecorder()
	s.treeChildrenJSON(rec, req.WithContext(ctx))
	return rec
}

func hasOptionWithLabel(rows []map[string]any, id, label string) bool {
	for _, row := range rows {
		if refValueString(row["id"]) == id && row["_label"] == label {
			return true
		}
	}
	return false
}

func fmt12(n int) string {
	return fmt.Sprintf("%012d", n)
}

func activityCatalogEntity() *metadata.Entity {
	return &metadata.Entity{
		Name: "Номенклатура",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Активный", Type: metadata.FieldTypeBool},
		},
		Activity: &metadata.ActivityConfig{
			Field:          "Активный",
			DefaultScope:   metadata.ActivityScopeActive,
			HideFromChoice: true,
		},
	}
}

func optionLabels(rows []map[string]any) []string {
	labels := make([]string, 0, len(rows))
	for _, row := range rows {
		if s, ok := row["_label"].(string); ok {
			labels = append(labels, s)
		}
	}
	return labels
}

func sameStringSet(a, b []string) bool {
	sort.Strings(a)
	sort.Strings(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
