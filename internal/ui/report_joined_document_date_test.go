package ui

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestReportJoinedDocumentPeriodMatrix(t *testing.T) {
	previous := time.Local
	time.Local = time.FixedZone("Europe/Moscow", 3*60*60)
	t.Cleanup(func() { time.Local = previous })
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		tp := metadata.TablePart{Name: "Вопросы", Fields: []metadata.Field{{Name: "Вопрос", Type: metadata.FieldTypeString}}}
		doc := &metadata.Entity{Name: "JoinedPeriod", Kind: metadata.KindDocument, Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}, {Name: "Метка", Type: metadata.FieldTypeString}}, TableParts: []metadata.TablePart{tp}}
		if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
			t.Fatal(err)
		}
		for _, point := range []struct {
			name   string
			moment time.Time
		}{
			{"BEFORE_BOUNDARY", time.Date(2041, 5, 31, 23, 59, 59, 0, time.Local)},
			{"AT_START", time.Date(2041, 6, 1, 0, 0, 0, 0, time.Local)},
			{"AT_END", time.Date(2041, 6, 30, 23, 59, 59, 0, time.Local)},
			{"AFTER_BOUNDARY", time.Date(2041, 7, 1, 0, 0, 0, 0, time.Local)},
		} {
			id := uuid.New()
			if err := db.Upsert(ctx, doc.Name, id, map[string]any{"Дата": point.moment, "Метка": point.name}, doc); err != nil {
				t.Fatal(err)
			}
			if err := db.UpsertTablePartRows(ctx, doc.Name, tp.Name, id, []map[string]any{{"Вопрос": "yes"}, {"Вопрос": "duplicate"}}, tp); err != nil {
				t.Fatal(err)
			}
		}
		for _, source := range []struct{ name, text string }{
			{"table-part-first", "Документ.JoinedPeriod.Вопросы КАК Т ВНУТРЕННЕЕ СОЕДИНЕНИЕ Документ.JoinedPeriod КАК О ПО О.Ссылка = Т.Ссылка"},
			{"document-first", "Документ.JoinedPeriod КАК О ВНУТРЕННЕЕ СОЕДИНЕНИЕ Документ.JoinedPeriod.Вопросы КАК Т ПО О.Ссылка = Т.Ссылка"},
			{"direct", "Документ.JoinedPeriod КАК О"},
		} {
			for _, form := range []struct{ name, text string }{
				{"comparisons", "О.Дата >= &Начало И О.Дата <= &Конец"},
				{"reversed", "&Начало <= О.Дата И &Конец >= О.Дата"},
				{"between", "О.Дата МЕЖДУ &Начало И &Конец"},
			} {
				t.Run(source.name+"/"+form.name, func(t *testing.T) {
					rep := &report.Report{Name: "JoinedPeriodReport", Query: "ВЫБРАТЬ РАЗЛИЧНЫЕ О.Метка КАК Метка ИЗ " + source.text + " ГДЕ " + form.text + " УПОРЯДОЧИТЬ ПО Метка", Params: []report.Param{{Name: "Начало", Type: "date"}, {Name: "Конец", Type: "date", PeriodEnd: true}}}
					count := *rep
					count.Name = "JoinedPeriodCount"
					count.Query = "ВЫБРАТЬ КОЛИЧЕСТВО(РАЗЛИЧНЫЕ О.Ссылка) КАК Количество ИЗ " + source.text + " ГДЕ " + form.text
					reg := runtime.NewRegistry()
					reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{doc}, Reports: []*report.Report{rep, &count}})
					s := &Server{store: db, reg: reg, interp: interpreter.New(), lockMgr: runtime.NewLockManager(), messages: NewMessageStore(), ops: newOperationLimiter()}
					t.Cleanup(s.Close)
					router := chi.NewRouter()
					router.Post("/ui/report/{name}", s.reportRun)
					values := url.Values{"Начало": {"2041-06-01"}, "Конец": {"2041-06-30"}}
					request := func(name string) *httptest.ResponseRecorder {
						w := httptest.NewRecorder()
						req := httptest.NewRequest("POST", "/ui/report/"+name, strings.NewReader(values.Encode()))
						req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
						router.ServeHTTP(w, req)
						return w
					}
					w := request(rep.Name)
					if w.Code != 200 {
						t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
					}
					body := w.Body.String()
					for _, want := range []string{"AT_START", "AT_END"} {
						if !strings.Contains(body, ">"+want+"</td>") {
							t.Fatalf("missing boundary %s: %s", want, body)
						}
					}
					for _, absent := range []string{"BEFORE_BOUNDARY", "AFTER_BOUNDARY"} {
						if strings.Contains(body, ">"+absent+"</td>") {
							t.Fatalf("outside boundary %s included", absent)
						}
					}
					counted := request(count.Name)
					if counted.Code != 200 || !strings.Contains(counted.Body.String(), ">2</td>") {
						t.Fatalf("distinct HTTP count: %d %s", counted.Code, counted.Body.String())
					}
				})
			}
		}
	})
}
