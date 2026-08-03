package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
)

func clientEntityAPI() *metadata.Entity {
	return &metadata.Entity{
		Name: "Клиент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Телефон", Type: metadata.FieldTypeString},
			{Name: "Паспорт", Type: metadata.FieldTypeString},
		},
	}
}

func maskUser(ops []string, fields auth.FieldPolicies) *auth.User {
	return apiUser("operator", auth.Permission{
		Catalogs:    map[string][]string{"Клиент": ops},
		FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{"Клиент": fields}},
	})
}

func TestAPI_FieldMask_GetAndList(t *testing.T) {
	cat := clientEntityAPI()
	h, ctx := newAPITestHandler(t, []*metadata.Entity{cat}, nil)
	id := uuid.New()
	if err := h.store.Upsert(ctx, "Клиент", id, map[string]any{
		"Наименование": "Иванов", "Телефон": "+79161234455", "Паспорт": "4509 123456",
	}, cat); err != nil {
		t.Fatal(err)
	}
	user := maskUser([]string{"read"}, auth.FieldPolicies{
		"Телефон": {Read: "mask_tail", Keep: 4},
		"Паспорт": {Read: "hide"},
	})

	getReq := withUser(reqWithEntity("GET", "/catalogs/Клиент/"+id.String(), nil,
		map[string]string{"entity": "Клиент", "id": id.String()}, nil), user)
	rec := httptest.NewRecorder()
	h.getObject(metadata.KindCatalog).ServeHTTP(rec, getReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}
	var obj map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &obj); err != nil {
		t.Fatal(err)
	}
	if obj["Телефон"] != "••••••••4455" {
		t.Fatalf("phone not masked: %v", obj["Телефон"])
	}
	if _, ok := obj["Паспорт"]; ok {
		t.Fatalf("passport must be hidden, got %v", obj["Паспорт"])
	}
	if obj["Наименование"] != "Иванов" {
		t.Fatalf("name must stay full: %v", obj["Наименование"])
	}

	listReq := withUser(reqWithEntity("GET", "/catalogs/Клиент", nil,
		map[string]string{"entity": "Клиент"}, nil), user)
	lrec := httptest.NewRecorder()
	h.listObjects(metadata.KindCatalog).ServeHTTP(lrec, listReq)
	var rows []map[string]any
	if err := json.Unmarshal(lrec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["Телефон"] != "••••••••4455" {
		t.Fatalf("list phone not masked: %v", rows)
	}
	if _, ok := rows[0]["Паспорт"]; ok {
		t.Fatal("list passport must be hidden")
	}
}

func TestAPI_FieldMask_AdminAndPlainRoleSeeFull(t *testing.T) {
	cat := clientEntityAPI()
	h, ctx := newAPITestHandler(t, []*metadata.Entity{cat}, nil)
	id := uuid.New()
	if err := h.store.Upsert(ctx, "Клиент", id, map[string]any{"Телефон": "+79161234455"}, cat); err != nil {
		t.Fatal(err)
	}
	// Роль с read без field_access видит полное значение (least-restrictive OR).
	plain := apiUser("full", auth.Permission{Catalogs: map[string][]string{"Клиент": {"read"}}})
	getReq := withUser(reqWithEntity("GET", "/catalogs/Клиент/"+id.String(), nil,
		map[string]string{"entity": "Клиент", "id": id.String()}, nil), plain)
	rec := httptest.NewRecorder()
	h.getObject(metadata.KindCatalog).ServeHTTP(rec, getReq)
	var obj map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &obj)
	if obj["Телефон"] != "+79161234455" {
		t.Fatalf("plain read role must see full phone: %v", obj["Телефон"])
	}
}

func TestAPI_FieldDisclose(t *testing.T) {
	cat := clientEntityAPI()
	h, ctx := newAPITestHandler(t, []*metadata.Entity{cat}, nil)
	if err := h.store.EnsureAuditSchema(ctx); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := h.store.Upsert(ctx, "Клиент", id, map[string]any{"Телефон": "+79161234455"}, cat); err != nil {
		t.Fatal(err)
	}
	fields := auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}}

	discloseReq := func(user *auth.User, query string) *httptest.ResponseRecorder {
		target := "/catalogs/Клиент/" + id.String() + "/field/Телефон" + query
		req := withUser(reqWithEntity("GET", target, nil,
			map[string]string{"name": "Клиент", "id": id.String(), "field": "Телефон"}, nil), user)
		rec := httptest.NewRecorder()
		h.discloseField(metadata.KindCatalog).ServeHTTP(rec, req)
		return rec
	}

	// Без права disclose → 403.
	reader := maskUser([]string{"read"}, fields)
	if rec := discloseReq(reader, "?disclose=1&reason="+url.QueryEscape("проверка")); rec.Code != http.StatusForbidden {
		t.Fatalf("disclose without right: expected 403, got %d %s", rec.Code, rec.Body.String())
	}
	// С правом, но без основания → 400.
	discloser := maskUser([]string{"read", "disclose"}, fields)
	if rec := discloseReq(discloser, "?disclose=1"); rec.Code != http.StatusBadRequest {
		t.Fatalf("disclose without reason: expected 400, got %d", rec.Code)
	}
	// С правом и основанием → 200, полное значение + аудит без значения.
	rec := discloseReq(discloser, "?disclose=1&reason="+url.QueryEscape("звонок клиента"))
	if rec.Code != http.StatusOK {
		t.Fatalf("disclose: %d %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			Value     string `json:"value"`
			Disclosed bool   `json:"disclosed"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Data.Value != "+79161234455" || !env.Data.Disclosed {
		t.Fatalf("disclose payload = %+v", env.Data)
	}
	entries, err := h.store.AuditByRecord(ctx, "Клиент", id)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range entries {
		if e.Action == "disclose" {
			found = true
			if e.Field != "Телефон" || e.Reason != "звонок клиента" {
				t.Fatalf("audit fields wrong: %+v", e)
			}
			if s, _ := e.NewValue.(string); s == "+79161234455" {
				t.Fatal("audit must NOT store the disclosed value")
			}
		}
	}
	if !found {
		t.Fatal("disclose audit entry not written")
	}

	// Without disclose flag → masked value, no audit needed.
	rec2 := discloseReq(reader, "")
	var env2 struct {
		Data struct {
			Value string `json:"value"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec2.Body.Bytes(), &env2)
	if env2.Data.Value != "••••••••4455" {
		t.Fatalf("plain field fetch must be masked: %q", env2.Data.Value)
	}
}

// Fail-closed регресс (CC-SEC-004): при отказе записи в аудит REST-раскрытие не
// выдаёт значение ПДн. Схему аудита намеренно не создаём — LogDisclose падает,
// раскрытие должно вернуть 500 без значения.
func TestAPI_FieldDisclose_FailClosedWhenAuditFails(t *testing.T) {
	cat := clientEntityAPI()
	h, ctx := newAPITestHandler(t, []*metadata.Entity{cat}, nil)
	// НАМЕРЕННО без EnsureAuditSchema → LogDisclose вернёт ошибку.
	id := uuid.New()
	if err := h.store.Upsert(ctx, "Клиент", id, map[string]any{"Телефон": "+79161234455"}, cat); err != nil {
		t.Fatal(err)
	}
	discloser := maskUser([]string{"read", "disclose"}, auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}})
	target := "/catalogs/Клиент/" + id.String() + "/field/Телефон?disclose=1&reason=" + url.QueryEscape("звонок клиента")
	req := withUser(reqWithEntity("GET", target, nil,
		map[string]string{"name": "Клиент", "id": id.String(), "field": "Телефон"}, nil), discloser)
	rec := httptest.NewRecorder()
	h.discloseField(metadata.KindCatalog).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("ожидался 500 при отказе аудита, получено %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "+79161234455") {
		t.Fatal("ПДн не должны раскрываться при отказе записи в аудит")
	}
}

func TestAPI_Report_MasksColumnAndDeniesFilter(t *testing.T) {
	cat := clientEntityAPI()
	repPhone := &reportpkg.Report{Name: "КлиентыТел", Query: `ВЫБРАТЬ Телефон ИЗ Справочник.Клиент`}
	repName := &reportpkg.Report{Name: "КлиентыИмя", Query: `ВЫБРАТЬ Наименование ИЗ Справочник.Клиент`}
	repFilter := &reportpkg.Report{Name: "КлиентыОтбор", Query: `ВЫБРАТЬ Наименование ИЗ Справочник.Клиент ГДЕ Телефон <> ""`}
	h, ctx := newAPITestHandlerWithReports(t, []*metadata.Entity{cat},
		[]*reportpkg.Report{repPhone, repName, repFilter}, nil)
	if err := h.store.Upsert(ctx, "Клиент", uuid.New(), map[string]any{
		"Наименование": "Иванов", "Телефон": "+79161234455",
	}, cat); err != nil {
		t.Fatal(err)
	}
	operator := apiUser("operator", auth.Permission{
		Catalogs:    map[string][]string{"Клиент": {"read"}},
		Reports:     map[string][]string{"КлиентыТел": {"run"}, "КлиентыИмя": {"run"}, "КлиентыОтбор": {"run"}},
		FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{"Клиент": {"Телефон": {Read: "mask_tail", Keep: 4}}}},
	})

	run := func(user *auth.User, name string) *httptest.ResponseRecorder {
		req := withUser(reqWithEntity("GET", "/api/v2/report/"+name, nil,
			map[string]string{"name": name}, nil), user)
		rec := httptest.NewRecorder()
		h.runReportV2().ServeHTTP(rec, req)
		return rec
	}

	// План 88E: отчёт с чувствительной колонкой строится, но значение приходит
	// замаскированным — реальный номер в ответ не попадает.
	rec := run(operator, "КлиентыТел")
	if rec.Code != http.StatusOK {
		t.Fatalf("masked-column report: expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "+79161234455") {
		t.Fatalf("реальный номер утёк в отчёт: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "4455") {
		t.Fatalf("ожидался хвост номера под маской mask_tail: %s", rec.Body.String())
	}
	// Отбор по защищённому полю — оракул перебора, отчёт запрещён целиком.
	if rec := run(operator, "КлиентыОтбор"); rec.Code != http.StatusForbidden {
		t.Fatalf("filter on masked field: expected 403, got %d %s", rec.Code, rec.Body.String())
	}
	// Отчёт без чувствительной колонки строится.
	if rec := run(operator, "КлиентыИмя"); rec.Code != http.StatusOK {
		t.Fatalf("safe report: expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	// Админ видит всё.
	admin := &auth.User{ID: "a", Login: "admin", IsAdmin: true}
	if rec := run(admin, "КлиентыТел"); rec.Code != http.StatusOK {
		t.Fatalf("admin masked-column report: expected 200, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestAPI_FieldMask_WriteGuard(t *testing.T) {
	cat := clientEntityAPI()
	h, ctx := newAPITestHandler(t, []*metadata.Entity{cat}, nil)
	id := uuid.New()
	if err := h.store.Upsert(ctx, "Клиент", id, map[string]any{
		"Наименование": "Иванов", "Телефон": "+79161234455",
	}, cat); err != nil {
		t.Fatal(err)
	}
	user := maskUser([]string{"read", "write"}, auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}})

	body := []byte(`{"Наименование":"Петров","Телефон":"7770001122"}`)
	req := withUser(reqWithEntity("PUT", "/catalogs/Клиент/"+id.String(), body,
		map[string]string{"entity": "Клиент", "id": id.String()}, nil), user)
	rec := httptest.NewRecorder()
	h.updateObject(metadata.KindCatalog).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}
	row, err := h.store.GetByID(ctx, "Клиент", id, cat)
	if err != nil {
		t.Fatal(err)
	}
	if row["Телефон"] != "+79161234455" {
		t.Fatalf("masked user must NOT overwrite phone, got %v", row["Телефон"])
	}
	if row["Наименование"] != "Петров" {
		t.Fatalf("visible field must update, got %v", row["Наименование"])
	}
}
