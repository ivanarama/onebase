package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
)

// Both mounted REST generations share the actual frontend entity service.
// Omitted required fields and table parts must survive a partial post payload.
func TestRESTWritePost_MountedMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		doc := &metadata.Entity{Name: "RESTЗаказ", Kind: metadata.KindDocument, Posting: true,
			Fields:     []metadata.Field{{Name: "Ввод", Type: metadata.FieldTypeString}, {Name: "Расчет", Type: metadata.FieldTypeString}, {Name: "След", Type: metadata.FieldTypeString}, {Name: "Прочитано", Type: metadata.FieldTypeString}, {Name: "Required", Type: metadata.FieldTypeString, Required: true}},
			TableParts: []metadata.TablePart{{Name: "Lines", Fields: []metadata.Field{{Name: "Item", Type: metadata.FieldTypeString, Required: true}}}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
			t.Fatal(err)
		}
		prog := mustParseProgram(t, `Процедура ПриЗаписи()
 ЭтотОбъект.След = "W";
 Если ЭтотОбъект.Ввод = "fail" Тогда ВызватьИсключение "write rejected"; КонецЕсли;
 ЭтотОбъект.Расчет = ЭтотОбъект.Ввод + "-W";
КонецПроцедуры
Процедура ОбработкаПроведения()
 Если ЭтотОбъект.След <> "W" Тогда ВызватьИсключение "write skipped"; КонецЕсли;
 ЭтотОбъект.След = ЭтотОбъект.След + "P";
 Зап = Новый Запрос;
 Зап.Текст = "ВЫБРАТЬ Д.Расчет КАК Значение ИЗ Документ.RESTЗаказ КАК Д ГДЕ Д.Ссылка = &ИД";
 Зап.УстановитьПараметр("ИД",ЭтотОбъект.Ссылка);
 Рез = Зап.Выполнить();
 ЭтотОбъект.Прочитано = Рез[0].Значение;
КонецПроцедуры`)
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{doc}, Programs: map[string]*ast.Program{doc.Name: prog}})
		interp := interpreter.New()
		interp.LookupProc = reg.GetModuleProc
		repo := auth.NewRepo(db)
		if err := repo.EnsureSchema(ctx); err != nil {
			t.Fatal(err)
		}
		front := ui.New(reg, db, interp, repo, ui.Config{}, nil)
		t.Cleanup(front.Close)
		srv := New(reg, db, interp, repo, "", 0, Config{}, front, nil)
		for _, generation := range []string{"v1", "v2"} {
			for _, body := range []string{"", `{"Ввод":"input"}`, `{"Ввод":"input","__tableparts":{"Lines":[]}}`, `{"Ввод":"fail"}`} {
				t.Run(generation+"/"+body, func(t *testing.T) {
					id := uuid.New()
					if err := db.Upsert(ctx, doc.Name, id, map[string]any{"Ввод": "old", "Required": "keep", "Расчет": "old-calc"}, doc); err != nil {
						t.Fatal(err)
					}
					if err := db.UpsertTablePartRows(ctx, doc.Name, "Lines", id, []map[string]any{{"Item": "keep-line"}}, doc.TableParts[0]); err != nil {
						t.Fatal(err)
					}
					path := "/documents/" + doc.Name + "/" + id.String() + "/post"
					if generation == "v2" {
						path = "/api/v2/document/" + doc.Name + "/" + id.String() + "/post"
					}
					req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
					req.Header.Set("Content-Type", "application/json")
					w := httptest.NewRecorder()
					srv.Handler().ServeHTTP(w, req)
					row, err := db.GetByID(ctx, doc.Name, id, doc)
					if err != nil {
						t.Fatal(err)
					}
					v, err := db.EntityVersion(ctx, doc.Name, id)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(body, "fail") {
						if w.Code != http.StatusUnprocessableEntity || v != 1 || row["Расчет"] != "old-calc" || row["posted"] != false {
							t.Fatalf("rejection HTTP=%d body=%s row=%v v=%d", w.Code, w.Body.String(), row, v)
						}
						return
					}
					if w.Code != http.StatusOK {
						t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
					}
					calc := "input-W"
					if body == "" {
						calc = "old-W"
					}
					if row["Расчет"] != calc || row["Прочитано"] != calc || row["След"] != "WP" || row["Required"] != "keep" || row["posted"] != true || v != 2 {
						t.Fatalf("final row=%v v=%d", row, v)
					}
					lines, err := db.GetTablePartRows(ctx, doc.Name, "Lines", id, doc.TableParts[0])
					if err != nil {
						t.Fatal(err)
					}
					want := 1
					if strings.Contains(body, "__tableparts") {
						want = 0
					}
					if len(lines) != want {
						t.Fatalf("tablepart=%v want %d", lines, want)
					}
					if want == 1 && fmt.Sprint(lines[0]["Item"]) != "keep-line" {
						t.Fatalf("omitted TP overwritten: %v", lines)
					}
				})
			}
		}
	})
}
