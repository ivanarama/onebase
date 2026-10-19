package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"golang.org/x/net/html"
)

// Кнопка, скрытая hidden_when при отрисовке, обязана попасть в разметку с
// display:none: событие формы, снявшее условие, показывает её по якорю, а
// пропущенную кнопку показать нечем. Так «Утвердить» после «ОК (зарезервировать)»
// появлялась только при повторном открытии документа.
func managedHiddenButtonEntity() *metadata.Entity {
	entity := &metadata.Entity{
		Name: "ДокументСУтверждением", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Стадия", Type: metadata.FieldTypeString}},
	}
	untilReserved := `Стадия <> "Зарезервирован"`
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: entity.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "ПолеСтадии", DataPath: "Объект.Стадия"},
			{Kind: metadata.FormElementButton, Name: "КнУтвердить", TitleMap: map[string]string{"ru": "Утвердить"},
				HiddenWhen: untilReserved, Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Утвердить"}},
			// width даёт кнопке обёртку раскладки: якорь и display:none — на обёртке.
			{Kind: metadata.FormElementButton, Name: "КнШирокая", TitleMap: map[string]string{"ru": "Широкая"},
				Width: 200, HiddenWhen: untilReserved, Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Утвердить"}},
			// Остальные виды пока пропускаются, как раньше (поля — отдельный #1914).
			{Kind: metadata.FormElementLabel, Name: "НадписьРезерва", TitleMap: map[string]string{"ru": "В резерве"}, HiddenWhen: untilReserved},
		},
	}
	entity.Forms = []*metadata.FormModule{form}
	return entity
}

func renderManagedHiddenButtonPage(t *testing.T, stage string) string {
	t.Helper()
	entity := managedHiddenButtonEntity()
	server, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
	id := uuid.New()
	if err := server.store.Upsert(ctx, entity.Name, id, map[string]any{"Стадия": stage}, entity); err != nil {
		t.Fatal(err)
	}
	req := reqWithChi(http.MethodGet, "/ui/catalog/"+entity.Name+"/"+id.String(), nil,
		map[string]string{"kind": "catalog", "entity": entity.Name, "id": id.String()})
	rec := httptest.NewRecorder()
	server.formEdit(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("public managed form render: status=%d body=%s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func managedStyleHidden(n *html.Node) bool {
	style, _ := managedHTMLAttr(n, "style")
	return strings.Contains(strings.ReplaceAll(style, " ", ""), "display:none")
}

func TestManagedHiddenButtonStaysInDOMThroughPublicForm(t *testing.T) {
	draft := renderManagedHiddenButtonPage(t, "Черновик")
	for _, name := range []string{"КнУтвердить", "КнШирокая"} {
		anchor := managedAnchorNode(t, draft, name)
		if anchor == nil {
			t.Fatalf("кнопка %q, скрытая при отрисовке, пропущена — показать её после события нечем", name)
		}
		if !managedStyleHidden(anchor) {
			t.Errorf("кнопка %q должна быть скрыта при отрисовке (display:none)", name)
		}
		button := managedDescendantButton(anchor)
		if button == nil {
			t.Fatalf("у якоря %q нет кнопки", name)
		}
		if fire, _ := managedHTMLAttr(button, "data-ob-fire-click"); fire != name {
			t.Errorf("скрытая кнопка %q без data-ob-fire-click=%q: после показа нажатие не дойдёт до сервера (%q)", name, name, fire)
		}
		if _, ok := managedHTMLAttr(anchor, "data-ob-el-path"); !ok {
			t.Errorf("у кнопки %q нет data-ob-el-path — ответ события её не найдёт", name)
		}
	}
	if managedAnchorNode(t, draft, "НадписьРезерва") != nil {
		t.Error("надпись со скрытием пропускается, как прежде: правка касается только кнопок")
	}

	reserved := renderManagedHiddenButtonPage(t, "Зарезервирован")
	for _, name := range []string{"КнУтвердить", "КнШирокая"} {
		anchor := managedAnchorNode(t, reserved, name)
		if anchor == nil || managedStyleHidden(anchor) {
			t.Errorf("при снятом условии кнопка %q должна быть видна", name)
		}
	}
}
