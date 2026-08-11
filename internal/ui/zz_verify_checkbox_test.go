package ui

import (
	"context"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func setupCheckboxFixture(t *testing.T) (*Server, *metadata.Entity, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "cb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	ent := &metadata.Entity{
		Name: "Задача", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Активна", Type: metadata.FieldTypeBool},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, ent.Name, id,
		map[string]any{"Наименование": "З-1", "Активна": true}, ent); err != nil {
		t.Fatal(err)
	}

	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: ent.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование"},
			{Kind: metadata.FormElementType("Флажок"), Name: "Активна", DataPath: "Объект.Активна"},
			{
				Kind: metadata.FormElementButton, Name: "КнопкаТест",
				Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Тест"},
			},
		},
		ProgramAST: mustParse(t, `
Процедура Тест()
	Сообщить("ok");
КонецПроцедуры
`),
	}
	ent.Forms = []*metadata.FormModule{form}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	srv := &Server{store: db, reg: reg, interp: interp,
		lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	srv.entitySvc = srv.newEntityService(nil)
	return srv, ent, id
}

// Пользователь снял галку → браузер не шлёт ключ «Активна». После события формы
// значение в ответе не должно стать true.
func TestVerify_СнятаяГалкаНеВозвращается(t *testing.T) {
	srv, ent, id := setupCheckboxFixture(t)

	body := url.Values{}
	body.Set("_id", id.String())
	body.Set("Наименование", "З-1")
	body.Set("_element", "КнопкаТест")
	body.Set("_event", string(metadata.FormEventOnClick))
	body.Set("_kind", "object")

	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	t.Logf("values=%#v", resp.Values)
	for k, v := range resp.Values {
		t.Logf("  %q = %#v (%T)", k, v, v)
	}
	if b, ok := resp.Values["Активна"]; ok {
		if b == true || b == "true" || b == float64(1) {
			t.Fatalf("FAIL: снятая галка вернулась взведённой: %#v", b)
		}
	}
	if b, ok := resp.Values["активна"]; ok {
		if b == true || b == "true" || b == float64(1) {
			t.Fatalf("FAIL(lower): снятая галка вернулась взведённой: %#v", b)
		}
	}
}
