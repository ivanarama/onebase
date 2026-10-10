package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/webhook"
)

// The fixture uses the real server/DSL environment. Child records have an FK
// to the parent, so each hook proves that its DB effects share the save scope.
func writePostServer(t *testing.T, db *storage.DB, failure string) (*Server, *metadata.Entity) {
	t.Helper()
	doc := &metadata.Entity{Name: "Заказ", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Ввод", Type: metadata.FieldTypeString},
			{Name: "Расчет", Type: metadata.FieldTypeString},
			{Name: "Прочитано", Type: metadata.FieldTypeString},
			{Name: "След", Type: metadata.FieldTypeString},
			{Name: "FormTrace", Type: metadata.FieldTypeString},
			{Name: "Обязательный", Type: metadata.FieldTypeString, Required: true},
		},
		ItemForm:   []metadata.ItemFormField{{Name: "Номер"}, {Name: "Ввод"}, {Name: "Расчет", ReadOnly: true}, {Name: "Обязательный"}, {Name: "tp.Товары.Item"}},
		TableParts: []metadata.TablePart{{Name: "Товары", Fields: []metadata.Field{{Name: "Item", Type: metadata.FieldTypeString, Required: true}}}},
	}
	child := &metadata.Entity{Name: "СледХука", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Владелец", Type: "reference:Заказ", RefEntity: "Заказ"},
		{Name: "Метка", Type: metadata.FieldTypeString},
	}}
	reg := &metadata.Register{Name: "ДвиженияТеста", Dimensions: []metadata.Field{{Name: "Item", Type: metadata.FieldTypeString}}, Resources: []metadata.Field{{Name: "Quantity", Type: metadata.FieldTypeNumber}}}
	ctx := context.Background()
	if err := db.Migrate(ctx, []*metadata.Entity{doc, child}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAuditSettings(ctx, storage.AuditSettings{Enabled: true, Create: true, Update: true, Post: true}); err != nil {
		t.Fatal(err)
	}
	failW, failP := "", ""
	if failure == "managed" {
		failW = `Если ЭтотОбъект.FormTrace <> "BA" Тогда ВызватьИсключение "form events skipped or doubled"; КонецЕсли;`
	}
	if failure == "W" {
		failW = `ВызватьИсключение "отказ W";`
	}
	if failure == "P" {
		failP = `ВызватьИсключение "отказ P";`
	}
	if failure == "conditional" {
		failP = `Если ЭтотОбъект.Ввод = "fail" Тогда ВызватьИсключение "retry"; КонецЕсли;`
	}
	if failure == "required" {
		failP = `ЭтотОбъект.Обязательный = "";`
	}
	source := fmt.Sprintf(`
Процедура ПриЗаписи()
 ЭтотОбъект.Расчет = ЭтотОбъект.Ввод + "-W";
 ЭтотОбъект.След = "W";
 Для Каждого Стр Из ЭтотОбъект.Товары Цикл
  Стр.Item = Стр.Item + "-W";
 КонецЦикла;
 Потомок = Справочники.СледХука.Создать();
 Потомок.Владелец = ЭтотОбъект.Ссылка;
 Потомок.Метка = "W";
 Потомок.Записать();
 Дв = Движения.ДвиженияТеста.Добавить();
 Дв.ВидДвижения = "Приход";
 Дв.Item = "ignored-write-collector";
 Дв.Quantity = 100;
 Сообщить("W");
 %s
КонецПроцедуры
Процедура ОбработкаПроведения()
 Если ЭтотОбъект.След <> "W" Тогда
  ВызватьИсключение "OnWrite skipped";
 КонецЕсли;
 Зап = Новый Запрос;
 Зап.Текст = "ВЫБРАТЬ Д.Расчет КАК Значение ИЗ Документ.Заказ КАК Д ГДЕ Д.Ссылка = &ИД";
 Зап.УстановитьПараметр("ИД", ЭтотОбъект.Ссылка);
 Выборка = Зап.Выполнить();
 Если Выборка.Количество() <> 1 Тогда ВызватьИсключение "нет parent prelude"; КонецЕсли;
 ЭтотОбъект.Прочитано = Выборка[0].Значение;
 Зап.Текст = "ВЫБРАТЬ Т.Item КАК Значение ИЗ Заказ_Товары КАК Т ГДЕ Т.parent_id = &ИД";
 Выборка = Зап.Выполнить();
 Если Выборка.Количество() > 0 Тогда
  ЭтотОбъект.Прочитано = ЭтотОбъект.Прочитано + "/" + Выборка[0].Значение;
 КонецЕсли;
 ЭтотОбъект.След = ЭтотОбъект.След + "P";
 ЭтотОбъект.Обязательный = "final";
 Потомок = Справочники.СледХука.Создать();
 Потомок.Владелец = ЭтотОбъект.Ссылка;
 Потомок.Метка = "P";
 Потомок.Записать();
 Дв = Движения.ДвиженияТеста.Добавить();
 Дв.ВидДвижения = "Приход";
 Дв.Item = "post-collector";
 Дв.Quantity = 2;
 Сообщить("P");
 %s
КонецПроцедуры`, failW, failP)
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{doc, child}, Registers: []*metadata.Register{reg}, Programs: map[string]*ast.Program{doc.Name: mustParse(t, source)}})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := New(registry, db, interp, nil, Config{}, nil)
	t.Cleanup(s.Close)
	return s, doc
}

func seedWritePost(t *testing.T, db *storage.DB, doc *metadata.Entity) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	if err := db.Upsert(ctx, doc.Name, id, map[string]any{"Номер": id.String(), "Ввод": "old", "Расчет": "old-calc", "Прочитано": "old-read", "След": "old-trace", "Обязательный": "old-required"}, doc); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTablePartRows(ctx, doc.Name, "Товары", id, []map[string]any{{"Item": "old-line"}}, doc.TableParts[0]); err != nil {
		t.Fatal(err)
	}
	return id
}

func checkWritePost(t *testing.T, db *storage.DB, doc *metadata.Entity, id uuid.UUID, version int64) {
	t.Helper()
	ctx := context.Background()
	row, err := db.GetByID(ctx, doc.Name, id, doc)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"Расчет": "input-W", "Прочитано": "input-W/line-W", "След": "WP", "Обязательный": "final"} {
		if got := fmt.Sprint(row[key]); got != want {
			t.Errorf("%s=%q, want %q", key, got, want)
		}
	}
	if row["posted"] != true {
		t.Errorf("posted=%v", row["posted"])
	}
	v, err := db.EntityVersion(ctx, doc.Name, id)
	if err != nil || v != version {
		t.Errorf("version=%d err=%v, want %d", v, err, version)
	}
	rows, err := db.GetTablePartRows(ctx, doc.Name, "Товары", id, doc.TableParts[0])
	if err != nil || len(rows) != 1 || rows[0]["Item"] != "line-W" {
		t.Errorf("table part=%v err=%v", rows, err)
	}
	var children int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM следхука WHERE владелец_id="+db.Dialect().Placeholder(1), id.String()).Scan(&children); err != nil {
		t.Fatal(err)
	}
	if children != 2 {
		t.Errorf("hook child writes=%d, want one W and one P", children)
	}
	var movements int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM рег_движениятеста WHERE recorder="+db.Dialect().Placeholder(1), id.String()).Scan(&movements); err != nil {
		t.Fatal(err)
	}
	if movements != 1 {
		t.Errorf("movements=%d, want only OnPost collector", movements)
	}
}

func TestWritePost_FormHTTPMatrix(t *testing.T) {
	for _, action := range []string{"post", "post_and_close"} {
		for _, isNew := range []bool{false, true} {
			for _, failure := range []string{"", "W", "P", "required"} {
				t.Run(fmt.Sprintf("%s/new=%v/failure=%s", action, isNew, failure), func(t *testing.T) {
					dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
						s, doc := writePostServer(t, db, failure)
						ctx := context.Background()
						id := uuid.Nil
						path := "/ui/document/Заказ/new"
						if !isNew {
							id = seedWritePost(t, db, doc)
							path = "/ui/document/Заказ/" + id.String()
						}
						body := url.Values{"_action": {action}, "_version": {"1"}, "Номер": {"form-number"}, "Ввод": {"input"}, "tp_json.Товары": {`[{"Item":"line"}]`}}
						req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body.Encode()))
						req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
						w := httptest.NewRecorder()
						r := chi.NewRouter()
						s.Mount(r)
						r.ServeHTTP(w, req)
						if failure == "" {
							if w.Code != http.StatusSeeOther {
								t.Fatalf("HTTP %d: %s", w.Code, regexp.MustCompile(`(?s)<div class="error">.*?</div>`).FindString(w.Body.String()))
							}
							if isNew {
								rows, err := db.List(ctx, doc.Name, doc, storage.ListParams{})
								if err != nil || len(rows) != 1 {
									t.Fatalf("new rows=%v err=%v", rows, err)
								}
								id = uuid.MustParse(fmt.Sprint(rows[0]["id"]))
							}
							version := int64(2)
							if isNew {
								version = 1
							}
							checkWritePost(t, db, doc, id, version)
						} else {
							if w.Code >= 500 {
								t.Fatalf("rejection became server error: %d %s", w.Code, w.Body.String())
							}
							if isNew {
								rows, err := db.List(ctx, doc.Name, doc, storage.ListParams{})
								if err != nil || len(rows) != 0 {
									t.Fatalf("rejected new parent survived: %v %v", rows, err)
								}
							} else {
								row, err := db.GetByID(ctx, doc.Name, id, doc)
								if err != nil {
									t.Fatal(err)
								}
								v, err := db.EntityVersion(ctx, doc.Name, id)
								if err != nil || v != 1 || row["Ввод"] != "old" || row["Расчет"] != "old-calc" || row["posted"] != false {
									t.Fatalf("rollback row=%v version=%d err=%v", row, v, err)
								}
								lines, err := db.GetTablePartRows(ctx, doc.Name, "Товары", id, doc.TableParts[0])
								if err != nil || len(lines) != 1 || lines[0]["Item"] != "old-line" {
									t.Fatalf("rollback TP=%v err=%v", lines, err)
								}
							}
							for _, table := range []string{"следхука", "рег_движениятеста"} {
								var count int
								if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
									t.Fatalf("rollback %s=%d err=%v", table, count, err)
								}
							}
						}
					})
				})
			}
		}
	}
}

func runWritePostProcessor(t *testing.T, s *Server, ctx context.Context, source string) ([]string, error) {
	t.Helper()
	proc := &processor.Processor{Name: "ПроверкаЗаписи", Trusted: true}
	s.reg.SetExternalProcessors([]*processor.Processor{proc}, map[string]*ast.Program{proc.Name: mustParse(t, source)})
	messages, runErr, err := s.RunProcessor(ctx, s.reg, proc.Name, nil, nil, nil)
	if err != nil {
		return messages, err
	}
	return messages, runErr
}

func TestWritePost_DSLProcessorMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		s, doc := writePostServer(t, db, "")
		messages, err := runWritePostProcessor(t, s, context.Background(), `Процедура Выполнить()
 Д = Документы.Заказ.Создать();
 Д.Номер = "dsl-number";
 Д.Ввод = "input";
 Стр = Д.Товары.Добавить(); Стр.Item = "line";
 Д.Провести();
КонецПроцедуры`)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(messages, ",") != "W,P" {
			t.Errorf("hook messages=%v", messages)
		}
		rows, err := db.List(context.Background(), doc.Name, doc, storage.ListParams{})
		if err != nil || len(rows) != 1 {
			t.Fatalf("rows=%v err=%v", rows, err)
		}
		checkWritePost(t, db, doc, uuid.MustParse(fmt.Sprint(rows[0]["id"])), 1)
	})
}

func TestWritePost_DeliveryAndOuterRollbackMatrix(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("commit=%v", commit), func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				s, doc := writePostServer(t, db, "")
				doc.NotifyChanges = true
				_, events, cancel := s.hub.Subscribe("u1", "ivan", nil)
				defer cancel()
				hits := make(chan string, 8)
				sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					b, _ := io.ReadAll(r.Body)
					hits <- string(b)
					w.WriteHeader(http.StatusOK)
				}))
				defer sink.Close()
				dispatcher := webhook.New([]webhook.Config{
					{Name: "post", On: "document.post", URL: sink.URL, Body: `post:{{расчет}}:{{след}}`},
					{Name: "save", On: "document.save", URL: sink.URL, Body: `unexpected save`},
				}, nil)
				defer func() { _ = dispatcher.Close(context.Background()) }()
				s.cfg.Webhooks = dispatcher
				s.entitySvc.Hooks = dispatcher
				observed := 0
				ctx := entityservice.ContextWithSaveObserver(context.Background(), func(e *metadata.Entity, res entityservice.SaveResult) {
					if e == doc {
						observed++
						if res.Version != 1 {
							t.Errorf("observer version=%d", res.Version)
						}
					}
				})
				abort := errors.New("outer rollback")
				err := db.WithTxScope(ctx, func(tx context.Context) error {
					_, err := runWritePostProcessor(t, s, tx, `Процедура Выполнить()
 Д = Документы.Заказ.Создать(); Д.Номер="delivery"; Д.Ввод="input";
 Стр=Д.Товары.Добавить(); Стр.Item="line";
 Д.Провести();
 Д.Расчет="changed after post";
КонецПроцедуры`)
					if err != nil {
						return err
					}
					if observed != 0 || dispatcher.Metrics().Dispatched != 0 {
						t.Fatal("delivery happened before outer commit")
					}
					select {
					case ev := <-events:
						t.Fatalf("live event before commit: %v", ev)
					default:
					}
					if !commit {
						return abort
					}
					return nil
				})
				if commit && err != nil || !commit && !errors.Is(err, abort) {
					t.Fatalf("outer result=%v", err)
				}
				dispatcher.Wait()
				if commit {
					if observed != 1 {
						t.Errorf("document observer calls=%d, want one", observed)
					}
					select {
					case body := <-hits:
						if body != "post:input-W:WP" {
							t.Errorf("final webhook=%q", body)
						}
					default:
						t.Fatal("missing post webhook")
					}
					select {
					case body := <-hits:
						t.Fatalf("extra webhook=%q", body)
					default:
					}
					select {
					case ev := <-events:
						if !strings.Contains(fmt.Sprint(ev), "проведён") {
							t.Fatalf("final event=%v", ev)
						}
					case <-time.After(2 * time.Second):
						t.Fatal("missing final live event")
					}
					select {
					case ev := <-events:
						t.Fatalf("extra live event=%v", ev)
					default:
					}
				} else {
					if observed != 0 || dispatcher.Metrics().Dispatched != 0 {
						t.Fatal("rollback delivered a notification")
					}
					rows, err := db.List(context.Background(), doc.Name, doc, storage.ListParams{})
					if err != nil || len(rows) != 0 {
						t.Fatalf("outer rollback rows=%v err=%v", rows, err)
					}
				}
			})
		})
	}
}

func TestWritePost_ManagedFormMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		s, doc := writePostServer(t, db, "managed")
		form := managedObjectForm(fieldEl("Number", "Object.Номер"), fieldEl("Input", "Object.Ввод"),
			&metadata.FormElement{Kind: metadata.FormElementTablePart, Name: "Lines", DataPath: "Object.Товары"})
		form.EntityName = doc.Name
		form.Handlers = map[metadata.FormEventType]string{metadata.FormEventBeforeWrite: "Before", metadata.FormEventOnWrite: "After"}
		form.ProgramAST = mustParse(t, `
Процедура Before()
 Если Объект.Ввод <> "input" Тогда ВызватьИсключение "bad form input"; КонецЕсли;
 Объект.FormTrace = "B";
КонецПроцедуры
Процедура After()
 Если Объект.FormTrace <> "B" Тогда ВызватьИсключение "bad form write"; КонецЕсли;
 Объект.FormTrace = Объект.FormTrace + "A";
КонецПроцедуры`)
		doc.Forms = []*metadata.FormModule{form}
		id := seedWritePost(t, db, doc)
		body := url.Values{"_action": {"post"}, "_version": {"1"}, "Номер": {"managed"}, "Ввод": {"input"}, "tp_json.Товары": {`[{"Item":"line"}]`}}
		req := httptest.NewRequest(http.MethodPost, "/ui/document/Заказ/"+id.String(), strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r := chi.NewRouter()
		s.Mount(r)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("managed HTTP %d: %s", rec.Code, regexp.MustCompile(`(?s)<div class="error">.*?</div>`).FindString(rec.Body.String()))
		}
		checkWritePost(t, db, doc, id, 2)
	})
}

func TestWritePost_StaleVersionBeforeHooksMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		s, doc := writePostServer(t, db, "")
		id := seedWritePost(t, db, doc)
		stale := int64(0)
		_, err := s.entityService().Save(context.Background(), entityservice.SaveRequest{Entity: doc, ID: id, Action: "post", ExpectedVersion: &stale, Fields: map[string]any{"Ввод": "input"}})
		if !errors.Is(err, storage.ErrVersionConflict) {
			t.Fatalf("stale token: %v", err)
		}
		row, _ := db.GetByID(context.Background(), doc.Name, id, doc)
		var children int
		if err := db.QueryRow(context.Background(), "SELECT COUNT(*) FROM "+metadata.TableName("СледХука")).Scan(&children); err != nil {
			t.Fatal(err)
		}
		if children != 0 || row["Расчет"] != "old-calc" {
			t.Fatalf("stale request ran hooks: %v children=%d", row, children)
		}
	})
}

func TestWritePost_PostOnlyMatrix(t *testing.T) {
	for _, path := range []string{"list", "reference"} {
		t.Run(path, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				s, doc := writePostServer(t, db, "")
				s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{doc}, Programs: map[string]*ast.Program{doc.Name: mustParse(t, `
Процедура ПриЗаписи()
 ВызватьИсключение "post-only must not write";
КонецПроцедуры
Процедура ОбработкаПроведения()
 ЭтотОбъект.След = "P-only";
КонецПроцедуры`)}})
				id := seedWritePost(t, db, doc)
				if path == "list" {
					req := httptest.NewRequest(http.MethodPost, "/ui/document/Заказ/"+id.String()+"/post", nil)
					r := chi.NewRouter()
					s.Mount(r)
					w := httptest.NewRecorder()
					r.ServeHTTP(w, req)
					if w.Code != http.StatusSeeOther {
						t.Fatalf("list HTTP %d", w.Code)
					}
				} else {
					_, err := runWritePostProcessor(t, s, context.Background(), fmt.Sprintf(`Процедура Выполнить() Экспорт
 С = Документы.Заказ.НайтиПоНомеру("%s"); Документы.Заказ.Провести(С);
КонецПроцедуры`, id.String()))
					if err != nil {
						t.Fatal(err)
					}
				}
				row, err := db.GetByID(context.Background(), doc.Name, id, doc)
				if err != nil || row["След"] != "P-only" || row["Расчет"] != "old-calc" || row["posted"] != true {
					t.Fatalf("post-only row=%v err=%v", row, err)
				}
			})
		})
	}
}

func TestWritePost_NilVersionConcurrentCASPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	schema := storage.NewEphemeralSchemaName()
	a, err := storage.ConnectWithSchema(ctx, dsn, schema)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CreateSchema(ctx, schema); err != nil {
		a.Close()
		t.Fatal(err)
	}
	b, err := storage.ConnectWithSchema(ctx, dsn, schema)
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		b.Close()
		if err := a.DropSchemaCascade(context.Background(), schema); err != nil {
			t.Error(err)
		}
		a.Close()
	})
	if err := a.EnsureServiceSchema(ctx); err != nil {
		t.Fatal(err)
	}
	s, doc := writePostServer(t, a, "")
	id := seedWritePost(t, a, doc)
	_, err = s.entityService().Save(ctx, entityservice.SaveRequest{Entity: doc, ID: id, Action: "post", Fields: map[string]any{"Ввод": "input"},
		Preflight: func(context.Context, *runtime.Object) error {
			return b.Upsert(ctx, doc.Name, id, map[string]any{"Расчет": "concurrent"}, doc)
		},
	})
	if !errors.Is(err, storage.ErrVersionConflict) {
		t.Fatalf("nil token CAS race=%v", err)
	}
	row, err := b.GetByID(ctx, doc.Name, id, doc)
	if err != nil || row["Расчет"] != "concurrent" || row["posted"] != false {
		t.Fatalf("lost update: %v %v", row, err)
	}
	var children int
	if err := b.QueryRow(ctx, "SELECT COUNT(*) FROM "+metadata.TableName("СледХука")).Scan(&children); err != nil {
		t.Fatal(err)
	}
	if children != 0 {
		t.Fatalf("OnWrite side effects survived failed CAS: %d", children)
	}
}

func TestWritePost_CaughtFailureRetryMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		s, doc := writePostServer(t, db, "conditional")
		_, err := runWritePostProcessor(t, s, context.Background(), `
Процедура Выполнить() Экспорт
 НачатьТранзакцию();
 Ранее=Справочники.СледХука.Создать(); Ранее.Метка="before"; Ранее.Записать();
 Д=Документы.Заказ.Создать(); Д.Ввод="fail";
 Стр=Д.Товары.Добавить(); Стр.Item="line";
 Попытка
  Д.Провести();
 Исключение
  Д.Ввод="input"; Стр.Item="line";
 КонецПопытки;
 Д.Провести();
 ЗафиксироватьТранзакцию();
КонецПроцедуры`)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := db.List(context.Background(), doc.Name, doc, storage.ListParams{})
		if err != nil || len(rows) != 1 {
			t.Fatalf("retry rows=%v %v", rows, err)
		}
		id := uuid.MustParse(fmt.Sprint(rows[0]["id"]))
		row, err := db.GetByID(context.Background(), doc.Name, id, doc)
		if err != nil || row["posted"] != true || row["Расчет"] != "input-W" {
			t.Fatalf("retry row=%v %v", row, err)
		}
		v, err := db.EntityVersion(context.Background(), doc.Name, id)
		if err != nil || v != 1 {
			t.Fatalf("retry version=%d %v", v, err)
		}
		var children int
		if err := db.QueryRow(context.Background(), "SELECT COUNT(*) FROM "+metadata.TableName("СледХука")).Scan(&children); err != nil {
			t.Fatal(err)
		}
		if children != 3 {
			t.Fatalf("caught failure lost preceding write or kept failed children: %d", children)
		}
	})
}

func TestWritePost_FinalPeriodMatrix(t *testing.T) {
	for _, changeDate := range []bool{false, true} {
		t.Run(fmt.Sprintf("hookDate=%v", changeDate), func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				s, doc := writePostServer(t, db, "")
				doc.Fields = append(doc.Fields, metadata.Field{Name: "Дата", Type: metadata.FieldTypeDate})
				ctx := context.Background()
				if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
					t.Fatal(err)
				}
				src := `Процедура ПриЗаписи() КонецПроцедуры
Процедура ОбработкаПроведения() КонецПроцедуры`
				if changeDate {
					src = `Процедура ПриЗаписи() КонецПроцедуры
Процедура ОбработкаПроведения() ЭтотОбъект.Дата=Дата(2026,10,10); КонецПроцедуры`
				}
				s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{doc}, Programs: map[string]*ast.Program{doc.Name: mustParse(t, src)}})
				id := seedWritePost(t, db, doc)
				oldDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
				if err := db.Upsert(ctx, doc.Name, id, map[string]any{"Дата": oldDate}, doc); err != nil {
					t.Fatal(err)
				}
				if err := db.SavePostingLockDate(ctx, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)); err != nil {
					t.Fatal(err)
				}
				result, err := s.entityService().Save(ctx, entityservice.SaveRequest{Entity: doc, ID: id, Action: "post", Fields: map[string]any{"Ввод": "partial"}})
				if err != nil {
					t.Fatal(err)
				}
				row, err := db.GetByID(ctx, doc.Name, id, doc)
				if err != nil {
					t.Fatal(err)
				}
				if changeDate {
					if result.DSLError != "" || row["posted"] != true || result.Movements.Period == nil || result.Movements.Period.Year() != 2026 || result.Movements.Period.Month() != 10 {
						t.Fatalf("final period: %v row=%v", result, row)
					}
				} else if result.DSLError == "" || row["posted"] != false {
					t.Fatalf("omitted locked date escaped guard: %v row=%v", result, row)
				}
			})
		})
	}
}
