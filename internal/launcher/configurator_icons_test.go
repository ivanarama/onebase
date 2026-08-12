package launcher

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/ui"
)

// План 73: конфигуратор рисует превью иконки ссылкой на общий спрайт, а список
// имён отдаёт полным. Тесты идут через отрендеренную оболочку (cfg-foot) — там,
// где эту разметку и получает браузер.

func TestConfigurator_IconDatalistCoversWholeLucide(t *testing.T) {
	html := renderCfgFoot(t)

	if !strings.Contains(html, `<datalist id="lucide-icons">`) {
		t.Fatal("в оболочке нет подсказки имён иконок (datalist#lucide-icons)")
	}
	// Набор A состоял из 44 имён; после перехода на спрайт подсказка обязана
	// покрывать весь Lucide, иначе пользователь не узнает о новых именах.
	if n := strings.Count(html, "<option value="); n < 1000 {
		t.Errorf("в подсказке всего %d имён — это не полный Lucide", n)
	}
	for _, name := range []string{"shopping-cart", "rocket", "stethoscope"} {
		if !strings.Contains(html, `<option value="`+name+`">`) {
			t.Errorf("в подсказке нет имени %q", name)
		}
	}
}

func TestLauncherLucideRouteCachesContentVersionedSprite(t *testing.T) {
	versionedPath := ui.LucideSpriteURL()
	req := httptest.NewRequest(http.MethodGet, versionedPath, nil)
	rec := httptest.NewRecorder()
	launcherLucideHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status=%d", versionedPath, rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("versioned Lucide route Cache-Control=%q, want immutable", cc)
	}

	req = httptest.NewRequest(http.MethodGet, "/vendor/lucide/sprite.svg", nil)
	rec = httptest.NewRecorder()
	launcherLucideHandler().ServeHTTP(rec, req)
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("unversioned Lucide route Cache-Control=%q, want no-cache", cc)
	}
}

func TestConfigurator_IconPreviewUsesSprite(t *testing.T) {
	html := renderCfgFoot(t)

	// Путь спрайта приходит на клиент из того же места, что и рендер навигации.
	if !strings.Contains(html, ui.LucideSpriteURL()) {
		t.Errorf("в оболочке нет пути спрайта %q", ui.LucideSpriteURL())
	}
	for _, frag := range []string{
		"var ALIASES =",     // синонимы разрешаются на клиенте
		"var SPRITE =",      // и превью собирает <use> само
		"createElementNS",   // элемент SVG создаётся, а не склеивается строкой
		"'use'",             // именно <use>
		"knownNames()[key]", // неизвестное имя → фолбэк, как на сервере
		"key = 'square'",    // и подставляет тот же квадрат
		`"home":"house"`,    // карта синонимов реально уехала на клиент
	} {
		if !strings.Contains(html, frag) {
			t.Errorf("в скрипте превью нет фрагмента %q", frag)
		}
	}

	// Разметку иконок в страницу больше не вшиваем: при полном наборе это сотни
	// килобайт. Признак старого подхода — тело пути иконки прямо в HTML.
	if strings.Contains(html, `<rect width="18" height="18" x="3" y="3"`) {
		t.Error("в страницу вшита инлайн-разметка иконок (подход A вернулся)")
	}
}
