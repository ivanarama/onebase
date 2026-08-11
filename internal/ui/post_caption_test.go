package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func formActionButtonLabel(t *testing.T, body, action string) (string, bool) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse rendered form: %v", err)
	}
	var text func(*html.Node, *strings.Builder)
	text = func(node *html.Node, out *strings.Builder) {
		if node.Type == html.TextNode {
			out.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			text(child, out)
		}
	}
	var walk func(*html.Node) (string, bool)
	walk = func(node *html.Node) (string, bool) {
		if node.Type == html.ElementNode && node.Data == "button" {
			attrs := map[string]string{}
			for _, attr := range node.Attr {
				attrs[attr.Key] = attr.Val
			}
			if attrs["name"] == "_action" && attrs["value"] == action {
				var label strings.Builder
				text(node, &label)
				return strings.Join(strings.Fields(label.String()), " "), true
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if label, ok := walk(child); ok {
				return label, true
			}
		}
		return "", false
	}
	return walk(doc)
}

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

	if label, ok := formActionButtonLabel(t, body, "post"); !ok || label != "Создать начисление" {
		t.Fatalf("подпись action=post: %q, exists=%v; тело:\n%s", label, ok, body)
	}
	if label, ok := formActionButtonLabel(t, body, "post_and_close"); !ok || label != "Создать начисление и закрыть" {
		t.Fatalf("подпись action=post_and_close: %q, exists=%v; тело:\n%s", label, ok, body)
	}
}

// post_and_close_hidden убирает вторую кнопку, оставляя основную.
func TestFormEdit_PostAndCloseHidden(t *testing.T) {
	_, _, s, dp, doc := newPostingDoc(t)
	s.cfg = Config{AppName: "test"}
	doc.PostAndCloseHidden = true

	body := renderCard(t, s, writeOne(t, dp, "ПОС-HID"))

	if _, ok := formActionButtonLabel(t, body, "post"); !ok {
		t.Fatalf("основная кнопка проведения должна остаться; тело:\n%s", body)
	}
	if _, ok := formActionButtonLabel(t, body, "post_and_close"); ok {
		t.Errorf("кнопка «Провести и закрыть» должна быть скрыта при post_and_close_hidden; тело:\n%s", body)
	}
}

// Без post_caption — прежнее поведение: обе стандартные кнопки на месте.
func TestFormEdit_PostCaption_DefaultUnchanged(t *testing.T) {
	_, _, s, dp, _ := newPostingDoc(t)
	s.cfg = Config{AppName: "test"}

	body := renderCard(t, s, writeOne(t, dp, "ПОС-DEF"))

	if label, ok := formActionButtonLabel(t, body, "post"); !ok || label != "Провести" {
		t.Errorf("подпись action=post: %q, exists=%v; тело:\n%s", label, ok, body)
	}
	if label, ok := formActionButtonLabel(t, body, "post_and_close"); !ok || label != "Провести и закрыть" {
		t.Errorf("подпись action=post_and_close: %q, exists=%v; тело:\n%s", label, ok, body)
	}
}
