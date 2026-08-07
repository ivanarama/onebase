package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/richtext"
	"github.com/ivantit66/onebase/internal/storage"
)

// Предел тела формы и предел самого richtext жили порознь: тело ограничивалось
// одним мегабайтом, richtext разрешал четыре, и проверка richtext была
// недостижима в принципе — запись объекта с парой вставленных скриншотов падала
// с сырым «http: request body too large», а введённый текст терялся целиком
// (issue #629).
//
// Тесты идут настоящим HTTP-запросом по роутеру — тем же путём, что и браузер
// пользователя. Приватные checkRichTextLimits/parseSubmitForm не зовём: именно
// потому, что существующие юниты на них дефект и пропустили.

func newRichTextFormServer(t *testing.T) (*httptest.Server, *Server, *metadata.Entity) {
	t.Helper()
	ent := richtextEntity()
	s, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
	r := chi.NewRouter()
	s.Mount(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts, s, ent
}

// postForm шлёт форму без следования редиректу: успешная запись отвечает 303, и
// http.Post превратил бы его в 200 страницы списка.
func postForm(t *testing.T, url string, form url.Values) (int, string) {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("запрос: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // тело читается ниже, закрытие вторично
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	return resp.StatusCode, string(buf[:n])
}

func submitURL(ts *httptest.Server, entity string) string {
	return ts.URL + "/ui/document/" + url.PathEscape(entity) + "/new"
}

// Значение в пределах richtext.MaxBytes обязано записаться. До фикса — 400
// «http: request body too large».
func TestSubmitRichText_3MiB_Saved(t *testing.T) {
	ts, s, ent := newRichTextFormServer(t)
	form := url.Values{}
	form.Set("Наименование", "Задача с картинками")
	form.Set("Результат", "<p>"+strings.Repeat("a", 3<<20)+"</p>")

	code, body := postForm(t, submitURL(ts, ent.Name), form)
	if code != http.StatusSeeOther {
		t.Fatalf("статус = %d, ожидался 303; тело: %s", code, body)
	}
	rows, err := s.store.List(t.Context(), ent.Name, ent, storage.ListParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("записей = %d, ожидалась одна", len(rows))
	}
	if got, _ := rows[0]["Результат"].(string); len(got) < 3<<20 {
		t.Errorf("richtext сохранён урезанным: %d байт", len(got))
	}
}

// Значение больше richtext.MaxBytes отклоняется — но ошибкой про richtext, а не
// про размер тела: до фикса исполнение до этой проверки не доходило.
func TestSubmitRichText_5MiB_RejectedWithRichTextMessage(t *testing.T) {
	ts, _, ent := newRichTextFormServer(t)
	form := url.Values{}
	form.Set("Наименование", "Слишком много")
	form.Set("Результат", strings.Repeat("a", 5<<20))

	code, body := postForm(t, submitURL(ts, ent.Name), form)
	if code < 400 || code >= 500 {
		t.Fatalf("статус = %d, ожидался 4xx; тело: %s", code, body)
	}
	if strings.Contains(body, "request body too large") {
		t.Errorf("пользователь видит сырую ошибку про тело запроса: %s", body)
	}
	if !strings.Contains(body, "richtext") {
		t.Errorf("в ответе нет упоминания предела richtext: %s", body)
	}
}

// Тело сверх нового предела — понятный текст про форматированный текст, а не
// «http: request body too large».
func TestSubmitRichText_BodyLimitMessageIsFriendly(t *testing.T) {
	ts, _, ent := newRichTextFormServer(t)
	form := url.Values{}
	form.Set("Наименование", "Огромное")
	form.Set("Результат", strings.Repeat("a", int(richTextFieldBodyBytes)+(4<<20)))

	code, body := postForm(t, submitURL(ts, ent.Name), form)
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("статус = %d, ожидался 413; тело: %s", code, body)
	}
	if strings.Contains(body, "request body too large") {
		t.Errorf("пользователь видит сырую ошибку про тело запроса: %s", body)
	}
	if !strings.Contains(body, "форматированный текст") {
		t.Errorf("в ответе нет объяснения про форматированный текст: %s", body)
	}
}

// Страховка от соблазна поднять defaultFormMemoryBytes глобально: у сущности
// без richtext предел остаётся прежним — его делят формы входа, 2FA и админки.
func TestSubmitWithoutRichText_BodyLimitUnchanged(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Простая",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Описание", Type: metadata.FieldTypeString},
		},
	}
	if got := formBodyLimit(ent); got != defaultFormMemoryBytes {
		t.Fatalf("предел тела = %d, ожидался %d", got, defaultFormMemoryBytes)
	}
	s, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
	r := chi.NewRouter()
	s.Mount(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)

	form := url.Values{}
	form.Set("Наименование", "Тест")
	form.Set("Описание", strings.Repeat("a", 2<<20))
	code, body := postForm(t, ts.URL+"/ui/catalog/"+url.PathEscape(ent.Name)+"/new", form)
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("статус = %d, ожидался 413; тело: %s", code, body)
	}
}

// Событийный путь управляемой формы: кнопка на форме с большим richtext ломалась
// ещё до записи — 413 «bad form: http: request body too large».
func TestManagedFormEventRichText_3MiB_Accepted(t *testing.T) {
	ts, _, ent := newRichTextFormServer(t)
	form := url.Values{}
	form.Set("_event", "Нажатие")
	form.Set("_element", "Кнопка")
	form.Set("Результат", strings.Repeat("a", 3<<20))

	code, body := postForm(t, ts.URL+"/ui/document/"+url.PathEscape(ent.Name)+"/form-event", form)
	if code == http.StatusRequestEntityTooLarge {
		t.Fatalf("событие формы отклонено по размеру тела: %s", body)
	}
	if strings.Contains(body, "request body too large") {
		t.Errorf("в ответе сырая ошибка про тело запроса: %s", body)
	}
}

// Предел тела обязан оставаться строго больше предела richtext, иначе проверка
// checkRichTextLimits снова станет недостижимой. Инвариант закреплён и на этапе
// компиляции, но здесь он читается явно.
func TestFormBodyLimit_ExceedsRichTextLimit(t *testing.T) {
	ent := richtextEntity()
	if got := formBodyLimit(ent); got <= int64(richtext.MaxBytes) {
		t.Fatalf("предел тела %d не больше richtext.MaxBytes %d", got, richtext.MaxBytes)
	}
	two := &metadata.Entity{Name: "Две", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "А", Type: metadata.FieldTypeRichText},
		{Name: "Б", Type: metadata.FieldTypeRichText},
	}}
	if formBodyLimit(two) <= formBodyLimit(ent) {
		t.Errorf("предел не растёт с числом richtext-реквизитов: %d против %d",
			formBodyLimit(two), formBodyLimit(ent))
	}
}
