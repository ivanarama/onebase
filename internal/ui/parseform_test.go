package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Сбой разбора формы обязан приводить к отказу, а не к продолжению с пустыми
// значениями. Раньше r.ParseForm() вызывался без проверки: при битом теле все
// FormValue возвращали "", и обработчик воспринимал это как осознанный ввод —
// в админке это стирало полное имя и снимало флаги is_admin/show_in_list, то
// есть битый запрос молча понижал права пользователя.
//
// Проверяем на infoRegSubmit: у него самые скромные предусловия из девяти
// затронутых обработчиков, а код разбора формы там тот же.
func TestParseFormError_RejectsRequest(t *testing.T) {
	ir := &metadata.InfoRegister{
		Name:       "КурсыВалют",
		Dimensions: []metadata.Field{{Name: "Валюта", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Курс", Type: metadata.FieldTypeNumber}},
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
	s := &Server{reg: reg}

	// «%zz» — некорректная процентная последовательность: ParseForm вернёт
	// ошибку, а FormValue после неё отдаст пустые строки.
	body := "Валюта=%zz&Курс=100"
	r := httptest.NewRequest(http.MethodPost, "/ui/inforeg/КурсыВалют/submit", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "КурсыВалют")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	s.infoRegSubmit(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("при битом теле формы ожидался 400, получен %d: %s",
			w.Code, strings.TrimSpace(w.Body.String()))
	}
}

// Корректное тело по-прежнему проходит разбор формы: guard, чтобы проверка не
// начала отклонять нормальные запросы. Дальше обработчик уйдёт в запись и
// упрётся в отсутствующее хранилище — нас интересует только то, что до 400 по
// причине разбора формы дело не дошло.
func TestParseFormOK_NotRejected(t *testing.T) {
	ir := &metadata.InfoRegister{
		Name:       "КурсыВалют",
		Dimensions: []metadata.Field{{Name: "Валюта", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Курс", Type: metadata.FieldTypeNumber}},
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
	s := &Server{reg: reg}

	r := httptest.NewRequest(http.MethodPost, "/ui/inforeg/КурсыВалют/submit",
		strings.NewReader("Валюта=USD&Курс=100"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "КурсыВалют")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	defer func() {
		// Хранилища нет — паника ниже по пути записи ожидаема и означает, что
		// разбор формы прошёл. Важно лишь, что мы не получили 400.
		_ = recover()
	}()
	w := httptest.NewRecorder()
	s.infoRegSubmit(w, r)
	if w.Code == http.StatusBadRequest {
		t.Fatalf("корректное тело формы отклонено как битое: %s",
			strings.TrimSpace(w.Body.String()))
	}
}
