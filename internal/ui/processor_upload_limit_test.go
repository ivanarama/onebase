package ui

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/processor"
)

// Параметр обработки типа file был обрезан одним мегабайтом вместо заявленных
// attachments.max_file_size_mb (issue #674): в обработчике стояли ДВА предела
// тела — сначала defaultFormMemoryBytes, следом limitMultipartRequest на предел
// вложений. Пределы не композируются, вложенный MaxBytesReader режет по
// МЕНЬШЕМУ, поэтому связывал всегда первый, а второй был мёртв.
//
// Пользователь при этом получал сырое «http: request body too large», хотя
// понятный текст про предел файла для этого случая уже написан
// (uploadTooLargeText) — до него исполнение просто не доходило.

func fileParamProcessor() *processor.Processor {
	return &processor.Processor{
		Name:  "Загрузка",
		Title: "Загрузка данных",
		Params: []processor.Param{
			{Name: "Файл", Type: "file", Label: "Файл"},
		},
	}
}

// postProcessorFile шлёт multipart с файлом заданного размера в параметр Файл.
func postProcessorFile(t *testing.T, ts *httptest.Server, procName string, size int) (int, string) {
	t.Helper()
	body := new(bytes.Buffer)
	mw := multipart.NewWriter(body)
	fw, err := mw.CreateFormFile("Файл", "data.csv")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(bytes.Repeat([]byte("a"), size)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart Close: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/ui/processor/"+procName, body)
	if err != nil {
		t.Fatalf("запрос: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // тело читается ниже
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	return resp.StatusCode, string(buf[:n])
}

func newProcessorTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	s, _ := newSubmitTestServer(t, nil)
	s.reg.LoadProcessors([]*processor.Processor{fileParamProcessor()})
	r := chi.NewRouter()
	s.Mount(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

// Файл на несколько мегабайт обязан доехать до обработчика: предел вложений по
// умолчанию — 50 МБ. До фикса — 413 с сырым «request body too large».
func TestProcessorRun_FileParamAboveOneMiB(t *testing.T) {
	ts := newProcessorTestServer(t)
	code, body := postProcessorFile(t, ts, "загрузка", 5<<20)
	if code == http.StatusRequestEntityTooLarge {
		t.Fatalf("файл на 5 МиБ отклонён по размеру тела: %s", body)
	}
	if strings.Contains(body, "request body too large") {
		t.Errorf("в ответе сырая ошибка про размер тела: %s", body)
	}
}

// Предел вложений при этом остаётся действующим — правка убирает мёртвый
// предел, а не защиту. Файл заведомо больше 50 МБ отклоняется.
func TestProcessorRun_FileParamAboveUploadLimitRejected(t *testing.T) {
	ts := newProcessorTestServer(t)
	code, _ := postProcessorFile(t, ts, "загрузка", int(defaultUIUploadBytes)+(4<<20))
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("статус = %d, ожидался 413: предел вложений должен остаться в силе", code)
	}
}
