package launcher

import (
	"bytes"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	oblog "github.com/ivantit66/onebase/internal/logging"
)

// failingWriter — ResponseWriter, у которого обрывается тело ответа: заголовки
// уже ушли, а Write возвращает ошибку. Так выглядит закрытая вкладка или
// оборванная закачка со стороны обработчика.
type failingWriter struct {
	hdr http.Header
	err error
}

func newFailingWriter() *failingWriter {
	return &failingWriter{hdr: http.Header{}, err: http.ErrBodyNotAllowed}
}

func (f *failingWriter) Header() http.Header       { return f.hdr }
func (f *failingWriter) WriteHeader(int)           {}
func (f *failingWriter) Write([]byte) (int, error) { return 0, f.err }

// captureLog подменяет логгер по умолчанию на JSON-буфер с уровнем Debug и
// возвращает функцию, отдающую накопленные записи.
func captureLog(t *testing.T) func() []map[string]any {
	t.Helper()
	prev := slog.Default()
	var buf bytes.Buffer
	slog.SetDefault(oblog.New(&buf, true, slog.LevelDebug))
	t.Cleanup(func() { slog.SetDefault(prev) })

	return func() []map[string]any {
		var recs []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			if line == "" {
				continue
			}
			var rec map[string]any
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatalf("запись журнала не разобралась: %v (%q)", err, line)
			}
			recs = append(recs, rec)
		}
		return recs
	}
}

func onlyRecord(t *testing.T, recs []map[string]any) map[string]any {
	t.Helper()
	if len(recs) != 1 {
		t.Fatalf("ожидалась одна запись журнала, получено %d: %v", len(recs), recs)
	}
	return recs[0]
}

// Обрыв страницы/фрагмента/JSON — штатная ситуация (закрыли вкладку). Она не
// должна подниматься выше Debug, иначе журнал конфигуратора зальёт при обычной
// работе. Тест фиксирует именно уровень, а не факт записи.
func TestWriteBodyLogsAtDebug(t *testing.T) {
	records := captureLog(t)

	writeBody(newFailingWriter(), []byte("<div>страница</div>"))

	rec := onlyRecord(t, records())
	if rec["level"] != "DEBUG" {
		t.Errorf("уровень = %v, ожидался DEBUG: обрыв страницы не повод шуметь в журнале", rec["level"])
	}
	if rec["component"] != "launcher" {
		t.Errorf("component = %v, ожидался launcher", rec["component"])
	}
}

func TestRespondJSONToLogsAtDebug(t *testing.T) {
	records := captureLog(t)

	respondJSONTo(newFailingWriter(), map[string]any{"ok": true})

	if rec := onlyRecord(t, records()); rec["level"] != "DEBUG" {
		t.Errorf("уровень = %v, ожидался DEBUG", rec["level"])
	}
}

// Обрыв закачки, наоборот, обязан быть виден: пользователь получает внешне
// целый, но усечённый файл — усечённый .obz всплывёт только при восстановлении.
// Вместе с уровнем фиксируем имя и ожидавшийся размер: без них по журналу не
// понять, какой именно файл испорчен.
func TestWriteDownloadLogsAtWarnWithFileName(t *testing.T) {
	records := captureLog(t)
	payload := []byte("PK\x03\x04содержимое архива")

	writeDownload(newFailingWriter(), "config_ab12cd34.obz", payload)

	rec := onlyRecord(t, records())
	if rec["level"] != "WARN" {
		t.Errorf("уровень = %v, ожидался WARN: усечённый файл пользователь не заметит", rec["level"])
	}
	if rec["file"] != "config_ab12cd34.obz" {
		t.Errorf("file = %v, ожидалось имя выгружаемого файла", rec["file"])
	}
	if size, ok := rec["size"].(float64); !ok || int(size) != len(payload) {
		t.Errorf("size = %v, ожидалось %d", rec["size"], len(payload))
	}
}

// Сбой шаблона — почти всегда ошибка в самом шаблоне, а не разрыв соединения:
// пользователь получает обрезанную страницу, и это надо видеть.
func TestRenderTemplateLogsAtWarn(t *testing.T) {
	records := captureLog(t)
	// Шаблон валиден при разборе, но падает на исполнении: у int нет поля Missing.
	tmpl := template.Must(template.New("cfg-login").Parse(`{{.Missing}}`))

	renderTemplate(httptest.NewRecorder(), tmpl, 42)

	rec := onlyRecord(t, records())
	if rec["level"] != "WARN" {
		t.Errorf("уровень = %v, ожидался WARN", rec["level"])
	}
	if rec["template"] != "cfg-login" {
		t.Errorf("template = %v, ожидалось cfg-login", rec["template"])
	}
}

// Успешный путь: помощники остаются обычной записью в ответ и ничего не пишут
// в журнал — иначе шум был бы на каждом запросе.
func TestRespondHelpersWriteAndStaySilent(t *testing.T) {
	records := captureLog(t)

	body := httptest.NewRecorder()
	writeBody(body, []byte("<div>ок</div>"))
	if got := body.Body.String(); got != "<div>ок</div>" {
		t.Errorf("writeBody записал %q", got)
	}

	dl := httptest.NewRecorder()
	writeDownload(dl, "config.zip", []byte("PK\x03\x04"))
	if got := dl.Body.String(); got != "PK\x03\x04" {
		t.Errorf("writeDownload записал %q", got)
	}

	js := httptest.NewRecorder()
	respondJSONTo(js, map[string]any{"ok": true})
	if got := strings.TrimSpace(js.Body.String()); got != `{"ok":true}` {
		t.Errorf("respondJSONTo записал %q", got)
	}

	tp := httptest.NewRecorder()
	renderTemplate(tp, template.Must(template.New("t").Parse(`привет, {{.}}`)), "мир")
	if got := tp.Body.String(); got != "привет, мир" {
		t.Errorf("renderTemplate записал %q", got)
	}

	if recs := records(); len(recs) != 0 {
		t.Errorf("на успешном пути журнал должен быть пуст, получено: %v", recs)
	}
}
