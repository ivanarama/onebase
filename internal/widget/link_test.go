package widget

import "testing"

// Ссылка карточки-счётчика ведёт к списку/отчёту с нужным отбором. Принимается
// только ВНУТРЕННИЙ путь: карточку настраивает конфигуратор, но произвольный
// внешний адрес на дашборде не нужен, а javascript:-ссылка тем более.
func TestСсылкаКарточки_ТолькоВнутреннийПуть(t *testing.T) {
	принимается := []string{
		"/ui/document/ПередачаЗаявки",
		"/ui/document/ПередачаЗаявки?f.СостояниеПередачи=ТребуетРазбора",
		"/ui/report/ПередачиВКарантине",
	}
	for _, s := range принимается {
		if got := safeWidgetLink(s); got != s {
			t.Errorf("safeWidgetLink(%q) = %q, ожидался тот же путь", s, got)
		}
	}

	отбрасывается := []string{
		"",
		"   ",
		"https://example.com/списки",
		"//example.com/списки",
		"javascript:alert(1)",
		"ui/document/Заявка", // без ведущего «/» — не путь приложения
	}
	for _, s := range отбрасывается {
		if got := safeWidgetLink(s); got != "" {
			t.Errorf("safeWidgetLink(%q) = %q, ожидалась пустая ссылка", s, got)
		}
	}
}

func TestСсылкаКарточки_ПробелыОбрезаются(t *testing.T) {
	if got := safeWidgetLink("  /ui/document/Заявка  "); got != "/ui/document/Заявка" {
		t.Errorf("= %q, ожидался путь без пробелов", got)
	}
}
