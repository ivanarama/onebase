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
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Mount both routes: the carry name must agree in the initial browser config,
// submitted body and response, without consulting the private allocator.
func mountCarryScopeProcessor(t *testing.T, db *storage.DB, proc *processor.Processor) http.Handler {
	t.Helper()
	if err := db.Migrate(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Programs: map[string]*ast.Program{proc.Name: mustParse(t, `Процедура Выполнить()
 Сообщить("carry-run:" + Строка(Параметры.Flag));
КонецПроцедуры`)}})
	reg.LoadProcessors([]*processor.Processor{proc})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	srv := &Server{store: db, reg: reg, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore(), ops: newOperationLimiter()}
	srv.entitySvc = srv.newEntityService(nil)
	router := chi.NewRouter()
	srv.Mount(router)
	return router
}

func carryScopePage(t *testing.T, router http.Handler, name string) (string, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/processor/"+name, nil))
	page := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status=%d: %s", rec.Code, page)
	}
	const startTag = `<script type="application/json" id="ob-managed-config">`
	_, tail, found := strings.Cut(page, startTag)
	if !found {
		t.Fatal("missing mounted form config")
	}
	raw, _, found := strings.Cut(tail, "</script>")
	if !found {
		t.Fatal("unfinished form config")
	}
	var config struct {
		Names  []string          `json:"formAttrs"`
		Values map[string]string `json:"formAttrValues"`
	}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Names) != 1 || config.Values[config.Names[0]] == "" {
		t.Fatalf("missing initial carry: %+v", config)
	}
	return config.Names[0], config.Values[config.Names[0]]
}

func carryScopePost(t *testing.T, router http.Handler, name string, body url.Values) formEventResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/ui/processor/"+name+"/form-event", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status=%d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK || resp.Error != "" {
		t.Fatalf("event failed: ok=%v error=%s", resp.OK, resp.Error)
	}
	return resp
}

func TestProcessorFormCarry_RootScopeHTTP(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, attr := range []bool{false, true} {
			for _, tc := range []struct {
				name, root, column      string
				child, readonly, hidden bool
			}{
				{name: "unplaced control"},
				{name: "full column path", column: "Объект.Rows.Secret"},
				{name: "short column path", column: "Secret"},
				{name: "root-like column path", column: "Объект.Secret"},
				{name: "table descendant", child: true},
				{name: "reference path", root: "Объект.Other.Secret"},
				{name: "other scope", root: "Список.Secret"},
				{name: "placed object", root: "Объект.Secret"},
				{name: "placed bare", root: "Secret"},
				{name: "placed form", root: "Форма.Secret"},
				{name: "placed English", root: "Object.Secret"},
				{name: "placed readonly mixed case", root: "оБъЕкТ.sEcReT", readonly: true},
				{name: "placed hidden", root: "Form.Secret", hidden: true},
			} {
				t.Run(fmt.Sprintf("attribute=%v/%s", attr, tc.name), func(t *testing.T) {
					form := processorExecutionForm(
						&metadata.FormElement{Kind: metadata.FormElementField, Name: "Marker", DataPath: "Объект.Note", HiddenWhen: "Flag"},
						&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Set", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Set"}},
						&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Check", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Check"}},
					)
					form.ProgramAST = mustParse(t, `Процедура Set()
 Объект.Secret = "synthetic-private-value";
 Объект.Flag = Истина;
КонецПроцедуры
Процедура Check()
 Сообщить(Строка(Объект.Secret = "synthetic-private-value") + "|" + Строка(Объект.Flag));
КонецПроцедуры`)
					proc := &processor.Processor{Name: "CarryRootScope", Forms: []*metadata.FormModule{form}, Params: []processor.Param{{Name: "Note", Type: "string"}, {Name: "Flag", Type: "bool", Default: true}}}
					if attr {
						form.Attributes = []*metadata.FormAttribute{{Name: "Secret", TypeRef: "string"}}
					} else {
						proc.Params = append(proc.Params, processor.Param{Name: "Secret", Type: "string"})
					}
					if tc.root != "" {
						el := &metadata.FormElement{Kind: metadata.FormElementField, Name: "RootSecret", DataPath: tc.root, ReadOnly: tc.readonly}
						if tc.hidden {
							el.HiddenWhen = "Flag"
						}
						form.Elements = append(form.Elements, el)
					}
					if tc.column != "" || tc.child {
						table := &metadata.FormElement{Kind: metadata.FormElementTablePart, Name: "Rows", DataPath: "Объект.Rows"}
						if tc.child {
							table.Children = []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "ChildSecret", DataPath: "Объект.Secret"}}
						} else {
							table.Columns = []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "ColumnSecret", DataPath: tc.column}}
						}
						form.Elements = append(form.Elements, table)
						proc.TableParts = []metadata.TablePart{{Name: "Rows", Fields: []metadata.Field{{Name: "Secret", Type: metadata.FieldTypeString}}}}
					}
					router := mountCarryScopeProcessor(t, db, proc)
					carryName, initial := carryScopePage(t, router, proc.Name)
					body := processorClickBody("Set")
					body.Set(carryName, initial)
					first := carryScopePost(t, router, proc.Name, body)
					placed := strings.HasPrefix(tc.name, "placed ")
					for event, resp := range []formEventResponse{first, func() formEventResponse {
						next := processorClickBody("Check")
						token, _ := first.Values[carryName].(string)
						if token == "" {
							t.Fatal("missing next carry")
						}
						next.Set(carryName, token)
						return carryScopePost(t, router, proc.Name, next)
					}()} {
						_, exposed := resp.Values["Secret"]
						if exposed != placed {
							t.Errorf("event %d: root exposure=%v, want %v", event, exposed, placed)
						}
						if placed && resp.Values["Secret"] != "synthetic-private-value" {
							t.Errorf("placed value lost: %v", resp.Values["Secret"])
						}
						if resp.ElementStates == nil || !resp.ElementStates.Hidden["0"] {
							t.Errorf("event %d lost hidden state", event)
						}
						if event == 1 && strings.Join(resp.Messages, "|") != "true|true" {
							t.Errorf("carried handler state=%v", resp.Messages)
						}
					}
				})
			}
		}
	})
}

func TestProcessorFormCarry_FieldNamespaceHTTP(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, tc := range []struct {
			name          string
			params, attrs []string
		}{
			{name: "no collision", attrs: []string{"UserText"}},
			{name: "attribute collision", attrs: []string{"_ob_carry"}},
			{name: "mixed case and repeated fallback", params: []string{"_OB_CARRY", "_OB_SERVICE_ob_carry_"}, attrs: []string{"_ob_service_ob_carry", "_ob_service_ob_carry__"}},
			{name: "attribute case collision", attrs: []string{"_OB_CaRrY", "_OB_SERVICE_OB_CARRY", "_ob_service_ob_carry_"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				form := processorExecutionForm(&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Check", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Check"}})
				proc := &processor.Processor{Name: "CarryFieldNamespace", Forms: []*metadata.FormModule{form}, Params: []processor.Param{{Name: "Flag", Type: "bool", Default: true}}}
				var names []string
				// Реквизит формы с именем на «_» — служебный: браузер его не
				// присылает (processorRequestControlsForForm), обработчик видит
				// пустое значение. Проверяется, что перенос не занял это имя и
				// не подставил туда свой токен.
				serviceAttr := map[string]bool{}
				for _, name := range tc.params {
					proc.Params = append(proc.Params, processor.Param{Name: name, Type: "string", Default: "initial-user-text"})
					names = append(names, name)
				}
				for _, name := range tc.attrs {
					form.Attributes = append(form.Attributes, &metadata.FormAttribute{Name: name, TypeRef: "string"})
					names = append(names, name)
					serviceAttr[name] = strings.HasPrefix(name, "_")
				}
				program := "Процедура Check()\n Сообщить(Строка(Объект.Flag));\n"
				for i, name := range names {
					form.Elements = append(form.Elements, &metadata.FormElement{Kind: metadata.FormElementField, Name: fmt.Sprintf("UserField%d", i), DataPath: "Объект." + name})
					program += " Сообщить(Объект." + name + ");\n"
				}
				form.ProgramAST = mustParse(t, program+"КонецПроцедуры")
				router := mountCarryScopeProcessor(t, db, proc)
				carryName, token := carryScopePage(t, router, proc.Name)
				for _, name := range names {
					if strings.EqualFold(name, carryName) {
						t.Errorf("initial carry occupies user field %s", name)
					}
				}
				for event := 0; event < 2; event++ {
					body := processorClickBody("Check")
					body.Set(carryName, token)
					want := []string{"true"}
					for i, name := range names {
						text := fmt.Sprintf("user-%d-event-%d", i, event)
						body.Set(name, text)
						if serviceAttr[name] {
							text = ""
						}
						want = append(want, text)
					}
					resp := carryScopePost(t, router, proc.Name, body)
					if strings.Join(resp.Messages, "|") != strings.Join(want, "|") {
						t.Errorf("event %d handler=%v want=%v", event, resp.Messages, want)
					}
					for i, name := range names {
						if got, _ := resp.Values[name].(string); got != want[i+1] {
							t.Errorf("event %d: user field %s overwritten", event, name)
						}
					}
					token, _ = resp.Values[carryName].(string)
					if token == "" {
						t.Fatal("missing next carry")
					}
				}

				runBody := url.Values{carryName: {token}}
				req := httptest.NewRequest(http.MethodPost, "/ui/processor/"+proc.Name, strings.NewReader(runBody.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `<div class="msg-info">carry-run:true</div>`) {
					t.Fatalf("run lost carried parameter: status=%d", rec.Code)
				}
				if !strings.Contains(rec.Body.String(), `"formAttrs":["`+carryName+`"]`) {
					t.Fatal("run result changed carry name")
				}
			})
		}
	})
}
