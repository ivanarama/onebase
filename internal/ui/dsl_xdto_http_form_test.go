package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

const xdtoHTTPExport = `XML = СериализаторXDTO.ЗаписатьXML(Объект);
Сообщить(XML);
Сообщить(СериализаторXDTO.ЗаписатьXML(СериализаторXDTO.ПрочитатьXML(XML)));`

// Load the form from disk, mount the production routes and send a real HTTP
// request. No private XDTO/hydration helper stands in for the browser path.
func xdtoHTTPFixture(t *testing.T, db *storage.DB, kind, before, policy string) (*Server, *metadata.Entity, string, *auth.User) {
	t.Helper()
	manager := "Справочники.Кат"
	if kind == "document" {
		manager = "Документы.Док"
	}
	proj, entity := xdtoGeneralProject(t, kind, `Процедура ПриЗаписи()
 Если ЭтотОбъект.Отказать Тогда ВызватьИсключение "ожидаемый отказ"; КонецЕсли;
КонецПроцедуры
Процедура ОбработкаПроведения() КонецПроцедуры`, "Процедура Выполнить()\n Об = "+manager+".НайтиПоИдентификатору(Идентификатор).ПолучитьОбъект();\n Сообщить(СериализаторXDTO.ЗаписатьXML(Об));\nКонецПроцедуры")
	dir := filepath.Join(proj.Dir, "forms", strings.ToLower(entity.Name))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: " + entity.Name + `
elements:
  - {kind: ПолеВвода, name: Имя, data_path: Объект.Наименование}
  - {kind: ПолеВвода, name: Телефон, data_path: Объект.Телефон}
  - {kind: ТабличнаяЧасть, name: Товары, data_path: Объект.Товары}
  - kind: Кнопка
    name: Экспорт
    events: {Нажатие: Выполнить}
`
	for name, text := range map[string]string{"объекта.form.yaml": yaml, "объекта.form.os": "Процедура Выполнить()\n" + before + "\n" + xdtoHTTPExport + "\nКонецПроцедуры"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := project.Load(proj.Dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(loaded.Close)
	entity = loaded.Entities[0]
	if len(entity.Forms) != 1 {
		t.Fatalf("form not loaded: %+v", entity.Forms)
	}
	if err := db.Migrate(t.Context(), loaded.Entities); err != nil {
		t.Fatal(err)
	}
	s, _, err := NewOfflineServer(loaded, db)
	if err != nil {
		t.Fatal(err)
	}
	user := xdtoGeneralUser(kind, policy)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), user)))
		})
	})
	s.Mount(router)
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)
	return s, entity, ts.URL + "/ui/" + kind + "/" + url.PathEscape(entity.Name) + "/form-event", user
}

func xdtoHTTPPost(t *testing.T, endpoint string, body url.Values) (formEventResponse, int) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint, strings.NewReader(body.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close response body: %v", err)
		}
	}()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var result formEventResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("response: %v: %s", err, raw)
	}
	return result, resp.StatusCode
}

func xdtoHTTPBody(id uuid.UUID, version int64) url.Values {
	body := url.Values{"_kind": {"object"}, "_element": {"Экспорт"}, "_event": {"Нажатие"},
		"Наименование": {"unsaved"}, "Телефон": {"+79161234455"},
		"tp_json.Товары": {`[{"Артикул":"SKU-1","Количество":2}]`},
		// Forged service state must never be accepted as the source of XDTO flags.
		"posted": {"true"}, "deletion_mark": {"true"}, "Posted": {"true"}, "DeletionMark": {"true"}}
	if id != uuid.Nil {
		body.Set("_id", id.String())
		body.Set("_version", strconv.FormatInt(version, 10))
	}
	return body
}

func xdtoHTTPSeed(t *testing.T, db *storage.DB, entity *metadata.Entity, posted, marked bool) (uuid.UUID, int64) {
	t.Helper()
	ctx := t.Context()
	id := uuid.New()
	fields := map[string]any{"Наименование": "stored", "Телефон": "+79161234455", "Номер": id.String(), "Дата": time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local)}
	if err := db.Upsert(ctx, entity.Name, id, fields, entity); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTablePartRows(ctx, entity.Name, "Товары", id, []map[string]any{{"Артикул": "STORED", "Количество": float64(9)}}, entity.TableParts[0]); err != nil {
		t.Fatal(err)
	}
	if entity.Posting {
		if err := db.SetPosted(ctx, entity.Name, id, posted); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.MarkForDeletion(ctx, entity.Name, id, marked); err != nil {
		t.Fatal(err)
	}
	v, err := db.EntityVersion(ctx, entity.Name, id)
	if err != nil {
		t.Fatal(err)
	}
	return id, v
}

func TestXDTOHTTPFormStateMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, kind := range []string{"document", "catalog"} {
			for _, policy := range []string{"full", "mask_tail", "hide"} {
				for _, assigned := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/assigned=%t", kind, policy, assigned), func(t *testing.T) {
						before := ""
						if assigned {
							before = `Объект["Телефон"] = "+79990001122";`
						}
						s, entity, endpoint, user := xdtoHTTPFixture(t, db, kind, before, policy)
						for _, posted := range []bool{false, true} {
							if kind == "catalog" && posted {
								continue
							}
							for _, marked := range []bool{false, true} {
								t.Run(fmt.Sprintf("posted=%t/marked=%t", posted, marked), func(t *testing.T) {
									id, version := xdtoHTTPSeed(t, db, entity, posted, marked)
									stored, err := db.GetByID(t.Context(), entity.Name, id, entity)
									if err != nil {
										t.Fatal(err)
									}
									// The public manager control must export the same durable flags.
									messages, runErr, err := s.RunProcessor(auth.ContextWithUser(t.Context(), user), s.reg, "Проба", nil, nil, map[string]any{"Идентификатор": id.String()})
									if err != nil || runErr != nil || len(messages) != 1 {
										t.Fatalf("manager control: %v / %v / %q", err, runErr, messages)
									}
									for _, want := range []string{fmt.Sprintf("<DeletionMark>%t</DeletionMark>", marked), "<Ref>" + id.String() + "</Ref>"} {
										if !strings.Contains(messages[0], want) {
											t.Fatal(messages)
										}
									}
									if kind == "document" && !strings.Contains(messages[0], fmt.Sprintf("<Posted>%t</Posted>", posted)) {
										t.Fatal(messages)
									}
									for _, forged := range []string{"false", "true"} {
										body := xdtoHTTPBody(id, version)
										body.Set("posted", forged)
										body.Set("deletion_mark", forged)
										result, status := xdtoHTTPPost(t, endpoint, body)
										if status != http.StatusOK || !result.OK || result.Error != "" || len(result.Messages) != 2 {
											t.Fatalf("export: %d %+v", status, result)
										}
										if result.Messages[0] != result.Messages[1] {
											t.Fatal("round-trip changed XML", result.Messages)
										}
										phone := xdtoGeneralPhone(policy)
										if assigned {
											phone = "+79990001122"
										}
										assertXDTOGeneralSnapshot(t, result.Messages[0], id, phone, posted, marked, kind == "document")
										nameTag := "Description"
										if kind == "document" {
											nameTag = "Наименование"
										}
										if !strings.Contains(result.Messages[0], "<"+nameTag+">unsaved</"+nameTag+">") {
											t.Fatal("unsaved field lost", result.Messages)
										}
										after, err := db.GetByID(t.Context(), entity.Name, id, entity)
										if err != nil {
											t.Fatal(err)
										}
										if !reflect.DeepEqual(stored, after) {
											t.Fatalf("pure export changed DB: before=%v after=%v", stored, after)
										}
										parts, err := db.GetTablePartRows(t.Context(), entity.Name, "Товары", id, entity.TableParts[0])
										if err != nil || len(parts) != 1 || parts[0]["Артикул"] != "STORED" {
											t.Fatalf("export changed TP: %v %v", parts, err)
										}
										if result.Version != 0 || result.SavedID != "" {
											t.Fatalf("export pretended to save: %+v", result)
										}
									}
								})
							}
						}
					})
				}
			}
		}
	})
}

func TestXDTOHTTPNewAndCopyMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, kind := range []string{"document", "catalog"} {
			t.Run(kind, func(t *testing.T) {
				_, entity, endpoint, _ := xdtoHTTPFixture(t, db, kind, "", "full")
				source, _ := xdtoHTTPSeed(t, db, entity, true, true)
				for _, copying := range []bool{false, true} {
					t.Run(fmt.Sprint(copying), func(t *testing.T) {
						body := xdtoHTTPBody(uuid.Nil, 0)
						if copying {
							body.Set(copySourceFormField, source.String())
						}
						result, status := xdtoHTTPPost(t, endpoint, body)
						if status != http.StatusOK || !result.OK || len(result.Messages) != 2 {
							t.Fatalf("new/copy: %d %+v", status, result)
						}
						for _, text := range result.Messages {
							if !strings.Contains(text, "<DeletionMark>false</DeletionMark>") || kind == "document" && !strings.Contains(text, "<Posted>false</Posted>") {
								t.Fatal(text)
							}
							if strings.Contains(text, "<Ref>"+source.String()+"</Ref>") {
								t.Fatal("copy retained source Ref", text)
							}
						}
						if result.Messages[0] != result.Messages[1] {
							t.Fatal(result.Messages)
						}
					})
				}
			})
		}
	})
}

// Live reads must follow Save/Post and rollback without replacing the browser's
// token with the latest DB version. The latter must still reject a stale write.
func TestXDTOHTTPFormLifecycleMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, kind := range []string{"document", "catalog"} {
			for _, operation := range []string{"save", "rollback", "reject", "conflict", "post", "post_rollback"} {
				if kind == "catalog" && strings.HasPrefix(operation, "post") {
					continue
				}
				t.Run(kind+"/"+operation, func(t *testing.T) {
					before := "Объект.Записать();"
					switch operation {
					case "rollback":
						before = "НачатьТранзакцию(); Объект.Записать(); ОтменитьТранзакцию();"
					case "reject":
						before = `Объект.Отказать = Истина; Попытка Объект.Записать(); Исключение Сообщить("отказ"); КонецПопытки;`
					case "post", "post_rollback":
						before = `Док = Объект.Ссылка.ПолучитьОбъект(); Док.Провести();`
						if operation == "post_rollback" {
							before = "НачатьТранзакцию(); " + before + " ОтменитьТранзакцию();"
						}
					}
					_, entity, endpoint, _ := xdtoHTTPFixture(t, db, kind, before, "full")
					posted := kind == "document" && !strings.HasPrefix(operation, "post")
					id, version := xdtoHTTPSeed(t, db, entity, posted, false)
					body := xdtoHTTPBody(id, version)
					if operation == "conflict" {
						body.Set("_version", strconv.FormatInt(version-1, 10))
					}
					result, status := xdtoHTTPPost(t, endpoint, body)
					if operation == "conflict" {
						if result.OK || result.Error == "" {
							t.Fatalf("stale write accepted: %d %+v", status, result)
						}
					} else {
						if status != http.StatusOK || !result.OK || len(result.Messages) < 2 {
							t.Fatalf("lifecycle: %d %+v", status, result)
						}
						xml := result.Messages[len(result.Messages)-2]
						wantPosted := posted && operation != "save" || operation == "post"
						if !strings.Contains(xml, fmt.Sprintf("<DeletionMark>%t</DeletionMark>", false)) || kind == "document" && !strings.Contains(xml, fmt.Sprintf("<Posted>%t</Posted>", wantPosted)) {
							t.Fatal(xml)
						}
						if xml != result.Messages[len(result.Messages)-1] {
							t.Fatal("round-trip", result.Messages)
						}
					}
					row, err := db.GetByID(t.Context(), entity.Name, id, entity)
					if err != nil {
						t.Fatal(err)
					}
					wantName := "stored"
					if operation == "save" {
						wantName = "unsaved"
					}
					if row["Наименование"] != wantName {
						t.Fatalf("persisted name: %v", row)
					}
					wantVersion := version
					if operation == "save" || operation == "post" {
						wantVersion++
					}
					if persistedEntityVersion(row) != wantVersion {
						t.Fatalf("version %v, want %d", row, wantVersion)
					}
				})
			}
		}
	})
}

func TestXDTOHTTPFormReadAccessMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, kind := range []string{"document", "catalog"} {
			for _, late := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/late=%t", kind, late), func(t *testing.T) {
					before := ""
					if late {
						before = `Другой = Объект.Ссылка.ПолучитьОбъект(); Другой.Наименование = "secret"; Другой.Записать();`
					}
					_, entity, endpoint, user := xdtoHTTPFixture(t, db, kind, before, "full")
					id, version := xdtoHTTPSeed(t, db, entity, true, true)
					pred := auth.RowPolicies{"read": {Field: "Наименование", Op: "eq", Value: auth.RowValue{Literal: "stored"}}}
					if !late {
						pred["read"] = auth.RowPolicy{Field: "Наименование", Op: "eq", Value: auth.RowValue{Literal: "other"}}
					}
					if kind == "document" {
						user.Roles[0].Permissions.RowAccess.Documents = map[string]auth.RowPolicies{entity.Name: pred}
					} else {
						user.Roles[0].Permissions.RowAccess.Catalogs = map[string]auth.RowPolicies{entity.Name: pred}
					}
					result, status := xdtoHTTPPost(t, endpoint, xdtoHTTPBody(id, version))
					if result.OK || result.Error == "" || !late && status != http.StatusForbidden {
						t.Fatalf("RLS accepted: %d %+v", status, result)
					}
					if late {
						row, err := db.GetByID(t.Context(), entity.Name, id, entity)
						if err != nil || row["Наименование"] != "secret" || !strings.Contains(result.Error, "доступ запрещён") {
							t.Fatalf("late RLS was not reached: %v %v %+v", row, err, result)
						}
					}
					if len(result.Messages) != 0 {
						t.Fatalf("RLS disclosed XML: %q", result.Messages)
					}
				})
			}
		}
	})
}

func TestXDTOHTTPNewFormLifecycleMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, kind := range []string{"document", "catalog"} {
			for _, operation := range []string{"save", "rollback", "post", "post_rollback"} {
				if kind == "catalog" && strings.HasPrefix(operation, "post") {
					continue
				}
				t.Run(kind+"/"+operation, func(t *testing.T) {
					before := "Объект.Записать();"
					posting := strings.HasPrefix(operation, "post")
					rollback := strings.Contains(operation, "rollback")
					if posting {
						before += ` Док = Объект.Ссылка.ПолучитьОбъект(); Док.Провести();`
					}
					if rollback {
						before = "НачатьТранзакцию(); " + before + "\n" + xdtoHTTPExport + "\nОтменитьТранзакцию();"
					}
					_, entity, endpoint, _ := xdtoHTTPFixture(t, db, kind, before, "full")
					result, status := xdtoHTTPPost(t, endpoint, xdtoHTTPBody(uuid.Nil, 0))
					wantMessages := 2
					if rollback {
						wantMessages = 4
					}
					if status != http.StatusOK || !result.OK || len(result.Messages) != wantMessages {
						t.Fatalf("new lifecycle: %d %+v", status, result)
					}
					var ref string
					for i, text := range result.Messages {
						start := strings.Index(text, "<Ref>") + len("<Ref>")
						end := strings.Index(text, "</Ref>")
						if start < len("<Ref>") || end < start {
							t.Fatal(text)
						}
						if ref != "" && ref != text[start:end] {
							t.Fatal("Ref changed after rollback", result.Messages)
						}
						ref = text[start:end]
						wantPosted := posting && (!rollback || i < 2)
						if !strings.Contains(text, "<DeletionMark>false</DeletionMark>") || kind == "document" && !strings.Contains(text, fmt.Sprintf("<Posted>%t</Posted>", wantPosted)) {
							t.Fatal(text)
						}
					}
					id := uuid.MustParse(ref)
					version, exists, err := db.EntityVersionExists(t.Context(), entity.Name, id)
					if err != nil || exists == rollback {
						t.Fatalf("durable identity: %d %t %v", version, exists, err)
					}
					if !rollback {
						expected := int64(1)
						if posting {
							expected = 2
						}
						if version != expected || result.SavedID != ref {
							t.Fatalf("saved state: %d %+v", version, result)
						}
					} else if result.SavedID != "" || result.Version != 0 {
						t.Fatalf("rollback exposed durable write: %+v", result)
					}
				})
			}
		}
	})
}
