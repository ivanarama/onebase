package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/xuri/excelize/v2"
)

// The journal handler must derive the policy from the concrete document field,
// even when the journal exposes that field under an unrelated output alias.
// Supplying an already-masked row to detailPanelJSON would not exercise this
// boundary and allowed the original regression to pass its tests.
func TestUI_JournalList_MasksMappedSourceAtHandlerBoundary(t *testing.T) {
	doc := &metadata.Entity{
		Name: "SecretDocument",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "SecretValue", Title: "Secret value", Type: metadata.FieldTypeString},
		},
	}
	j := &metadata.Journal{
		Name:      "SecretJournal",
		Documents: []string{doc.Name},
		Columns: []metadata.JournalColumn{{
			Field: "PublicColumn",
			Label: "Public column",
			Map:   map[string]string{doc.Name: "SecretValue"},
		}},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{doc})
	s.reg.LoadJournals([]*metadata.Journal{j})
	const secret = "JOURNAL-SECRET-754"
	if err := s.store.Upsert(ctx, doc.Name, uuid.New(), map[string]any{"SecretValue": secret}, doc); err != nil {
		t.Fatal(err)
	}
	user := &auth.User{ID: "journal-reader", Login: "journal-reader", Roles: []*auth.Role{{
		Name: "Journal reader",
		Permissions: auth.Permission{
			Documents: map[string][]string{doc.Name: {"read"}},
			FieldAccess: auth.FieldAccess{Documents: map[string]auth.FieldPolicies{
				doc.Name: {"SecretValue": {Read: "mask_all"}},
			}},
		},
	}}}

	r := reqWithChi(http.MethodGet, "/ui/journal/"+j.Name, nil, map[string]string{"name": j.Name})
	r = r.WithContext(auth.ContextWithUser(r.Context(), user))
	w := httptest.NewRecorder()
	s.journalList(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("journalList: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	page := w.Body.String()
	if strings.Contains(page, secret) {
		t.Fatalf("mapped source leaked through journal HTML/detail payload: %s", page)
	}
	panel := firstDetailPanelData(t, page)
	if got, ok := detailPanelValueByLabel(panel, "Public column"); !ok || got != "••••••" {
		t.Fatalf("mapped journal value was not masked at handler boundary: got=%q ok=%v payload=%+v", got, ok, panel)
	}

	// The same storage query feeds the Excel endpoint, which is another output
	// boundary and must not silently bypass the list's mask.
	excelReq := reqWithChi(http.MethodGet, "/ui/journal/"+j.Name+"/excel", nil, map[string]string{"name": j.Name})
	excelReq = excelReq.WithContext(auth.ContextWithUser(excelReq.Context(), user))
	excelW := httptest.NewRecorder()
	s.journalExcel(excelW, excelReq)
	if excelW.Code != http.StatusOK {
		t.Fatalf("journalExcel: expected 200, got %d: %s", excelW.Code, excelW.Body.String())
	}
	workbook, err := excelize.OpenReader(bytes.NewReader(excelW.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := workbook.Close(); err != nil {
			t.Errorf("close journal Excel workbook: %v", err)
		}
	}()
	sheets := workbook.GetSheetList()
	if len(sheets) == 0 {
		t.Fatal("journal Excel has no worksheets")
	}
	xlsRows, err := workbook.GetRows(sheets[0])
	if err != nil {
		t.Fatal(err)
	}
	flat := fmt.Sprint(xlsRows)
	if strings.Contains(flat, secret) || !strings.Contains(flat, "••••••") {
		t.Fatalf("journal Excel did not preserve the field mask: %s", flat)
	}
}

// A fallback journal expression is COALESCE(source...). SQL does not return
// provenance for the selected value, so a protected candidate forces the
// output alias to a fail-closed policy.
func TestUI_MaskJournalRecords_FallbackFailsClosed(t *testing.T) {
	doc := &metadata.Entity{
		Name: "FallbackDocument",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "OpenValue", Type: metadata.FieldTypeString},
			{Name: "SecretValue", Type: metadata.FieldTypeString},
		},
	}
	j := &metadata.Journal{
		Name:      "FallbackJournal",
		Documents: []string{doc.Name},
		Columns: []metadata.JournalColumn{{
			Field: "Summary", Fallback: []string{"OpenValue", "SecretValue"},
		}},
	}
	user := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{
		Documents: map[string][]string{doc.Name: {"read"}},
		FieldAccess: auth.FieldAccess{Documents: map[string]auth.FieldPolicies{
			doc.Name: {"SecretValue": {Read: "hide"}},
		}},
	}}}}
	rows := []map[string]any{{"_doc_kind": doc.Name, "Summary": "OPEN-CANDIDATE"}}
	s := &Server{}
	s.maskJournalRecords(auth.ContextWithUser(context.Background(), user), j, map[string]*metadata.Entity{doc.Name: doc}, rows)
	if _, ok := rows[0]["Summary"]; ok {
		t.Fatalf("fallback alias with a hidden candidate was exposed: %#v", rows[0])
	}
}

// Different masks are not composable. For example, mask_city applied to a
// phone number (selected from a mask_tail fallback source) can return the phone
// unchanged. Ambiguous strategies therefore collapse to mask_all.
func TestUI_MaskJournalRecords_ConflictingFallbackMasksCollapseToMaskAll(t *testing.T) {
	doc := &metadata.Entity{
		Name: "MixedFallbackDocument",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Address", Type: metadata.FieldTypeString},
			{Name: "Phone", Type: metadata.FieldTypeString},
		},
	}
	j := &metadata.Journal{
		Name:      "MixedFallbackJournal",
		Documents: []string{doc.Name},
		Columns: []metadata.JournalColumn{{
			Field: "Contact", Fallback: []string{"Address", "Phone"},
		}},
	}
	user := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{
		Documents: map[string][]string{doc.Name: {"read"}},
		FieldAccess: auth.FieldAccess{Documents: map[string]auth.FieldPolicies{
			doc.Name: {
				"Address": {Read: "mask_city"},
				"Phone":   {Read: "mask_tail", Keep: 4},
			},
		}},
	}}}}
	rows := []map[string]any{{"_doc_kind": doc.Name, "Contact": "+79161234567"}}
	(&Server{}).maskJournalRecords(
		auth.ContextWithUser(context.Background(), user), j,
		map[string]*metadata.Entity{doc.Name: doc}, rows,
	)
	if got := fmt.Sprint(rows[0]["Contact"]); got != "••••••" {
		t.Fatalf("conflicting fallback masks must collapse to mask_all, got %q", got)
	}
}

// The information-register list is the full end-to-end contract: raw storage
// rows are masked first, reference UUIDs are resolved to labels, and periodic
// identity is present in the detail payload.
func TestUI_InfoRegList_MasksAtHandlerBoundaryAndBuildsSafeDetailPayload(t *testing.T) {
	goods := &metadata.Entity{
		Name:   "Goods",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}},
	}
	ir := &metadata.InfoRegister{
		Name:     "SecretPrices",
		Periodic: true,
		Dimensions: []metadata.Field{{
			Name: "Product", Title: "Product", Type: "reference:Goods", RefEntity: goods.Name,
		}},
		Resources: []metadata.Field{{
			Name: "SecretValue", Title: "Secret value", Type: metadata.FieldTypeString,
		}},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{goods})
	if err := s.store.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatal(err)
	}
	s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{goods}, InfoRegs: []*metadata.InfoRegister{ir}})
	productID := uuid.New()
	if err := s.store.Upsert(ctx, goods.Name, productID, map[string]any{"Name": "Chair reference label"}, goods); err != nil {
		t.Fatal(err)
	}
	period := time.Date(2026, time.August, 12, 12, 0, 0, 0, time.Local)
	const secret = "INFOREG-SECRET-754"
	if err := s.store.InfoRegSet(ctx, ir,
		map[string]any{"Product": productID.String()},
		map[string]any{"SecretValue": secret}, &period); err != nil {
		t.Fatal(err)
	}
	user := &auth.User{ID: "inforeg-reader", Login: "inforeg-reader", Roles: []*auth.Role{{
		Name: "Info register reader",
		Permissions: auth.Permission{
			Catalogs: map[string][]string{goods.Name: {"read"}},
			InfoRegs: map[string][]string{ir.Name: {"read"}},
			FieldAccess: auth.FieldAccess{InfoRegs: map[string]auth.FieldPolicies{
				ir.Name: {"SecretValue": {Read: "mask_all"}},
			}},
		},
	}}}

	r := reqWithChi(http.MethodGet, "/ui/inforeg/"+ir.Name, nil, map[string]string{"name": ir.Name})
	r = r.WithContext(auth.ContextWithUser(r.Context(), user))
	w := httptest.NewRecorder()
	s.infoRegList(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("infoRegList: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	page := w.Body.String()
	if strings.Contains(page, secret) {
		t.Fatalf("information-register secret leaked through HTML/detail payload: %s", page)
	}
	panel := firstDetailPanelData(t, page)
	for label, want := range map[string]string{
		"Период":       "12.08.2026",
		"Product":      "Chair reference label",
		"Secret value": "••••••",
	} {
		if got, ok := detailPanelValueByLabel(panel, label); !ok || got != want {
			t.Errorf("%s = %q, %v; expected %q; payload=%+v", label, got, ok, want, panel)
		}
	}
	panelJSON, err := json.Marshal(panel)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(panelJSON), productID.String()) {
		t.Fatalf("detail payload contains UUID instead of the resolved label: %s", panelJSON)
	}

	// A protected reference dimension proves the ordering, not merely that a
	// mask is eventually applied. If resolveInfoRegRows ran first, its stale
	// Product_label would win in the table even after Product itself was masked.
	maskedRefUser := &auth.User{ID: "masked-ref-reader", Login: "masked-ref-reader", Roles: []*auth.Role{{
		Name: "Masked reference reader",
		Permissions: auth.Permission{
			Catalogs: map[string][]string{goods.Name: {"read"}},
			InfoRegs: map[string][]string{ir.Name: {"read"}},
			FieldAccess: auth.FieldAccess{InfoRegs: map[string]auth.FieldPolicies{
				ir.Name: {
					"Product":     {Read: "mask_all"},
					"SecretValue": {Read: "mask_all"},
				},
			}},
		},
	}}}
	maskedReq := reqWithChi(http.MethodGet, "/ui/inforeg/"+ir.Name, nil, map[string]string{"name": ir.Name})
	maskedReq = maskedReq.WithContext(auth.ContextWithUser(maskedReq.Context(), maskedRefUser))
	maskedW := httptest.NewRecorder()
	s.infoRegList(maskedW, maskedReq)
	if maskedW.Code != http.StatusOK {
		t.Fatalf("infoRegList masked reference: expected 200, got %d: %s", maskedW.Code, maskedW.Body.String())
	}
	maskedPage := maskedW.Body.String()
	rowStart := strings.Index(maskedPage, `<tr data-ob-list-row`)
	if rowStart < 0 {
		t.Fatalf("masked info-register page has no data row: %s", maskedPage)
	}
	rowEnd := strings.Index(maskedPage[rowStart:], `</tr>`)
	if rowEnd < 0 {
		t.Fatalf("masked info-register data row is not closed: %s", maskedPage[rowStart:])
	}
	rowHTML := maskedPage[rowStart : rowStart+rowEnd]
	if strings.Contains(rowHTML, productID.String()) || strings.Contains(rowHTML, "Chair reference label") {
		t.Fatalf("protected reference was resolved before masking and leaked in its row: %s", rowHTML)
	}
	maskedPanel := firstDetailPanelData(t, rowHTML)
	if got, ok := detailPanelValueByLabel(maskedPanel, "Product"); !ok || got != "••••••" {
		t.Fatalf("protected reference is not masked in detail payload: got=%q ok=%v payload=%+v", got, ok, maskedPanel)
	}
}

func TestUI_MaskInfoRegRecords_PeriodAlsoProtectsMachineKey(t *testing.T) {
	ir := &metadata.InfoRegister{Name: "ProtectedPeriod", Periodic: true}
	user := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{
		InfoRegs: map[string][]string{ir.Name: {"read"}},
		FieldAccess: auth.FieldAccess{InfoRegs: map[string]auth.FieldPolicies{
			ir.Name: {"period": {Read: "hide"}},
		}},
	}}}}
	rows := []map[string]any{{"period": "12.08.2026", "period_key": "2026-08-12T00:00:00Z"}}
	(&Server{}).maskInfoRegRecords(auth.ContextWithUser(context.Background(), user), ir, rows)
	if _, ok := rows[0]["period"]; ok {
		t.Fatalf("hidden period remained in row: %#v", rows[0])
	}
	if _, ok := rows[0]["period_key"]; ok {
		t.Fatalf("hidden period leaked through machine key: %#v", rows[0])
	}
}

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
