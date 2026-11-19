package ui

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/xuri/excelize/v2"
)

func TestReportPhoneComparisonHTTP(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		doc := &metadata.Entity{Name: "ReportPhoneCall", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Phone", Type: metadata.FieldTypeString, PII: true},
			{Name: "Branch", Type: metadata.FieldTypeString},
		}}
		s := newSubmitTestServerOn(t, db, []*metadata.Entity{doc})
		s.ops = newOperationLimiter()
		const first, second, repeat, hidden = "79120000001", "79120000002", "9120000001", "79120000003"
		ids := make(map[string]uuid.UUID)
		for _, number := range []string{first, second, repeat, hidden} {
			id := uuid.New()
			ids[number] = id
			branch := "visible"
			if number == hidden {
				branch = "hidden"
			}
			if err := db.Upsert(context.Background(), doc.Name, id, map[string]any{"Phone": number, "Branch": branch}, doc); err != nil {
				t.Fatal(err)
			}
		}
		const field = "ReportPhoneCall.Phone"
		for _, tc := range []struct {
			name, optIn, query                      string
			external, hidden, noRead, wantPublished bool
		}{
			{name: "queryless", optIn: field, wantPublished: true},
			{name: "with-query", optIn: field, query: "ВЫБРАТЬ 1 КАК Marker", wantPublished: true},
			{name: "no-opt-in"},
			{name: "external", optIn: field, external: true},
			{name: "ordinary-field", optIn: "ReportPhoneCall.Branch"},
			{name: "hidden-row", optIn: field, hidden: true},
			{name: "no-document-read", optIn: field, noRead: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				last := repeat
				if tc.hidden {
					last = hidden
				}
				rep := &reportpkg.Report{Name: "ReportPhoneTotals", ResultProc: "Build", Query: tc.query, ComparePhoneField: tc.optIn, External: tc.external}
				program := mustParse(t, fmt.Sprintf(`Функция Build()
    Ссылки = Новый Массив;
    Ссылки.Добавить("%[1]s");
    Ссылки.Добавить("%[2]s");
    Ссылки.Добавить("%[3]s");
    Ключи = СопоставлениеТелефонов.КлючиДокументов(Ссылки);
    Первый = Ключи.Получить("%[1]s");
    Другой = Ключи.Получить("%[2]s");
    Если Первый = Другой ИЛИ Первый <> Ключи.Получить("%[3]s") ИЛИ Первый <> СопоставлениеТелефонов.Ключ("9120000001") Тогда
        ВызватьИсключение "unexpected comparison";
    КонецЕсли;
    З = Новый Запрос;
    З.Текст = "ВЫБРАТЬ Phone ИЗ Документ.ReportPhoneCall";
    Для Каждого Стр Из З.Выполнить() Цикл
        Если Стр.Phone = "%[4]s" ИЛИ Стр.Phone = "%[5]s" ИЛИ Стр.Phone = "%[6]s" Тогда
            ВызватьИсключение "unmasked query";
        КонецЕсли;
    КонецЦикла;
    Д = Новый ТабличныйДокумент;
    Д.Ячейка(1,1).Текст = "3 calls, 2 distinct";
    Возврат Д;
КонецФункции`, ids[first], ids[second], ids[last], first, second, repeat))
				s.reg.Load(runtime.LoadOptions{
					Entities: []*metadata.Entity{doc},
					Reports:  []*reportpkg.Report{rep},
					Programs: map[string]*ast.Program{rep.Name: program},
				})
				perms := auth.Permission{
					Reports:     map[string][]string{rep.Name: {"run"}},
					Documents:   map[string][]string{doc.Name: {"read"}},
					FieldAccess: auth.FieldAccess{Documents: map[string]auth.FieldPolicies{doc.Name: {"Phone": {Read: "mask_all"}}}},
					RowAccess: auth.RowAccess{Documents: map[string]auth.RowPolicies{
						doc.Name: {"read": {Field: "Branch", Op: "eq", Value: auth.RowValue{Literal: "visible"}}},
					}},
				}
				if tc.noRead {
					perms.Documents = nil
				}
				actor := &auth.User{ID: "report-export", Login: "report-export", Roles: []*auth.Role{{Name: "Export", Permissions: perms}}}
				const session = "report-comparison-session"
				run := tabularHTTP(s, actor, session, http.MethodPost, "/ui/report/"+rep.Name, url.Values{"compare_phone_field": {field}})
				if !tc.wantPublished {
					if run.Code != http.StatusInternalServerError || run.Header().Get("Location") != "" {
						t.Fatalf("comparison should be unavailable: HTTP %d, %s", run.Code, run.Body)
					}
					return
				}
				path := run.Header().Get("Location")
				if run.Code != http.StatusSeeOther || !strings.HasPrefix(path, "/ui/tabular/") {
					t.Fatalf("report did not publish: HTTP %d, %s", run.Code, run.Body)
				}
				const total = "3 calls, 2 distinct"
				view := tabularHTTP(s, actor, session, http.MethodGet, path, nil)
				if view.Code != http.StatusOK || !strings.Contains(view.Body.String(), total) {
					t.Fatalf("viewer did not render totals: HTTP %d", view.Code)
				}
				xlsx := tabularHTTP(s, actor, session, http.MethodGet, path+"/xlsx", nil)
				if xlsx.Code != http.StatusOK {
					t.Fatalf("xlsx HTTP %d", xlsx.Code)
				}
				book, err := excelize.OpenReader(bytes.NewReader(xlsx.Body.Bytes()))
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := book.Close(); err != nil {
						t.Error(err)
					}
				}()
				rows, err := book.GetRows(book.GetSheetName(0))
				if err != nil || len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] != total {
					t.Fatalf("unexpected worksheet content: %v, %v", rows, err)
				}
			})
		}
	})
}
