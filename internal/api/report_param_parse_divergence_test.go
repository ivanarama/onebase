package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
)

// Вторая половина намеренного расхождения (первая — в internal/ui): экран
// оставляет негодную дату строкой и строит отчёт, а API отвечает 400. Клиент
// API должен узнать, что параметр негоден, а не получить молча отчёт по строке.
func TestAPIV2_ReportRejectsInvalidDateParam(t *testing.T) {
	cat := &metadata.Entity{
		Name: "КлиентАПИ",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	rep := &reportpkg.Report{
		Name:   "КлиентыНаДатуАПИ",
		Params: []reportpkg.Param{{Name: "НаДату", Type: "date"}, {Name: "Только", Type: "bool"}},
		Query:  `ВЫБРАТЬ Наименование ИЗ Справочник.КлиентАПИ ГДЕ Наименование >= &НаДату`,
	}
	h, _ := newAPITestHandlerWithReports(t, []*metadata.Entity{cat}, []*reportpkg.Report{rep}, nil)
	user := apiUser("api", auth.Permission{
		Catalogs: map[string][]string{cat.Name: {"read"}},
		Reports:  map[string][]string{rep.Name: {"run"}},
	})
	run := func(query string) *httptest.ResponseRecorder {
		req := withUser(reqWithEntity("GET", "/api/v2/report/"+rep.Name+query, nil,
			map[string]string{"name": rep.Name}, nil), user)
		rec := httptest.NewRecorder()
		h.runReportV2().ServeHTTP(rec, req)
		return rec
	}

	rec := run("?%D0%9D%D0%B0%D0%94%D0%B0%D1%82%D1%83=%D0%BC%D1%83%D1%81%D0%BE%D1%80")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("API ответил %d на негодную дату, ожидался 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid report parameter") {
		t.Fatalf("в ответе нет причины отказа: %s", rec.Body.String())
	}

	// Контроль: годная дата строится тем же путём.
	if rec := run("?%D0%9D%D0%B0%D0%94%D0%B0%D1%82%D1%83=2026-08-29"); rec.Code != http.StatusOK {
		t.Fatalf("API ответил %d на годную дату: %s", rec.Code, rec.Body.String())
	}
	// Пустой точный bool остаётся Ложью, а не nil — краевой случай сохранён.
	if rec := run("?%D0%A2%D0%BE%D0%BB%D1%8C%D0%BA%D0%BE="); rec.Code != http.StatusOK {
		t.Fatalf("API ответил %d на пустой bool: %s", rec.Code, rec.Body.String())
	}
}

// Параметр datetime в query-строке: значение со временем суток разбирается,
// негодное даёт 400 с внятным текстом (#1204, план 160).
func TestAPIV2_ReportParsesDatetimeParam(t *testing.T) {
	cat := &metadata.Entity{
		Name: "СобытиеАПИ",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Момент", Type: metadata.FieldTypeDate},
		},
	}
	rep := &reportpkg.Report{
		Name:   "СобытияАПИ",
		Params: []reportpkg.Param{{Name: "С", Type: "datetime"}},
		Query:  `ВЫБРАТЬ Наименование ИЗ Справочник.СобытиеАПИ ГДЕ Момент >= &С`,
	}
	h, _ := newAPITestHandlerWithReports(t, []*metadata.Entity{cat}, []*reportpkg.Report{rep}, nil)
	user := apiUser("api", auth.Permission{
		Catalogs: map[string][]string{cat.Name: {"read"}},
		Reports:  map[string][]string{rep.Name: {"run"}},
	})
	run := func(query string) *httptest.ResponseRecorder {
		req := withUser(reqWithEntity("GET", "/api/v2/report/"+rep.Name+query, nil,
			map[string]string{"name": rep.Name}, nil), user)
		rec := httptest.NewRecorder()
		h.runReportV2().ServeHTTP(rec, req)
		return rec
	}
	if rec := run("?%D0%A1=2026-08-29T12:30:41"); rec.Code != http.StatusOK {
		t.Fatalf("API ответил %d на datetime: %s", rec.Code, rec.Body.String())
	}
	rec := run("?%D0%A1=%D0%BC%D1%83%D1%81%D0%BE%D1%80")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("API ответил %d на негодный datetime, ожидался 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "expected datetime") {
		t.Fatalf("в ответе нет причины отказа: %s", rec.Body.String())
	}
}
