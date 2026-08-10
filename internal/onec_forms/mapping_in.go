package onec_forms

import (
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// NormalizeForImport преобразует «сырой» IR из reader_xml.go в нормализованный
// IR для записи в OneBase YAML.
//
// На входе IR хранит имена элементов «как в XML 1С» (InputField, UsualGroup,
// Pages, ColumnGroup и т.д.) и события «как в XML 1С» (OnOpen, OnChange).
// На выходе:
//   - imена элементов заменены на OneBase-канон (ПолеВвода, ГруппаФормы, …);
//   - события заменены на OneBase-канон (ПриОткрытии, ПриИзменении, …);
//   - типы реквизитов уже нормализованы reader_xml-ом через Type1CToOneBase;
//   - Decoration становится Надпись + props.decoration=true;
//   - CommandBarButton (Button с props.Type=CommandBarButton) помечается
//     props.in_command_bar=true.
//
// Возвращает дополнительные предупреждения (например, для неизвестных
// событий или элементов без 1:1 соответствия).
func NormalizeForImport(form *IRForm) []Warning {
	if form == nil {
		return nil
	}
	var warns Warnings

	// Form-level events: OnOpen → ПриОткрытии и т.д.
	form.Events = normalizeEventMap(form.Events, "", &warns)

	// Дерево элементов.
	for _, el := range form.Elements {
		normalizeElement(el, &warns)
	}
	if form.AutoCommandBar != nil {
		for _, btn := range form.AutoCommandBar.Buttons {
			// у кнопок ACB props["Type"]=CommandBarButton — оставляем как есть,
			// эти кнопки не попадают в дерево Elements.
			if btn.Title != nil && len(btn.Title) == 0 {
				btn.Title = nil
			}
		}
	}

	return []Warning(warns)
}

// normalizeElement рекурсивно нормализует один элемент.
func normalizeElement(el *IRElement, warns *Warnings) {
	if el == nil {
		return
	}

	// Декорация — это LabelField с пометкой. В выгрузках встречаются оба
	// написания: <Decoration> и <LabelDecoration> (реальные формы 1С пишут
	// второе — на нём импорт спотыкался и оставлял неизвестный вид).
	// alreadyMapped — вид уже приведён к каноническому имени OneBase явным
	// правилом ниже. Без этого флага такой элемент попадал в общий маппинг
	// «1С → OneBase», не находился там (потому что имя уже наше) и получал
	// ложное W010 «без соответствия».
	alreadyMapped := false

	if el.Kind == "Decoration" || el.Kind == "LabelDecoration" {
		el.Kind = string(metadata.FormElementLabel)
		if el.Props == nil {
			el.Props = map[string]any{}
		}
		el.Props["decoration"] = true
		alreadyMapped = true
	}

	// Popup — всплывающая группа (подменю командной панели). Прямого аналога
	// нет: в вебе меню раскрывается само. Переносим содержимое как обычную
	// группу и помечаем — при экспорте обратно пометка вернёт Popup, а до тех
	// пор элементы группы видны, а не теряются вместе с неизвестным видом.
	if el.Kind == "Popup" {
		el.Kind = string(metadata.FormElementGroupBox)
		if el.Props == nil {
			el.Props = map[string]any{}
		}
		el.Props["popup"] = true
		alreadyMapped = true
	}

	// CommandBarButton (определяется по полю <Type>CommandBarButton</Type>
	// внутри Button) — внутри обычной формы это не встречается на верхнем
	// уровне ChildItems, но возможно во вложенных таблицах/группах.
	if el.Kind == "Button" {
		if t, ok := el.Props["Type"].(string); ok && t == "CommandBarButton" {
			if el.Props == nil {
				el.Props = map[string]any{}
			}
			el.Props["in_command_bar"] = true
		}
	}

	// Маппинг типа элемента: InputField → ПолеВвода и т.д.
	if mapped, ok := Element1CToOneBase(el.Kind); ok {
		el.Kind = string(mapped)
	} else if !alreadyMapped {
		// Неизвестный — оставляем имя XML и эмитим W010.
		warns.Add(Warning{
			Severity: SeverityWarn,
			Code:     W010_UnknownElement,
			Element:  el.Name,
			Field:    el.Kind,
			Message:  "элемент XML 1С без соответствия в OneBase, оставлен в Kind как есть",
		})
	}

	// События элемента: OnChange → ПриИзменении и т.д.
	el.Events = normalizeEventMap(el.Events, el.Name, warns)

	// Дети — рекурсивно.
	for _, c := range el.Children {
		normalizeElement(c, warns)
	}
}

// normalizeEventMap преобразует ключи мапы из 1С-имени в OneBase-канон.
// Если для какого-то события нет 1:1 аналога — складываем в Props через
// служебный ключ events_unmapped и эмитим W030.
func normalizeEventMap(in map[string]string, ownerName string, warns *Warnings) map[string]string {
	if len(in) == 0 {
		return in
	}
	out := make(map[string]string, len(in))
	var unmapped map[string]string
	for k, v := range in {
		if mapped, ok := Event1CToOneBase(k); ok {
			out[string(mapped)] = v
			continue
		}
		// Событие без аналога: оставляем имя как есть в out (UI/рендерер
		// просто не вызовет такой обработчик), но эмитим W030.
		if unmapped == nil {
			unmapped = map[string]string{}
		}
		unmapped[k] = v
		out[k] = v
		warns.Add(Warning{
			Severity: SeverityInfo,
			Code:     W030_UnmappedEvent,
			Element:  ownerName,
			Field:    k,
			Message:  "событие 1С без 1:1 аналога в OneBase, сохранено как есть",
		})
	}
	_ = unmapped // зарезервировано на случай если потребуется хранить отдельно
	return out
}

// ToFormModule конвертирует нормализованный IR в metadata.FormModule.
// Этот объект уже можно (a) записать в YAML через writer_yaml, или
// (b) сразу отдать рантайму OneBase для рендеринга.
func ToFormModule(form *IRForm) *metadata.FormModule {
	if form == nil {
		return nil
	}
	fm := &metadata.FormModule{
		EntityName:             form.Entity,
		Name:                   form.Name,
		Kind:                   form.Kind,
		LayoutKind:             metadata.FormLayoutManaged,
		Title:                  map[string]string(form.Title),
		OriginalID:             form.OriginalID,
		AutoSaveDataInSettings: form.AutoSaveDataInSettings,
		VerticalScroll:         form.VerticalScroll,
		Handlers:               irEventsToFormEvents(form.Events),
		Procedures:             map[string]*metadata.FormProcedure{},
	}

	for _, a := range form.Attributes {
		fm.Attributes = append(fm.Attributes, irAttributeToMeta(a))
	}
	for _, c := range form.Commands {
		fm.Commands = append(fm.Commands, irCommandToMeta(c))
	}
	if form.AutoCommandBar != nil {
		fm.AutoCommandBar = irCommandBarToMeta(form.AutoCommandBar)
	}
	for _, el := range form.Elements {
		fm.Elements = append(fm.Elements, irElementToMeta(el))
	}

	if len(form.UnknownTopLevel) > 0 {
		// Сохраняем неузнанные top-level узлы в OneCMeta — потом writer_yaml
		// сериализует их в oneC_meta.unknown_xml как base64.
		if fm.OneCMeta == nil {
			fm.OneCMeta = map[string]any{}
		}
		fm.OneCMeta["unknown_xml_count"] = len(form.UnknownTopLevel)
	}

	return fm
}

func irAttributeToMeta(a *IRAttribute) *metadata.FormAttribute {
	if a == nil {
		return nil
	}
	out := &metadata.FormAttribute{
		OriginalID:    a.OriginalID,
		Name:          a.Name,
		Title:         map[string]string(a.Title),
		TypeRef:       a.TypeRef,
		Length:        a.Length,
		Precision:     a.Precision,
		AllowedLength: a.AllowedLength,
		Save:          a.Save,
		FillingValue:  a.FillingValue,
		MainAttribute: a.MainAttribute,
		Props:         a.Props,
	}
	for _, col := range a.Columns {
		out.Columns = append(out.Columns, &metadata.FormAttributeColumn{
			OriginalID: col.OriginalID,
			Name:       col.Name,
			Title:      map[string]string(col.Title),
			TypeRef:    col.TypeRef,
			Length:     col.Length,
			Precision:  col.Precision,
			Props:      col.Props,
		})
	}
	return out
}

func irCommandToMeta(c *IRCommand) *metadata.FormCommand {
	if c == nil {
		return nil
	}
	return &metadata.FormCommand{
		OriginalID: c.OriginalID,
		Name:       c.Name,
		Title:      map[string]string(c.Title),
		Group:      c.Group,
		Picture:    c.Picture,
		Action:     c.Action,
		Props:      c.Props,
	}
}

func irCommandBarToMeta(b *IRCommandBar) *metadata.FormCommandBar {
	if b == nil {
		return nil
	}
	out := &metadata.FormCommandBar{
		OriginalID: b.OriginalID,
		Name:       b.Name,
		Visible:    b.Visible,
	}
	for _, btn := range b.Buttons {
		out.Buttons = append(out.Buttons, &metadata.FormCommandBarButton{
			OriginalID:     btn.OriginalID,
			Name:           btn.Name,
			Title:          map[string]string(btn.Title),
			CommandName:    btn.CommandName,
			Representation: btn.Representation,
			Picture:        btn.Picture,
		})
	}
	return out
}

func irElementToMeta(el *IRElement) *metadata.FormElement {
	if el == nil {
		return nil
	}
	out := &metadata.FormElement{
		ID:              el.ID,
		OriginalID:      el.OriginalID,
		Name:            el.Name,
		Kind:            metadata.FormElementType(el.Kind),
		Title:           el.Title.Get("ru"),
		TitleMap:        map[string]string(el.Title),
		DataPath:        el.DataPath,
		Picture:         el.Picture,
		ValuesPicture:   el.Values,
		Visible:         el.Visible,
		Enabled:         el.Enabled,
		Required:        el.Required,
		ReadOnly:        el.ReadOnly,
		Choice:          el.Choice,
		Width:           el.Width,
		Height:          el.Height,
		HorizontalAlign: el.HAlign,
		VerticalAlign:   el.VAlign,
		Hint:            el.Hint,
		Mask:            el.Mask,
		Handlers:        irEventsToFormEvents(el.Events),
		Props:           el.Props,
		UnknownXML:      el.UnknownXML,
	}
	for _, c := range el.Children {
		out.Children = append(out.Children, irElementToMeta(c))
	}
	return out
}

func irEventsToFormEvents(in map[string]string) map[metadata.FormEventType]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[metadata.FormEventType]string, len(in))
	for k, v := range in {
		out[metadata.FormEventType(k)] = v
	}
	return out
}

// DataPathPrefix вспомогательная: возвращает первый компонент пути
// "Объект.Контрагент" → "Объект". Используется при выборе шаблона
// рендеринга или диагностике.
func DataPathPrefix(p string) string {
	if i := strings.Index(p, "."); i > 0 {
		return p[:i]
	}
	return p
}
