package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestInfoOwnershipUIConflict(t *testing.T) {
	for _, mode := range []string{"ordinary", "managed", "close-intent", "list-post"} {
		for _, edit := range []bool{false, true} {
			if mode == "list-post" && !edit {
				continue
			}
			t.Run(fmt.Sprintf("%s/edit=%v", mode, edit), func(t *testing.T) {
				dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
					ctx := context.Background()
					doc := &metadata.Entity{Name: "Установка", Kind: metadata.KindDocument, Posting: true,
						Fields:     []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString}, {Name: "Цена", Type: metadata.FieldTypeNumber}},
						TableParts: []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{{Name: "Текст", Type: metadata.FieldTypeString}}}},
					}
					if mode != "ordinary" && mode != "list-post" {
						doc.Forms = []*metadata.FormModule{managedObjectForm(
							fieldEl("КлючПоле", "Объект.Ключ"), fieldEl("ЦенаПоле", "Объект.Цена"),
							&metadata.FormElement{Kind: metadata.FormElementTablePart, Name: "Таблица", DataPath: "Объект.Строки", Children: []*metadata.FormElement{fieldEl("ТекстПоле", "Текст")}},
							fieldEl("ЗаметкаПоле", "Заметка"),
						)}
						doc.Forms[0].Attributes = []*metadata.FormAttribute{{Name: "Заметка", TypeRef: "string"}}
					}
					ir := &metadata.InfoRegister{Name: "ЦеныОтказа", Recorder: true, Dimensions: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString}}, Resources: []metadata.Field{{Name: "Цена", Type: metadata.FieldTypeNumber}}}
					if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
						t.Fatal(err)
					}
					if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
						t.Fatal(err)
					}
					prog := mustParse(t, `Процедура OnPost()
       Дв = Движения.ЦеныОтказа.Добавить(); Дв.Ключ = this.Ключ; Дв.Цена = this.Цена;
      КонецПроцедуры`)
					reg := runtime.NewRegistry()
					reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{doc}, InfoRegs: []*metadata.InfoRegister{ir}, Programs: map[string]*ast.Program{doc.Name: prog}})
					interp := interpreter.New()
					interp.LookupProc = reg.GetModuleProc
					bundle, err := i18n.Load(i18n.EmbeddedLocales, "")
					if err != nil {
						t.Fatal(err)
					}
					s := New(reg, db, interp, nil, Config{Bundle: bundle, Lang: "en"}, nil)
					router := chi.NewRouter()
					s.Mount(router)
					owner := uuid.New()
					result, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{Entity: doc, ID: owner, IsNew: true, Action: "post", Fields: map[string]any{"Ключ": "занято", "Цена": float64(10)}})
					if err != nil || result.DSLError != "" {
						t.Fatalf("owner: %v %s", err, result.DSLError)
					}
					second := uuid.New()
					target := "/ui/document/" + doc.Name + "/new"
					body := url.Values{"Ключ": {"занято"}, "Цена": {"20"}, "tp.Строки.0.Текст": {"попытка"}, "Заметка": {"сохранить заметку"}, "_action": {"post_and_close"}}
					if mode == "managed" || mode == "close-intent" {
						body.Del("tp.Строки.0.Текст")
						body.Set("tp_json.Строки", `[{"Текст":"попытка"}]`)
					}
					if edit {
						fields := map[string]any{"Ключ": "свободно", "Цена": float64(5)}
						if mode == "list-post" {
							fields["Ключ"] = "занято"
						}
						result, err = s.entitySvc.Save(ctx, entityservice.SaveRequest{Entity: doc, ID: second, IsNew: true, Fields: fields, TablePartRows: map[string][]map[string]any{"Строки": {{"Текст": "до"}}}})
						if err != nil || result.DSLError != "" {
							t.Fatalf("second: %v %s", err, result.DSLError)
						}
						target = "/ui/document/" + doc.Name + "/" + second.String()
						body.Set("_version", "1")
					}
					var rec *httptest.ResponseRecorder
					if mode == "close-intent" {
						closeBody := closeIntentBody(uuid.NewString(), "ok", "")
						for key, values := range body {
							closeBody[key] = values
						}
						closeBody.Set("_close_mode", "post")
						if edit {
							closeBody.Set("_id", second.String())
						}
						rec = executeFormCloseIntent(t, s, doc, closeBody)
					} else {
						if mode == "list-post" {
							target += "/post"
						}
						req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body.Encode()))
						req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
						req = req.WithContext(auth.ContextWithOpenAccess(req.Context()))
						rec = httptest.NewRecorder()
						router.ServeHTTP(rec, req)
					}
					if mode == "list-post" {
						if rec.Code != http.StatusSeeOther {
							t.Fatalf("list status=%d: %s", rec.Code, rec.Body.String())
						}
						loc, err := url.Parse(rec.Header().Get("Location"))
						if err != nil {
							t.Fatal(err)
						}
						if !strings.Contains(loc.Query().Get("posting_error"), owner.String()) {
							t.Fatalf("missing conflict: %s", loc)
						}
					} else {
						if rec.Code != http.StatusConflict {
							t.Fatalf("status=%d, want 409: %s", rec.Code, rec.Body.String())
						}
						text := rec.Body.String()
						for _, part := range []string{"information register", ir.Name, owner.String()} {
							if !strings.Contains(text, part) {
								t.Errorf("message missing %q: %s", part, text)
							}
						}
						if mode == "close-intent" {
							response := decodeCloseIntentResponse(t, rec)
							if response.OK || response.Close == nil || response.Close.Allowed || response.Close.Saved || response.Dirty == nil || !*response.Dirty {
								t.Fatalf("close discarded attempt: %+v", response)
							}
							// Unchanged submitted values can be omitted by the compact delta protocol.
							for key, want := range map[string]string{"Ключ": "занято", "Цена": "20", "Заметка": "сохранить заметку"} {
								if got, ok := response.Values[key]; ok && fmt.Sprint(got) != want {
									t.Errorf("state changed %s: %v", key, got)
								}
							}
							if rows, ok := response.TableParts["Строки"]; ok && (len(rows) != 1 || rows[0]["Текст"] != "попытка") {
								t.Errorf("close response changed submitted table part: %#v", rows)
							}
							if response.Version != 0 && response.Version != 1 {
								t.Errorf("failed close advanced version: %d", response.Version)
							}
						} else {
							for _, part := range []string{`value="занято"`, `value="20"`, "попытка"} {
								if !strings.Contains(text, part) {
									t.Errorf("submitted input missing %q", part)
								}
							}
							if mode == "managed" && !strings.Contains(text, "сохранить заметку") {
								t.Error("form attribute lost")
							}
							if edit && !strings.Contains(text, `name="_version" value="1"`) {
								t.Error("edit version lost")
							}
							if rec.Header().Get("Location") != "" {
								t.Error("conflict redirected and discarded input")
							}
						}
					}
					var price, recorder string
					if err := db.QueryRow(ctx, "SELECT CAST(цена AS TEXT), CAST(recorder AS TEXT) FROM "+metadata.InfoRegTableName(ir.Name)).Scan(&price, &recorder); err != nil {
						t.Fatal(err)
					}
					if price != "10" || recorder != owner.String() {
						t.Fatalf("owner changed: %s %s", price, recorder)
					}
					docs, err := db.List(ctx, doc.Name, doc, storage.ListParams{})
					if err != nil {
						t.Fatal(err)
					}
					want := 1
					if edit {
						want = 2
					}
					if len(docs) != want {
						t.Fatalf("new document not rolled back: %d", len(docs))
					}
					if edit {
						row, err := db.GetByID(ctx, doc.Name, second, doc)
						if err != nil {
							t.Fatal(err)
						}
						key := "свободно"
						if mode == "list-post" {
							key = "занято"
						}
						if row["Ключ"] != key || fmt.Sprint(row["Цена"]) != "5" || fmt.Sprint(row["_version"]) != "1" || asBool(row["posted"]) {
							t.Fatalf("second changed: %#v", row)
						}
						rows, err := db.GetTablePartRows(ctx, doc.Name, "Строки", second, doc.TableParts[0])
						if err != nil {
							t.Fatal(err)
						}
						if len(rows) != 1 || rows[0]["Текст"] != "до" {
							t.Fatalf("table part changed: %#v", rows)
						}
					}
				})
			})
		}
	}
}
