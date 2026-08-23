package ui

import (
	"bytes"
	"strings"
	"testing"

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
