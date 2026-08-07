package launcher

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Регрессия к issue #630: обработчик, не поставивший свой предел, не должен
// читать тело без ограничения.
//
// Раньше предел целиком лежал на обработчике, а страховкой считался gosec G120.
// Страховка не работала: G120 видит только присваивание r.Body в теле САМОЙ
// функции, а разбор идёт через parseBoundedForm без http.ResponseWriter.
// Четыре обработчика читали тело без предела, два из них (cfgLoginSubmit,
// cfg2FASubmit) — до аутентификации.

// bodyReadCounter считает, сколько байт у него реально прочитали. Нужен, чтобы
// отличить «предел сработал» от «тело прочитали целиком и отвергли потом».
type bodyReadCounter struct {
	r    io.Reader
	read int64
}

func (c *bodyReadCounter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += int64(n)
	return n, err
}
func (c *bodyReadCounter) Close() error { return nil }

func TestParseFormLimited_AppliesDefaultLimitWhenHandlerForgot(t *testing.T) {
	// Тело заведомо больше умолчания: 12 МиБ против maxFormBody = 8 МиБ.
	huge := strings.Repeat("a", 12<<20)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("v="+huge))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	counter := &bodyReadCounter{r: req.Body}
	req.Body = counter
	rec := httptest.NewRecorder()

	// Обработчик СВОЙ предел не ставит — ровно тот случай, который раньше
	// оставлял тело без ограничения.
	err := parseFormLimited(rec, req)
	if err == nil {
		t.Fatalf("разбор 12 МиБ прошёл без ошибки — предел по умолчанию не применён")
	}
	if got := formErrorStatus(err); got != http.StatusRequestEntityTooLarge {
		t.Errorf("статус ошибки = %d, ожидался 413: %v", got, err)
	}
	// Ключевое: тело не должно быть прочитано целиком. Допуск на буферизацию —
	// вдвое от предела, лишь бы не 12 МиБ.
	if counter.read > 2*maxFormBody {
		t.Errorf("прочитано %d байт при пределе %d — тело читается без ограничения",
			counter.read, maxFormBody)
	}
}

// Обработчик со СВОИМ меньшим пределом не должен пострадать от умолчания:
// его предел строже и обязан сработать первым.
func TestParseFormLimited_HandlerOwnSmallerLimitStillWins(t *testing.T) {
	body := strings.Repeat("a", 1<<20) // 1 МиБ — меньше умолчания, но больше своего предела
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("v="+body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	req.Body = http.MaxBytesReader(rec, req.Body, 64<<10) // 64 КиБ

	err := parseFormLimited(rec, req)
	if err == nil {
		t.Fatal("свой предел 64 КиБ не сработал")
	}
	if got := formErrorStatus(err); got != http.StatusRequestEntityTooLarge {
		t.Errorf("статус ошибки = %d, ожидался 413: %v", got, err)
	}
}

// Обычная форма в пределах умолчания разбирается как раньше.
func TestParseFormLimited_NormalFormStillParses(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("name=База&port=8080"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	if err := parseFormLimited(rec, req); err != nil {
		t.Fatalf("обычная форма не разобралась: %v", err)
	}
	if got := req.FormValue("name"); got != "База" {
		t.Errorf("name = %q, ожидалось «База»", got)
	}
	if got := req.FormValue("port"); got != "8080" {
		t.Errorf("port = %q, ожидалось 8080", got)
	}
}

// Multipart — тот случай, где отсутствие предела опаснее всего: части сверх
// formMemoryBytes уходят во ВРЕМЕННЫЕ ФАЙЛЫ, и собственного общего потолка у
// ParseMultipartForm нет (встроенный лимит 10 МБ у net/http есть только для
// urlencoded-тела POST). Неаутентифицированный клиент мог заполнить диск.
func TestParseFormLimited_DefaultLimitAppliesToMultipart(t *testing.T) {
	var b strings.Builder
	const boundary = "----obtest"
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Disposition: form-data; name=\"f\"; filename=\"big.bin\"\r\n")
	b.WriteString("Content-Type: application/octet-stream\r\n\r\n")
	b.WriteString(strings.Repeat("a", 12<<20)) // 12 МиБ > maxFormBody
	b.WriteString("\r\n--" + boundary + "--\r\n")

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(b.String()))
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	rec := httptest.NewRecorder()

	err := parseFormLimited(rec, req)
	if err == nil {
		t.Fatal("multipart на 12 МиБ разобрался без ошибки — предел не применён")
	}
	if got := formErrorStatus(err); got != http.StatusRequestEntityTooLarge {
		t.Errorf("статус = %d, ожидался 413: %v", got, err)
	}
}
