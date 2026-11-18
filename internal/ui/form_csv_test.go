package ui

import (
	"encoding/csv"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
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
