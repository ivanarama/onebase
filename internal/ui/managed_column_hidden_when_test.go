package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/metadata"
)

// hidden_when у колонки таблицы формы. Поиск адреса показывает «Улица» и
// «Представление адреса» при поиске по классификатору и одну «Улицу» при
// поиске DaData; переключает обработчик кнопки через реквизит формы. Колонка
// обязана исчезать и появляться без перезагрузки — по ответу события.
func columnHiddenWhenEntity(t *testing.T, valueTable bool) *metadata.Entity {
	t.Helper()
	ent := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{{Name: "Найдено", Fields: []metadata.Field{
			{Name: "Улица", Type: metadata.FieldTypeString},
			{Name: "Представление", Type: metadata.FieldTypeString},
			{Name: "Ид", Type: metadata.FieldTypeString},
		}}},
	}
	table := &metadata.FormElement{Kind: metadata.FormElementTablePart, Name: "ТаблицаНайдено", DataPath: "Объект.Найдено",
		Children: []*metadata.FormElement{
			{Kind: metadata.FormElementColumn, Name: "КолУлица", DataPath: "Улица"},
			{Kind: metadata.FormElementColumn, Name: "КолПредставление", DataPath: "Представление",
				HiddenWhen: "НЕ ПоКлассификатору"},
		}}
	attrs := []*metadata.FormAttribute{{Name: "ПоКлассификатору", TypeRef: "bool", Save: false}}
	if valueTable {
		table.DataPath = "Форма.Результаты"
		attrs = append(attrs, &metadata.FormAttribute{Name: "Результаты", TypeRef: "ValueTable", Save: false,
			Columns: []*metadata.FormAttributeColumn{
				{Name: "Улица", TypeRef: "string"}, {Name: "Представление", TypeRef: "string"}, {Name: "Ид", TypeRef: "string"},
			}})
	}
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: ent.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Attributes: attrs,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "ПолеНаим", DataPath: "Объект.Наименование",
				Handlers: map[metadata.FormEventType]string{metadata.FormEventOnChange: "ПолеНаимПриИзменении"}},
			table,
		},
		ProgramAST: mustParse(t, `
Процедура ПолеНаимПриИзменении()
	Объект.ПоКлассификатору = (Объект.Наименование = "классификатор");
КонецПроцедуры
`),
	}
	ent.Forms = []*metadata.FormModule{form}
	return ent
}

func openNewColumnHiddenWhenForm(t *testing.T, srv *Server, ent *metadata.Entity) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/ui/catalog/"+ent.Name+"/new", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("entity", ent.Name)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	srv.form(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET формы: %d %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func columnHiddenWhenEvent(t *testing.T, srv *Server, ent *metadata.Entity, name string) map[string]bool {
	t.Helper()
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, url.Values{
		"_element": {"ПолеНаим"}, "_event": {string(metadata.FormEventOnChange)}, "Наименование": {name},
	}).Body.Bytes())
	if !resp.OK {
		t.Fatalf("событие: %s", resp.Error)
	}
	if resp.ElementStates == nil {
		t.Fatal("ответ события без elementStates")
	}
	return resp.ElementStates.Hidden
}

// Колонку с hidden_when путь размещения «1.1» (вторая колонка таблицы,
// стоящей вторым элементом формы) связывает с картой состояний ответа.
const columnHiddenWhenPath = "1.1"

func TestManagedTPColumnHiddenWhen_RenderAndFormEvent(t *testing.T) {
	ent := columnHiddenWhenEntity(t, false)
	srv, _ := newSubmitTestServer(t, []*metadata.Entity{ent})

	cols := parseManagedTPColumns(t, openNewColumnHiddenWhenForm(t, srv, ent))
	byID := map[string]managedTPColumnJSON{}
	for _, col := range cols {
		byID[col.ID] = col
	}
	// Реквизит формы пуст — условие «НЕ ПоКлассификатору» истинно, колонка
	// скрыта, но осталась в описании таблицы со своим путём.
	repr := byID["Представление"]
	if repr.ElPath != columnHiddenWhenPath || !repr.CondHidden || repr.Hidden {
		t.Fatalf("колонка с hidden_when при отрисовке: %+v", repr)
	}
	if street := byID["Улица"]; street.ElPath != "" || street.CondHidden || street.Hidden {
		t.Fatalf("колонка без условия получила его признаки: %+v", street)
	}
	// Невыбранный реквизит по-прежнему скрыт навсегда и пути не имеет.
	if id := byID["Ид"]; !id.Hidden || id.ElPath != "" {
		t.Fatalf("невыбранный реквизит: %+v", id)
	}

	if hidden := columnHiddenWhenEvent(t, srv, ent, "классификатор"); hidden[columnHiddenWhenPath] {
		t.Fatalf("поиск по классификатору: колонка должна показаться, состояния %+v", hidden)
	}
	if hidden, ok := columnHiddenWhenEvent(t, srv, ent, "dadata")[columnHiddenWhenPath]; !ok || !hidden {
		t.Fatalf("поиск DaData: колонка должна скрыться (ok=%v hidden=%v)", ok, hidden)
	}
}

func TestManagedValueTableColumnHiddenWhen_RenderAndFormEvent(t *testing.T) {
	ent := columnHiddenWhenEntity(t, true)
	srv, _ := newSubmitTestServer(t, []*metadata.Entity{ent})

	body := openNewColumnHiddenWhenForm(t, srv, ent)
	for _, want := range []string{
		`data-vt="Результаты"`,
		`data-vt-cond="Представление=` + columnHiddenWhenPath + `"`,
		`,Представление:h,`, // флаг перестройки строк: скрыта условием
		`<th data-ob-vt-col="` + columnHiddenWhenPath + `" style="display:none;">`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("в разметке ValueTable нет %q:\n%s", want, body)
		}
	}
	// Колонка без условия видна и якоря не несёт.
	if strings.Contains(body, `data-ob-vt-col=""`) || strings.Count(body, `data-ob-vt-col=`) != 1 {
		t.Fatalf("якорь получила не только колонка с условием:\n%s", body)
	}

	if hidden := columnHiddenWhenEvent(t, srv, ent, "классификатор"); hidden[columnHiddenWhenPath] {
		t.Fatalf("поиск по классификатору: колонка ValueTable должна показаться, состояния %+v", hidden)
	}
	if hidden, ok := columnHiddenWhenEvent(t, srv, ent, "dadata")[columnHiddenWhenPath]; !ok || !hidden {
		t.Fatalf("поиск DaData: колонка ValueTable должна скрыться (ok=%v hidden=%v)", ok, hidden)
	}
}

func TestManagedColumnHiddenWhenBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the column hidden_when behavior regression test")
	}
	cmd := exec.Command(node, "--test", "static/managed_column_hidden_when_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node column hidden_when behavior test: %v\n%s", err, output)
	}
}
