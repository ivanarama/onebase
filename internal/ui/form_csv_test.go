package ui

import (
	"encoding/csv"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/shopspring/decimal"
)

func TestProcessorFormCSVDownload(t *testing.T) {
	form := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementButton, Name: "Выгрузить",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "ВыгрузитьНажатие"},
	})
	form.ProgramAST = mustParse(t, `
Процедура ВыгрузитьНажатие()
    Строки = Новый Массив;
    Строки.Добавить(СтрРазделить("Вопрос,Комментарий,Балл", ","));
    Строка = Новый Массив;
    Строка.Добавить("Русский; вопрос");
    Строка.Добавить("=1+1");
    Строка.Добавить(-3);
    Строки.Добавить(Строка);
    СкачатьCSV("Тест.csv", Строки);
КонецПроцедуры
`)
	proc := &processor.Processor{Name: "Выгрузка", Forms: []*metadata.FormModule{form}}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	body := processorClickBody("Выгрузить")
	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK || resp.CSV == nil || resp.CSV.Name != "Тест.csv" {
		t.Fatalf("CSV result: %+v", resp)
	}
	content := string(resp.CSV.Content)
	if !strings.HasPrefix(content, "\uFEFF") {
		t.Fatalf("Нет BOM: %q", content)
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(content, "\uFEFF")))
	reader.Comma = ';'
	rows, err := reader.ReadAll()
	if err != nil || len(rows) != 2 || strings.Join(rows[0], "|") != "Вопрос|Комментарий|Балл" ||
		strings.Join(rows[1], "|") != "Русский; вопрос|'=1+1|-3" {
		t.Fatalf("read CSV: %+v, %v", rows, err)
	}
}
func TestProcessorFormCSVDownloadEmptyCell(t *testing.T) {
	form := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementButton, Name: "Выгрузить",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "ВыгрузитьНажатие"},
	})
	form.ProgramAST = mustParse(t, `
Процедура ВыгрузитьНажатие()
    Строки = Новый Массив;
    Строки.Добавить(СтрРазделить("Комментарий", ","));
    Пустая = Новый Массив;
    Пустая.Добавить(Неопределено);
    Строки.Добавить(Пустая);
    Строки.Добавить(СтрРазделить("Заполнено", ","));
    СкачатьCSV("Тест.csv", Строки);
КонецПроцедуры
`)
	proc := &processor.Processor{Name: "Выгрузка", Forms: []*metadata.FormModule{form}}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(processorClickBody("Выгрузить").Encode()))
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK || resp.CSV == nil {
		t.Fatalf("CSV result: %+v", resp)
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(resp.CSV.Content), "\uFEFF")))
	reader.Comma = ';'
	rows, err := reader.ReadAll()
	if err != nil || len(rows) != 3 || rows[0][0] != "Комментарий" || rows[1][0] != "" || rows[2][0] != "Заполнено" {
		t.Fatalf("read CSV: %+v, %v", rows, err)
	}
}

func TestProcessorFormCSVDownloadReferencePresentationIsText(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"reference", &interpreter.Ref{Name: " =1+1"}, "' =1+1"},
		{"ordinary reference", &interpreter.Ref{Name: "Мастер"}, "Мастер"},
		{"text", "-12", "'-12"},
		{"decimal", decimal.New(-125, -1), "-12.5"},
		{"integer", -12, "-12"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := formCSVCell(tc.value); got != tc.want {
				t.Fatalf("CSV cell = %q, want %q", got, tc.want)
			}
		})
	}
}
