package ui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestTablePartFKFormHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		target := &metadata.Entity{Name: "ЦельФормыТЧ", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
		entity := &metadata.Entity{Name: "ФормаСсылокТЧ", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}, TableParts: []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{{Name: "Ссылка", Type: "reference:ЦельФормыТЧ", RefEntity: target.Name}}}}}
		ts := tpw1074Server(t, db, []*metadata.Entity{target, entity})
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		known, parent, missing := uuid.New(), uuid.New(), uuid.New()
		if err := db.Upsert(ctx, target.Name, known, map[string]any{"Наименование": "Цель"}, target); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, entity.Name, parent, map[string]any{"Наименование": "Исходное"}, entity); err != nil {
			t.Fatal(err)
		}
		tp := entity.TableParts[0]

		// Default migrations constrain only parent_id. Model an existing schema
		// with real reference constraints without changing the migration contract.
		if _, err := db.Exec(ctx, "DROP TABLE "+metadata.TablePartTableName(entity.Name, tp.Name)); err != nil {
			t.Fatal(err)
		}
		ddl := strings.TrimSuffix(storage.CreateTablePartSQL(db.Dialect(), entity, tp), "\n)")
		for _, f := range tp.Fields {
			ddl += ", FOREIGN KEY (" + metadata.ColumnName(f) + ") REFERENCES " + metadata.TableName(f.RefEntity) + "(id)"
		}
		if _, err := db.Exec(ctx, ddl+"\n)"); err != nil {
			t.Fatal(err)
		}

		if err := db.UpsertTablePartRows(ctx, entity.Name, tp.Name, parent, []map[string]any{{"Ссылка": known.String()}}, tp); err != nil {
			t.Fatal(err)
		}
		for _, managed := range []bool{false, true} {
			name := "named-fields"
			if managed {
				name = "managed-json"
				entity.Forms = []*metadata.FormModule{{Name: "ФормаОбъекта", Kind: "object", EntityName: entity.Name, LayoutKind: metadata.FormLayoutManaged, Elements: []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование"}, {Kind: metadata.FormElementTablePart, Name: "Строки", DataPath: "Объект.Строки"}}}}
			}
			t.Run(name, func(t *testing.T) {
				form := url.Values{"Наименование": {"Новое"}, "action": {"save"}}
				if managed {
					blob, err := json.Marshal([]map[string]any{{"Ссылка": known.String()}, {"Ссылка": missing.String()}})
					if err != nil {
						t.Fatal(err)
					}
					form.Set("tp_json.Строки", string(blob))
				} else {
					form.Set("tp.Строки.0.Ссылка", known.String())
					form.Set("tp.Строки.1.Ссылка", missing.String())
				}
				resp, err := client.PostForm(ts.URL+"/ui/catalog/"+url.PathEscape(entity.Name)+"/"+parent.String(), form)
				if err != nil {
					t.Fatal(err)
				}
				body, readErr := io.ReadAll(resp.Body)
				if err := resp.Body.Close(); err != nil {
					t.Fatal(err)
				}
				if readErr != nil {
					t.Fatal(readErr)
				}
				// UI retains its existing 500 contract; unlike REST, it displays diagnostics.
				if resp.StatusCode != http.StatusInternalServerError {
					t.Fatalf("status=%d: %s", resp.StatusCode, body)
				}
				for _, want := range []string{"row 2", "поле Ссылка → " + target.Name, missing.String()} {
					if !strings.Contains(string(body), want) {
						t.Errorf("missing %q: %s", want, body)
					}
				}
				got, err := db.GetByID(ctx, entity.Name, parent, entity)
				if err != nil {
					t.Fatal(err)
				}
				if got["Наименование"] != "Исходное" {
					t.Errorf("header not rolled back: %#v", got)
				}
				rows, err := db.GetTablePartRows(ctx, entity.Name, tp.Name, parent, tp)
				if err != nil {
					t.Fatal(err)
				}
				if len(rows) != 1 || rows[0]["Ссылка"] != known.String() {
					t.Errorf("rows not rolled back: %#v", rows)
				}
			})
		}
	})
}
