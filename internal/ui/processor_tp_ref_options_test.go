package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"golang.org/x/net/html"
)

const processorTPRefTargetName = "Товар-вне-первой-страницы"

func processorTPRefOptionsFixture(t *testing.T) (*Server, *processor.Processor, *metadata.Entity, uuid.UUID) {
	t.Helper()
	form := processorExecutionForm(
		&metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "Заполнить",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Заполнить"},
		},
		&metadata.FormElement{
			Kind: metadata.FormElementTablePart, Name: "СтрокиФормы", DataPath: "Объект.Строки",
		},
	)
	form.ProgramAST = mustParse(t, `
Процедура Заполнить()
	Строка = Объект.Строки.Добавить();
	Строка.Товар = Справочники.Товары.НайтиПоНаименованию("`+processorTPRefTargetName+`");
КонецПроцедуры
`)
	proc := &processor.Processor{
		Name: "ПодборТоваров",
		TableParts: []metadata.TablePart{{
			Name: "Строки",
			Fields: []metadata.Field{{
				Name: "Товар", Type: metadata.FieldType("reference:Товары"), RefEntity: "Товары",
			}},
		}},
		Forms: []*metadata.FormModule{form},
	}
	srv, db := newProcessorFormEventExecutionServer(t, proc, nil)
	refEntity := &metadata.Entity{
		Name: "Товары", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	if err := db.Migrate(t.Context(), []*metadata.Entity{refEntity}); err != nil {
		t.Fatal(err)
	}
	srv.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{refEntity}})
	for i := 1; i <= refPickerDefaultLimit; i++ {
		id := uuid.MustParse("00000000-0000-0000-0000-" + fmt.Sprintf("%012d", i))
		if err := db.Upsert(t.Context(), refEntity.Name, id, map[string]any{
			"Наименование": fmt.Sprintf("Товар-%03d", i),
		}, refEntity); err != nil {
			t.Fatal(err)
		}
	}
	target := uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err := db.Upsert(t.Context(), refEntity.Name, target, map[string]any{
		"Наименование": processorTPRefTargetName,
	}, refEntity); err != nil {
		t.Fatal(err)
	}
	return srv, proc, refEntity, target
}

func TestProcessorManagedFormLoadsTablePartReferenceOptions(t *testing.T) {
	srv, proc, _, target := processorTPRefOptionsFixture(t)
	initial, err := srv.initialReferenceOptions(t.Context(), srv.reg.GetEntity("Товары"), refOptionsChoice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if hasOptionWithLabel(initial, target.String(), processorTPRefTargetName) {
		t.Fatal("предусловие нарушено: целевая ссылка попала в первую страницу")
	}

	req := httptest.NewRequest(http.MethodGet, "/ui/processor/"+proc.Name, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", proc.Name)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	srv.processorForm(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET managed processor: status=%d body=%s", rec.Code, rec.Body.String())
	}
	doc, err := html.Parse(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	host := keyboardFindHTML(doc, func(node *html.Node) bool {
		name, ok := keyboardHTMLAttr(node, "data-sg-tp")
		return node.Type == html.ElementNode && ok && name == "Строки"
	})
	if host == nil {
		t.Fatal("HTTP-рендер обработки потерял SlickGrid табличной части")
	}
	raw, ok := keyboardHTMLAttr(host, "data-sg-ref")
	if !ok {
		t.Fatal("SlickGrid обработки не получил data-sg-ref")
	}
	var options map[string][]map[string]any
	if err := json.Unmarshal([]byte(raw), &options); err != nil {
		t.Fatalf("data-sg-ref не JSON: %v: %s", err, raw)
	}
	if len(options["Товар"]) != refPickerDefaultLimit {
		t.Fatalf("первичный рендер получил %d опций, ожидалось %d", len(options["Товар"]), refPickerDefaultLimit)
	}
}

func TestProcessorFormEventReturnsCurrentTablePartReferenceOptions(t *testing.T) {
	srv, proc, refEntity, target := processorTPRefOptionsFixture(t)
	initial, err := srv.initialReferenceOptions(t.Context(), refEntity, refOptionsChoice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if hasOptionWithLabel(initial, target.String(), processorTPRefTargetName) {
		t.Fatal("предусловие нарушено: целевая ссылка попала в первую страницу")
	}

	body := processorClickBody("Заполнить")
	body.Set("tp_json.Строки", "[]")
	resp := decodeFormEventResponse(t, postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode())).Body.Bytes())
	if !resp.OK || resp.Error != "" {
		t.Fatalf("processor form-event: ok=%v error=%q", resp.OK, resp.Error)
	}
	rows := resp.TableParts["Строки"]
	if len(rows) != 1 || refValueString(rows[0]["Товар"]) != target.String() {
		t.Fatalf("обработчик не вернул целевую ссылку в строке: %#v", rows)
	}
	if !hasOptionWithLabel(resp.TPRefOptions["Строки"]["Товар"], target.String(), processorTPRefTargetName) {
		t.Fatalf("tpRefOptions не содержит подпись ссылки вне первой страницы: %#v", resp.TPRefOptions)
	}
}

func TestProcessorFormEventTablePartReferenceOptionsRespectReadPermission(t *testing.T) {
	srv, proc, _, target := processorTPRefOptionsFixture(t)
	form := proc.ManagedForm()
	form.Elements[0].Handlers[metadata.FormEventOnClick] = "Проверить"
	form.ProgramAST = mustParse(t, `
Процедура Проверить()
КонецПроцедуры
`)
	body := url.Values{}
	body.Set("_element", "Заполнить")
	body.Set("_event", string(metadata.FormEventOnClick))
	body.Set("tp_json.Строки", `[{"Товар":"`+target.String()+`"}]`)
	req := httptest.NewRequest(http.MethodPost, "/ui/processor/"+proc.Name+"/form-event", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", proc.Name)
	user := &auth.User{Login: "operator", Roles: []*auth.Role{{Permissions: auth.Permission{
		Processors: map[string][]string{proc.Name: {"run"}},
		Catalogs:   map[string][]string{},
	}}}}
	req = req.WithContext(auth.ContextWithUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx), user))
	rec := httptest.NewRecorder()
	srv.handleProcessorFormEvent(rec, req)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK || resp.Error != "" {
		t.Fatalf("processor form-event: ok=%v error=%q", resp.OK, resp.Error)
	}
	if rows := resp.TPRefOptions["Строки"]["Товар"]; len(rows) != 0 {
		t.Fatalf("подпись ссылки утекла без catalog/read: %#v", rows)
	}
}
