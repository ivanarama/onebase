package widget

import (
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

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
		// Браузер для http(s) нормализует «\» в «/», поэтому все эти четыре —
		// тот же протокол-относительный переход на example.com, что и «//host».
		`/\example.com/списки`,
		`\\example.com/списки`,
		`/\/example.com/списки`,
		`\/example.com/списки`,
		// Обратный слэш в середине пути приложения не встречается, а браузер и
		// его прочтёт как «/» — такую ссылку тоже не пропускаем.
		`/ui/document\Заявка`,
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

// Тот же отбор, но через публичную точку входа: до дашборда ссылка доезжает
// полем Result.Link, и проверять надо именно этот путь, а не только функцию.
func TestRun_СсылкаКарточки_ВнешнийАдресНеДоезжает(t *testing.T) {
	ctx, runner, _ := newRowAccessRunner(t)
	runner.User = nil // права здесь не проверяем

	// Тип «actions» декларативен: запрос не выполняется, остаётся только ссылка.
	run := func(link string) string {
		return runner.Run(ctx, &metadata.Widget{
			Name: "Карточка",
			Type: metadata.WidgetTypeActions,
			Link: link,
		}).Link
	}

	const внутренний = "/ui/document/ПередачаЗаявки?f.Состояние=ТребуетРазбора"
	if got := run(внутренний); got != внутренний {
		t.Errorf("Run().Link = %q, ожидался внутренний путь %q", got, внутренний)
	}
	for _, s := range []string{`/\example.com/списки`, `\\example.com/списки`, "//example.com/списки"} {
		if got := run(s); got != "" {
			t.Errorf("Run().Link для %q = %q, ожидалась пустая ссылка", s, got)
		}
	}
}
