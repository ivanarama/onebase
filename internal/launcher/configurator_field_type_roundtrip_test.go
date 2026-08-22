package launcher

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// browserSubmit собирает то, что отправил бы БРАУЗЕР при нажатии «Сохранить» в
// форме с указанным action. Это принципиально: тест, который сам придумывает
// значения полей формы, проверяет обработчик на данных, которых интерфейс
// физически не может прислать, и потому пропускает целый класс дефектов —
// именно так дожил до пользователя #1090.
//
// Правила разметки воспроизводим по HTML: у <select> без multiple/size браузер
// отправляет значение помеченного selected пункта, а если такого нет — ПЕРВОГО.
// Отсюда и порча: у поля с типом, которого нет в списке, selected не проставлен
// ни у одного пункта, и форма уносит на сервер первый — «строка».
func browserSubmit(t *testing.T, page, actionSuffix string) url.Values {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatalf("разбор HTML: %v", err)
	}
	form := findForm(doc, actionSuffix)
	if form == nil {
		t.Fatalf("в разметке нет формы с action, оканчивающимся на %q", actionSuffix)
	}
	values := url.Values{}
	collectControls(form, values)
	return values
}

func attr(n *html.Node, name string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val, true
		}
	}
	return "", false
}

func findForm(n *html.Node, actionSuffix string) *html.Node {
	if n.Type == html.ElementNode && n.Data == "form" {
		if action, ok := attr(n, "action"); ok && strings.HasSuffix(action, actionSuffix) {
			return n
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if got := findForm(c, actionSuffix); got != nil {
			return got
		}
	}
	return nil
}

func collectControls(n *html.Node, values url.Values) {
	if n.Type == html.ElementNode {
		if _, disabled := attr(n, "disabled"); disabled {
			return
		}
		name, named := attr(n, "name")
		switch {
		case n.Data == "input" && named && name != "":
			typ, _ := attr(n, "type")
			val, _ := attr(n, "value")
			switch strings.ToLower(typ) {
			case "checkbox", "radio":
				if _, checked := attr(n, "checked"); checked {
					if val == "" {
						val = "on"
					}
					values.Add(name, val)
				}
			case "submit", "button", "reset", "image", "file":
				// кнопки без нажатия не отправляются, file в форме типов нет
			default:
				values.Add(name, val)
			}
		case n.Data == "select" && named && name != "":
			values.Add(name, selectedOption(n))
			return
		case n.Data == "textarea" && named && name != "":
			values.Add(name, textOf(n))
			return
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectControls(c, values)
	}
}

// selectedOption — значение, которое браузер отправит для <select>: пункт с
// selected, иначе первый пункт списка.
func selectedOption(sel *html.Node) string {
	first := ""
	found := false
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "option" {
			val, ok := attr(n, "value")
			if !ok {
				val = strings.TrimSpace(textOf(n))
			}
			if !found {
				first = val
				found = true
			}
			if _, isSel := attr(n, "selected"); isSel {
				first = val
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for c := sel.FirstChild; c != nil; c = c.NextSibling {
		walk(c)
	}
	return first
}

func textOf(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

// Заявка #1090: пользователь добавил в Номенклатуру реквизит «строка» и нажал
// «Сохранить типы полей» — после чего база перестала запускаться с ошибкой
// «tile_view.image field Фото must have type image».
//
// Причина: выпадающий список типа реквизита не знал ни image, ни richtext,
// поэтому у таких полей ни один пункт не помечался selected, браузер отправлял
// первый («строка»), а сохранение переписывало YAML отправленным типом без
// сверки с прежним. Правка ЛЮБОГО реквизита тихо превращала картинку в строку.
//
// Тест идёт тем же путём, что пользователь: рендерит настоящую страницу
// конфигуратора, снимает с неё поля формы как браузер и отправляет их в
// обработчик сохранения.
func TestSaveFields_KeepsTypesAbsentFromDropdown(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "catalogs", "Номенклатура.yaml", `name: Номенклатура
title: Номенклатура
fields:
  - name: Наименование
    type: string
  - name: Артикул
    type: string
  - name: Фото
    type: image
  - name: ПодробноеОписание
    type: richtext
tile_view:
  image: Фото
  title: Наименование
`)

	b, err := h.store.Get("test")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	data := h.loadCfgData(context.Background(), b, "tree")
	if data.Error != "" {
		t.Fatalf("конфигурация не загрузилась: %s", data.Error)
	}
	form := browserSubmit(t, renderCfgTree(t, data), "/configurator/fields")

	// Единственное действие пользователя поверх снятой формы — новый реквизит.
	form.Set("new_field.1.name", "Фото2")
	form.Set("new_field.1.type", "string")
	// Заодно проверяем, что картинку теперь можно и завести: до правки пункта
	// «картинка» в списке не было вовсе, то есть поле-картинку конфигуратор
	// умел только сломать, но не создать.
	form.Set("new_field.2.name", "Обложка")
	form.Set("new_field.2.type", "image")

	rec := postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}

	assertFileContains(t, p,
		"Фото2",          // правка применилась
		"Обложка",        // новое поле-картинка завелось
		"type: image",    // Фото осталось картинкой
		"type: richtext", // ПодробноеОписание осталось форматированным текстом
	)

	// Главное следствие: конфигурация обязана остаться загружаемой. Без этой
	// проверки тест сверял бы текст YAML, а пользователь ловил бы отказ старта.
	after := h.loadCfgData(context.Background(), b, "tree")
	if after.Error != "" {
		t.Fatalf("после сохранения конфигурация не загружается: %s", after.Error)
	}
}

// Тот же дефект вне справочников: измерение регистра типа enum: список типов
// регистра перечислений не предлагает вовсе, поэтому правка регистра меняла
// тип измерения на «строку». В examples/crm таких измерений три
// (ВоронкаПродаж, ВыполнениеЗадач, ОплатыКлиентов) — потеря была не
// теоретической.
func TestSaveRegisterFields_KeepsEnumDimension(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	writeCfgFile(t, cfgDir, "enums", "результатсделки.yaml", `name: РезультатСделки
values:
  - Выиграна
  - Проиграна
`)
	p := writeCfgFile(t, cfgDir, "registers", "воронкапродаж.yaml", `name: ВоронкаПродаж
dimensions:
  - name: Результат
    type: enum:РезультатСделки
resources:
  - name: Сумма
    type: number
`)

	b, err := h.store.Get("test")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	data := h.loadCfgData(context.Background(), b, "tree")
	if data.Error != "" {
		t.Fatalf("конфигурация не загрузилась: %s", data.Error)
	}
	form := browserSubmit(t, renderCfgTree(t, data), "/configurator/register-fields")

	rec := postCfg(t, "test", "/bases/test/configurator/register-fields", form, h.configuratorSaveRegisterFields)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}
	assertFileContains(t, p, "type: enum:РезультатСделки")
}

// Константы: набор списка исторически называет булев тип «boolean», а
// метаданные пишут «bool» — значение из файла не совпадало ни с одним пунктом,
// и правка любой константы делала булеву константу строковой.
func TestSaveConstant_KeepsBoolType(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "constants", "константы.yaml", `constants:
  - name: ВестиУчётПоСкладам
    type: bool
    label: Вести учёт по складам
`)

	b, err := h.store.Get("test")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	data := h.loadCfgData(context.Background(), b, "tree")
	if data.Error != "" {
		t.Fatalf("конфигурация не загрузилась: %s", data.Error)
	}
	form := browserSubmit(t, renderCfgTree(t, data), "/configurator/constant")

	rec := postCfg(t, "test", "/bases/test/configurator/constant", form, h.configuratorSaveConstant)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}
	assertFileContains(t, p, "type: bool")
}
