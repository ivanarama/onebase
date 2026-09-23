package ui

import (
	"strings"
	"testing"
)

// Окно приложения не должно быть выше экрана. С одним min-height:100vh страницу
// растягивало самое высокое содержимое (обычно длинное меню слева): тело
// становилось 1283px при окне 908, вкладка-iframe получала ту же высоту, и любой
// диалог с position:fixed внутри неё центрировался по фрейму — уезжал под нижний
// край экрана. Правило легко потерять при следующей правке стилей, поэтому оно
// зафиксировано тестом, а не только комментарием.
func TestAppShellIsCappedToViewport(t *testing.T) {
	src := templateSource()
	body := cssRule(t, src, "body{font-family:system-ui")
	for _, want := range []string{"height:100vh", "overflow:hidden"} {
		if !strings.Contains(body, want) {
			t.Errorf("в правиле body нет %q: %s", want, body)
		}
	}
	// min-height:auto у flex-элемента не даёт ему стать ниже содержимого —
	// без явного нуля меню и рабочая область снова растянут страницу.
	for _, rule := range []string{".app-body{", "aside{width:210px", "main{flex:1"} {
		got := cssRule(t, src, rule)
		if !strings.Contains(got, "min-height:0") {
			t.Errorf("в правиле %q нет min-height:0: %s", rule, got)
		}
	}
	// На узком экране страница прокручивается целиком: там ограничение снимается,
	// иначе низ длинной формы стал бы недоступен.
	mobile := src[strings.Index(src, "@media (max-width:820px){"):]
	if !strings.Contains(mobile[:400], "body{height:auto;overflow:visible}") {
		t.Errorf("мобильная раскладка не вернула странице высоту по содержимому: %s", mobile[:400])
	}
}

// cssRule возвращает одно правило стилей целиком — от префикса до закрывающей
// скобки. Тесту нужен именно текст правила: искать подстроку по всему шаблону
// значило бы принять совпадение из соседнего селектора.
func cssRule(t *testing.T, src, prefix string) string {
	t.Helper()
	i := strings.Index(src, prefix)
	if i < 0 {
		t.Fatalf("правило %q не найдено в шаблоне", prefix)
	}
	j := strings.Index(src[i:], "}")
	if j < 0 {
		t.Fatalf("правило %q не закрыто", prefix)
	}
	return src[i : i+j+1]
}
