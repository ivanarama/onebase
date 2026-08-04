package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func uiClientEntity() *metadata.Entity {
	return &metadata.Entity{
		Name: "Клиент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Телефон", Type: metadata.FieldTypeString},
			{Name: "Адрес", Type: metadata.FieldTypeString},
		},
	}
}

func uiMaskUser(ops []string, fields auth.FieldPolicies) *auth.User {
	return &auth.User{
		ID:    "op",
		Login: "operator",
		Roles: []*auth.Role{{
			Name: "Оператор",
			Permissions: auth.Permission{
				Catalogs:    map[string][]string{"Клиент": ops},
				FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{"Клиент": fields}},
			},
		}},
	}
}

// План 88E: защищённое поле в простой колонке выборки маскируется в результате
// (в т.ч. под алиасом КАК и в проекции «*»), а поле, участвующее в отборе,
// группировке или агрегате, по-прежнему закрывает запрос целиком — маска на
// выходе от перебора условием не защищает.
func TestUI_QueryProjectionMaskGate(t *testing.T) {
	cat := uiClientEntity()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{cat})
	if err := s.store.Upsert(ctx, "Клиент", uuid.New(), map[string]any{
		"Наименование": "Иванов", "Телефон": "+79161234455", "Адрес": "Москва, Тверская 1",
	}, cat); err != nil {
		t.Fatal(err)
	}
	user := uiMaskUser([]string{"read"}, auth.FieldPolicies{
		"Телефон": {Read: "mask_tail", Keep: 4},
		"Адрес":   {Read: "mask_all"},
	})
	uctx := auth.ContextWithUser(ctx, user)

	for _, denied := range []string{
		`ВЫБРАТЬ Строка(Телефон) КАК Контакт ИЗ Справочник.Клиент`,
		`ВЫБРАТЬ Телефон + " " + Адрес КАК Контакт ИЗ Справочник.Клиент`,
		`ВЫБРАТЬ Наименование ИЗ Справочник.Клиент ГДЕ Телефон <> ""`,
		`ВЫБРАТЬ Наименование ИЗ Справочник.Клиент УПОРЯДОЧИТЬ ПО Телефон`,
	} {
		compiled, err := s.compileQueryWithRowAccess(uctx, denied, nil)
		if err != nil {
			t.Fatal(err)
		}
		if plan := s.queryMaskPlan(uctx, compiled); plan.Denied == "" {
			t.Fatalf("защищённое поле пропущено вне простой колонки: %s", denied)
		}
	}

	for _, tc := range []struct{ text, column, want string }{
		{`ВЫБРАТЬ Телефон КАК Контакт ИЗ Справочник.Клиент`, "контакт", "••••••••4455"},
		{`ВЫБРАТЬ Телефон ИЗ Справочник.Клиент`, "телефон", "••••••••4455"},
		{`ВЫБРАТЬ Адрес ИЗ Справочник.Клиент`, "адрес", "••••••"},
		{`ВЫБРАТЬ * ИЗ Справочник.Клиент`, "телефон", "••••••••4455"},
	} {
		compiled, err := s.compileQueryWithRowAccess(uctx, tc.text, nil)
		if err != nil {
			t.Fatal(err)
		}
		plan := s.queryMaskPlan(uctx, compiled)
		if plan.Denied != "" {
			t.Fatalf("простая колонка отклонена вместо маскирования: %s (%s)", tc.text, plan.Denied)
		}
		rows, _, err := s.store.RunQuery(uctx, compiled.SQL, compiled.Args)
		if err != nil {
			t.Fatal(err)
		}
		if err := plan.Apply(rows); err != nil {
			t.Fatalf("%s: %v", tc.text, err)
		}
		if got := fmt.Sprint(rows[0][tc.column]); got != tc.want {
			t.Fatalf("%s: колонка %q = %q, ожидалось %q", tc.text, tc.column, got, tc.want)
		}
	}

	// Роль без field_access и админ читают запрос без маски.
	full := uiMaskUser([]string{"read"}, nil)
	compiled, err := s.compileQueryWithRowAccess(uctx, `ВЫБРАТЬ Телефон ИЗ Справочник.Клиент`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan := s.queryMaskPlan(auth.ContextWithUser(ctx, full), compiled); !plan.Empty() {
		t.Fatalf("роль без field_access не должна маскировать: %+v", plan)
	}
}

// Основная защита формы: пользователь, видящий поле лишь замаскированным, не
// перезаписывает реальное значение при сохранении карточки (submitEdit).
func TestUI_SubmitEdit_MaskedFieldWriteGuard(t *testing.T) {
	cat := uiClientEntity()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{cat})
	id := uuid.New()
	if err := s.store.Upsert(ctx, "Клиент", id, map[string]any{
		"Наименование": "Иванов", "Телефон": "+79161234455",
	}, cat); err != nil {
		t.Fatal(err)
	}
	user := uiMaskUser([]string{"read", "write"}, auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}})

	form := url.Values{"Наименование": {"Петров"}, "Телефон": {"7770001122"}}
	r := reqWithChi("POST", "/ui/catalog/Клиент/"+id.String(), form,
		map[string]string{"kind": "catalog", "entity": "Клиент", "id": id.String()})
	r = r.WithContext(auth.ContextWithUser(r.Context(), user))
	w := httptest.NewRecorder()
	s.submitEdit(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("submitEdit: expected 303, got %d: %s", w.Code, w.Body.String())
	}
	row, err := s.store.GetByID(ctx, "Клиент", id, cat)
	if err != nil {
		t.Fatal(err)
	}
	if row["Телефон"] != "+79161234455" {
		t.Fatalf("masked field must NOT be overwritten, got %v", row["Телефон"])
	}
	if row["Наименование"] != "Петров" {
		t.Fatalf("visible field must update, got %v", row["Наименование"])
	}
}

// Раскрытие под правом disclose: возвращает полное значение и пишет аудит без
// значения.
func TestUI_DiscloseField(t *testing.T) {
	cat := uiClientEntity()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{cat})
	if err := s.store.EnsureAuditSchema(ctx); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := s.store.Upsert(ctx, "Клиент", id, map[string]any{"Телефон": "+79161234455"}, cat); err != nil {
		t.Fatal(err)
	}
	user := uiMaskUser([]string{"read", "disclose"}, auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}})

	form := url.Values{"field": {"Телефон"}, "reason": {"звонок клиента"}}
	r := reqWithChi("POST", "/ui/catalog/Клиент/"+id.String()+"/disclose", form,
		map[string]string{"kind": "catalog", "entity": "Клиент", "id": id.String()})
	rctx := auth.ContextWithUser(r.Context(), user)
	rctx = storage.WithAuditUser(rctx, user.ID, user.Login)
	r = r.WithContext(rctx)
	w := httptest.NewRecorder()
	s.discloseField(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("disclose: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Value     string `json:"value"`
		Disclosed bool   `json:"disclosed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Value != "+79161234455" || !resp.Disclosed {
		t.Fatalf("disclose response = %+v", resp)
	}
	entries, err := s.store.AuditByRecord(ctx, "Клиент", id)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range entries {
		if e.Action == "disclose" {
			found = true
			if e.Reason != "звонок клиента" || e.Field != "Телефон" {
				t.Fatalf("audit entry wrong: %+v", e)
			}
			if sv, _ := e.NewValue.(string); sv == "+79161234455" {
				t.Fatal("audit must not store disclosed value")
			}
		}
	}
	if !found {
		t.Fatal("disclose audit entry missing")
	}
}

// LEAK A регресс (план 88): выбранная ссылка, догруженная ВНЕ первой страницы
// пикера (appendSelectedRefOptions → store.GetByID), должна маскироваться так же,
// как строки списка — иначе ПДн утекли бы в JSON опций выбора (HTML/DevTools).
func TestUI_AppendSelectedRefOptions_MasksPII(t *testing.T) {
	cat := uiClientEntity()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{cat})
	id := uuid.New()
	if err := s.store.Upsert(ctx, "Клиент", id, map[string]any{
		"Наименование": "Иванов", "Телефон": "+79161234455",
	}, cat); err != nil {
		t.Fatal(err)
	}
	user := uiMaskUser([]string{"read"}, auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}})
	uctx := auth.ContextWithUser(ctx, user)

	// rows пуст → выбранный id идёт через GetByID — путь, который раньше не
	// маскировался.
	rows := s.appendSelectedRefOptions(uctx, nil, cat, []string{id.String()})
	if len(rows) != 1 {
		t.Fatalf("ожидалась 1 выбранная опция, получено %d", len(rows))
	}
	phone, _ := rows[0]["Телефон"].(string)
	if phone == "+79161234455" {
		t.Fatalf("ПДн утекли в опции выбора: %q", phone)
	}
	if !strings.HasSuffix(phone, "4455") || !strings.Contains(phone, "•") {
		t.Fatalf("Телефон должен быть замаскирован (mask_tail keep=4), получено %q", phone)
	}
	if rows[0]["_label"] != "Иванов" {
		t.Fatalf("подпись опции должна остаться видимой, получено %v", rows[0]["_label"])
	}
}

// Vertical UUID→label resolvers must mask the referenced row before choosing
// its first string field. Otherwise a report/export that projects only a UUID
// can disclose a protected value that was never present in the projection.
func TestUI_ReferenceLabelsMaskFirstStringField(t *testing.T) {
	client := &metadata.Entity{
		Name: "Клиент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Телефон", Type: metadata.FieldTypeString},
		},
	}
	call := &metadata.Entity{
		Name: "Звонок",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Клиент", Type: "reference:Клиент", RefEntity: client.Name},
		},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client, call})
	id := uuid.New()
	const secret = "+79161234455"
	if err := s.store.Upsert(ctx, client.Name, id, map[string]any{"Телефон": secret}, client); err != nil {
		t.Fatal(err)
	}
	user := uiMaskUser([]string{"read"}, auth.FieldPolicies{"Телефон": {Read: "mask_all"}})
	uctx := auth.ContextWithUser(ctx, user)

	reportRows := []map[string]any{{"Клиент": id.String()}}
	s.resolveUUIDsInReport(uctx, reportRows, "")
	if got := fmt.Sprint(reportRows[0]["Клиент"]); got == secret || got != "••••••" {
		t.Fatalf("report UUID label = %q, want fixed mask", got)
	}

	refRows := []map[string]any{{"Клиент": id.String()}}
	s.resolveRefs(uctx, call, refRows)
	if got := fmt.Sprint(refRows[0]["Клиент"]); got == secret || got != "••••••" {
		t.Fatalf("reference field label = %q, want fixed mask", got)
	}
}

// LEAK B+C регресс (план 88): buildPrintRefs разрешает связанные записи для
// печатной формы (декларативный и DSL-пути) и обязан маскировать их ПДн —
// иначе полное значение поля ссылки утекло бы в готовый PDF.
func TestUI_BuildPrintRefs_MasksPII(t *testing.T) {
	client := uiClientEntity()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client})
	clientID := uuid.New()
	if err := s.store.Upsert(ctx, "Клиент", clientID, map[string]any{
		"Наименование": "Иванов", "Телефон": "+79161234455",
	}, client); err != nil {
		t.Fatal(err)
	}
	// Документ с полем-ссылкой на Клиента; сам документ в реестр не нужен —
	// buildPrintRefs берёт метаданные ссылки из реестра (Клиент зарегистрирован).
	doc := &metadata.Entity{
		Name: "Звонок", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Клиент", RefEntity: "Клиент"}},
	}
	user := uiMaskUser([]string{"read"}, auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}})
	uctx := auth.ContextWithUser(ctx, user)

	refs := s.buildPrintRefs(uctx, map[string]any{"Клиент": clientID.String()}, doc, nil)
	ref := refs[clientID.String()]
	if ref == nil {
		t.Fatal("ссылка на клиента не разрешена")
	}
	phone, _ := ref["Телефон"].(string)
	if phone == "+79161234455" {
		t.Fatalf("ПДн утекли в связанные записи печати: %q", phone)
	}
	if !strings.HasSuffix(phone, "4455") || !strings.Contains(phone, "•") {
		t.Fatalf("Телефон ссылки должен быть замаскирован, получено %q", phone)
	}
}

// Fail-closed регресс (CC-SEC-004): если запись в аудит раскрытия не удалась,
// значение ПДн НЕ выдаётся клиенту. Схему аудита намеренно не создаём, поэтому
// LogDisclose падает — раскрытие должно вернуть 500 без значения.
func TestUI_DiscloseField_FailClosedWhenAuditFails(t *testing.T) {
	cat := uiClientEntity()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{cat})
	// НАМЕРЕННО не вызываем EnsureAuditSchema → LogDisclose вернёт ошибку.
	id := uuid.New()
	if err := s.store.Upsert(ctx, "Клиент", id, map[string]any{"Телефон": "+79161234455"}, cat); err != nil {
		t.Fatal(err)
	}
	user := uiMaskUser([]string{"read", "disclose"}, auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}})

	form := url.Values{"field": {"Телефон"}, "reason": {"звонок клиента"}}
	r := reqWithChi("POST", "/ui/catalog/Клиент/"+id.String()+"/disclose", form,
		map[string]string{"kind": "catalog", "entity": "Клиент", "id": id.String()})
	rctx := auth.ContextWithUser(r.Context(), user)
	rctx = storage.WithAuditUser(rctx, user.ID, user.Login)
	r = r.WithContext(rctx)
	w := httptest.NewRecorder()
	s.discloseField(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("ожидался 500 при отказе аудита, получено %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "+79161234455") {
		t.Fatal("ПДн не должны раскрываться при отказе записи в аудит")
	}
}

// План 88E, добор по ревью: формулировки, которыми поколоночный разбор
// обходили. Каждая из них до починки выполнялась с реальными значениями, хотя
// в 88D такой запрос отклонялся.
func TestUI_QueryProjectionMaskОбходыРазбора(t *testing.T) {
	cat := uiClientEntity()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{cat})
	if err := s.store.Upsert(ctx, "Клиент", uuid.New(), map[string]any{
		"Наименование": "Иванов", "Телефон": "+79161234455", "Адрес": "Москва, Тверская 1",
	}, cat); err != nil {
		t.Fatal(err)
	}
	user := uiMaskUser([]string{"read"}, auth.FieldPolicies{
		"Телефон": {Read: "mask_tail", Keep: 4},
	})
	uctx := auth.ContextWithUser(ctx, user)

	// Отбор, группировка и сортировка по защищённой колонке — отказ, как бы на
	// колонку ни сослались: по имени поля, по выходному алиасу или по номеру.
	// Подзапрос от отказа тоже не спасает: он лишь делает разбор непростым, а
	// запасной путь смотрел только список выборки и никогда — ГДЕ.
	for _, denied := range []string{
		`ВЫБРАТЬ Наименование, Телефон КАК Т ИЗ Справочник.Клиент ГДЕ Т = "+79161234455"`,
		`ВЫБРАТЬ Наименование, Телефон КАК Т ИЗ Справочник.Клиент УПОРЯДОЧИТЬ ПО Т`,
		`ВЫБРАТЬ Телефон, Наименование ИЗ Справочник.Клиент УПОРЯДОЧИТЬ ПО 1`,
		`ВЫБРАТЬ Наименование ИЗ Справочник.Клиент ` +
			`ГДЕ Телефон = "+79161234455" И Наименование В (ВЫБРАТЬ Наименование ИЗ Справочник.Клиент)`,
	} {
		compiled, err := s.compileQueryWithRowAccess(uctx, denied, nil)
		if err != nil {
			t.Fatal(err)
		}
		if plan := s.queryMaskPlan(uctx, compiled); plan.Denied == "" {
			t.Fatalf("защищённое поле пропущено вне простой колонки: %s", denied)
		}
	}

	// Квалифицированная звёздочка — такая же проекция «*», как голая: колонки
	// маскируются, а не отдаются как есть.
	compiled, err := s.compileQueryWithRowAccess(uctx, `ВЫБРАТЬ К.* ИЗ Справочник.Клиент КАК К`, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan := s.queryMaskPlan(uctx, compiled)
	if plan.Denied != "" {
		t.Fatalf("К.* отклонено вместо маскирования: %s", plan.Denied)
	}
	rows, _, err := s.store.RunQuery(uctx, compiled.SQL, compiled.Args)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(rows); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(rows[0]["телефон"]); got != "••••••••4455" {
		t.Fatalf("К.* отдало реальное значение: %q", got)
	}
	if got := fmt.Sprint(rows[0]["наименование"]); got != "Иванов" {
		t.Fatalf("К.* испортило незащищённую колонку: %q", got)
	}
}
