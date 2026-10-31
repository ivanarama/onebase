package formdoc

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ivantit66/onebase/internal/metadata"
)

// ElementNode — элемент формы, декодированный из дерева yaml.Node вместе с его
// адресом (node-id). По node-id визуальный конструктор (#164) наводит правки на
// конкретный узел: SetProp/InsertElement/Move адресуются строкой вида
// "elements.2.children.0".
//
// El несёт декодированные поля элемента (kind/name/data_path/...) для рендера
// холста. Обходи дерево по Children (они несут node-id), а не по El.Children —
// последнее лишь побочный продукт декодирования и адресов не содержит.
type ElementNode struct {
	NodeID   string
	El       *metadata.FormElement
	Children []*ElementNode
}

const (
	elementsKey = "elements"
	childrenKey = "children"
)

// topMapping возвращает верхний mapping-узел документа (содержимое DocumentNode).
func (d *Doc) topMapping() *yaml.Node {
	n := &d.root
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0]
	}
	return n
}

// mappingValue возвращает value-узел по ключу в mapping-узле, или nil.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// Elements декодирует дерево элементов формы вместе с node-id каждого элемента.
func (d *Doc) Elements() ([]*ElementNode, error) {
	seq := mappingValue(d.topMapping(), elementsKey)
	return elementsFromSeq(seq, elementsKey)
}

func elementsFromSeq(seq *yaml.Node, prefix string) ([]*ElementNode, error) {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil, nil
	}
	out := make([]*ElementNode, 0, len(seq.Content))
	for i, item := range seq.Content {
		nodeID := prefix + "." + strconv.Itoa(i)
		var el metadata.FormElement
		if err := item.Decode(&el); err != nil {
			return nil, fmt.Errorf("formdoc: декод элемента %s: %w", nodeID, err)
		}
		en := &ElementNode{NodeID: nodeID, El: &el}
		if childSeq := mappingValue(item, childrenKey); childSeq != nil {
			kids, err := elementsFromSeq(childSeq, nodeID+"."+childrenKey)
			if err != nil {
				return nil, err
			}
			en.Children = kids
		}
		out = append(out, en)
	}
	return out, nil
}

// FormMeta — корневые свойства формы для панели «Свойства формы» (follow-up #164
// batch B2/B3). Заголовок и вид формы лежат внутри блока form:, а события и
// действия уровня формы — в верхнем mapping-узле рядом с form/elements.
type FormMeta struct {
	TitleRU string
	Kind    string
	Events  map[string]string
	Actions map[string]bool // имя действия → visible (по умолчанию true)
	// RefCardButton — form.ref_card_button (по умолчанию true: кнопка есть).
	RefCardButton bool
	// RefCardAdminOnly — form.ref_card_button_admin_only (#1876).
	RefCardAdminOnly bool
}

// FormMeta декодирует корневые свойства формы для панели свойств.
func (d *Doc) FormMeta() (FormMeta, error) {
	fm := FormMeta{RefCardButton: true}
	top := d.topMapping()
	if f := mappingValue(top, "form"); f != nil {
		var block struct {
			Title            map[string]string `yaml:"title"`
			Kind             string            `yaml:"kind"`
			RefCardButton    *bool             `yaml:"ref_card_button"`
			RefCardAdminOnly bool              `yaml:"ref_card_button_admin_only"`
		}
		if err := f.Decode(&block); err != nil {
			return fm, fmt.Errorf("formdoc: декод form: %w", err)
		}
		fm.Kind = block.Kind
		fm.RefCardButton = block.RefCardButton == nil || *block.RefCardButton
		fm.RefCardAdminOnly = block.RefCardAdminOnly
		if block.Title != nil {
			fm.TitleRU = block.Title["ru"]
		}
	}
	if ev := mappingValue(top, "events"); ev != nil {
		_ = ev.Decode(&fm.Events)
	}
	if ac := mappingValue(top, "actions"); ac != nil {
		var raw map[string]struct {
			Visible *bool `yaml:"visible"`
		}
		if err := ac.Decode(&raw); err == nil {
			fm.Actions = make(map[string]bool, len(raw))
			for k, v := range raw {
				fm.Actions[k] = v.Visible == nil || *v.Visible
			}
		}
	}
	return fm, nil
}

// NodeByID разрешает node-id в узел элемента (mapping). Сегменты чередуются:
// ключ mapping-узла либо индекс sequence-узла. Несуществующий путь — ошибка.
func (d *Doc) NodeByID(id string) (*yaml.Node, error) {
	if id == "" {
		return nil, fmt.Errorf("formdoc: пустой node-id")
	}
	cur := d.topMapping()
	for _, seg := range strings.Split(id, ".") {
		if idx, err := strconv.Atoi(seg); err == nil {
			if cur == nil || cur.Kind != yaml.SequenceNode || idx < 0 || idx >= len(cur.Content) {
				return nil, fmt.Errorf("formdoc: node-id %q: индекс %d вне диапазона", id, idx)
			}
			cur = cur.Content[idx]
			continue
		}
		next := mappingValue(cur, seg)
		if next == nil {
			return nil, fmt.Errorf("formdoc: node-id %q: ключ %q не найден", id, seg)
		}
		cur = next
	}
	return cur, nil
}
