package ui

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/metadata"
	processorpkg "github.com/ivantit66/onebase/internal/processor"
)

// Форма обработки: обязательные параметры и индикатор выполнения.
//
// Проверяется РАЗМЕТКА, а не поле структуры. Признак, дошедший до Param и
// потерянный в шаблоне, дал бы зелёный тест при форме, которая по-прежнему
// уходит на сервер пустой — ровно то, что случилось с человеком за демонстрацией:
// он нажал «Выполнить» второй раз (первый раз ничего видимо не происходило,
// шла загрузка), а файл в поле браузер не вернул.
func renderProcessorForm(t *testing.T, params []processorpkg.Param) string {
	t.Helper()
	var buf bytes.Buffer
	proc := &processorpkg.Processor{Name: "ИмпортИзYML", Title: "Импорт каталога", Params: params}
	data := map[string]any{
		"Lang":               "ru",
		"Cfg":                Config{AppName: "TestApp"},
		"Nav":                nil,
		"Subsystems":         nil,
		"CurrentSubsystem":   "",
		"IsAdmin":            true,
		"Processor":          proc,
		"ParamValues":        map[string]any{},
		"RefOptions":         map[string][]map[string]any{},
		"ProcessorRefEntity": map[string]string{},
	}
	if err := tmpl.ExecuteTemplate(&buf, "page-processor", data); err != nil {
		t.Fatalf("render page-processor: %v", err)
	}
	return buf.String()
}

func TestUI_ProcessorForm_RequiredParams(t *testing.T) {
	html := renderProcessorForm(t, []processorpkg.Param{
		{Name: "Файл", Type: "file", Label: "Файл выгрузки (YML)", Required: true},
		{Name: "Комментарий", Type: "string", Label: "Комментарий"},
	})

	if !strings.Contains(html, `<input type="file" name="Файл" required>`) {
		t.Errorf("обязательный файловый параметр без required в разметке:\n%s", html)
	}
	// Необязательное поле обязательным становиться не должно: иначе признак
	// «required» приклеивается ко всей форме и ломает обработки без него.
	if strings.Contains(html, `name="Комментарий" required`) {
		t.Errorf("необязательный параметр помечен required:\n%s", html)
	}
}

func TestUI_ProcessorForm_BusyIndicator(t *testing.T) {
	html := renderProcessorForm(t, []processorpkg.Param{
		{Name: "Файл", Type: "file", Label: "Файл выгрузки (YML)", Required: true},
	})
	if !strings.Contains(html, `data-ob-busy="Выполняется…"`) {
		t.Errorf("форма обработки без признака индикации выполнения:\n%s", html)
	}
}

// Серверная проверка — не дубль браузерной: запрос приходит и мимо формы.
func TestProcessorRun_MissingRequiredParams(t *testing.T) {
	params := []processorpkg.Param{
		{Name: "Сайт", Type: "reference:Сайты", Label: "Сайт-владелец каталога", Required: true},
		{Name: "Файл", Type: "file", Label: "Файл выгрузки (YML)", Required: true},
		{Name: "Комментарий", Type: "string", Label: "Комментарий"},
	}
	for _, c := range []struct {
		name   string
		values map[string]any
		want   string
	}{
		{"пусто всё", map[string]any{}, "«Сайт-владелец каталога», «Файл выгрузки (YML)»"},
		{"файл потерян при повторной отправке", map[string]any{"Сайт": "uuid", "Файл": ""}, "«Файл выгрузки (YML)»"},
		{"пробелы не значение", map[string]any{"Сайт": "uuid", "Файл": "   "}, "«Файл выгрузки (YML)»"},
		{"всё на месте", map[string]any{"Сайт": "uuid", "Файл": "catalog.yml"}, ""},
		{"необязательное пустое не мешает", map[string]any{"Сайт": "uuid", "Файл": "catalog.yml", "Комментарий": ""}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := missingRequiredParams(params, c.values); got != c.want {
				t.Errorf("получено %q, ждали %q", got, c.want)
			}
		})
	}
}

// runProcessorEmpty шлёт «Выполнить» без единого значения — ровно то, что
// уходит на сервер при повторном нажатии: файл браузер в поле не вернул.
func runProcessorEmpty(t *testing.T, proc *processorpkg.Processor) (int, string) {
	t.Helper()
	s, _ := newSubmitTestServer(t, nil)
	s.reg.LoadProcessors([]*processorpkg.Processor{proc})
	r := chi.NewRouter()
	s.Mount(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)

	resp, err := ts.Client().PostForm(ts.URL+"/ui/processor/"+proc.Name, url.Values{})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // тело читается ниже
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("чтение ответа: %v", err)
	}
	return resp.StatusCode, string(body)
}

// Отказ проверяется через БОЕВОЙ маршрут, а не вызовом missingRequiredParams:
// признак, доехавший до Param и потерянный в обработчике, дал бы зелёный тест
// при форме, которая по-прежнему улетает на сервер за ошибкой прикладного
// модуля про ключ командной строки.
func TestProcessorRun_RequiredParamRefusedThroughHTTP(t *testing.T) {
	code, body := runProcessorEmpty(t, &processorpkg.Processor{
		Name:  "ИмпортБезФайла",
		Title: "Импорт каталога",
		Params: []processorpkg.Param{
			{Name: "Файл", Type: "file", Label: "Файл выгрузки (YML)", Required: true},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("статус = %d, ожидалась перерисованная форма", code)
	}
	if !strings.Contains(body, "Заполните обязательные поля:") ||
		!strings.Contains(body, "Файл выгрузки (YML)") {
		t.Errorf("отказ не назвал незаполненное поле:\n%s", body)
	}
	// Ошибка прикладного модуля означала бы, что проверка не сработала и
	// обработка всё-таки запустилась.
	if strings.Contains(body, "--file") {
		t.Errorf("обработка запустилась вместо отказа формы:\n%s", body)
	}
}

// У обработки с управляемой формой отказ обязан прийти ЕЮ ЖЕ. page-processor —
// не её страница: человек получил бы вместо своей формы автогенерённую, без
// собственных элементов и табличных частей. Соседние отказы запуска в этом
// обработчике так и делают, и новая проверка не должна выпадать из ряда.
func TestProcessorRun_RequiredParamRefusalKeepsManagedForm(t *testing.T) {
	form := &metadata.FormModule{
		Name:       "ФормаОбработки",
		Kind:       "object",
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "ПолеФайл", DataPath: "Объект.Файл", Type: "file"},
			{Kind: metadata.FormElementButton, Name: "Выполнить"},
		},
	}
	code, body := runProcessorEmpty(t, &processorpkg.Processor{
		Name:  "ИмпортУправляемый",
		Title: "Импорт каталога",
		Params: []processorpkg.Param{
			{Name: "Файл", Type: "file", Label: "Файл выгрузки (YML)", Required: true},
		},
		Forms: []*metadata.FormModule{form},
	})
	if code != http.StatusOK {
		t.Fatalf("статус = %d, ожидалась перерисованная форма", code)
	}
	if !strings.Contains(body, "Заполните обязательные поля:") {
		t.Errorf("отказ не доехал до управляемой формы:\n%s", body)
	}
	// data-ob-busy рисует только автогенерённая форма обработки: её признак в
	// ответе означает, что управляемую подменили страницей по умолчанию.
	if strings.Contains(body, "data-ob-busy") {
		t.Errorf("вместо управляемой формы отрисована автогенерённая:\n%s", body)
	}
}

// Тот же блок разметки прячет и остальные отказы запуска. Обработка без
// процедуры Выполнить() — ближайший сосед: сообщение о ней собиралось, но под
// «{{if .Ran}}» не рисовалось никогда, потому что запуска не было.
func TestProcessorRun_MissingProcedureIsVisible(t *testing.T) {
	code, body := runProcessorEmpty(t, &processorpkg.Processor{
		Name:  "БезПроцедуры",
		Title: "Обработка без модуля",
	})
	if code != http.StatusOK {
		t.Fatalf("статус = %d, ожидалась перерисованная форма", code)
	}
	if !strings.Contains(body, "Процедура Выполнить() не найдена") {
		t.Errorf("отказ запуска не показан человеку:\n%s", body)
	}
}
