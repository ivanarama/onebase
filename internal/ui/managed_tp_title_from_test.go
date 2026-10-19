package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Заголовок колонки из реквизита (title_from) — аналог Колонка.ТекстШапки 1С:
// табель мастера подписывает дни месяца «1 пн … 31 ср» под выбранный месяц.
func titleFromEntity(t *testing.T) *metadata.Entity {
	t.Helper()
	ent := &metadata.Entity{
		Name: "Табель", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "ШапкаЧасов", Type: metadata.FieldTypeString},
		},
		TableParts: []metadata.TablePart{{Name: "Дни", Fields: []metadata.Field{
			{Name: "День", Type: metadata.FieldTypeString},
			{Name: "Часы", Type: metadata.FieldTypeNumber, Title: "Часы реквизита"},
		}}},
	}
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: ent.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Attributes: []*metadata.FormAttribute{{Name: "ЗаголовокДня", TypeRef: "string", Save: false}},
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "ПолеНаим", DataPath: "Объект.Наименование",
				Handlers: map[metadata.FormEventType]string{metadata.FormEventOnChange: "ПолеНаимПриИзменении"}},
			{Kind: metadata.FormElementTablePart, Name: "Дни", DataPath: "Объект.Дни",
				Children: []*metadata.FormElement{
					{Kind: metadata.FormElementColumn, Name: "КолДень", DataPath: "Объект.Дни.День",
						TitleMap: map[string]string{"ru": "1"}, TitleFrom: "ЗаголовокДня"},
					{Kind: metadata.FormElementColumn, Name: "КолЧасы", DataPath: "Объект.Дни.Часы",
						TitleFrom: "Объект.ШапкаЧасов"},
				}},
		},
		ProgramAST: mustParse(t, `
Процедура ПолеНаимПриИзменении()
	Объект.ЗаголовокДня = Объект.Наименование;
КонецПроцедуры
`),
	}
	ent.Forms = []*metadata.FormModule{form}
	return ent
}

func TestManagedTPColumnTitleFromRendersHeaderValue(t *testing.T) {
	ent := titleFromEntity(t)
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"ШапкаЧасов": "Часы (март)"}, ent); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.formEdit(rec, reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/"+id.String(), nil,
		map[string]string{"kind": "catalog", "entity": ent.Name, "id": id.String()}))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET формы: %d %s", rec.Code, rec.Body.String())
	}
	cols := parseManagedTPColumns(t, rec.Body.String())
	if len(cols) != 2 {
		t.Fatalf("колонки: %+v", cols)
	}
	// Реквизит формы пуст — колонка держит свой статичный заголовок.
	if cols[0].ID != "День" || cols[0].Name != "1" || !cols[0].TitleFrom {
		t.Fatalf("пустой title_from должен оставить заголовок элемента: %+v", cols[0])
	}
	// Поле шапки объекта задано — его значение и есть заголовок.
	if cols[1].ID != "Часы" || cols[1].Name != "Часы (март)" || !cols[1].TitleFrom {
		t.Fatalf("title_from: Объект.ШапкаЧасов не стал заголовком: %+v", cols[1])
	}
}

func TestManagedTPColumnTitleFromFollowsFormEvent(t *testing.T) {
	ent := titleFromEntity(t)
	srv, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
	event := func(name string) formEventResponse {
		t.Helper()
		resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, url.Values{
			"_element": {"ПолеНаим"}, "_event": {string(metadata.FormEventOnChange)}, "Наименование": {name},
		}).Body.Bytes())
		if !resp.OK {
			t.Fatalf("событие: %s", resp.Error)
		}
		return resp
	}

	resp := event("1 <b>пн</b>")
	days := resp.ColumnTitles["Дни"]
	// Сервер отдаёт текст как есть — экранирует клиент перед выводом в шапку.
	if days["День"] != "1 <b>пн</b>" {
		t.Fatalf("заголовок дня после обработчика: %+v", resp.ColumnTitles)
	}
	// ШапкаЧасов в событии не пришла и пуста — у колонки заголовок реквизита ТЧ.
	if days["Часы"] != "Часы реквизита" {
		t.Fatalf("пустой источник должен вернуть статичный заголовок: %+v", resp.ColumnTitles)
	}

	// Обработчик очистил реквизит — колонка возвращает заголовок элемента.
	if got := event("").ColumnTitles["Дни"]["День"]; got != "1" {
		t.Fatalf("очищенный title_from: заголовок %q, want «1»", got)
	}
}
