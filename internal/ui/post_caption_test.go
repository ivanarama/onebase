package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// renderCard рендерит карточку документа ПоступлениеТоваров через реальный
// HTTP-handler formEdit (автогенерируемая форма — у документа нет FormModule).
func renderCard(t *testing.T, s *Server, id string) string {
	t.Helper()
	r := reqWithChi("GET", "/ui/document/поступлениетоваров/"+id, url.Values{},
		map[string]string{"kind": "document", "entity": "поступлениетоваров", "id": id})
	rec := httptest.NewRecorder()
	s.formEdit(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("ожидался 200, получен %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// writeOne создаёт и записывает (без проведения) непомеченный документ, чтобы
// кнопки проведения гарантированно рендерились.
func writeOne(t *testing.T, dp *docProxy, num string) string {
	t.Helper()
	w := dp.CallMethod("создать", nil).(*docWriter)
	w.Set("Номер", num)
	w.CallMethod("записать", nil)
	return w.obj.ID.String()
}

// Issue #497: post_caption переопределяет подпись кнопки проведения; вторая
// кнопка получает подпись «<подпись> и закрыть».
func TestFormEdit_PostCaption_CustomLabel(t *testing.T) {
	_, _, s, dp, doc := newPostingDoc(t)
	s.cfg = Config{AppName: "test"}
	doc.PostCaption = "Создать начисление"

	body := renderCard(t, s, writeOne(t, dp, "ПОС-CAP"))

	// Санити: кнопка проведения вообще рендерится (CanPost=true в харнессе).
	if !strings.Contains(body, `value="post"`) {
		t.Fatalf("нет кнопки проведения (value=\"post\"); тело:\n%s", body)
	}
	if !strings.Contains(body, `value="post" form="main-form">Создать начисление<`) {
		t.Errorf("кастомная подпись «Создать начисление» не отрендерилась; тело:\n%s", body)
	}
	// Подпись проверяем отдельно от атрибутов: их состав у кнопки меняется
	// (например, добавилась подсказка сочетания клавиш), а к тексту подписи это
	// отношения не имеет — привязка к соседству делала тест ломким.
	if !strings.Contains(body, `value="post_and_close"`) || !strings.Contains(body, `>Создать начисление и закрыть<`) {
		t.Errorf("вторая кнопка должна быть «Создать начисление и закрыть»; тело:\n%s", body)
	}
	if strings.Contains(body, `>Провести<`) {
		t.Errorf("осталась стандартная подпись «Провести» вместо кастомной; тело:\n%s", body)
	}
}

// post_and_close_hidden убирает вторую кнопку, оставляя основную.
func TestFormEdit_PostAndCloseHidden(t *testing.T) {
	_, _, s, dp, doc := newPostingDoc(t)
	s.cfg = Config{AppName: "test"}
	doc.PostAndCloseHidden = true

	body := renderCard(t, s, writeOne(t, dp, "ПОС-HID"))

	if !strings.Contains(body, `value="post"`) {
		t.Fatalf("основная кнопка проведения должна остаться; тело:\n%s", body)
	}
	if strings.Contains(body, `value="post_and_close"`) {
		t.Errorf("кнопка «Провести и закрыть» должна быть скрыта при post_and_close_hidden; тело:\n%s", body)
	}
}

// Без post_caption — прежнее поведение: обе стандартные кнопки на месте.
func TestFormEdit_PostCaption_DefaultUnchanged(t *testing.T) {
	_, _, s, dp, _ := newPostingDoc(t)
	s.cfg = Config{AppName: "test"}

	body := renderCard(t, s, writeOne(t, dp, "ПОС-DEF"))

	if !strings.Contains(body, `value="post" form="main-form">Провести<`) {
		t.Errorf("стандартная кнопка «Провести» отсутствует; тело:\n%s", body)
	}
	if !strings.Contains(body, `value="post_and_close"`) || !strings.Contains(body, `>Провести и закрыть<`) {
		t.Errorf("стандартная кнопка «Провести и закрыть» отсутствует; тело:\n%s", body)
	}
}
