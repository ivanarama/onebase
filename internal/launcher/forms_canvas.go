package launcher

import (
	"bytes"
	"fmt"
	"html"
	"strings"

	"github.com/ivantit66/onebase/internal/formdoc"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Интерактивный рендер холста визуального конструктора форм (#164, слайс 4).
//
// В отличие от read-only renderManagedFormPreview, холст несёт адресацию: каждый
// элемент помечен data-node-id (путь в дереве yaml.Node), между детьми контейнеров
// вставлены drop-зоны (data-parent + data-index) для приёма реквизита из палитры,
// а выбранный элемент подсвечивается классом fc-selected. Клиент (слайс 6) вешает
// на это drag/drop, клик-выбор и панель свойств; правки уходят на /forms/edit-op.
//
// Возвращается HTML-фрагмент (не iframe): холст встроен в страницу редактора, так
// что ванильный JS напрямую им управляет.

// renderFormCanvas рендерит дерево элементов формы как интерактивный холст.
// selectedID — node-id выбранного элемента (пустой = ничего не выбрано).
func renderFormCanvas(doc *formdoc.Doc, selectedID string) (string, error) {
	els, err := doc.Elements()
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, `<div class="fc-canvas" data-selected="%s">`, html.EscapeString(selectedID))
	renderCanvasChildren(&buf, "", els, selectedID)
	buf.WriteString(`</div>`)
	return buf.String(), nil
}

// renderCanvasChildren рисует последовательность детей контейнера parentID,
// чередуя drop-зоны: перед каждым ребёнком и одну после последнего (N+1 зон).
func renderCanvasChildren(buf *bytes.Buffer, parentID string, els []*formdoc.ElementNode, selectedID string) {
	buf.WriteString(`<div class="fc-children">`)
	for i, en := range els {
		renderDropZone(buf, parentID, i)
		renderCanvasElement(buf, en, selectedID)
	}
	renderDropZone(buf, parentID, len(els))
	buf.WriteString(`</div>`)
}

// renderDropZone — точка приёма реквизита: вставка в контейнер parentID на
// позицию index. Пустой parentID — верхний уровень (elements).
func renderDropZone(buf *bytes.Buffer, parentID string, index int) {
	fmt.Fprintf(buf, `<div class="fc-drop" data-parent="%s" data-index="%d"></div>`,
		html.EscapeString(parentID), index)
}

// renderPageDropZone — точка приёма страницы в набор СтраницыФормы: вставка
// kind:Страница в контейнер pagesID на позицию index. Отдельный класс
// fc-drop-page принимает только структурные элементы (страницы), чтобы реквизит
// не попадал прямым ребёнком в Pages (follow-up #164, слайс C).
func renderPageDropZone(buf *bytes.Buffer, pagesID string, index int) {
	fmt.Fprintf(buf, `<div class="fc-drop-page" data-parent="%s" data-index="%d">+ страница</div>`,
		html.EscapeString(pagesID), index)
}

// elWrapClass собирает классы обёртки элемента, добавляя fc-selected выбранному.
func elWrapClass(base, nodeID, selectedID string) string {
	cls := "fc-el " + base
	if nodeID != "" && nodeID == selectedID {
		cls += " fc-selected"
	}
	return cls
}

func renderCanvasElement(buf *bytes.Buffer, en *formdoc.ElementNode, selectedID string) {
	if en == nil || en.El == nil {
		return
	}
	el := en.El
	id := en.NodeID
	kind := html.EscapeString(string(el.Kind))
	title := html.EscapeString(canvasTitle(el.Name, el.TitleMap, el.Title))

	switch el.Kind {
	case metadata.FormElementGroupBox, metadata.FormElementCommandBar:
		groupClass := "fc-group"
		if strings.EqualFold(el.Orientation, "horizontal") {
			groupClass += " fc-group-horizontal"
		}
		fmt.Fprintf(buf, `<fieldset class="%s" data-node-id="%s" data-kind="%s"><legend class="fc-pick">%s</legend>`,
			elWrapClass(groupClass, id, selectedID), id, kind, title)
		renderCanvasChildren(buf, id, en.Children, selectedID)
		buf.WriteString(`</fieldset>`)

	case metadata.FormElementPages:
		fmt.Fprintf(buf, `<div class="%s" data-node-id="%s" data-kind="%s">`,
			elWrapClass("fc-pages", id, selectedID), id, kind)
		for i, p := range en.Children {
			renderPageDropZone(buf, id, i)
			if p == nil || p.El == nil {
				continue
			}
			if p.El.Kind != metadata.FormElementPage {
				// В набор страниц затесался не-Страница (ошибка структуры, напр.
				// случайно брошенный СтраницыФормы) — рисуем обычным рендером,
				// чтобы было видно, что это не вкладка, а не маскировать под неё.
				renderCanvasElement(buf, p, selectedID)
				continue
			}
			ptitle := html.EscapeString(canvasTitle(p.El.Name, p.El.TitleMap, p.El.Title))
			fmt.Fprintf(buf, `<div class="%s" data-node-id="%s" data-kind="%s"><div class="fc-tab fc-pick">%s</div>`,
				elWrapClass("fc-page", p.NodeID, selectedID), p.NodeID, html.EscapeString(string(p.El.Kind)), ptitle)
			renderCanvasChildren(buf, p.NodeID, p.Children, selectedID)
			buf.WriteString(`</div>`)
		}
		renderPageDropZone(buf, id, len(en.Children))
		buf.WriteString(`</div>`)

	case metadata.FormElementPage:
		// Отдельная страница (вне набора СтраницыФормы — редкий случай) рисуется
		// как блок-вкладка со своими детьми, а не как «неизвестный» элемент.
		// Страницы внутри СтраницыФормы рендерит ветка Pages выше.
		fmt.Fprintf(buf, `<div class="%s" data-node-id="%s" data-kind="%s"><div class="fc-tab fc-pick">%s</div>`,
			elWrapClass("fc-page", id, selectedID), id, kind, title)
		renderCanvasChildren(buf, id, en.Children, selectedID)
		buf.WriteString(`</div>`)

	case metadata.FormElementField, metadata.FormElementDatePicker, metadata.FormElementInputList, metadata.FormElementFormField:
		req := ""
		if el.Required {
			req = ` <span class="fc-req">*</span>`
		}
		field := lastSegment(el.DataPath)
		if field == "" {
			field = el.Name
		}
		ro := ""
		if el.ReadOnly {
			ro = " readonly"
		}
		fmt.Fprintf(buf, `<div class="%s fc-pick" data-node-id="%s" data-kind="%s"><label>%s%s</label><input type="text" disabled%s placeholder="%s"></div>`,
			elWrapClass("fc-field", id, selectedID), id, kind, title, req, ro, html.EscapeString(field))

	case metadata.FormElementCheckbox:
		fmt.Fprintf(buf, `<div class="%s fc-pick" data-node-id="%s" data-kind="%s"><input type="checkbox" disabled> <span>%s</span></div>`,
			elWrapClass("fc-check", id, selectedID), id, kind, title)

	case metadata.FormElementSwitch:
		// Переключатель: набор radio (или список) по Options/перечислению. Выбор
		// кликом по обёртке; значения и представление правятся в панели свойств.
		fmt.Fprintf(buf, `<div class="%s fc-pick" data-node-id="%s" data-kind="%s"><label>%s</label><div class="fc-switch">`,
			elWrapClass("fc-field", id, selectedID), id, kind, title)
		if len(el.Options) == 0 {
			buf.WriteString(`<span class="fc-cols-empty">значения по перечислению (или задайте в свойствах)</span>`)
		}
		for _, o := range el.Options {
			fmt.Fprintf(buf, `<label class="fc-opt"><input type="radio" disabled> %s</label>`, html.EscapeString(o.Label()))
		}
		buf.WriteString(`</div></div>`)

	case metadata.FormElementLabel:
		fmt.Fprintf(buf, `<div class="%s fc-pick" data-node-id="%s" data-kind="%s">%s</div>`,
			elWrapClass("fc-label", id, selectedID), id, kind, title)

	case metadata.FormElementButton:
		fmt.Fprintf(buf, `<div class="%s fc-pick" data-node-id="%s" data-kind="%s"><button type="button" disabled>%s</button></div>`,
			elWrapClass("fc-btn", id, selectedID), id, kind, title)

	case metadata.FormElementPicture:
		fmt.Fprintf(buf, `<div class="%s fc-pick" data-node-id="%s" data-kind="%s"><div class="fc-pic">&#x1F5BC; %s</div></div>`,
			elWrapClass("fc-pic-wrap", id, selectedID), id, kind, title)

	case metadata.FormElementTable:
		fmt.Fprintf(buf, `<div class="%s fc-pick" data-node-id="%s" data-kind="%s"><div class="fc-tp">▦ %s</div></div>`,
			elWrapClass("fc-table", id, selectedID), id, kind, title)

	case metadata.FormElementTablePart:
		// Заголовок ТЧ (выбирается кликом) + колонки-дети kind:Колонка, каждая со
		// своим node-id, чтобы их можно было выбирать/удалять/переставлять и
		// редактировать состав (follow-up #164, слайсы D1/D2).
		fmt.Fprintf(buf, `<div class="%s" data-node-id="%s" data-kind="%s"><div class="fc-tp fc-pick">▦ %s</div><div class="fc-cols">`,
			elWrapClass("fc-table", id, selectedID), id, kind, title)
		// Без явных колонок рантайм показывает ВСЕ реквизиты ТЧ
		// (managedTPColumnPlan: «ничего не выбрано» = «показать всё»). Холст
		// метаданных сущности не знает и перечислить их не может, но обязан
		// назвать состояние верно: прежнее «колонки не выбраны» читалось как
		// «ТЧ пустая» и толкало поставить галочку — а первая же галочка
		// переключает смысл с «все» на «только отмеченные» и убирает
		// остальные колонки (#1123).
		if !hasColumnChild(en) {
			buf.WriteString(`<span class="fc-cols-empty" title="Состав не задан: показываются все реквизиты табличной части. Задать состав явно можно галочками в свойствах.">все колонки (по умолчанию)</span>`)
		}
		for _, c := range en.Children {
			if c == nil || c.El == nil {
				continue
			}
			fmt.Fprintf(buf, `<span class="%s fc-pick" data-node-id="%s" data-kind="%s">%s</span>`,
				elWrapClass("fc-col", c.NodeID, selectedID), c.NodeID, html.EscapeString(string(c.El.Kind)), html.EscapeString(columnLabel(c.El)))
		}
		buf.WriteString(`</div></div>`)

	default:
		fmt.Fprintf(buf, `<div class="%s fc-pick" data-node-id="%s" data-kind="%s">%s <span class="fc-kind">%s</span></div>`,
			elWrapClass("fc-unknown", id, selectedID), id, kind, title, kind)
	}
}

// hasColumnChild — есть ли у элемента хоть один ребёнок kind: Колонка. Считаем
// именно их, а не всех детей: рантайм (managedTPColumnPlan) переходит в режим
// «показать выбранное» тоже только по ним, и холст обязан говорить о том же
// признаке, иначе состояния разъезжаются на ТЧ с посторонним ребёнком.
func hasColumnChild(en *formdoc.ElementNode) bool {
	for _, c := range en.Children {
		if c != nil && c.El != nil && c.El.Kind == metadata.FormElementColumn {
			return true
		}
	}
	return false
}

// canvasElementInfo — редактируемые поля элемента для панели свойств клиента.
// Плоская карта node-id → info отдаётся вместе с холстом, чтобы клик по элементу
// открывал панель без повторного парсинга YAML в браузере.
type canvasElementInfo struct {
	NodeID   string `json:"nodeId"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	TitleRU  string `json:"titleRu"`
	DataPath string `json:"dataPath"`
	// Field — ключ `field:` элемента. Рантайм сопоставляет колонку реквизиту по
	// data_path, field ИЛИ имени (managedTPFieldIndexForColumn), а в модель
	// холста поле не попадало вовсе — поэтому колонка, объявленная как
	// `field: Цена`, в рантайме была видна, а в панели свойств стояла без
	// галочки (#1123).
	Field     string `json:"field"`
	Required  bool   `json:"required"`
	ReadOnly  bool   `json:"readonly"`
	Hint      string `json:"hint"`
	Container bool   `json:"container"`
	// Свойства batch A (выводятся в панель только там, где влияют на рантайм).
	Mask      string `json:"mask"`      // ПолеВвода: проверка значения (regexp → pattern)
	InputMask string `json:"inputMask"` // ПолеВвода: шаблон ввода (#763), подставляет разделители
	FileType  bool   `json:"fileType"`  // ПолеВвода: Type == "file" (файловое поле)
	Picture   string `json:"picture"`   // ПолеКартинки: путь к картинке
	Width     int    `json:"width"`     // ПолеКартинки: ширина
	Height    int    `json:"height"`    // ПолеКартинки: высота
	NoGrid    bool   `json:"noGrid"`    // ТабличнаяЧасть: простая таблица вместо SlickGrid
	AutoSum   bool   `json:"autoSum"`   // ТабличнаяЧасть: Сумма = Количество × Цена по именам колонок
	// Orientation — раскладка детей контейнера: ""/"vertical" или "horizontal".
	Orientation string `json:"orientation"`
	// События элемента (batch B1): имя события → имя процедуры в .form.os.
	Events map[string]string `json:"events"`
	// Набор значений Переключателя/ПолеСписка (batch C1).
	Options []canvasOption `json:"options"`
	View    string         `json:"view"` // radio|select
}

// canvasOption — значение набора Переключателя для редактора опций (C1).
type canvasOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// canvasModel разворачивает дерево формы в плоскую карту node-id → редактируемые
// поля — источник данных для панели свойств визуального конструктора (#164).
func canvasModel(doc *formdoc.Doc) (map[string]canvasElementInfo, error) {
	els, err := doc.Elements()
	if err != nil {
		return nil, err
	}
	m := make(map[string]canvasElementInfo)
	var walk func(ens []*formdoc.ElementNode)
	walk = func(ens []*formdoc.ElementNode) {
		for _, en := range ens {
			el := en.El
			info := canvasElementInfo{
				NodeID:      en.NodeID,
				Kind:        string(el.Kind),
				Name:        el.Name,
				DataPath:    el.DataPath,
				Field:       el.FieldName,
				Required:    el.Required,
				ReadOnly:    el.ReadOnly,
				Hint:        el.Hint,
				Container:   el.IsContainer(),
				Mask:        el.Mask,
				InputMask:   el.InputMask,
				FileType:    el.Type == "file",
				Picture:     el.Picture,
				Width:       el.Width,
				Height:      el.Height,
				NoGrid:      el.NoGrid,
				AutoSum:     el.AutoSum,
				Orientation: el.Orientation,
				View:        el.View,
			}
			if el.TitleMap != nil {
				info.TitleRU = el.TitleMap["ru"]
			}
			if len(el.Handlers) > 0 {
				info.Events = make(map[string]string, len(el.Handlers))
				for k, v := range el.Handlers {
					info.Events[string(k)] = v
				}
			}
			for _, o := range el.Options {
				info.Options = append(info.Options, canvasOption{Value: o.ValueStr(), Label: o.Label()})
			}
			m[en.NodeID] = info
			walk(en.Children)
		}
	}
	walk(els)
	return m, nil
}

// columnLabel выбирает подпись колонки ТЧ для холста: ru-заголовок → поле
// (field или последний сегмент data_path) → имя.
func columnLabel(el *metadata.FormElement) string {
	if el.TitleMap != nil && el.TitleMap["ru"] != "" {
		return el.TitleMap["ru"]
	}
	if el.Title != "" {
		return el.Title
	}
	if el.FieldName != "" {
		return el.FieldName
	}
	if f := lastSegment(el.DataPath); f != "" {
		return f
	}
	return el.Name
}

// canvasTitle выбирает отображаемый заголовок элемента: ru-локаль → legacy
// Title → имя. Совпадает с приоритетом read-only предпросмотра.
func canvasTitle(name string, titleMap map[string]string, legacy string) string {
	if titleMap != nil && titleMap["ru"] != "" {
		return titleMap["ru"]
	}
	if legacy != "" {
		return legacy
	}
	return name
}
