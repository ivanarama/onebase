package launcher

// Отказ предохранителя обязан вести туда, где галочка действительно лежит.
//
// Сообщение звало «в конфигураторе (Система → Настройки)»: такого пункта в
// конфигураторе нет вовсе — меню «Система» есть только в «Предприятии», и
// предохранителя там нет ни под каким пунктом. Галочка живёт в конфигураторе:
// Меню → «Параметры базы» → «Безопасность». Человек, получивший отказ, шёл
// искать несуществующий экран.
//
// Тест сверяет подсказку с реально отрисованной страницей и с меню оболочки:
// каждая строка интерфейса, обещанная в «ёлочках», обязана существовать в UI.
// Поэтому переименование галочки или пункта меню роняет тест, а не тихо
// разъезжается с текстом отказа, как в прошлый раз.

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
)

// guillemetFragments — подстроки в «ёлочках»: ровно те строки интерфейса,
// которые подсказка обещает пользователю показать на экране.
func guillemetFragments(s string) []string {
	var out []string
	for {
		i := strings.Index(s, "«")
		if i < 0 {
			return out
		}
		s = s[i+len("«"):]
		j := strings.Index(s, "»")
		if j < 0 {
			return out
		}
		out = append(out, s[:j])
		s = s[j+len("»"):]
	}
}

// renderBaseSettingsPage отрисовывает страницу «Параметры базы» тем же
// хендлером, который открывает пункт меню конфигуратора.
func renderBaseSettingsPage(t *testing.T) string {
	t.Helper()
	b := &Base{ID: "guard-hint", Name: "g", ConfigSource: "file", Path: t.TempDir(),
		DBType: "sqlite", DBPath: filepath.Join(t.TempDir(), "g.db")}
	st := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	if err := st.Add(b); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	t.Cleanup(CloseAuthPools)

	req := httptest.NewRequest("GET", "/bases/"+b.ID+"/configurator/admin/settings", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", b.ID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	(&handler{store: st}).cfgAdminSettings(rec, req)
	return rec.Body.String()
}

func TestПодсказкиПредохранителей_ВедутНаСуществующийЭкран(t *testing.T) {
	page := renderBaseSettingsPage(t)
	if !strings.Contains(page, "Параметры базы") {
		t.Fatalf("страница «Параметры базы» не отрисовалась:\n%s", page)
	}
	// Пункт меню обязан открывать именно этот экран — иначе путь «Меню →
	// Параметры базы» в подсказке ведёт в никуда.
	if !strings.Contains(cfgHead, "Параметры базы") || !strings.Contains(cfgHead, "cfgAdmin('settings')") {
		t.Error("в меню конфигуратора нет пункта «Параметры базы», открывающего страницу настроек")
	}

	for _, h := range []struct {
		hint string
		key  string
	}{
		{storage.NetworkEnabledHint, "net.enabled"},
		{storage.ExecEnabledHint, "exec.enabled"},
	} {
		var onPage, inMenu bool
		for _, frag := range guillemetFragments(h.hint) {
			// Флаги независимы: «Параметры базы» — и заголовок страницы, и пункт
			// меню, и засчитать её только чему-то одному значит потерять вторую
			// проверку.
			p, m := strings.Contains(page, frag), strings.Contains(cfgHead, frag)
			onPage, inMenu = onPage || p, inMenu || m
			if !p && !m {
				t.Errorf("подсказка %s обещает «%s», но такой строки нет ни на странице настроек, ни в меню", h.key, frag)
			}
		}
		// Без этих двух проверок тест зелен и на подсказке, которая вообще
		// перестала называть экраны: «нет обещаний — нечего сверять».
		if !onPage {
			t.Errorf("подсказка %s не называет ни одной строки со страницы настроек: %s", h.key, h.hint)
		}
		if !inMenu {
			t.Errorf("подсказка %s не называет пункт меню, которым эта страница открывается: %s", h.key, h.hint)
		}
		// Ключ и команда — для headless, где до конфигуратора не дойти (#709).
		if !strings.Contains(h.hint, "onebase settings set "+h.key) {
			t.Errorf("подсказка %s не называет команду onebase settings set", h.key)
		}
	}
}

// Все видимые пользователю отказы предохранителей ведут по одному адресу и не
// поминают несуществующий экран.
func TestОтказыПредохранителей_ОдинПутьБезФантомногоЭкрана(t *testing.T) {
	for name, msg := range map[string]string{
		"ui.ErrNetworkLocked":        ui.ErrNetworkLocked.Error(),
		"ui.ErrExecLocked":           ui.ErrExecLocked.Error(),
		"scheduler.ErrNetworkLocked": scheduler.ErrNetworkLocked.Error(),
	} {
		if !strings.Contains(msg, "Параметры базы") {
			t.Errorf("%s не называет экран «Параметры базы»: %s", name, msg)
		}
		if strings.Contains(msg, "Система →") {
			t.Errorf("%s зовёт в несуществующий экран «Система → Настройки»: %s", name, msg)
		}
	}
}
