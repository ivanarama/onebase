package ui

// ПриИзмененииСтроки и ПослеДобавленияСтроки были объявлены в метаданных, но
// клиент их не отправлял, а серверный allowlist не пропускал. Обработчик можно
// было написать — сработать он не мог.

import (
	"bytes"
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Диспетчер исполняет ПриИзмененииСтроки с контекстом строки.
func TestRowEvents_OnRowChangedFires(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ТоварыПриИзмененииСтроки()
	Сообщить("строка " + Строка(НомерСтроки) + " изменена");
КонецПроцедуры
`, nil,
		[]*metadata.FormElement{{
			Kind:     metadata.FormElementTablePart,
			Name:     "ЭлементТовары",
			DataPath: "Объект.Товары",
			Handlers: map[metadata.FormEventType]string{
				metadata.FormEventOnRowChanged: "ТоварыПриИзмененииСтроки",
			},
		}})
	ent.TableParts = []metadata.TablePart{{
		Name:   "Товары",
		Fields: []metadata.Field{{Name: "Цена", Type: metadata.FieldTypeNumber}},
	}}

	body := url.Values{}
	body.Set("_element", "ЭлементТовары")
	body.Set("_event", string(metadata.FormEventOnRowChanged))
	body.Set("_kind", "object")
	body.Set("_tp", "Товары")
	body.Set("_tp_row", "0")
	body.Set("_tp_row_number", "1")
	body.Set("tp_json.Товары", `[{"Цена":10}]`)

	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if len(resp.Messages) != 1 || !strings.Contains(resp.Messages[0], "строка 1 изменена") {
		t.Errorf("messages=%v", resp.Messages)
	}
}

// ПослеДобавленияСтроки — тоже полноценное событие, а не синоним.
func TestRowEvents_AfterRowAddFires(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ТоварыПослеДобавления()
	Сообщить("после добавления");
КонецПроцедуры
`, nil,
		[]*metadata.FormElement{{
			Kind:     metadata.FormElementTablePart,
			Name:     "ЭлементТовары",
			DataPath: "Объект.Товары",
			Handlers: map[metadata.FormEventType]string{
				metadata.FormEventAfterRowAdd: "ТоварыПослеДобавления",
			},
		}})
	ent.TableParts = []metadata.TablePart{{
		Name:   "Товары",
		Fields: []metadata.Field{{Name: "Цена", Type: metadata.FieldTypeNumber}},
	}}

	body := url.Values{}
	body.Set("_element", "ЭлементТовары")
	body.Set("_event", string(metadata.FormEventAfterRowAdd))
	body.Set("_kind", "object")
	body.Set("_tp", "Товары")
	body.Set("tp_json.Товары", `[{"Цена":10}]`)

	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if len(resp.Messages) != 1 || resp.Messages[0] != "после добавления" {
		t.Errorf("messages=%v", resp.Messages)
	}
}

// Флаги проставляются независимо: объявить можно любое одно событие.
func TestRowEvents_GridFlagsIndependent(t *testing.T) {
	render := func(canWrite bool, handlers map[metadata.FormEventType]string) string {
		el := &metadata.FormElement{
			Kind: metadata.FormElementTablePart, Name: "ЭлементТовары",
			TitleMap: map[string]string{"ru": "Товары"},
			DataPath: "Объект.Товары", Handlers: handlers,
		}
		form := &metadata.FormModule{
			Name: "ФормаОбъекта", Kind: "object", EntityName: "Заказ",
			LayoutKind: metadata.FormLayoutManaged, Title: map[string]string{"ru": "Заказ"},
			Elements: []*metadata.FormElement{el},
		}
		ent := &metadata.Entity{
			Name: "Заказ", Kind: metadata.KindDocument,
			TableParts: []metadata.TablePart{{Name: "Товары", Fields: []metadata.Field{{Name: "Цена", Type: "number"}}}},
			Forms:      []*metadata.FormModule{form},
		}
		data := map[string]any{
			"Entity": ent, "Form": form, "IsNew": true, "CanWrite": canWrite,
			"Values": map[string]string{}, "RefOptions": map[string]any{},
			"EnumOptions": map[string]any{}, "ChoiceOptions": map[string]any{},
			"TPRefOptions":  map[string]any{},
			"TPEnumLabels":  map[string]map[string]map[string]string{},
			"TPEnumOrder":   map[string]map[string][]string{},
			"TPRefMeta":     map[string]any{},
			"TablePartRows": map[string][]map[string]any{"Товары": {}},
			"User":          nil, "Lang": "ru",
		}
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
			t.Fatalf("ExecuteTemplate: %v", err)
		}
		return buf.String()
	}

	only := render(true, map[metadata.FormEventType]string{metadata.FormEventOnRowChanged: "Ф"})
	if !strings.Contains(only, `data-sg-rowchange="1"`) {
		t.Error("нет data-sg-rowchange при объявленном ПриИзмененииСтроки")
	}
	if strings.Contains(only, "data-sg-rowafteradd") {
		t.Error("data-sg-rowafteradd проставлен без обработчика")
	}

	after := render(true, map[metadata.FormEventType]string{metadata.FormEventAfterRowAdd: "Ф"})
	if !strings.Contains(after, `data-sg-rowafteradd="1"`) {
		t.Error("нет data-sg-rowafteradd при объявленном ПослеДобавленияСтроки")
	}

	none := render(true, nil)
	if strings.Contains(none, "data-sg-rowchange") || strings.Contains(none, "data-sg-rowafteradd") {
		t.Error("флаги проставлены без обработчиков — форма гоняла бы сеть впустую")
	}

	readOnly := render(false, map[metadata.FormEventType]string{
		metadata.FormEventOnRowChanged: "ИзменитьСтроку",
		metadata.FormEventAfterRowAdd:  "ПослеДобавления",
	})
	if strings.Contains(readOnly, "data-sg-rowchange") || strings.Contains(readOnly, "data-sg-rowafteradd") {
		t.Error("CanWrite=false объявляет недоступные события строк")
	}
}

// Клиент отправляет оба события и делает это ПОСЛЕДОВАТЕЛЬНО: оба ответа
// применяют значения к форме, параллельный запуск дал бы гонку.
func TestRowEvents_ClientChainsSequentially(t *testing.T) {
	js := string(managedJS)
	for _, want := range []string{
		`"ПриИзмененииСтроки"`, `"ПослеДобавленияСтроки"`,
		"obFireRowEventChain", "data-sg-rowchange", "data-sg-rowafteradd",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("/static/managed.js не содержит %q", want)
		}
	}
}
