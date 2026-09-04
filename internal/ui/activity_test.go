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
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
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
		// Состояние списка формы поиска и отбора берут из параметров запроса —
		// ровно то, что отдаёт обработчик после клика по «Скрытые».
		"Query":      url.Values{"activity": {metadata.ActivityScopeInactive}},
		"CanWrite":   true,
		"Lang":       "ru",
		"Total":      1,
		"Page":       1,
		"TotalPages": 1,
	})

	for _, want := range []string{
		"Активные",
		"Скрытые",
		"Все",
		`href="?activity=active"`,
		`href="?activity=inactive"`,
		`href="?activity=all"`,
		`name="activity" value="inactive"`,
		`data-ob-row-activity-enabled="1"`,
		`data-activity-inactive="1"`,
		`data-ob-row-activity-hide-tpl=`,
		`/__ID__/activity?active=0`,
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

func TestSetRecordActivity_ClearsServiceResponseCache(t *testing.T) {
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
	id := uuid.New()
	if err := db.Upsert(ctx, ent.Name, id, map[string]any{
		"Наименование": "Скрыть",
		"Активный":     true,
	}, ent); err != nil {
		t.Fatal(err)
	}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
	cache := newServiceCache(0)
	cache.Put("cached-page", "site", &cachedResponse{
		Status: http.StatusOK,
		Header: make(http.Header),
		Body:   []byte("old response"),
	}, time.Minute)
	if cache.Size() == 0 {
		t.Fatal("service cache was not populated")
	}

	srv := &Server{reg: reg, store: db, svcCache: cache}
	req := httptest.NewRequest(http.MethodPost,
		"/ui/catalog/"+url.PathEscape(ent.Name)+"/"+id.String()+"/activity?active=0", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("entity", ent.Name)
	rctx.URLParams.Add("id", id.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	srv.setRecordActivity(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if got := cache.Size(); got != 0 {
		t.Fatalf("service cache size after activity update = %d, want 0", got)
	}
	row, err := db.GetByID(ctx, ent.Name, id, ent)
	if err != nil {
		t.Fatal(err)
	}
	if active, _ := row["Активный"].(bool); active {
		t.Fatal("activity flag was not updated")
	}
}

func TestSetRecordActivity_RLSDoesNotRevealRecordExistence(t *testing.T) {
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
	deniedID := uuid.New()
	if err := db.Upsert(ctx, ent.Name, deniedID, map[string]any{
		"Наименование": "Скрытая запись",
		"Активный":     true,
	}, ent); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
	srv := &Server{reg: reg, store: db}
	user := &auth.User{Login: "restricted", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs: map[string][]string{ent.Name: {"write"}},
		RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
			ent.Name: {"write": {Field: "Наименование", Op: "eq", Value: auth.RowValue{Literal: "Разрешённая запись"}}},
		}},
	}}}}

	call := func(id uuid.UUID) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost,
			"/ui/catalog/"+url.PathEscape(ent.Name)+"/"+id.String()+"/activity?active=0", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("entity", ent.Name)
		rctx.URLParams.Add("id", id.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		req = req.WithContext(auth.ContextWithUser(req.Context(), user))
		rec := httptest.NewRecorder()
		srv.setRecordActivity(rec, req)
		return rec
	}

	denied := call(deniedID)
	missing := call(uuid.New())
	if denied.Code != http.StatusForbidden || missing.Code != http.StatusForbidden {
		t.Fatalf("restricted activity statuses: denied=%d missing=%d, want both %d",
			denied.Code, missing.Code, http.StatusForbidden)
	}
}

func TestSetRecordActivity_RunsOnWriteWithCompleteSnapshot(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ent := &metadata.Entity{
		Name:         "ИерархияАктивности",
		Kind:         metadata.KindCatalog,
		Hierarchical: true,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Активный", Type: metadata.FieldTypeBool},
			{Name: "Сводка", Type: metadata.FieldTypeString},
		},
		TableParts: []metadata.TablePart{{
			Name: "Контакты",
			Fields: []metadata.Field{
				{Name: "Значение", Type: metadata.FieldTypeString},
			},
		}},
		Activity: &metadata.ActivityConfig{
			Field:          "Активный",
			DefaultScope:   metadata.ActivityScopeActive,
			HideFromChoice: true,
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}

	rootID := uuid.New()
	if err := db.Upsert(ctx, ent.Name, rootID, map[string]any{
		"Наименование": "Корень",
		"Активный":     true,
		"Сводка":       "root",
		"is_folder":    true,
	}, ent); err != nil {
		t.Fatal(err)
	}
	targetID := uuid.New()
	if err := db.Upsert(ctx, ent.Name, targetID, map[string]any{
		"Наименование": "Не затирать",
		"Активный":     true,
		"Сводка":       "before-hook",
		"is_folder":    true,
		"parent_id":    rootID,
	}, ent); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTablePartRows(ctx, ent.Name, "Контакты", targetID,
		[]map[string]any{{"Значение": "kept-row"}}, ent.TableParts[0]); err != nil {
		t.Fatal(err)
	}

	program := mustParse(t, `
Процедура ПриЗаписи()
	Если ЗначениеЗаполнено(ЭтотОбъект._version) Тогда
		ВызватьИсключение("служебная версия попала в OnWrite");
	КонецЕсли;
	Если ЗначениеЗаполнено(ЭтотОбъект.id) Тогда
		ВызватьИсключение("служебный id попал в OnWrite");
	КонецЕсли;
	Если ЭтотОбъект.Активный Тогда
		ЭтотОбъект.Сводка = "active";
	Иначе
		ЭтотОбъект.Сводка = "inactive";
	КонецЕсли;
	Для Каждого Стр Из ЭтотОбъект.Контакты Цикл
		ЭтотОбъект.Сводка = ЭтотОбъект.Сводка + ":" + Стр.Значение;
	КонецЦикла;
КонецПроцедуры`)
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{ent},
		Programs: map[string]*ast.Program{ent.Name: program},
	})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	srv := &Server{
		reg:      reg,
		store:    db,
		interp:   interp,
		lockMgr:  runtime.NewLockManager(),
		messages: NewMessageStore(),
	}
	srv.entitySvc = srv.newEntityService(nil)

	req := httptest.NewRequest(http.MethodPost,
		"/ui/catalog/"+url.PathEscape(ent.Name)+"/"+targetID.String()+"/activity?active=0", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("entity", ent.Name)
	rctx.URLParams.Add("id", targetID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	srv.setRecordActivity(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	row, err := db.GetByID(ctx, ent.Name, targetID, ent)
	if err != nil {
		t.Fatal(err)
	}
	if asBool(row["Активный"]) {
		t.Fatal("activity flag was not updated")
	}
	if got := row["Сводка"]; got != "inactive:kept-row" {
		t.Fatalf("OnWrite summary = %v, want inactive:kept-row", got)
	}
	if got := row["Наименование"]; got != "Не затирать" {
		t.Fatalf("unrelated field = %v, want preserved value", got)
	}
	if !asBool(row["is_folder"]) {
		t.Fatal("activity toggle changed a folder into an element")
	}
	if got := refValueString(row["parent_id"]); got != rootID.String() {
		t.Fatalf("parent_id = %q, want %s", got, rootID)
	}
	rows, err := db.GetTablePartRows(ctx, ent.Name, "Контакты", targetID, ent.TableParts[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["Значение"] != "kept-row" {
		t.Fatalf("table part rows after activity toggle = %#v", rows)
	}
}

func TestSetRecordActivity_MediaRevokesAndRepublishesImage(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	media := &metadata.Entity{
		Name: "Медиа",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Файл", Type: metadata.FieldTypeImage},
			{Name: "ПубличнаяСсылка", Type: metadata.FieldTypeString},
			{Name: "Активен", Type: metadata.FieldTypeBool},
		},
		Activity: &metadata.ActivityConfig{
			Field:          "Активен",
			DefaultScope:   metadata.ActivityScopeActive,
			HideFromChoice: true,
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{media}); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureBlobTable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureAttachmentTable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePublicFilesSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveNetworkEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	blob, err := db.PutBlob(ctx, "image/png", strings.NewReader("MEDIA-IMAGE"), 1<<20,
		storage.BlobOwner{Kind: "catalog", Entity: media.Name})
	if err != nil {
		t.Fatal(err)
	}
	token, err := db.PublishBlob(ctx, blob.ID, storage.PublishOptions{})
	if err != nil {
		t.Fatal(err)
	}
	oldURL := publicFileURL(token)
	mediaID := uuid.New()
	if err := db.Upsert(ctx, media.Name, mediaID, map[string]any{
		"Наименование":    "Логотип",
		"Файл":            blob.ID.String(),
		"ПубличнаяСсылка": oldURL,
		"Активен":         true,
	}, media); err != nil {
		t.Fatal(err)
	}

	program := mustParse(t, `
Процедура ПриЗаписи()
	Если ЗначениеЗаполнено(ЭтотОбъект.Файл) Тогда
		Если ЭтотОбъект.Активен Тогда
			ЭтотОбъект.ПубличнаяСсылка = ОпубликоватьКартинку(ЭтотОбъект.Файл);
		Иначе
			СнятьПубликациюКартинки(ЭтотОбъект.Файл);
			ЭтотОбъект.ПубличнаяСсылка = "";
		КонецЕсли;
	КонецЕсли;
КонецПроцедуры`)
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{media},
		Programs: map[string]*ast.Program{media.Name: program},
	})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	srv := &Server{
		reg:      reg,
		store:    db,
		interp:   interp,
		lockMgr:  runtime.NewLockManager(),
		messages: NewMessageStore(),
	}
	srv.entitySvc = srv.newEntityService(nil)
	publicRouter := chi.NewRouter()
	srv.MountServices(publicRouter)

	fetchPublic := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		publicRouter.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	toggle := func(active bool) *httptest.ResponseRecorder {
		t.Helper()
		activeValue := "0"
		if active {
			activeValue = "1"
		}
		req := httptest.NewRequest(http.MethodPost,
			"/ui/catalog/"+url.PathEscape(media.Name)+"/"+mediaID.String()+"/activity?active="+activeValue, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("entity", media.Name)
		rctx.URLParams.Add("id", mediaID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		srv.setRecordActivity(rec, req)
		return rec
	}

	if rec := fetchPublic(oldURL); rec.Code != http.StatusOK || rec.Body.String() != "MEDIA-IMAGE" {
		t.Fatalf("published image precondition: status=%d body=%q", rec.Code, rec.Body.String())
	}
	if rec := toggle(false); rec.Code != http.StatusSeeOther {
		t.Fatalf("deactivate status=%d body=%q", rec.Code, rec.Body.String())
	}
	row, err := db.GetByID(ctx, media.Name, mediaID, media)
	if err != nil {
		t.Fatal(err)
	}
	if asBool(row["Активен"]) || row["ПубличнаяСсылка"] != "" {
		t.Fatalf("deactivated media row = %#v", row)
	}
	if rec := fetchPublic(oldURL); rec.Code != http.StatusNotFound {
		t.Fatalf("revoked URL status=%d, want %d", rec.Code, http.StatusNotFound)
	}

	if rec := toggle(true); rec.Code != http.StatusSeeOther {
		t.Fatalf("reactivate status=%d body=%q", rec.Code, rec.Body.String())
	}
	row, err = db.GetByID(ctx, media.Name, mediaID, media)
	if err != nil {
		t.Fatal(err)
	}
	newURL, _ := row["ПубличнаяСсылка"].(string)
	if !asBool(row["Активен"]) || !strings.HasPrefix(newURL, "/pub/") {
		t.Fatalf("reactivated media row = %#v", row)
	}
	if newURL == oldURL {
		t.Fatalf("reactivation reused revoked URL %q", newURL)
	}
	if rec := fetchPublic(newURL); rec.Code != http.StatusOK || rec.Body.String() != "MEDIA-IMAGE" {
		t.Fatalf("republished image: status=%d body=%q", rec.Code, rec.Body.String())
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
	wantDetailURL := "/ui/catalog/" + strings.ToLower(ent.Name) + "/" + childID.String() + "/detail-panel"
	if row.DetailURL != wantDetailURL {
		t.Fatalf("detail URL = %q, want %q", row.DetailURL, wantDetailURL)
	}
	if strings.Contains(rec.Body.String(), `"detail":`) {
		t.Fatalf("lazy tree response contains an inline detail payload: %s", rec.Body.String())
	}
}

func TestTreeChildrenJSON_HonorsExplicitDetailPanel(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ent := &metadata.Entity{
		Name: "Groups", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{
			{Name: "Name", Type: metadata.FieldTypeString},
			{Name: "Secret", Type: metadata.FieldTypeString},
		},
		ListForm:    []string{"Name"},
		DetailPanel: &metadata.DetailPanel{Fields: []string{"Name"}, FieldsSet: true},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	parentID := uuid.New()
	childID := uuid.New()
	if err := db.Upsert(ctx, ent.Name, parentID, map[string]any{"Name": "Parent", "is_folder": true}, ent); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, ent.Name, childID, map[string]any{"Name": "Child", "Secret": "must-not-appear", "parent_id": parentID}, ent); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
	s := &Server{reg: reg, store: db}
	rec := serveTreeChildren(t, s, ent.Name, "parent="+url.QueryEscape(parentID.String())+"&depth=0", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var got treeChildrenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Rows) != 1 {
		t.Fatalf("decode rows: err=%v rows=%#v", err, got.Rows)
	}
	wantDetailURL := "/ui/catalog/groups/" + childID.String() + "/detail-panel"
	if got.Rows[0].DetailURL != wantDetailURL {
		t.Fatalf("detail URL = %q, want %q", got.Rows[0].DetailURL, wantDetailURL)
	}
	if strings.Contains(rec.Body.String(), "must-not-appear") || strings.Contains(rec.Body.String(), `"detail":`) {
		t.Fatalf("tree response contains an inline detail payload: %s", rec.Body.String())
	}

	panelReq := reqWithChi(http.MethodGet, wantDetailURL, nil, map[string]string{
		"kind": "catalog", "entity": ent.Name, "id": childID.String(),
	})
	panelRec := httptest.NewRecorder()
	s.detailPanelRecord(panelRec, panelReq)
	if panelRec.Code != http.StatusOK {
		t.Fatalf("detail panel code=%d body=%s", panelRec.Code, panelRec.Body.String())
	}
	var panel detailPanelData
	if err := json.Unmarshal(panelRec.Body.Bytes(), &panel); err != nil {
		t.Fatal(err)
	}
	if _, ok := detailPanelValueByLabel(panel, "Name"); !ok {
		t.Fatalf("explicit field missing: %+v", panel)
	}
	if _, ok := detailPanelValueByLabel(panel, "Secret"); ok {
		t.Fatalf("lazy tree ignored explicit composition: %+v", panel)
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
