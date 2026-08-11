package ui

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Иконка теперь не инлайнится, а ссылается на символ общего спрайта (план 73),
// поэтому проверяем ссылку, а не разметку пути.
func useOf(name string) string { return `<use href="/vendor/lucide/sprite.svg#` + name + `"` }

func TestLucideIcon_Known(t *testing.T) {
	got := string(LucideIcon("shopping-cart"))
	if got == "" {
		t.Fatal("LucideIcon(shopping-cart) пустой")
	}
	if !strings.Contains(got, "<svg") || !strings.Contains(got, "</svg>") {
		t.Errorf("нет обёртки <svg>: %q", got)
	}
	if !strings.Contains(got, `class="lucide ob-icon"`) {
		t.Errorf("нет класса lucide ob-icon: %q", got)
	}
	if !strings.Contains(got, useOf("shopping-cart")) {
		t.Errorf("нет ссылки на символ shopping-cart: %q", got)
	}
	// Обводка остаётся на внешнем svg: символы спрайта её не несут, без этих
	// атрибутов иконка вышла бы залитым чёрным силуэтом.
	for _, attr := range []string{`fill="none"`, `stroke="currentColor"`, `stroke-width="2"`} {
		if !strings.Contains(got, attr) {
			t.Errorf("на обёртке нет атрибута %s: %q", attr, got)
		}
	}
}

// TestLucideIcon_AnyLucideName — ради этого и затевался план 73: работает любое
// имя Lucide, а не курируемый набор из 44 иконок подхода A.
func TestLucideIcon_AnyLucideName(t *testing.T) {
	// Ни одного из этих имён в наборе A не было.
	for _, name := range []string{"rocket", "cat", "bike", "graduation-cap", "stethoscope", "anchor"} {
		got := string(LucideIcon(name))
		if !strings.Contains(got, useOf(name)) {
			t.Errorf("имя %q не отрисовалось своей иконкой (скатилось в фолбэк?): %q", name, got)
		}
	}
}

func TestLucideIcon_EmptyIsEmpty(t *testing.T) {
	if got := LucideIcon(""); got != "" {
		t.Errorf("LucideIcon(\"\") = %q, ожидалась пустая строка", got)
	}
	if got := LucideIcon("   "); got != "" {
		t.Errorf("LucideIcon(пробелы) = %q, ожидалась пустая строка", got)
	}
}

func TestLucideIcon_UnknownFallsBackToSquare(t *testing.T) {
	got := string(LucideIcon("definitely-not-an-icon-xyz"))
	if got == "" {
		t.Fatal("неизвестная иконка дала пустую строку (нет фолбэка)")
	}
	if !strings.Contains(got, useOf("square")) {
		t.Errorf("фолбэк не ссылается на square: %q", got)
	}
	// Ссылка на несуществующий символ рисует пустоту молча — этого быть не должно.
	if strings.Contains(got, "definitely-not-an-icon-xyz") {
		t.Errorf("неизвестное имя утекло в разметку: %q", got)
	}
	if strings.Contains(got, `href=""`) || strings.Contains(got, `#"`) {
		t.Errorf("в фолбэке пустая ссылка: %q", got)
	}
}

// TestLucideIcon_HostileNameCannotEscapeAttribute — имя иконки приходит из
// конфигурации и теперь подставляется в href, а не ищется по карте. В разметку
// попадает только имя, найденное среди символов спрайта; всё прочее — фолбэк.
func TestLucideIcon_HostileNameCannotEscapeAttribute(t *testing.T) {
	for _, hostile := range []string{
		`shopping-cart" onload="alert(1)`,
		`x"><script>alert(1)</script>`,
		`../../etc/passwd`,
		`square#extra`,
	} {
		got := string(LucideIcon(hostile))
		if !strings.Contains(got, useOf("square")) {
			t.Errorf("враждебное имя %q не свелось к фолбэку: %q", hostile, got)
		}
		for _, bad := range []string{"<script", "onload", "..", "passwd"} {
			if strings.Contains(got, bad) {
				t.Errorf("в разметке остался фрагмент %q из имени %q: %q", bad, hostile, got)
			}
		}
	}
}

func TestLucideIcon_CaseAndTrim(t *testing.T) {
	want := LucideIcon("shopping-cart")
	for _, in := range []string{"Shopping-Cart", "  shopping-cart  ", "SHOPPING-CART", "Shopping Cart", "shopping_cart"} {
		if got := LucideIcon(in); got != want {
			t.Errorf("LucideIcon(%q) не совпал с каноничным", in)
		}
	}
}

func TestLucideIcon_Aliases(t *testing.T) {
	cases := [][2]string{
		{"home", "house"},
		{"cart", "shopping-cart"},
		{"ruble", "russian-ruble"},
		{"bar-chart-3", "chart-column"},
		{"pie-chart", "chart-pie"},
	}
	for _, c := range cases {
		if LucideIcon(c[0]) != LucideIcon(c[1]) {
			t.Errorf("синоним %q не разрешился в %q", c[0], c[1])
		}
		if LucideIcon(c[0]) == "" {
			t.Errorf("синоним %q дал пустую иконку", c[0])
		}
		// Синоним обязан вести на реальный символ, а не на фолбэк.
		if strings.Contains(string(LucideIcon(c[0])), useOf("square")) && c[1] != "square" {
			t.Errorf("синоним %q свернулся в фолбэк вместо %q", c[0], c[1])
		}
	}
}

func TestNormalizeIconName(t *testing.T) {
	cases := map[string]string{
		"":                      "",
		"   ":                   "",
		"shopping-cart":         "shopping-cart",
		"Shopping Cart":         "shopping-cart",
		"shopping_cart":         "shopping-cart",
		"  Layout--Dashboard  ": "layout-dashboard",
		"LAYOUT DASHBOARD":      "layout-dashboard",
		"-leading-dash":         "leading-dash",
		"trailing-dash-":        "trailing-dash",
	}
	for in, want := range cases {
		if got := NormalizeIconName(in); got != want {
			t.Errorf("NormalizeIconName(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}

func TestLucideNames_SortedAndCanonical(t *testing.T) {
	names := LucideNames()
	if len(names) == 0 {
		t.Fatal("список имён пуст")
	}
	// Набор A состоял из 44 иконок; полный Lucide — больше тысячи. Порог ловит
	// ситуацию, когда спрайт подменили огрызком и подсказка обеднела.
	if len(names) < 1000 {
		t.Errorf("в спрайте всего %d имён — похоже, это не полный Lucide", len(names))
	}
	if !sort.StringsAreSorted(names) {
		t.Error("список имён не отсортирован")
	}
	idx := make(map[string]bool, len(names))
	for _, n := range names {
		idx[n] = true
	}
	for _, must := range []string{"square", "shopping-cart", "layout-dashboard", "rocket"} {
		if !idx[must] {
			t.Errorf("в списке нет каноничного имени %q", must)
		}
	}
	// Синонимы в подсказку не попадают — только каноничные имена.
	if idx["home"] {
		t.Error("в списке имён не должно быть синонима home")
	}
}

func TestLucideAliasesJSON_ParsesAndResolvesToRealSymbols(t *testing.T) {
	raw := string(LucideAliasesJSON())
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("LucideAliasesJSON не парсится как JSON: %v", err)
	}
	if m["home"] != "house" || m["cart"] != "shopping-cart" {
		t.Errorf("в JSON нет ожидаемых синонимов: %v", m)
	}
	// Каждая цель синонима обязана быть реальным символом спрайта, иначе превью
	// в конфигураторе показывало бы квадрат там, где навигация рисует иконку.
	names := make(map[string]bool, len(LucideNames()))
	for _, n := range LucideNames() {
		names[n] = true
	}
	for alias, canon := range m {
		if !names[canon] {
			t.Errorf("синоним %q ведёт на несуществующий символ %q", alias, canon)
		}
	}
	// Безопасность встраивания в <script>: сырых < быть не должно.
	if strings.Contains(raw, "<") {
		t.Errorf("в JSON есть сырой символ < (небезопасно для <script>)")
	}
}

// TestSubsysBar_RendersIcons — панель подсистем выводит иконку перед заголовком;
// пустое имя иконки не даёт лишней разметки; неизвестное имя сворачивается в
// фолбэк (square).
func TestSubsysBar_RendersIcons(t *testing.T) {
	data := map[string]any{
		"Cfg":              Config{},
		"Lang":             "ru",
		"IsAdmin":          false,
		"CurrentSubsystem": "",
		"Subsystems": []*metadata.Subsystem{
			{Name: "Продажи", Title: "Продажи", Icon: "shopping-cart"},
			{Name: "Склад", Title: "Склад", Icon: ""},              // пусто → без иконки
			{Name: "Прочее", Title: "Прочее", Icon: "no-such-xyz"}, // неизвестно → square
		},
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "nav", data); err != nil {
		t.Fatalf("execute nav: %v", err)
	}
	html := buf.String()

	if !strings.Contains(html, `<nav class="subsys-bar">`) {
		t.Fatal("нет панели подсистем")
	}
	// Заголовки всех подсистем на месте.
	for _, name := range []string{"Продажи", "Склад", "Прочее"} {
		if !strings.Contains(html, name) {
			t.Errorf("в панели нет подсистемы %q", name)
		}
	}
	if !strings.Contains(html, useOf("shopping-cart")) {
		t.Error("иконка shopping-cart не отрисована в панели")
	}
	if !strings.Contains(html, useOf("square")) {
		t.Error("неизвестная иконка не дала фолбэк square")
	}
	// Ровно две иконки: shopping-cart и фолбэк. Подсистема с пустым icon иконку
	// не рисует (иначе была бы битая/лишняя разметка).
	if n := strings.Count(html, "lucide ob-icon"); n != 2 {
		t.Errorf("ожидалось 2 иконки в панели, получено %d", n)
	}
	// html/template не должен экранировать ссылку на символ: href остаётся рабочим.
	if strings.Contains(html, "ZgotmplZ") || strings.Contains(html, "#ZgotmplZ") {
		t.Error("html/template забраковал href спрайта (ZgotmplZ)")
	}
}
