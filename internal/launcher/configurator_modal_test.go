package launcher

import (
	"strings"
	"testing"
)

// TestConfiguratorModalsCloseOnlyExplicitly — тот же инвариант, что и в
// internal/ui (TestModalsCloseOnlyExplicitly), для окон конфигуратора:
// модальное окно закрывается только кнопкой/крестиком, но не кликом мимо него.
// Клик по фону прилетал от обычного выделения текста мышью (browser шлёт click
// общему предку mousedown и mouseup) и закрывал окно вместе с введёнными
// данными — конструктор запроса с набранным текстом, диалог импорта PDF с
// заполненными полями, административную панель с формой.
func TestConfiguratorModalsCloseOnlyExplicitly(t *testing.T) {
	js, err := staticFiles.ReadFile("static/configurator.js")
	if err != nil {
		t.Fatalf("static/configurator.js: %v", err)
	}
	for _, bad := range []string{"e.target === overlay", "e.target===this", "e.target === modal"} {
		if strings.Contains(string(js), bad) {
			t.Errorf("configurator.js: вернулось закрытие модального окна по клику мимо него (%q)", bad)
		}
	}
	// Админ-панель рисует шапку с крестиком сразу, ещё до ответа сервера: иначе
	// не ответивший запрос оставил бы «Загрузка...» без единого способа закрыть.
	if got := strings.Count(string(js), "overlay.innerHTML = frame("); got != 2 {
		t.Errorf("cfgAdmin должен рисовать шапку с крестиком и в состоянии «Загрузка...», и после ответа сервера (frame() вызван %d раз)", got)
	}

	out := renderCfgMain(t, richCfgData("tree"))
	if strings.Contains(out, "event.target===this") {
		t.Error("шаблон конфигуратора: overlay снова закрывается по клику мимо окна")
	}
	// Явные способы закрытия на месте.
	for _, want := range []string{"dbgValModalClose()", "cfgModalClose()", `id="qb-close"`} {
		if !strings.Contains(out, want) {
			t.Errorf("шаблон конфигуратора: нет явной кнопки закрытия %q", want)
		}
	}
}
