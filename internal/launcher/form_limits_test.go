package launcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// postForm собирает POST /bases/{id} с заданными query и телом.
func postForm(t *testing.T, id, rawQuery, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/bases/"+id+"?"+rawQuery, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func storedBase(t *testing.T, st *Store, id string) *Base {
	t.Helper()
	b, err := st.Get(id)
	if err != nil {
		t.Fatalf("Get(%q): %v", id, err)
	}
	return b
}

func baseFixture(t *testing.T) (*handler, *Store) {
	t.Helper()
	st := newTestStore(t)
	if err := st.Add(&Base{
		ID: "base", Name: "Рабочая", ConfigSource: "file",
		Path: "/srv/onebase/cfg", DB: "postgres://localhost/onebase",
		DBType: "postgres", Port: 9090,
	}); err != nil {
		t.Fatal(err)
	}
	return &handler{store: st}, st
}

// Главный случай, ради которого правка и делалась.
//
// ParseForm разбирает и тело, и строку запроса, а ошибку тела возвращает уже
// после того, как значения из query попали в r.Form. Поэтому битое тело — это не
// «все поля пустые»: имя приходит из URL и проходит проверку, а Path и DBPath
// оказываются пустыми и уходят в хранилище. Без проверки ошибки регистрация базы
// теряла путь к конфигурации — по журналу неотличимо от правки администратора.
func TestUpdateRejectsMalformedFormBodyAndKeepsBase(t *testing.T) {
	h, st := baseFixture(t)
	before := storedBase(t, st, "base")

	// %zz — некорректная escape-последовательность: url.ParseQuery на теле
	// вернёт ошибку, а значения из query при этом уже разобраны.
	req := postForm(t, "base", "name=Рабочая&db_type=postgres&db=postgres://localhost/onebase", "path=%zz")
	rec := httptest.NewRecorder()

	h.update(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("код = %d, ожидался 400: битую форму нельзя принимать за ввод", rec.Code)
	}
	after := storedBase(t, st, "base")
	if after.Path != before.Path {
		t.Errorf("Path затёрт: было %q, стало %q", before.Path, after.Path)
	}
	if after.ConfigSource != before.ConfigSource {
		t.Errorf("ConfigSource затёрт: было %q, стало %q", before.ConfigSource, after.ConfigSource)
	}
	if after.Port != before.Port {
		t.Errorf("Port затёрт: было %d, стало %d", before.Port, after.Port)
	}
}

// Тело сверх предела — 413, а не 400: клиенту важно различать «прислал мусор» и
// «прислал слишком много».
func TestUpdateRejectsOversizedFormBody(t *testing.T) {
	h, st := baseFixture(t)

	big := "path=" + url.QueryEscape(strings.Repeat("x", int(maxFormBody)+1))
	req := postForm(t, "base", "", big)
	rec := httptest.NewRecorder()

	h.update(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("код = %d, ожидался 413", rec.Code)
	}
	if got := storedBase(t, st, "base").Path; got != "/srv/onebase/cfg" {
		t.Errorf("Path изменён на переполнении: %q", got)
	}
}

// Обычная форма по-прежнему проходит: ограничение не должно ломать штатный путь.
func TestUpdateAcceptsWellFormedForm(t *testing.T) {
	h, st := baseFixture(t)

	body := url.Values{
		"name":          {"Рабочая"},
		"config_source": {"file"},
		"path":          {"/srv/onebase/cfg2"},
		"db":            {"postgres://localhost/onebase"},
		"db_type":       {"postgres"},
		"port":          {"9091"},
	}.Encode()
	req := postForm(t, "base", "", body)
	rec := httptest.NewRecorder()

	h.update(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("код = %d, ожидался 302; body=%q", rec.Code, rec.Body.String())
	}
	after := storedBase(t, st, "base")
	if after.Path != "/srv/onebase/cfg2" || after.Port != 9091 {
		t.Errorf("форма не применилась: Path=%q Port=%d", after.Path, after.Port)
	}
}

// parseBoundedForm не должен подменять обычную форму multipart-разбором: на
// urlencoded ParseMultipartForm вернул бы ErrNotMultipart, и обработчик отказал
// бы в корректном запросе.
func TestParseBoundedFormHandlesBothEncodings(t *testing.T) {
	urlencoded := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("a=1"))
	urlencoded.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := parseBoundedForm(urlencoded, formMemoryBytes); err != nil {
		t.Fatalf("urlencoded: %v", err)
	}
	if got := urlencoded.FormValue("a"); got != "1" {
		t.Errorf("urlencoded: FormValue(a) = %q", got)
	}

	body := "--b\r\nContent-Disposition: form-data; name=\"a\"\r\n\r\n1\r\n--b--\r\n"
	multi := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	multi.Header.Set("Content-Type", `multipart/form-data; boundary=b`)
	if err := parseBoundedForm(multi, formMemoryBytes); err != nil {
		t.Fatalf("multipart: %v", err)
	}
	if got := multi.FormValue("a"); got != "1" {
		t.Errorf("multipart: FormValue(a) = %q", got)
	}
}
