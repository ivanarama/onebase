package onec_forms

import (
	"encoding/base64"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// WriteFormYAML сериализует FormModule в .form.yaml по схеме onebase.form/v1.
// Порядок секций фиксирован промежуточной структурой formYAMLOut: schema,
// form, attributes, commands, command_bar, elements, events, oneC_meta.
//
// Использует промежуточную структуру (а не yaml.Marshal напрямую на
// FormModule) ради:
//   - стабильного порядка ключей в schema-style выводе,
//   - явного контроля имён (schema, form, …) который не совпадает с полями.
func WriteFormYAML(form *IRForm, dstPath string) error {
	doc := buildYAMLDoc(form)

	data, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("yaml.Marshal: %w", err)
	}
	if err := os.WriteFile(dstPath, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dstPath, err)
	}
	return nil
}

// formYAMLOut — промежуточная структура для сериализации в YAML.
// Зеркало formYAMLDoc из managed_form_loader.go, но с дополнительными
// полями (schema, oneC_meta). Поля идут в порядке, в котором их сериализует yaml.v3.
type formYAMLOut struct {
	Schema string `yaml:"schema"`

	Form struct {
		Name                   string            `yaml:"name"`
		Kind                   string            `yaml:"kind"`
		Entity                 string            `yaml:"entity"`
		Title                  map[string]string `yaml:"title,omitempty"`
		OriginalID             string            `yaml:"original_id,omitempty"`
		AutoSaveDataInSettings bool              `yaml:"auto_save_settings,omitempty"`
		VerticalScroll         string            `yaml:"vertical_scroll,omitempty"`
	} `yaml:"form"`

	Attributes []yamlAttribute   `yaml:"attributes,omitempty"`
	Commands   []yamlCommand     `yaml:"commands,omitempty"`
	CommandBar *yamlCommandBar   `yaml:"command_bar,omitempty"`
	Elements   []yamlElement     `yaml:"elements,omitempty"`
	Events     map[string]string `yaml:"events,omitempty"`
	Resources  []yamlResource    `yaml:"resources,omitempty"`
	OneCMeta   map[string]any    `yaml:"oneC_meta,omitempty"`
}

type yamlAttribute struct {
	Name          string                `yaml:"name"`
	Type          string                `yaml:"type"`
	Title         map[string]string     `yaml:"title,omitempty"`
	OriginalID    string                `yaml:"original_id,omitempty"`
	Save          bool                  `yaml:"save,omitempty"`
	Main          bool                  `yaml:"main,omitempty"`
	Length        int                   `yaml:"length,omitempty"`
	Precision     int                   `yaml:"precision,omitempty"`
	AllowedLength string                `yaml:"allowed_length,omitempty"`
	FillingValue  string                `yaml:"filling_value,omitempty"`
	Columns       []yamlAttributeColumn `yaml:"columns,omitempty"`
}

type yamlAttributeColumn struct {
	Name       string            `yaml:"name"`
	Type       string            `yaml:"type"`
	Title      map[string]string `yaml:"title,omitempty"`
	OriginalID string            `yaml:"original_id,omitempty"`
	Length     int               `yaml:"length,omitempty"`
	Precision  int               `yaml:"precision,omitempty"`
}

type yamlCommand struct {
	Name       string            `yaml:"name"`
	Title      map[string]string `yaml:"title,omitempty"`
	Action     string            `yaml:"action,omitempty"`
	Picture    string            `yaml:"picture,omitempty"`
	Group      string            `yaml:"group,omitempty"`
	OriginalID string            `yaml:"original_id,omitempty"`
}

type yamlCommandBar struct {
	Name       string                 `yaml:"name,omitempty"`
	Visible    bool                   `yaml:"visible,omitempty"`
	OriginalID string                 `yaml:"original_id,omitempty"`
	Buttons    []yamlCommandBarButton `yaml:"buttons,omitempty"`
}

type yamlCommandBarButton struct {
	Name           string            `yaml:"name"`
	Command        string            `yaml:"command,omitempty"`
	Title          map[string]string `yaml:"title,omitempty"`
	Representation string            `yaml:"representation,omitempty"`
	Picture        string            `yaml:"picture,omitempty"`
	OriginalID     string            `yaml:"original_id,omitempty"`
}

type yamlElement struct {
	Kind          string            `yaml:"kind"`
	Name          string            `yaml:"name,omitempty"`
	Title         map[string]string `yaml:"title,omitempty"`
	DataPath      string            `yaml:"data_path,omitempty"`
	OriginalID    string            `yaml:"original_id,omitempty"`
	Picture       string            `yaml:"picture,omitempty"`
	ValuesPicture string            `yaml:"values_picture,omitempty"`
	ReadOnly      bool              `yaml:"readonly,omitempty"`
	Required      bool              `yaml:"required,omitempty"`
	Choice        bool              `yaml:"choice,omitempty"`
	Width         int               `yaml:"width,omitempty"`
	Height        int               `yaml:"height,omitempty"`
	Hint          string            `yaml:"hint,omitempty"`
	Mask          string            `yaml:"mask,omitempty"`
	Events        map[string]string `yaml:"events,omitempty"`
	Props         map[string]any    `yaml:"props,omitempty"`
	Children      []yamlElement     `yaml:"children,omitempty"`
	UnknownXMLB64 string            `yaml:"unknown_xml_b64,omitempty"`
}

type yamlResource struct {
	Path         string `yaml:"path"`
	Element      string `yaml:"element,omitempty"`
	OriginalName string `yaml:"original_name,omitempty"`
}

// buildYAMLDoc собирает промежуточную структуру для сериализации.
func buildYAMLDoc(form *IRForm) formYAMLOut {
	var doc formYAMLOut
	doc.Schema = "onebase.form/v1"

	doc.Form.Name = form.Name
	doc.Form.Kind = form.Kind
	doc.Form.Entity = form.Entity
	doc.Form.Title = nonEmptyTitle(form.Title)
	doc.Form.OriginalID = form.OriginalID
	doc.Form.AutoSaveDataInSettings = form.AutoSaveDataInSettings
	doc.Form.VerticalScroll = form.VerticalScroll

	for _, a := range form.Attributes {
		doc.Attributes = append(doc.Attributes, attrToYAML(a))
	}
	for _, c := range form.Commands {
		doc.Commands = append(doc.Commands, cmdToYAML(c))
	}
	if form.AutoCommandBar != nil {
		bar := commandBarToYAML(form.AutoCommandBar)
		doc.CommandBar = &bar
	}
	for _, el := range form.Elements {
		doc.Elements = append(doc.Elements, elementToYAML(el))
	}

	if len(form.Events) > 0 {
		doc.Events = form.Events
	}

	if len(form.Resources) > 0 {
		for _, r := range form.Resources {
			doc.Resources = append(doc.Resources, yamlResource{
				Path:         r.Path,
				Element:      r.ElementName,
				OriginalName: r.OriginalName,
			})
		}
	}

	if len(form.UnknownTopLevel) > 0 {
		if doc.OneCMeta == nil {
			doc.OneCMeta = map[string]any{}
		}
		if form.Version != "" {
			doc.OneCMeta["version"] = form.Version
		}
		unknowns := make([]map[string]string, 0, len(form.UnknownTopLevel))
		for _, u := range form.UnknownTopLevel {
			unknowns = append(unknowns, map[string]string{
				"element": u.OwnerElement,
				"xml_b64": base64.StdEncoding.EncodeToString(u.XML),
			})
		}
		doc.OneCMeta["unknown_xml"] = unknowns
	} else if form.Version != "" {
		doc.OneCMeta = map[string]any{"version": form.Version}
	}

	return doc
}

func attrToYAML(a *IRAttribute) yamlAttribute {
	out := yamlAttribute{
		Name:          a.Name,
		Type:          a.TypeRef,
		Title:         nonEmptyTitle(a.Title),
		OriginalID:    a.OriginalID,
		Save:          a.Save,
		Main:          a.MainAttribute,
		Length:        a.Length,
		Precision:     a.Precision,
		AllowedLength: a.AllowedLength,
		FillingValue:  a.FillingValue,
	}
	for _, c := range a.Columns {
		out.Columns = append(out.Columns, yamlAttributeColumn{
			Name:       c.Name,
			Type:       c.TypeRef,
			Title:      nonEmptyTitle(c.Title),
			OriginalID: c.OriginalID,
			Length:     c.Length,
			Precision:  c.Precision,
		})
	}
	return out
}

func cmdToYAML(c *IRCommand) yamlCommand {
	return yamlCommand{
		Name:       c.Name,
		Title:      nonEmptyTitle(c.Title),
		Action:     c.Action,
		Picture:    c.Picture,
		Group:      c.Group,
		OriginalID: c.OriginalID,
	}
}

func commandBarToYAML(b *IRCommandBar) yamlCommandBar {
	out := yamlCommandBar{
		Name:       b.Name,
		Visible:    b.Visible,
		OriginalID: b.OriginalID,
	}
	for _, btn := range b.Buttons {
		out.Buttons = append(out.Buttons, yamlCommandBarButton{
			Name:           btn.Name,
			Command:        btn.CommandName,
			Title:          nonEmptyTitle(btn.Title),
			Representation: btn.Representation,
			Picture:        btn.Picture,
			OriginalID:     btn.OriginalID,
		})
	}
	return out
}

func elementToYAML(el *IRElement) yamlElement {
	out := yamlElement{
		Kind:          el.Kind,
		Name:          el.Name,
		Title:         nonEmptyTitle(el.Title),
		DataPath:      el.DataPath,
		OriginalID:    el.OriginalID,
		Picture:       el.Picture,
		ValuesPicture: el.Values,
		ReadOnly:      el.ReadOnly,
		Required:      el.Required,
		Choice:        el.Choice,
		Width:         el.Width,
		Height:        el.Height,
		Hint:          el.Hint,
		Mask:          el.Mask,
	}
	if len(el.Events) > 0 {
		out.Events = el.Events
	}
	if len(el.Props) > 0 {
		out.Props = el.Props
	}
	for _, c := range el.Children {
		out.Children = append(out.Children, elementToYAML(c))
	}
	if len(el.UnknownXML) > 0 {
		out.UnknownXMLB64 = base64.StdEncoding.EncodeToString(el.UnknownXML)
	}
	return out
}

// nonEmptyTitle возвращает map, либо nil если там нет значений с непустым контентом.
// Это нужно, чтобы в YAML не вылезали `title: {}` для элементов без подписи.
func nonEmptyTitle(t IRTitle) map[string]string {
	if len(t) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range t {
		if v != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// WriteFormOS пишет .form.os рядом с .form.yaml. Используется фасадом
// ImportFromOneC: сначала собираем процедуры через ReadBSL → EmitDSLSource,
// затем записываем как один файл.
func WriteFormOS(source, dstPath string) error {
	return os.WriteFile(dstPath, []byte(source), 0o644)
}
