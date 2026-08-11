package ui

// ПриАктивизацииСтроки (issue #670, часть 1). Событие было объявлено в
// метаданных (FormEventOnRowActivated), но НИКОГДА не вызывалось: клиент
// дёргал только ПриДобавленииСтроки/ПриУдаленииСтроки. Форма могла объявить
// обработчик, а сработать он не мог — обхода у прикладного разработчика не
// было вовсе.

import (
	"bytes"
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Обработчик получает контекст активированной строки — имя ТЧ, номер строки и
// саму строку как DSL-объект. Без контекста событие бесполезно: «строка
// сменилась, а какая — неизвестно».
func TestHandleManagedFormEvent_RowActivatedContext(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ТоварыПриАктивизацииСтроки()
	Сообщить(ИмяТабличнойЧасти);
	Сообщить(НомерСтроки);
	Сообщить(ТекущаяСтрока.Цена);
КонецПроцедуры
`, nil,
		[]*metadata.FormElement{
			{
				Kind:     metadata.FormElementTablePart,
				Name:     "ЭлементТовары",
				DataPath: "Объект.Товары",
				Handlers: map[metadata.FormEventType]string{
					metadata.FormEventOnRowActivated: "ТоварыПриАктивизацииСтроки",
				},
			},
		})
	ent.TableParts = []metadata.TablePart{{
		Name: "Товары",
		Fields: []metadata.Field{
			{Name: "Количество", Type: metadata.FieldTypeNumber},
			{Name: "Цена", Type: metadata.FieldTypeNumber},
		},
	}}

	body := url.Values{}
	body.Set("_element", "ЭлементТовары")
	body.Set("_event", string(metadata.FormEventOnRowActivated))
	body.Set("_kind", "object")
	body.Set("_tp", "Товары")
	body.Set("_tp_row", "1")
	body.Set("_tp_row_number", "2")
	body.Set("tp_json.Товары", `[{"Количество":1,"Цена":10},{"Количество":2,"Цена":20}]`)

	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK {
		t.Fatalf("ожидался ok=true, error=%q", resp.Error)
	}
	want := []string{"Товары", "2", "20"}
	if len(resp.Messages) != len(want) {
		t.Fatalf("messages=%v, ожидалось %v", resp.Messages, want)
	}
	for i := range want {
		if resp.Messages[i] != want[i] {
			t.Errorf("messages[%d]=%q, ожидалось %q (все messages=%v)", i, resp.Messages[i], want[i], resp.Messages)
		}
	}
}

// Обработчик может менять реквизиты формы — ради этого событие и нужно:
// надпись/картинка «по текущей строке» пересчитывается при переходе по ТЧ.
func TestHandleManagedFormEvent_RowActivatedUpdatesValues(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ТоварыПриАктивизацииСтроки()
	Объект.Комментарий = "строка " + Строка(НомерСтроки);
КонецПроцедуры
`, nil,
		[]*metadata.FormElement{
			{
				Kind:     metadata.FormElementTablePart,
				Name:     "ЭлементТовары",
				DataPath: "Объект.Товары",
				Handlers: map[metadata.FormEventType]string{
					metadata.FormEventOnRowActivated: "ТоварыПриАктивизацииСтроки",
				},
			},
		})
	ent.Fields = append(ent.Fields, metadata.Field{Name: "Комментарий", Type: metadata.FieldTypeString})
	ent.TableParts = []metadata.TablePart{{
		Name:   "Товары",
		Fields: []metadata.Field{{Name: "Цена", Type: metadata.FieldTypeNumber}},
	}}

	body := url.Values{}
	body.Set("_element", "ЭлементТовары")
	body.Set("_event", string(metadata.FormEventOnRowActivated))
	body.Set("_kind", "object")
	body.Set("_tp", "Товары")
	body.Set("_tp_row", "0")
	body.Set("_tp_row_number", "1")
	body.Set("tp_json.Товары", `[{"Цена":10}]`)

	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK {
		t.Fatalf("ожидался ok=true, error=%q", resp.Error)
	}
	if got := resp.Values["Комментарий"]; got != "строка 1" {
		t.Errorf("Values[Комментарий]=%v, ожидалось «строка 1» (values=%v)", got, resp.Values)
	}
}

// Рендер грида проставляет data-sg-rowactivate только при объявленном
// обработчике. Флаг — единственное, что отличает форму, которой событие нужно,
// от всех остальных: без него подписка не ставится и сеть не гоняется на
// каждое движение курсора.
func TestManagedFormGridRowActivatedAttr(t *testing.T) {
	render := func(withHandler bool) string {
		el := &metadata.FormElement{
			Kind:     metadata.FormElementTablePart,
			Name:     "ЭлементТовары",
			TitleMap: map[string]string{"ru": "Товары"},
			DataPath: "Объект.Товары",
		}
		if withHandler {
			el.Handlers = map[metadata.FormEventType]string{
				metadata.FormEventOnRowActivated: "ТоварыПриАктивизацииСтроки",
			}
		}
		form := &metadata.FormModule{
			Name:       "ФормаОбъекта",
			Kind:       "object",
			EntityName: "Заказ",
			LayoutKind: metadata.FormLayoutManaged,
			Title:      map[string]string{"ru": "Заказ"},
			Elements:   []*metadata.FormElement{el},
		}
		ent := &metadata.Entity{
			Name: "Заказ",
			Kind: metadata.KindDocument,
			TableParts: []metadata.TablePart{{
				Name:   "Товары",
				Fields: []metadata.Field{{Name: "Цена", Type: "number"}},
			}},
			Forms: []*metadata.FormModule{form},
		}
		data := map[string]any{
			"Entity":        ent,
			"Form":          form,
			"IsNew":         true,
			"Values":        map[string]string{},
			"RefOptions":    map[string]any{},
			"EnumOptions":   map[string]any{},
			"ChoiceOptions": map[string]any{},
			"TPRefOptions":  map[string]any{},
			"TPEnumLabels":  map[string]map[string]map[string]string{},
			"TPEnumOrder":   map[string]map[string][]string{},
			"TPRefMeta":     map[string]any{},
			"TablePartRows": map[string][]map[string]any{"Товары": {}},
			"User":          nil,
			"Lang":          "ru",
		}
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
			t.Fatalf("ExecuteTemplate: %v", err)
		}
		return buf.String()
	}

	if html := render(true); !strings.Contains(html, `data-sg-rowactivate="1"`) {
		t.Error("нет data-sg-rowactivate при объявленном ПриАктивизацииСтроки")
	}
	if html := render(false); strings.Contains(html, "data-sg-rowactivate") {
		t.Error("флаг проставлен без обработчика — форма гоняла бы сеть на каждое движение курсора")
	}
}

// Клиентский runtime подписан на смену активной ячейки и шлёт именно
// ПриАктивизацииСтроки. Это тот конец провода, которого не хватало: сервер
// событие обрабатывал всегда, но никто его не отправлял.
func TestManagedJS_FiresRowActivated(t *testing.T) {
	js := string(managedJS)
	for _, want := range []string{
		`data-sg-rowactivate`,
		`onActiveCellChanged`,
		`"ПриАктивизацииСтроки"`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("/static/managed.js не содержит %q — событие не будет отправлено", want)
		}
	}
	// Дебаунс обязателен: без него прокрутка стрелкой через 50 строк даёт
	// 50 запросов подряд, и форма встаёт колом на большой ТЧ.
	if !strings.Contains(js, "actTimer") {
		t.Error("подписка на активизацию строки без дебаунса")
	}
}
