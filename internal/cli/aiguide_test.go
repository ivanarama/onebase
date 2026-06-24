package cli

import (
	"strings"
	"testing"
)

func TestGenerateAIGuide_HasSignaturesAndSections(t *testing.T) {
	g := generateAIGuide()
	for _, want := range []string{
		"## Язык DSL",
		"### Методы объектов",
		"### Язык запросов",
		"СтрЗаменить(",     // сигнатура функции
		"Запрос.Выполнить", // метод объекта
		"onebase query --project <dir> --sql",
		"onebase eval",
		"onebase widget explain",
		"onebase report explain",
	} {
		if !strings.Contains(g, want) {
			t.Errorf("в guide нет ожидаемого фрагмента: %q", want)
		}
	}
	if strings.Contains(g, "Сигнатуры смотрите в примерах") {
		t.Error("guide всё ещё содержит устаревший дисклеймер о сигнатурах")
	}
}
