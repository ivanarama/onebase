package onec_forms

import (
	"encoding/base64"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ReadFormYAML читает .form.yaml в IRForm. Дополняет управляемой формы
// уже нормализованные имена (OneBase-канон: ПолеВвода, ПриИзменении,
// CatalogRef.X). Эту IR можно отдать в mapping_out + writer_xml для
// получения Form.xml.
//
// В отличие от managed_form_loader (который заполняет metadata.FormModule
// для рантайма OneBase), здесь мы строим нейтральный IR — те же поля,
// но без зависимости от пакета metadata.
func ReadFormYAML(path string) (*IRForm, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseFormYAMLBytes(data)
}

func parseFormYAMLBytes(data []byte) (*IRForm, error) {
	var doc formYAMLOut // используем ту же структуру, что и writer (writer_yaml.go)
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if doc.Schema != "" && doc.Schema != "onebase.form/v1" {
		return nil, fmt.Errorf("неподдерживаемая схема %q (ожидается onebase.form/v1)", doc.Schema)
	}

	form := &IRForm{
		Name:                   doc.Form.Name,
		Kind:                   doc.Form.Kind,
		Entity:                 doc.Form.Entity,
		Title:                  IRTitle(doc.Form.Title),
		OriginalID:             doc.Form.OriginalID,
		AutoSaveDataInSettings: doc.Form.AutoSaveDataInSettings,
		VerticalScroll:         doc.Form.VerticalScroll,
		Events:                 doc.Events,
	}
	for _, a := range doc.Attributes {
		form.Attributes = append(form.Attributes, yamlAttributeToIR(a))
	}
	for _, c := range doc.Commands {
		form.Commands = append(form.Commands, yamlCommandToIR(c))
	}
	if doc.CommandBar != nil {
		form.AutoCommandBar = yamlCommandBarToIR(doc.CommandBar)
	}
	for _, e := range doc.Elements {
		form.Elements = append(form.Elements, yamlElementToIR(e))
	}
	for _, r := range doc.Resources {
		form.Resources = append(form.Resources, IRResource{
			ElementName:  r.Element,
			Path:         r.Path,
			OriginalName: r.OriginalName,
		})
	}
	// oneC_meta: version + unknown_xml (base64).
	if doc.OneCMeta != nil {
		if v, ok := doc.OneCMeta["version"].(string); ok {
			form.Version = v
		}
		if raw, ok := doc.OneCMeta["unknown_xml"].([]any); ok {
			for _, item := range raw {
				m, _ := item.(map[string]any)
				if m == nil {
					continue
				}
				elem, _ := m["element"].(string)
				xb64, _ := m["xml_b64"].(string)
				xml, _ := base64.StdEncoding.DecodeString(xb64)
				form.UnknownTopLevel = append(form.UnknownTopLevel, &IRUnknownXML{
					OwnerElement: elem,
					XML:          xml,
				})
			}
		}
	}
	if form.Version == "" {
		form.Version = "2.20"
	}
	return form, nil
}

func yamlAttributeToIR(a yamlAttribute) *IRAttribute {
	out := &IRAttribute{
		Name:          a.Name,
		TypeRef:       a.Type,
		Title:         IRTitle(a.Title),
		OriginalID:    a.OriginalID,
		Save:          a.Save,
		MainAttribute: a.Main,
		Length:        a.Length,
		Precision:     a.Precision,
		AllowedLength: a.AllowedLength,
		FillingValue:  a.FillingValue,
	}
	for _, c := range a.Columns {
		out.Columns = append(out.Columns, &IRAttributeColumn{
			Name:       c.Name,
			TypeRef:    c.Type,
			Title:      IRTitle(c.Title),
			OriginalID: c.OriginalID,
			Length:     c.Length,
			Precision:  c.Precision,
		})
	}
	return out
}

func yamlCommandToIR(c yamlCommand) *IRCommand {
	return &IRCommand{
		Name:       c.Name,
		Title:      IRTitle(c.Title),
		Action:     c.Action,
		Picture:    c.Picture,
		Group:      c.Group,
		OriginalID: c.OriginalID,
	}
}

func yamlCommandBarToIR(b *yamlCommandBar) *IRCommandBar {
	out := &IRCommandBar{
		Name:       b.Name,
		Visible:    b.Visible,
		OriginalID: b.OriginalID,
	}
	for _, btn := range b.Buttons {
		out.Buttons = append(out.Buttons, &IRCommandBarButton{
			Name:           btn.Name,
			CommandName:    btn.Command,
			Title:          IRTitle(btn.Title),
			Representation: btn.Representation,
			Picture:        btn.Picture,
			OriginalID:     btn.OriginalID,
		})
	}
	return out
}

func yamlElementToIR(el yamlElement) *IRElement {
	out := &IRElement{
		Kind:       el.Kind,
		Name:       el.Name,
		Title:      IRTitle(el.Title),
		DataPath:   el.DataPath,
		OriginalID: el.OriginalID,
		Picture:    el.Picture,
		Values:     el.ValuesPicture,
		ReadOnly:   el.ReadOnly,
		Required:   el.Required,
		Choice:     el.Choice,
		Width:      el.Width,
		Height:     el.Height,
		Multiline:  el.Multiline,
		Hint:       el.Hint,
		Mask:       el.Mask,
		Events:     el.Events,
		Props:      el.Props,
		Visible:    true,
		Enabled:    true,
	}
	for _, c := range el.Children {
		out.Children = append(out.Children, yamlElementToIR(c))
	}
	if el.UnknownXMLB64 != "" {
		if x, err := base64.StdEncoding.DecodeString(el.UnknownXMLB64); err == nil {
			out.UnknownXML = x
		}
	}
	return out
}
