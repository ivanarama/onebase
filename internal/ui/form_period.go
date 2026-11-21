package ui

import (
	"fmt"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

type questionPeriod struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

func newPeriodDialogBuiltin(sink *questionPayload) interpreter.BuiltinFunc {
	return func(args []any, _ string, _ int) (any, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("ПоказатьПериод(Начало, Конец): ожидаются две даты")
		}
		dates := [2]string{}
		for i, value := range args {
			if value == nil {
				continue
			}
			date, ok := value.(time.Time)
			if !ok {
				return nil, fmt.Errorf("ПоказатьПериод: аргумент %d должен быть датой", i+1)
			}
			if !date.IsZero() {
				dates[i] = date.Format(time.DateOnly)
			}
		}
		*sink = questionPayload{
			Text:     "Укажите начало и конец периода",
			Title:    "Выбор периода",
			Variants: []string{"ОК", "Отмена"},
			Period:   &questionPeriod{Start: dates[0], End: dates[1]},
		}
		return nil, nil
	}
}
