package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"golang.org/x/net/html"
)

func managedDynamicAnchorEntity() *metadata.Entity {
	entity := &metadata.Entity{
		Name: "ЗаявкаСДинамическимОформлением", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Стадия", Type: metadata.FieldTypeString}},
	}
	whenAccepted := `Стадия = "Принята"`
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: entity.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Commands: []*metadata.FormCommand{{
			Name: "Применить", Action: "Применить", Title: map[string]string{"ru": "Применить"},
		}},
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementLabel, Name: "НадписьСтатуса", TitleMap: map[string]string{"ru": "Черновик"}, HiddenWhen: whenAccepted},
			{Kind: metadata.FormElementPicture, Name: "КартинкаСФайлом", Picture: "logo.png", HiddenWhen: whenAccepted},
			{Kind: metadata.FormElementPicture, Name: "КартинкаБезФайла", HiddenWhen: whenAccepted},
			{Kind: metadata.FormElementCommandBar, Name: "ПанельКоманд", ReadOnlyWhen: whenAccepted},
			{Kind: metadata.FormElementField, Name: "ПолеСтадии", DataPath: "Объект.Стадия"},
		},
	}
	entity.Forms = []*metadata.FormModule{form}
	return entity
}

// renderManagedDynamicAnchorPage идёт через production HTTP-обработчик
// карточки: тест проверяет ту же обвязку автоматической панели, которую видит
// браузер, а не только изолированный managed-element template.
func renderManagedDynamicAnchorPage(t *testing.T, stage string) string {
	t.Helper()
	entity := managedDynamicAnchorEntity()
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

func managedAnchorNode(t *testing.T, rendered, name string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(rendered))
	if err != nil {
		t.Fatal(err)
	}
	var found *html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if found != nil {
			return
		}
		if value, ok := managedHTMLAttr(node, "data-ob-el"); ok && value == name {
			found = node
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return found
}

func managedDescendantButton(root *html.Node) *html.Node {
	if root == nil {
		return nil
	}
	if root.Type == html.ElementNode && root.Data == "button" {
		return root
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if found := managedDescendantButton(child); found != nil {
			return found
		}
	}
	return nil
}

func TestManagedDynamicAnchorsRenderThroughPublicForm(t *testing.T) {
	draft := renderManagedDynamicAnchorPage(t, "Черновик")
	for _, name := range []string{"НадписьСтатуса", "КартинкаСФайлом", "КартинкаБезФайла", "ПанельКоманд"} {
		if managedAnchorNode(t, draft, name) == nil {
			t.Errorf("draft form has no dynamic-state anchor %q", name)
		}
	}
	draftButton := managedDescendantButton(managedAnchorNode(t, draft, "ПанельКоманд"))
	if draftButton == nil {
		t.Fatal("real command bar has no command button")
	}
	if _, disabled := managedHTMLAttr(draftButton, "disabled"); disabled {
		t.Fatal("draft command bar must remain editable")
	}

	accepted := renderManagedDynamicAnchorPage(t, "Принята")
	for _, name := range []string{"НадписьСтатуса", "КартинкаСФайлом", "КартинкаБезФайла"} {
		if managedAnchorNode(t, accepted, name) != nil {
			t.Errorf("server render did not hide %q", name)
		}
	}
	acceptedButton := managedDescendantButton(managedAnchorNode(t, accepted, "ПанельКоманд"))
	if acceptedButton == nil {
		t.Fatal("accepted form has no real command-bar button")
	}
	if _, disabled := managedHTMLAttr(acceptedButton, "disabled"); !disabled {
		t.Fatal("server render did not apply command-bar readonly_when")
	}
}

func TestManagedDynamicAnchorsApplyProductionClientState(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the managed dynamic-anchor regression test")
	}

	rendered := renderManagedDynamicAnchorPage(t, "Черновик")
	doc, err := html.Parse(strings.NewReader(rendered))
	if err != nil {
		t.Fatal(err)
	}
	body := findHTMLElement(doc, "body")
	if body == nil {
		t.Fatalf("rendered managed form has no body:\n%s", rendered)
	}
	tree, err := json.Marshal(domElementNode(body))
	if err != nil {
		t.Fatal(err)
	}
	domPath := filepath.Join(t.TempDir(), "managed-dynamic-anchors.json")
	if err := os.WriteFile(domPath, tree, 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(node, "--test", "static/managed_dynamic_anchor_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Env = append(os.Environ(), "ONEBASE_DYNAMIC_ANCHORS_DOM="+domPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node managed dynamic-anchor behavior test: %v\n%s", err, output)
	}
}
