package ui

import (
	"context"
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

// #1557 (план 158 срез B): ОткрытьФорму(ТипизированнаяСсылка) кладёт в ответ
// form-event canonical URL, построенный сервером. Переходит только
// инициирующая вкладка; несохранённая форма остаётся на месте (guard в
// managed.js сторожит window._obFormDirty).

const navSeedID = "79f4d98a-3ce0-4da4-82ea-e9c8686e804f"

func navigationTestServer(t *testing.T) (*Server, *metadata.Entity) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "nav.db"))
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
		Fields: []metadata.Field{{Name: "Тема", Type: metadata.FieldTypeString}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{направления, ent}); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, "Направления", uuid.MustParse(navSeedID), map[string]any{"Наименование": "Химчистка"}, направления); err != nil {
		t.Fatal(err)
	}

	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: "Звонок", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{
				Kind: metadata.FormElementButton, Name: "Команда",
				Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "КомандаНажатие"},
			},
			{
				Kind: metadata.FormElementButton, Name: "КомандаНеСсылка",
				Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "КомандаНеСсылка"},
			},
		},
		ProgramAST: mustParse(t, `
Процедура КомандаНажатие()
	Ссылка = Справочники.Направления.НайтиПоИдентификатору("`+navSeedID+`");
	ОткрытьФорму(Ссылка);
КонецПроцедуры

Процедура КомандаНеСсылка()
	ОткрытьФорму("не ссылка");
КонецПроцедуры
`),
	}
	ent.Forms = []*metadata.FormModule{form}

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
	return s, ent
}

func navigationEvent(t *testing.T, srv *Server, ent *metadata.Entity, element string) formEventResponse {
	t.Helper()
	body := url.Values{}
	body.Set("_element", element)
	body.Set("_event", string(metadata.FormEventOnClick))
	req := httptest.NewRequest("POST", "/ui/document/"+ent.Name+"/form-event", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("kind", "document")
	rctx.URLParams.Add("entity", ent.Name)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	srv.handleManagedFormEvent(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	return decodeFormEventResponse(t, rec.Body.Bytes())
}

func TestOpenForm_NavigationURLInResponse(t *testing.T) {
	srv, ent := navigationTestServer(t)
	resp := navigationEvent(t, srv, ent, "Команда")
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if resp.Navigation == nil {
		t.Fatalf("response carries no navigation: %+v", resp)
	}
	want := "/ui/catalog/Направления/" + navSeedID
	if resp.Navigation.URL != want {
		t.Fatalf("navigation url=%q want %q", resp.Navigation.URL, want)
	}
}

func TestOpenForm_NotAReferenceFailsClosed(t *testing.T) {
	srv, ent := navigationTestServer(t)
	resp := navigationEvent(t, srv, ent, "КомандаНеСсылка")
	if resp.OK {
		t.Fatalf("non-reference argument must fail the event")
	}
	if !strings.Contains(resp.Error, "типизированной ссылкой") {
		t.Fatalf("error=%q", resp.Error)
	}
}
