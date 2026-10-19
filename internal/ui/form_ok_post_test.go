package ui

// «ОК» проводимого документа с actions.ok.post — «записать, провести и
// закрыть», как в 1С: кнопка отдаёт контроллеру закрытия режим post (тот же,
// что «Провести и закрыть»). Рендер — через публичный
// обработчик формы.

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func okButtonMode(t *testing.T, posting, okPost bool) string {
	t.Helper()
	srv, ent := setupManagedEventsServer(t, "", nil, nil)
	ent.Posting = posting
	if okPost {
		yes := true
		ent.Forms[0].Actions = map[string]*metadata.FormAction{"ok": {Post: &yes}}
	}
	page := httptest.NewRecorder()
	srv.form(page, reqWithChi("GET", "/ui/catalog/"+ent.Name+"/new", nil, map[string]string{"entity": ent.Name, "kind": "catalog"}))
	if page.Code != 200 {
		t.Fatalf("GET form: %d: %s", page.Code, page.Body.String())
	}
	body := page.Body.String()
	i := strings.Index(body, `data-ob-close-reason="ok"`)
	if i < 0 {
		t.Fatalf("нет кнопки «ОК»:\n%s", body)
	}
	start := strings.LastIndex(body[:i], "<button")
	tag := body[start:i]
	_, mode, ok := strings.Cut(tag, `data-ob-form-close="`)
	if !ok {
		t.Fatalf("у «ОК» нет режима: %s", tag)
	}
	mode, _, _ = strings.Cut(mode, `"`)
	return mode
}

func TestFormOKPost_ПроводимыйДокументПроводит(t *testing.T) {
	if got := okButtonMode(t, true, true); got != "post" {
		t.Fatalf("«ОК» с actions.ok.post у проводимого документа — post, получено %q", got)
	}
}

func TestFormOKPost_ПоУмолчаниюЗапись(t *testing.T) {
	if got := okButtonMode(t, true, false); got != "save" {
		t.Fatalf("без ключа «ОК» — запись, получено %q", got)
	}
	if got := okButtonMode(t, false, true); got != "save" {
		t.Fatalf("у непроводимой сущности ключ игнорируется, получено %q", got)
	}
}
