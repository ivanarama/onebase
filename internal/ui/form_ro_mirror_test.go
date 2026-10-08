package ui

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// #1672 (план 181C): disabled select браузер не отправляет, поэтому значение
// недоступного поля терялось при записи. Сервер рисует скрытое зеркало
// data-ob-ro-mirror; управляемость состояний — в managed.js.

func roMirrorEntity() (*metadata.Entity, *metadata.FormModule) {
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: "Звонок", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "ПолеНаправление",
				DataPath: "Объект.Направление", ReadOnly: true},
			{Kind: metadata.FormElementField, Name: "ПолеТип",
				DataPath: "Объект.Тип", ReadOnly: true},
			{Kind: metadata.FormElementField, Name: "ПолеФилиал",
				DataPath: "Объект.Филиал", ReadOnlyWhen: `Филиал = "x"`},
		},
	}
	ent := &metadata.Entity{
		Name: "Звонок", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Направление", Type: metadata.FieldType("reference:Направления")},
			{Name: "Тип", Type: metadata.FieldType("reference:枚")},
			{Name: "Филиал", Type: metadata.FieldType("reference:Филиалы")},
		},
		Forms: []*metadata.FormModule{form},
	}
	return ent, form
}

func roMirrorRender(t *testing.T, values map[string]string) string {
	t.Helper()
	ent, form := roMirrorEntity()
	data := map[string]any{
		"Entity": ent, "Form": form, "IsNew": true, "CanWrite": true,
		"Values": values, "RefOptions": map[string]any{},
		"EnumOptions": map[string]any{}, "TPRefOptions": map[string]any{},
		"FormCloseTimeoutMS": int64(31500), "User": nil, "Lang": "ru",
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

func TestManagedForm_ROMirror_Render(t *testing.T) {
	id := "79f4d98a-3ce0-4da4-82ea-e9c8686e804f"
	// Постоянный readonly: select отключён, зеркало активно и везёт значение.
	html := roMirrorRender(t, map[string]string{"Направление": id, "Тип": "", "Филиал": ""})
	if !strings.Contains(html, `<input type="hidden" name="Направление" value="`+id+`" id="ro-mirror-Направление" data-ob-ro-mirror="1">`) {
		t.Fatalf("readonly ref misses active mirror")
	}
	if !strings.Contains(html, `name="Тип" value="" id="ro-mirror-Тип" data-ob-ro-mirror="1"`) {
		t.Fatalf("readonly second field misses mirror")
	}
	// У зеркала нет disabled — иначе оно само не отправилось бы.
	mirrorIdx := strings.Index(html, `data-ob-ro-mirror="1"`)
	if strings.Contains(html[mirrorIdx:mirrorIdx+160], `data-ob-ro-mirror="1" disabled`) {
		t.Fatalf("active mirror must not be disabled: %s", html[mirrorIdx:mirrorIdx+160])
	}
	// Зеркал ровно по числу условных/запертых select-полей (Направление, Тип, Филиал).
	if got := strings.Count(html, "data-ob-ro-mirror"); got != 3 {
		t.Fatalf("mirror count=%d, want 3 (ref, enum, conditional)", got)
	}
}

func TestManagedForm_ROMirror_UnconditionalEditableHasNoMirror(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Звонок", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Направление", Type: metadata.FieldType("reference:Направления")}},
	}
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: "Звонок", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "ПолеНаправление", DataPath: "Объект.Направление"},
		},
	}
	ent.Forms = []*metadata.FormModule{form}
	data := map[string]any{
		"Entity": ent, "Form": form, "IsNew": true, "CanWrite": true,
		"Values": map[string]string{"Направление": ""}, "RefOptions": map[string]any{},
		"EnumOptions": map[string]any{}, "TPRefOptions": map[string]any{},
		"FormCloseTimeoutMS": int64(31500), "User": nil, "Lang": "ru",
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(buf.String(), "data-ob-ro-mirror") {
		t.Fatalf("editable field must not carry a mirror: %s", buf.String())
	}
}

// Публичный POST: значение запертого ссылочного поля, отправленное зеркалом,
// доезжает до базы ( acceptance из триажа #1672 ).
func TestManagedForm_ROMirror_PostPersists(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "mirror.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	направления := &metadata.Entity{
		Name: "Направления", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	ent := &metadata.Entity{
		Name: "Звонок", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Тема", Type: metadata.FieldTypeString},
			{Name: "Направление", Type: metadata.FieldType("reference:Направления")},
		},
		Forms: []*metadata.FormModule{{
			Name: "ФормаОбъекта", Kind: "object", EntityName: "Звонок", LayoutKind: metadata.FormLayoutManaged,
			Elements: []*metadata.FormElement{
				{Kind: metadata.FormElementField, Name: "ПолеТема", DataPath: "Объект.Тема"},
				{Kind: metadata.FormElementField, Name: "ПолеНаправление",
					DataPath: "Объект.Направление", ReadOnly: true},
			},
		}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{направления, ent}); err != nil {
		t.Fatal(err)
	}
	refID := uuid.New()
	if err := db.Upsert(ctx, "Направления", refID, map[string]any{"Наименование": "Химчистка"}, направления); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{направления, ent}})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	s := &Server{
		store:    db,
		reg:      reg,
		interp:   interp,
		lockMgr:  runtime.NewLockManager(),
		messages: NewMessageStore(),
	}
	s.entitySvc = s.newEntityService(nil)

	form := url.Values{}
	form.Set("Тема", "Звонок по филиалу")
	form.Set("Направление", refID.String()) // это значение несёт зеркало запертого поля
	req := httptest.NewRequest(http.MethodPost, "/ui/document/Звонок/new", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("kind", "document")
	rctx.URLParams.Add("entity", "Звонок")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.submit(rec, req)
	// Создание завершается редиректом на карточку записи.
	if rec.Code != http.StatusOK && rec.Code != http.StatusSeeOther {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	rows, err := db.List(ctx, "Звонок", ent, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows=%d want 1", len(rows))
	}
	got, _ := rows[0]["Направление"].(string)
	if got != refID.String() {
		t.Fatalf("locked field value lost on save: got %q want %q (row=%v)", got, refID.String(), rows[0])
	}
}
