package ui

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// «Создать копированием» (issue #762): GET /ui/{kind}/{name}/new?copy=<id>
// открывает форму создания значениями существующей записи. Проверяем перенос
// значений, исключения (номер и дата документа, реквизиты под маской), права,
// иерархию и то, что копирование ничего не записывает.

func copyTestCatalog() *metadata.Entity {
	return &metadata.Entity{
		Name: "Клиент", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Телефон", Type: metadata.FieldTypeString},
			{Name: "Код", Type: metadata.FieldTypeString},
		},
		TableParts: []metadata.TablePart{{Name: "Контакты", Fields: []metadata.Field{
			{Name: "Вид", Type: metadata.FieldTypeString},
			{Name: "Значение", Type: metadata.FieldTypeString},
		}}},
	}
}

func serveCopyForm(t *testing.T, s *Server, ctx context.Context, entity *metadata.Entity, query string) *httptest.ResponseRecorder {
	t.Helper()
	slug := strings.ToLower(entity.Name)
	req := httptest.NewRequest("GET", "/ui/"+strings.ToLower(string(entity.Kind))+"/"+slug+"/new?"+query, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("entity", slug)
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.form(rec, req)
	return rec
}

func TestApplyCopyFromQuery_CopiesHeaderAndTableParts(t *testing.T) {
	ent := copyTestCatalog()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})

	srcID := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, srcID, map[string]any{
		"Наименование": "ООО Ромашка", "Телефон": "+7 900 111-22-33", "Код": "К-001",
	}, ent); err != nil {
		t.Fatal(err)
	}
	tpRows := []map[string]any{
		{"Вид": "рабочий", "Значение": "+7 495 000-00-00"},
		{"Вид": "личный", "Значение": "+7 916 000-00-00"},
	}
	if err := s.store.UpsertTablePartRows(ctx, ent.Name, "Контакты", srcID, tpRows, ent.TableParts[0]); err != nil {
		t.Fatal(err)
	}

	values := map[string]string{}
	tps := map[string][]map[string]any{}
	r := httptest.NewRequest("GET", "/ui/catalog/клиент/new?copy="+srcID.String(), nil)
	if errText := s.applyCopyFromQuery(r.WithContext(ctx), ent, srcID.String(), values, tps); errText != "" {
		t.Fatalf("applyCopyFromQuery: %s", errText)
	}

	if values["Наименование"] != "ООО Ромашка" || values["Телефон"] != "+7 900 111-22-33" {
		t.Errorf("шапка не скопирована: %#v", values)
	}
	// Код справочника без нумератора платформа не генерирует — копия несёт его
	// как есть (со включённым нумератором см. TestApplyCopyFromQuery_CatalogCode…).
	if values["Код"] != "К-001" {
		t.Errorf("Код = %q, ожидался перенос из источника", values["Код"])
	}
	if got := tps["Контакты"]; len(got) != 2 || got[0]["Значение"] != "+7 495 000-00-00" {
		t.Errorf("табличная часть не скопирована: %#v", got)
	}

	// Копирование ничего не пишет: в базе по-прежнему одна запись.
	rows, err := s.store.List(ctx, ent.Name, ent, storage.ListParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("записей после копирования = %d, ожидалась 1 (копия живёт только в форме)", len(rows))
	}
}

// Справочник с нумератором сам выдаёт «Код» (план 117), поэтому копия обязана
// получить свой: с numerator.unique перенос чужого кода не дал бы записать
// копию вовсе, а без него завёл бы второй элемент с тем же кодом.
func TestApplyCopyFromQuery_CatalogCodeIsReissuedWhenNumbered(t *testing.T) {
	ent := copyTestCatalog()
	ent.Numerator = &metadata.Numerator{Prefix: "К-", Length: 5, Unique: true}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})

	srcID := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, srcID, map[string]any{
		"Наименование": "ООО Ромашка", "Код": "К-00001",
	}, ent); err != nil {
		t.Fatal(err)
	}

	values := map[string]string{}
	r := httptest.NewRequest("GET", "/ui/catalog/клиент/new?copy="+srcID.String(), nil)
	if errText := s.applyCopyFromQuery(r.WithContext(ctx), ent, srcID.String(), values, map[string][]map[string]any{}); errText != "" {
		t.Fatalf("applyCopyFromQuery: %s", errText)
	}
	if values["Код"] != "" {
		t.Errorf("Код = %q, копия справочника с нумератором должна получить свой код при записи", values["Код"])
	}
	if values["Наименование"] != "ООО Ромашка" {
		t.Errorf("остальные реквизиты должны копироваться: %#v", values)
	}
}

func TestApplyCopyFromQuery_DocumentKeepsFreshNumberAndDate(t *testing.T) {
	doc := &metadata.Entity{
		Name: "Реализация", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Дата", Type: metadata.FieldTypeDate},
			{Name: "СрокОплаты", Type: metadata.FieldTypeDate},
			{Name: "Покупатель", Type: metadata.FieldTypeString},
		},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{doc})

	srcID := uuid.New()
	if err := s.store.Upsert(ctx, doc.Name, srcID, map[string]any{
		"Номер": "РТ-001", "Дата": time.Date(2025, 1, 2, 9, 0, 0, 0, time.Local),
		"СрокОплаты": time.Date(2026, 9, 15, 18, 30, 0, 0, time.Local),
		"Покупатель": "ООО Ромашка",
	}, doc); err != nil {
		t.Fatal(err)
	}

	// Дату форма подставляет до копирования — копия должна её сохранить.
	values := map[string]string{"Дата": "2026-08-12T10:00"}
	r := httptest.NewRequest("GET", "/ui/document/реализация/new?copy="+srcID.String(), nil)
	if errText := s.applyCopyFromQuery(r.WithContext(ctx), doc, srcID.String(), values, map[string][]map[string]any{}); errText != "" {
		t.Fatalf("applyCopyFromQuery: %s", errText)
	}
	if values["Номер"] != "" {
		t.Errorf("Номер = %q, копия должна получить номер при записи", values["Номер"])
	}
	if values["Дата"] != "2026-08-12T10:00" {
		t.Errorf("Дата = %q, ожидалась дата нового документа", values["Дата"])
	}
	if values["СрокОплаты"] != "2026-09-15T18:30" {
		t.Errorf("СрокОплаты = %q, бизнес-дата должна переноситься из источника", values["СрокОплаты"])
	}
	if values["Покупатель"] != "ООО Ромашка" {
		t.Errorf("Покупатель = %q, ожидался перенос из источника", values["Покупатель"])
	}
}

func TestApplyCopyFromQuery_SQLiteBoolUsesCanonicalTrue(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Настройка", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Включена", Type: metadata.FieldTypeBool}},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	sourceID := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, sourceID, map[string]any{"Включена": true}, ent); err != nil {
		t.Fatal(err)
	}

	values := map[string]string{}
	request := httptest.NewRequest(http.MethodGet, "/ui/catalog/настройка/new?copy="+sourceID.String(), nil).WithContext(ctx)
	if errText := s.applyCopyFromQuery(request, ent, sourceID.String(), values, map[string][]map[string]any{}); errText != "" {
		t.Fatalf("applyCopyFromQuery: %s", errText)
	}
	if values["Включена"] != "true" {
		t.Fatalf("SQLite bool = %q, ожидается каноническое значение true", values["Включена"])
	}
}

func TestApplyCopyFromQuery_DeclarativeRowRLSDeniesExactSource(t *testing.T) {
	ent := copyTestCatalog()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	sourceID := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, sourceID, map[string]any{
		"Наименование": "СКРЫТАЯ-СТРОКА", "Код": "PRIVATE",
	}, ent); err != nil {
		t.Fatal(err)
	}
	user := &auth.User{ID: "reader", Login: "reader", Roles: []*auth.Role{{
		Permissions: auth.Permission{
			Catalogs: map[string][]string{ent.Name: {"read", "write"}},
			RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
				ent.Name: {"read": {Field: "Код", Op: "eq", Value: auth.RowValue{Literal: "PUBLIC"}}},
			}},
		},
	}}}
	request := httptest.NewRequest(http.MethodGet, "/ui/catalog/клиент/new?copy="+sourceID.String(), nil)
	request = request.WithContext(auth.ContextWithUser(ctx, user))
	values := map[string]string{}
	if errText := s.applyCopyFromQuery(request, ent, sourceID.String(), values, map[string][]map[string]any{}); errText == "" {
		t.Fatal("declarative row RLS должна запретить копирование скрытой строки")
	}
	if len(values) != 0 {
		t.Fatalf("RLS-denied source leaked into copy values: %#v", values)
	}
}

func TestApplyCopyFromQuery_OnReadAtServerDeniesWithoutLeak(t *testing.T) {
	s, ent := setupManagedEventsServer(t, `
Процедура ПроверитьДоступ()
	ВызватьИсключение("Нет доступа");
КонецПроцедуры
`, map[metadata.FormEventType]string{
		metadata.FormEventOnReadAtServer: "ПроверитьДоступ",
	}, []*metadata.FormElement{{
		Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование",
	}})
	sourceID := insertContragent(t, s, ent, "СЕКРЕТ-ИЗ-READ-HOOK")
	request := httptest.NewRequest(http.MethodGet, "/ui/catalog/контрагент/new?copy="+sourceID.String(), nil)
	values := map[string]string{}
	if errText := s.applyCopyFromQuery(request, ent, sourceID.String(), values, map[string][]map[string]any{}); errText == "" {
		t.Fatal("ПриЧтенииНаСервере должен запретить копирование")
	}
	if len(values) != 0 {
		t.Fatalf("read-hook denied source leaked into values: %#v", values)
	}
}

func TestManagedCopySubmitRechecksOnReadAtServer(t *testing.T) {
	s, ent := setupManagedEventsServer(t, `
Процедура ПроверитьДоступ()
	ВызватьИсключение("Доступ отозван после открытия формы");
КонецПроцедуры
`, map[metadata.FormEventType]string{
		metadata.FormEventOnReadAtServer: "ПроверитьДоступ",
	}, []*metadata.FormElement{{
		Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование",
	}})
	sourceID := insertContragent(t, s, ent, "SOURCE-MUST-NOT-BE-COPIED")
	request := reqWithChi(http.MethodPost, "/ui/catalog/"+ent.Name+"/new", url.Values{
		copySourceFormField: {sourceID.String()},
		"Наименование":      {"FORGED-CLONE"},
	}, map[string]string{"entity": ent.Name})
	recorder := httptest.NewRecorder()
	s.submit(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("POST must recheck ПриЧтенииНаСервере: got %d, body=%s", recorder.Code, recorder.Body.String())
	}
	rows, err := s.store.List(context.Background(), ent.Name, ent, storage.ListParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("denied POST created a clone: %#v", rows)
	}
}

func TestManagedCopyPreservesCanonicalReadonlyAndUnplacedState(t *testing.T) {
	doc := &metadata.Entity{
		Name: "ЗаказКопия", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Дата", Type: metadata.FieldTypeDate},
			{Name: "СрокОплаты", Type: metadata.FieldTypeDate},
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "ТолькоЧтение", Type: metadata.FieldTypeString},
			{Name: "Скрытое", Type: metadata.FieldTypeString},
			{Name: "Активен", Type: metadata.FieldTypeBool},
			{Name: "Секрет", Type: metadata.FieldTypeString},
			{Name: "Фото", Type: metadata.FieldTypeImage},
		},
		TableParts: []metadata.TablePart{
			{Name: "Товары", Fields: []metadata.Field{{Name: "Текст", Type: metadata.FieldTypeString}}},
			{Name: "Аудит", Fields: []metadata.Field{{Name: "Текст", Type: metadata.FieldTypeString}}},
			{Name: "СлужебныеСтроки", Fields: []metadata.Field{{Name: "Текст", Type: metadata.FieldTypeString}}},
		},
	}
	doc.Forms = []*metadata.FormModule{{
		Name: "ФормаОбъекта", Kind: "object", EntityName: doc.Name, LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "Имя", DataPath: "Объект.Наименование"},
			{Kind: metadata.FormElementField, Name: "Readonly", DataPath: "Объект.ТолькоЧтение", ReadOnly: true},
			{Kind: metadata.FormElementField, Name: "Active", DataPath: "Объект.Активен", ReadOnly: true},
			{Kind: metadata.FormElementTablePart, Name: "Goods", DataPath: "Объект.Товары", NoGrid: true},
			{Kind: metadata.FormElementTablePart, Name: "Audit", DataPath: "Объект.Аудит", NoGrid: true, ReadOnly: true},
		},
	}}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{doc})
	if err := s.store.EnsureBlobTable(ctx); err != nil {
		t.Fatal(err)
	}
	blob, err := s.store.PutBlob(ctx, "image/png", bytes.NewBufferString("png"), 1<<20,
		storage.BlobOwner{Kind: string(doc.Kind), Entity: doc.Name})
	if err != nil {
		t.Fatal(err)
	}

	sourceID := uuid.New()
	sourceDue := time.Date(2026, 10, 20, 12, 45, 0, 0, time.Local)
	if err := s.store.Upsert(ctx, doc.Name, sourceID, map[string]any{
		"Номер": "SRC-001", "Дата": time.Date(2025, 1, 1, 8, 0, 0, 0, time.Local),
		"СрокОплаты": sourceDue, "Наименование": "Источник",
		"ТолькоЧтение": "SERVER-READONLY", "Скрытое": "SERVER-HIDDEN",
		"Активен": true, "Секрет": "SERVER-SECRET", "Фото": blob.ID.String(),
	}, doc); err != nil {
		t.Fatal(err)
	}
	for index, rows := range [][]map[string]any{
		{{"Текст": "SOURCE-GOODS"}},
		{{"Текст": "SOURCE-AUDIT"}},
		{{"Текст": "SOURCE-SERVICE"}},
	} {
		part := doc.TableParts[index]
		if err := s.store.UpsertTablePartRows(ctx, doc.Name, part.Name, sourceID, rows, part); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.store.EnsureAttachmentTable(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.UploadAttachment(ctx, string(doc.Kind), doc.Name, sourceID,
		"source.txt", "text/plain", "tester", bytes.NewBufferString("attachment"), 1<<20); err != nil {
		t.Fatal(err)
	}

	user := &auth.User{ID: "operator", Login: "operator", Roles: []*auth.Role{{
		Permissions: auth.Permission{
			Documents: map[string][]string{doc.Name: {"read", "write"}},
			FieldAccess: auth.FieldAccess{Documents: map[string]auth.FieldPolicies{
				doc.Name: {"Секрет": {Read: "mask_all"}},
			}},
		},
	}}}
	userCtx := auth.ContextWithUser(ctx, user)
	get := serveCopyForm(t, s, userCtx, doc, "copy="+sourceID.String())
	if get.Code != http.StatusOK {
		t.Fatalf("copy form: %d: %s", get.Code, get.Body.String())
	}
	if !strings.Contains(get.Body.String(), `name="_copy_source_id" value="`+sourceID.String()+`"`) {
		t.Fatal("managed copy form lost the canonical source marker")
	}

	post := url.Values{
		copySourceFormField: {sourceID.String()},
		"Наименование":      {"ИзмененоПользователем"},
		"Номер":             {"FORGED-NUMBER"},
		"Дата":              {"2020-01-01T00:00"},
		"СрокОплаты":        {"2020-02-02T00:00"},
		"ТолькоЧтение":      {"FORGED-READONLY"},
		"Скрытое":           {"FORGED-HIDDEN"},
		"Активен":           {"false"},
		"Секрет":            {"FORGED-SECRET"},
		"Фото":              {uuid.New().String()},
		"tp.Товары.0.Текст": {"BROWSER-GOODS"},
		"tp.Аудит.0.Текст":  {"FORGED-AUDIT"},
		"tp.СлужебныеСтроки.0.Текст": {"FORGED-SERVICE"},
	}
	request := reqWithChi(http.MethodPost, "/ui/document/"+doc.Name+"/new", post,
		map[string]string{"entity": doc.Name})
	request = request.WithContext(auth.ContextWithUser(request.Context(), user))
	recorder := httptest.NewRecorder()
	s.submit(recorder, request)
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("submit copy: %d: %s", recorder.Code, recorder.Body.String())
	}

	rows, err := s.store.List(ctx, doc.Name, doc, storage.ListParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("records after copy = %d, want source + clone", len(rows))
	}
	var clone map[string]any
	var cloneID uuid.UUID
	for _, row := range rows {
		id, parseErr := uuid.Parse(asString(row["id"]))
		if parseErr == nil && id != sourceID {
			clone, cloneID = row, id
			break
		}
	}
	if clone == nil {
		t.Fatal("saved clone not found")
	}
	assertCopyString := func(field, want string) {
		t.Helper()
		got, _ := maskCIKeyValue(clone, field)
		if asString(got) != want {
			t.Errorf("%s = %q, want %q", field, asString(got), want)
		}
	}
	assertCopyString("Наименование", "ИзмененоПользователем")
	assertCopyString("ТолькоЧтение", "SERVER-READONLY")
	assertCopyString("Скрытое", "SERVER-HIDDEN")
	assertCopyString("Фото", blob.ID.String())
	if got, _ := maskCIKeyValue(clone, "Номер"); strings.TrimSpace(asString(got)) == "" || asString(got) == "FORGED-NUMBER" || asString(got) == "SRC-001" {
		t.Errorf("Номер = %q, ожидался новый server-generated номер", asString(got))
	}
	if got, _ := maskCIKeyValue(clone, "Дата"); strings.HasPrefix(formatDateValueForInput(got), "2020-01-01") || strings.HasPrefix(formatDateValueForInput(got), "2025-01-01") {
		t.Errorf("Дата = %v, ожидалась свежая системная дата", got)
	}
	if got, _ := maskCIKeyValue(clone, "СрокОплаты"); formatDateValueForInput(got) != formatDateValueForInput(sourceDue) {
		t.Errorf("СрокОплаты = %v, want source business date %v", got, sourceDue)
	}
	if got, _ := maskCIKeyValue(clone, "Активен"); !asBool(got) {
		t.Errorf("readonly bool = %v, want source true", got)
	}
	if got, _ := maskCIKeyValue(clone, "Секрет"); got != nil && strings.TrimSpace(asString(got)) != "" {
		t.Errorf("masked field accepted/copied forbidden value: %v", got)
	}

	for index, want := range []string{"BROWSER-GOODS", "SOURCE-AUDIT", "SOURCE-SERVICE"} {
		part := doc.TableParts[index]
		partRows, loadErr := s.store.GetTablePartRows(ctx, doc.Name, part.Name, cloneID, part)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if len(partRows) != 1 || asString(partRows[0]["Текст"]) != want {
			t.Errorf("%s = %#v, want one canonical row %q", part.Name, partRows, want)
		}
	}

	cloneAttachments, err := s.store.ListAttachments(ctx, string(doc.Kind), doc.Name, cloneID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cloneAttachments) != 0 {
		t.Fatalf("attachments must stay on source, clone got %#v", cloneAttachments)
	}
	sourceAttachments, err := s.store.ListAttachments(ctx, string(doc.Kind), doc.Name, sourceID)
	if err != nil || len(sourceAttachments) != 1 {
		t.Fatalf("source attachment was moved/lost: %#v, err=%v", sourceAttachments, err)
	}

	// Image fields intentionally share one immutable blob UUID. Once the source
	// row is gone, the clone alone must keep that blob live for GC.
	if err := s.store.Delete(ctx, doc.Name, sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.SweepOrphanBlobs(ctx, []*metadata.Entity{doc}, 0, false); err != nil {
		t.Fatal(err)
	}
	_, content, err := s.store.OpenBlob(ctx, blob.ID)
	if err != nil {
		t.Fatalf("shared image blob was collected while clone still references it: %v", err)
	}
	_ = content.Close()
}

func TestApplyCopyFromQuery_MaskedFieldIsNotCopied(t *testing.T) {
	ent := copyTestCatalog()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})

	srcID := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, srcID, map[string]any{
		"Наименование": "ООО Ромашка", "Телефон": "+7 900 111-22-33",
	}, ent); err != nil {
		t.Fatal(err)
	}

	user := uiMaskUser([]string{"read", "write"}, auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}})
	r := httptest.NewRequest("GET", "/ui/catalog/клиент/new?copy="+srcID.String(), nil)
	r = r.WithContext(auth.ContextWithUser(ctx, user))

	values := map[string]string{}
	if errText := s.applyCopyFromQuery(r, ent, srcID.String(), values, map[string][]map[string]any{}); errText != "" {
		t.Fatalf("applyCopyFromQuery: %s", errText)
	}
	if values["Наименование"] != "ООО Ромашка" {
		t.Errorf("незащищённый реквизит не скопирован: %#v", values)
	}
	if v, ok := values["Телефон"]; ok {
		t.Errorf("Телефон = %q: закрытый маской реквизит в копию не переносится — иначе в новую запись уедет строка-маска", v)
	}
}

func TestApplyCopyFromQuery_RequiresReadOnSource(t *testing.T) {
	ent := copyTestCatalog()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})

	srcID := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, srcID, map[string]any{"Наименование": "ООО Ромашка"}, ent); err != nil {
		t.Fatal(err)
	}

	user := &auth.User{Login: "чужой", Roles: []*auth.Role{{
		Permissions: auth.Permission{Catalogs: map[string][]string{"Другое": {"read"}}},
	}}}
	r := httptest.NewRequest("GET", "/ui/catalog/клиент/new?copy="+srcID.String(), nil)
	r = r.WithContext(auth.ContextWithUser(ctx, user))

	values := map[string]string{}
	if errText := s.applyCopyFromQuery(r, ent, srcID.String(), values, map[string][]map[string]any{}); errText == "" {
		t.Fatal("копирование без права чтения источника должно возвращать ошибку")
	}
	if len(values) != 0 {
		t.Errorf("значения источника утекли в форму: %#v", values)
	}
}

func TestApplyCopyFromQuery_BadIDAndMissingRow(t *testing.T) {
	ent := copyTestCatalog()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	r := httptest.NewRequest("GET", "/ui/catalog/клиент/new", nil).WithContext(ctx)

	if errText := s.applyCopyFromQuery(r, ent, "не-uuid", map[string]string{}, map[string][]map[string]any{}); errText == "" {
		t.Error("некорректный идентификатор должен давать ошибку")
	}
	values := map[string]string{}
	if errText := s.applyCopyFromQuery(r, ent, uuid.New().String(), values, map[string][]map[string]any{}); errText == "" {
		t.Error("несуществующая запись должна давать ошибку")
	}
	if len(values) != 0 {
		t.Errorf("значения не должны появляться при ошибке: %#v", values)
	}
}

// Копия группы остаётся группой и в той же родительской группе.
func TestApplyCopyFromQuery_HierarchyFollowsSource(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Папки", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})

	parentID := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, parentID, map[string]any{"Наименование": "Родитель", "is_folder": true}, ent); err != nil {
		t.Fatal(err)
	}
	srcID := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, srcID, map[string]any{
		"Наименование": "Вложенная группа", "is_folder": true, "parent_id": parentID.String(),
	}, ent); err != nil {
		t.Fatal(err)
	}

	values := map[string]string{}
	r := httptest.NewRequest("GET", "/ui/catalog/папки/new?copy="+srcID.String(), nil).WithContext(ctx)
	if errText := s.applyCopyFromQuery(r, ent, srcID.String(), values, map[string][]map[string]any{}); errText != "" {
		t.Fatalf("applyCopyFromQuery: %s", errText)
	}
	if values["is_folder"] != "true" {
		t.Errorf("is_folder = %q, копия группы должна остаться группой", values["is_folder"])
	}
	if values["parent_id"] != parentID.String() {
		t.Errorf("parent_id = %q, ожидалась группа оригинала %s", values["parent_id"], parentID)
	}
}

// Сквозная проверка обработчика: ?copy= доезжает до HTML формы создания,
// запись при этом не создаётся.
func TestForm_CopyQueryRendersFilledCreateForm(t *testing.T) {
	ent := copyTestCatalog()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})

	srcID := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, srcID, map[string]any{
		"Наименование": "ООО Ромашка", "Телефон": "+7 900 111-22-33",
	}, ent); err != nil {
		t.Fatal(err)
	}

	rec := serveCopyForm(t, s, ctx, ent, "copy="+srcID.String())
	if rec.Code != 200 {
		t.Fatalf("code = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `value="ООО Ромашка"`) {
		t.Error("значение источника не попало в форму создания")
	}
	// «+» шаблон отдаёт как &#43;, поэтому сравниваем по остатку номера.
	if !strings.Contains(body, "7 900 111-22-33") {
		t.Error("второй реквизит источника не попал в форму создания")
	}

	rows, err := s.store.List(ctx, ent.Name, ent, storage.ListParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("записей = %d, копирование не должно ничего записывать", len(rows))
	}
}

// Список отдаёт строке ссылку копирования, а карточка — кнопку «Скопировать».
func TestListAndCardOfferCopy(t *testing.T) {
	ent := copyTestCatalog()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})

	srcID := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, srcID, map[string]any{"Наименование": "ООО Ромашка"}, ent); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/ui/catalog/клиент", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("entity", "клиент")
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.list(rec, req)
	if rec.Code != 200 {
		t.Fatalf("список: code = %d, body: %s", rec.Code, rec.Body.String())
	}
	// Имя сущности шаблон отдаёт percent-encoded, поэтому сверяем хвост ссылки.
	if !strings.Contains(rec.Body.String(), "data-copy-url=") ||
		!strings.Contains(rec.Body.String(), "/new?copy="+srcID.String()) {
		t.Error("строка списка не несёт ссылку копирования (пункт меню и F9 без неё не работают)")
	}

	card := httptest.NewRequest("GET", "/ui/catalog/клиент/"+srcID.String(), nil)
	cardCtx := chi.NewRouteContext()
	cardCtx.URLParams.Add("entity", "клиент")
	cardCtx.URLParams.Add("id", srcID.String())
	card = card.WithContext(context.WithValue(ctx, chi.RouteCtxKey, cardCtx))
	cardRec := httptest.NewRecorder()
	s.formEdit(cardRec, card)
	if cardRec.Code != 200 {
		t.Fatalf("карточка: code = %d, body: %s", cardRec.Code, cardRec.Body.String())
	}
	if !strings.Contains(cardRec.Body.String(), "/new?copy="+srcID.String()) {
		t.Error("на карточке нет кнопки «Скопировать»")
	}
}
