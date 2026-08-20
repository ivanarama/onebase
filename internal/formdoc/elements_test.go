package formdoc

import (
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Elements должен разворачивать дерево элементов формы вместе с node-id — это
// адресация, по которой визуальный конструктор (#164) наводит правки на узлы
// дерева yaml.Node. node-id = путь "elements.<i>.children.<j>".
func TestElements_TreeAndNodeIDs(t *testing.T) {
	doc, err := Load([]byte(elemSample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	els, err := doc.Elements()
	if err != nil {
		t.Fatalf("Elements: %v", err)
	}
	if len(els) != 1 {
		t.Fatalf("ожидался 1 верхний элемент, получено %d", len(els))
	}
	g := els[0]
	if g.NodeID != "elements.0" {
		t.Errorf("node-id группы = %q, ожидался elements.0", g.NodeID)
	}
	if g.El.Kind != metadata.FormElementGroupBox {
		t.Errorf("kind группы = %q, ожидался ГруппаФормы", g.El.Kind)
	}
	if len(g.Children) != 2 {
		t.Fatalf("ожидалось 2 дочерних элемента, получено %d", len(g.Children))
	}
	c0, c1 := g.Children[0], g.Children[1]
	if c0.NodeID != "elements.0.children.0" {
		t.Errorf("node-id поля 0 = %q", c0.NodeID)
	}
	if c0.El.Kind != metadata.FormElementField || c0.El.DataPath != "Объект.Номер" {
		t.Errorf("поле 0 декодировано неверно: kind=%q data_path=%q", c0.El.Kind, c0.El.DataPath)
	}
	if c1.NodeID != "elements.0.children.1" {
		t.Errorf("node-id поля 1 = %q", c1.NodeID)
	}
	if c1.El.DataPath != "Объект.Дата" {
		t.Errorf("поле 1 data_path = %q, ожидался Объект.Дата", c1.El.DataPath)
	}
}

// NodeByID должен разрешать любой выданный Elements node-id обратно в mapping-узел
// того же элемента — иначе операции правки (SetProp/Insert/Move) промахнутся.
func TestNodeByID_ResolvesEveryElement(t *testing.T) {
	doc, err := Load([]byte(elemSample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	els, err := doc.Elements()
	if err != nil {
		t.Fatalf("Elements: %v", err)
	}
	var check func(ens []*ElementNode)
	check = func(ens []*ElementNode) {
		for _, en := range ens {
			n, err := doc.NodeByID(en.NodeID)
			if err != nil {
				t.Fatalf("NodeByID(%q): %v", en.NodeID, err)
			}
			var el metadata.FormElement
			if err := n.Decode(&el); err != nil {
				t.Fatalf("decode %q: %v", en.NodeID, err)
			}
			if el.Kind != en.El.Kind {
				t.Errorf("node-id %q: kind %q != %q", en.NodeID, el.Kind, en.El.Kind)
			}
			check(en.Children)
		}
	}
	check(els)
}

// Невалидный node-id (вне диапазона, неизвестный ключ, пустой) — ошибка, а не
// паника: команда в устаревший/битый узел должна откатываться штатно (план 149).
func TestNodeByID_Errors(t *testing.T) {
	doc, err := Load([]byte(elemSample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, bad := range []string{"", "elements.9", "elements.0.children.5", "form.nope", "elements.0.bogus"} {
		if _, err := doc.NodeByID(bad); err == nil {
			t.Errorf("ожидалась ошибка для node-id %q, получено nil", bad)
		}
	}
}
