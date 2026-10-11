package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Exercise the mounted UI routes: DB -> initial grid -> event -> save. SQLite
// returns INTEGER booleans; PostgreSQL returns bools. Both must reach the browser
// as booleans, and older grids posting 1/0 must still preserve their checkboxes.
func TestTablePartBoolHTTP1743(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		entity := &metadata.Entity{
			Name: "Флаги" + uuid.New().String()[:8], Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Единица", Type: metadata.FieldTypeNumber},
				{Name: "Ноль", Type: metadata.FieldTypeNumber},
			},
			TableParts: []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{
				{Name: "Имя", Type: metadata.FieldTypeString}, {Name: "Флаг", Type: metadata.FieldTypeBool},
			}}},
		}
		const eventSource = `
Процедура Проба()
 Сообщить("ok");
КонецПроцедуры
Процедура Переключить()
 Первая = Объект.Строки.Получить(0);
 Первая.Флаг = %s;
 Вторая = Объект.Строки.Получить(1);
 Вторая.Флаг = %s;
КонецПроцедуры
`
		form := &metadata.FormModule{
			Name: "ФормаОбъекта", Kind: "object", EntityName: entity.Name, LayoutKind: metadata.FormLayoutManaged,
			Elements: []*metadata.FormElement{
				{Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование"},
				{Kind: metadata.FormElementTablePart, Name: "ТабСтроки", DataPath: "Объект.Строки"},
				{Kind: metadata.FormElementButton, Name: "Проба", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Проба"}},
				{Kind: metadata.FormElementButton, Name: "Переключить", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Переключить"}},
			},
		}
		form.ProgramAST = mustParse(t, fmt.Sprintf(eventSource, "Ложь", "Истина"))
		entity.Forms = []*metadata.FormModule{form}
		ts := tpw1074Server(t, db, []*metadata.Entity{entity})
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		seed := func(t *testing.T) uuid.UUID {
			t.Helper()
			id := uuid.New()
			if err := db.Upsert(context.Background(), entity.Name, id, map[string]any{"Наименование": "Флаги", "Единица": 1, "Ноль": 0}, entity); err != nil {
				t.Fatal(err)
			}
			if err := db.UpsertTablePartRows(context.Background(), entity.Name, "Строки", id, []map[string]any{
				{"Имя": "а", "Флаг": true}, {"Имя": "б", "Флаг": false},
			}, entity.TableParts[0]); err != nil {
				t.Fatal(err)
			}
			return id
		}
		endpoint := func(id uuid.UUID) string {
			return ts.URL + "/ui/catalog/" + url.PathEscape(entity.Name) + "/" + id.String()
		}
		post := func(t *testing.T, id uuid.UUID, rows []map[string]any, event string, named bool) (int, []byte) {
			t.Helper()
			body := url.Values{"Наименование": {"Флаги"}, "Единица": {"1"}, "Ноль": {"0"}, "action": {"save"}}
			if named {
				for i, row := range rows {
					for key, v := range row {
						body.Set(fmt.Sprintf("tp.Строки.%d.%s", i, key), fmt.Sprint(v))
					}
				}
			} else {
				blob, err := json.Marshal(rows)
				if err != nil {
					t.Fatal(err)
				}
				body.Set("tp_json.Строки", string(blob))
			}
			target := endpoint(id)
			if event != "" {
				target = ts.URL + "/ui/catalog/" + url.PathEscape(entity.Name) + "/form-event"
				body.Set("_id", id.String())
				body.Set("_kind", "object")
				body.Set("_element", event)
				body.Set("_event", string(metadata.FormEventOnClick))
			}
			resp, err := client.PostForm(target, body)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := resp.Body.Close(); err != nil {
					t.Errorf("close response: %v", err)
				}
			}()
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			return resp.StatusCode, data
		}
		checkStored := func(t *testing.T, id uuid.UUID, want []bool) {
			t.Helper()
			rows, err := db.GetTablePartRows(context.Background(), entity.Name, "Строки", id, entity.TableParts[0])
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != len(want) {
				t.Fatalf("persisted rows: %#v", rows)
			}
			for i, row := range rows {
				var got bool
				switch v := row["Флаг"].(type) {
				case bool:
					got = v
				case int64:
					got = v == 1
				default:
					t.Fatalf("unexpected DB boolean %#v", v)
				}
				if got != want[i] {
					t.Errorf("stored row %d flag=%v, want %v", i, got, want[i])
				}
			}
		}
		checkJSON := func(t *testing.T, rows []map[string]any, want []bool) {
			t.Helper()
			if len(rows) != len(want) {
				t.Fatalf("JSON rows: %#v", rows)
			}
			for i, row := range rows {
				got, ok := row["Флаг"].(bool)
				if !ok || got != want[i] {
					t.Errorf("JSON row %d flag=%#v (%T), want bool %v", i, row["Флаг"], row["Флаг"], want[i])
				}
			}
		}
		for _, tc := range []struct {
			name, no, yes string
		}{
			{"boolean", "Ложь", "Истина"},
			{"decimal", "0", "1"},
			// The form parser supplies number fields as float64; DSL literals use Decimal.
			{"float", "Объект.Ноль", "Объект.Единица"},
		} {
			t.Run("open-event-save/"+tc.name, func(t *testing.T) {
				form.ProgramAST = mustParse(t, fmt.Sprintf(eventSource, tc.no, tc.yes))
				id := seed(t)
				resp, err := client.Get(endpoint(id))
				if err != nil {
					t.Fatal(err)
				}
				page, err := io.ReadAll(resp.Body)
				if err := resp.Body.Close(); err != nil {
					t.Fatalf("close response: %v", err)
				}
				if err != nil || resp.StatusCode != http.StatusOK {
					t.Fatalf("GET status=%d err=%v: %s", resp.StatusCode, err, page)
				}
				match := regexp.MustCompile(`data-sg-rows='([^']*)'`).FindSubmatch(page)
				if len(match) != 2 {
					t.Fatalf("missing initial grid rows: %s", page)
				}
				var rows []map[string]any
				if err := json.Unmarshal([]byte(html.UnescapeString(string(match[1]))), &rows); err != nil {
					t.Fatal(err)
				}
				checkJSON(t, rows, []bool{true, false})
				status, data := post(t, id, rows, "", false)
				if status != http.StatusSeeOther {
					t.Fatalf("unchanged save status=%d: %s", status, data)
				}
				checkStored(t, id, []bool{true, false})
				// No-op events may refresh the object from the DB even when the posted
				// values are already bool. Verify two consecutive events and their save.
				for range 2 {
					status, data := post(t, id, rows, "Проба", false)
					if status != http.StatusOK {
						t.Fatalf("event status=%d: %s", status, data)
					}
					event := decodeFormEventResponse(t, data)
					if !event.OK {
						t.Fatal(event.Error)
					}
					rows = event.TableParts["Строки"]
					checkJSON(t, rows, []bool{true, false})
				}
				status, data = post(t, id, rows, "", false)
				if status != http.StatusSeeOther {
					t.Fatalf("save status=%d: %s", status, data)
				}
				checkStored(t, id, []bool{true, false})
				status, data = post(t, id, rows, "Переключить", false)
				if status != http.StatusOK {
					t.Fatalf("toggle status=%d: %s", status, data)
				}
				event := decodeFormEventResponse(t, data)
				if !event.OK {
					t.Fatal(event.Error)
				}
				rows = event.TableParts["Строки"]
				checkJSON(t, rows, []bool{false, true})
				status, data = post(t, id, rows, "", false)
				if status != http.StatusSeeOther {
					t.Fatalf("toggle save status=%d: %s", status, data)
				}
				checkStored(t, id, []bool{false, true})
			})
		}
		for _, path := range []string{"grid", "nogrid", "legacy"} {
			t.Run(path, func(t *testing.T) {
				if path == "legacy" {
					entity.Forms = nil
				} else {
					entity.Forms = []*metadata.FormModule{form}
					form.Elements[1].NoGrid = path == "nogrid"
				}
				defer func() { entity.Forms = []*metadata.FormModule{form}; form.Elements[1].NoGrid = false }()
				for _, tc := range []struct {
					name    string
					yes, no any
				}{
					{"bool", true, false}, {"number", 1, 0}, {"numeric-string", "1", "0"}, {"boolean-string", "true", "false"}, {"unchecked-empty", "true", ""},
				} {
					t.Run(tc.name, func(t *testing.T) {
						rows := []map[string]any{{"Имя": "а", "Флаг": tc.yes}, {"Имя": "б", "Флаг": tc.no}}
						id := seed(t)
						status, data := post(t, id, rows, "", path != "grid")
						if status != http.StatusSeeOther {
							t.Fatalf("save status=%d: %s", status, data)
						}
						checkStored(t, id, []bool{true, false})
						if path != "legacy" {
							id = seed(t)
							status, data = post(t, id, rows, "Проба", path != "grid")
							if status != http.StatusOK {
								t.Fatalf("event status=%d: %s", status, data)
							}
							event := decodeFormEventResponse(t, data)
							if !event.OK {
								t.Fatal(event.Error)
							}
							checkJSON(t, event.TableParts["Строки"], []bool{true, false})
						}
					})
				}
				for _, bad := range []any{"not-a-boolean", 2, -1} {
					t.Run(fmt.Sprint(bad), func(t *testing.T) {
						for _, event := range []string{"", "Проба"} {
							if path == "legacy" && event != "" {
								continue
							}
							id := seed(t)
							status, data := post(t, id, []map[string]any{{"Имя": "а", "Флаг": bad}}, event, path != "grid")
							if event == "" {
								if status != http.StatusBadRequest {
									t.Errorf("invalid flag %v save status=%d: %s", bad, status, data)
								}
							} else {
								response := decodeFormEventResponse(t, data)
								if status != http.StatusOK || response.OK || response.Error == "" || len(response.Messages) != 0 {
									t.Errorf("invalid flag %v event status=%d: %s", bad, status, data)
								}
							}
							checkStored(t, id, []bool{true, false})
						}
					})
				}
			})
		}
	})
}
