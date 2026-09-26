package ui

import (
	"testing"

	"github.com/ivantit66/onebase/internal/runtime"
)

// Заполнить(Объект) обязан принимать объект документа, созданный в коде
// (Документы.X.Создать): так вводят на основании обработки и тесты уровня
// конфигурации. Заполнение по ССЫЛКЕ тут не замена — у ссылки реквизиты
// через точку недоступны, и ОбработкаЗаполнения получила бы пустое основание.
func TestDocWriterIsAcceptedAsFillSource(t *testing.T) {
	объект := &runtime.Object{Type: "А_Звонок", Kind: "document"}
	var источник any = &docWriter{obj: объект}

	принимающий, ok := источник.(interface{ runtimeObject() *runtime.Object })
	if !ok {
		t.Fatal("docWriter не распознаётся fill как объект-основание — Заполнить(Объект) из обработки упадёт")
	}
	if принимающий.runtimeObject() != объект {
		t.Fatal("docWriter отдал не свою запись")
	}

	// Нулевой приёмник не должен ронять fill: он ответит «объект-основание пустой».
	var пустой *docWriter
	if пустой.runtimeObject() != nil {
		t.Fatal("нулевой docWriter обязан отдавать nil")
	}
}
