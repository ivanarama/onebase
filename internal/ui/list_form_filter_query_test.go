package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"golang.org/x/net/html"
)

// listQueryFilterFixture — справочник «Заявка» с двумя записями и формой
// списка, отбор которой задан условиями conds.
func listQueryFilterFixture(t *testing.T, conds ...metadata.FormListCondition) (*Server, func(user *auth.User, query string) string) {
	t.Helper()
	заявка := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Автор", Type: metadata.FieldTypeString},
		},
	}
	заявка.Forms = []*metadata.FormModule{{
		Name: "ФормаСписка", Kind: "list", EntityName: заявка.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "КолонкаНаименование", DataPath: "Наименование"},
		},
		ListFilter: conds,
	}}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{заявка})
	for _, row := range []map[string]any{
		{"Наименование": "Заявка оператора", "Автор": "u-op"},
		{"Наименование": "Чужая заявка", "Автор": "u-other"},
	} {
		if err := s.store.Upsert(ctx, заявка.Name, uuid.New(), row, заявка); err != nil {
			t.Fatal(err)
		}
	}
	список := func(user *auth.User, query string) string {
		t.Helper()
		target := "/ui/catalog/" + заявка.Name
		if query != "" {
			target += "?" + query
		}
		req := reqWithChi(http.MethodGet, target, nil, map[string]string{"kind": "catalog", "entity": заявка.Name})
		req = req.WithContext(auth.ContextWithUser(req.Context(), user))
		rec := httptest.NewRecorder()
		s.list(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("list: status=%d body=%s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	return s, список
}

func listQueryUser(id string) *auth.User {
	return &auth.User{ID: id, Login: id, Roles: []*auth.Role{{Name: "Оператор", Permissions: auth.Permission{
		Catalogs: map[string][]string{"Заявка": {"read"}},
	}}}}
}

const listQueryMine = "ВЫБРАТЬ З.Ссылка КАК Ссылка ИЗ Справочник.Заявка КАК З ГДЕ З.Автор = &ТекущийПользователь"

// Условие запросом: список показывает только ссылки из первой колонки, а
// &ТекущийПользователь — идентификатор учётки смотрящего.
func TestUI_EntityList_FormFilterQuery(t *testing.T) {
	_, список := listQueryFilterFixture(t, metadata.FormListCondition{Query: listQueryMine})
	html := список(listQueryUser("u-op"), "")
	if !strings.Contains(html, "Заявка оператора") || strings.Contains(html, "Чужая заявка") {
		t.Fatal("условие запросом: оператору — только его заявка")
	}
	if strings.Contains(html, "data-ob-list-form-toggles") {
		t.Fatal("неснимаемое условие получило переключатель")
	}
	// Неснимаемое условие адресом не отключить.
	if html := список(listQueryUser("u-op"), "lf_off=0"); strings.Contains(html, "Чужая заявка") {
		t.Fatal("lf_off отключил условие без removable")
	}
	админ := список(&auth.User{ID: "root", Login: "admin", IsAdmin: true}, "")
	if !strings.Contains(админ, "Заявка оператора") || !strings.Contains(админ, "Чужая заявка") {
		t.Fatal("администратора условие запросом не касается")
	}
}

// Снимаемый отбор по умолчанию: включён и подписан над списком; щелчок
// (lf_off) показывает весь список, переключатель — снятым.
func TestUI_EntityList_FormFilterRemovable(t *testing.T) {
	_, список := listQueryFilterFixture(t, metadata.FormListCondition{
		Query: listQueryMine, Removable: true, Label: map[string]string{"ru": "Только мои"},
	})
	html := список(listQueryUser("u-op"), "")
	if strings.Contains(html, "Чужая заявка") {
		t.Fatal("отбор по умолчанию не применился")
	}
	if !strings.Contains(html, `aria-pressed="true">☑ Только мои`) || !strings.Contains(html, "lf_off=0") {
		t.Fatalf("нет включённого переключателя «Только мои» со ссылкой на снятие:\n%s", listToggleFragment(html))
	}
	снят := список(listQueryUser("u-op"), "lf_off=0")
	if !strings.Contains(снят, "Чужая заявка") || !strings.Contains(снят, "Заявка оператора") {
		t.Fatal("снятый отбор: весь список")
	}
	if !strings.Contains(снят, `aria-pressed="false">☐ Только мои`) {
		t.Fatalf("переключатель снятого отбора не показан снятым:\n%s", listToggleFragment(снят))
	}
	// Снятый отбор переживает остальные действия со списком: формы отбора и
	// поиска несут его скрытым полем, ссылки сортировки — параметром адреса.
	if n := strings.Count(снят, `<input type="hidden" name="lf_off" value="0">`); n < 2 {
		t.Fatalf("формы отбора и поиска не переносят снятый отбор (lf_off): скрытых полей %d", n)
	}
	if !strings.Contains(снят, "lf_off=0&amp;sort=") {
		t.Fatal("ссылки сортировки не переносят снятый отбор (lf_off)")
	}
	// Вместе с отбором панели: снятый отбор по умолчанию остаётся снятым, а
	// переключатель включает его обратно, не теряя отбор панели.
	сОтбором := список(listQueryUser("u-op"), "lf_off=0&f.Наименование=Чужая")
	if !strings.Contains(сОтбором, "Чужая заявка") || strings.Contains(сОтбором, "Заявка оператора") {
		t.Fatal("снятый отбор по умолчанию вместе с отбором панели: нужна только «Чужая заявка»")
	}
	toggle, pressed := parsedListFormFilterToggle(t, сОтбором)
	if pressed != "false" || toggle.Get("f.Наименование") != "Чужая" || toggle.Has("lf_off") ||
		len(toggle["sort_default"]) != 1 || toggle.Get("sort_default") != "1" || toggle.Has("sort") || toggle.Has("sort_snapshot") {
		t.Fatalf("переключатель потерял отбор панели или исходную сортировку: pressed=%q query=%v", pressed, toggle)
	}
}

// Запрос с ошибкой закрывает отбор: правило «свои записи» сломалось, и показать
// всё вместо своего хуже, чем ничего.
func TestUI_EntityList_FormFilterQueryErrorClosesList(t *testing.T) {
	_, список := listQueryFilterFixture(t, metadata.FormListCondition{Query: "ВЫБРАТЬ З.Ссылка ИЗ Справочник.НетТакого КАК З"})
	html := список(listQueryUser("u-op"), "")
	if strings.Contains(html, "Заявка оператора") || strings.Contains(html, "Чужая заявка") {
		t.Fatal("ошибка запроса открыла список")
	}
}

func listToggleFragment(html string) string {
	i := strings.Index(html, "data-ob-list-form-toggles")
	if i < 0 {
		return "(переключателей нет)"
	}
	end := i + 400
	if end > len(html) {
		end = len(html)
	}
	return html[i:end]
}

// Read the actual removable-filter anchor, rather than an unrelated link or
// the spelling/order of its encoded query string. These fixtures have one toggle.
func parsedListFormFilterToggle(t *testing.T, body string) (url.Values, string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var containers, anchors []*html.Node
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inside bool) {
		if _, ok := keyboardHTMLAttr(n, "data-ob-list-form-toggles"); ok {
			containers = append(containers, n)
			inside = true
		}
		if inside && n.Type == html.ElementNode && n.Data == "a" {
			anchors = append(anchors, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inside)
		}
	}
	walk(doc, false)
	if len(containers) != 1 || len(anchors) != 1 {
		t.Fatalf("expected one form-filter toggle, got %d containers and %d anchors", len(containers), len(anchors))
	}
	anchor := anchors[0]
	href, ok := keyboardHTMLAttr(anchor, "href")
	if !ok {
		t.Fatal("form-filter toggle has no href")
	}
	u, err := url.Parse(href)
	if err != nil || u.IsAbs() || u.Host != "" || u.Path != "" || u.Fragment != "" {
		t.Fatalf("invalid form-filter toggle href %q: %v", href, err)
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		t.Fatal(err)
	}
	pressed, ok := keyboardHTMLAttr(anchor, "aria-pressed")
	if !ok || (pressed != "true" && pressed != "false") {
		t.Fatalf("form-filter toggle has invalid aria-pressed %q", pressed)
	}
	return q, pressed
}
