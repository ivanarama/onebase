package ui

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Ввод по строке (input_by_string): подсказка ищет по реквизиту «Наименование»,
// а не по всем строковым реквизитам справочника. Тест ходит публичным маршрутом
// подбора — тем же, которым пользуется поле формы.
func TestRefOptionsByStringSearchesNameOnly(t *testing.T) {
	f := newChoiceHTTPFixture(t)

	// «anna» есть только в реквизите «Аудитория»: общий поиск её находит,
	// поиск по наименованию — нет. Иначе подсказка показывала бы строки, ничуть
	// не похожие на набранное.
	general := f.serveRefOptions(t, f.target, url.Values{"q": {"anna"}, "limit": {"100"}})
	if len(decodeChoiceHTTP(t, general).Items) == 0 {
		t.Fatal("общий поиск по «anna» ничего не нашёл — тест не проверяет сужение")
	}
	byName := f.serveRefOptions(t, f.target, url.Values{"q": {"anna"}, "by": {"name"}, "limit": {"100"}})
	if got := decodeChoiceHTTP(t, byName).Items; len(got) != 0 {
		t.Fatalf("by=name вернул строки, совпавшие не по наименованию: %v", got)
	}

	found := f.serveRefOptions(t, f.target, url.Values{"q": {"needle 001"}, "by": {"name"}, "limit": {"100"}})
	items := decodeChoiceHTTP(t, found).Items
	if len(items) != 1 {
		t.Fatalf("by=name не нашёл единственное наименование: %v", items)
	}
	if label, _ := items[0]["_label"].(string); label != "needle 001" {
		t.Fatalf("подпись найденной строки = %q", label)
	}
}

// Порог ввода — три символа. Короткий ввод не ищется ни клиентом, ни сервером:
// подстрока из двух символов совпадает почти со всем справочником.
func TestRefOptionsByStringRequiresThreeChars(t *testing.T) {
	f := newChoiceHTTPFixture(t)

	short := f.serveRefOptions(t, f.target, url.Values{"q": {"ne"}, "by": {"name"}})
	response := decodeChoiceHTTP(t, short)
	if short.Code != http.StatusOK || len(response.Items) != 0 || response.Total != 0 {
		t.Fatalf("двухсимвольный ввод не должен искать: код %d, %v", short.Code, response)
	}

	// Ровно три символа — уже поиск.
	ok := f.serveRefOptions(t, f.target, url.Values{"q": {"nee"}, "by": {"name"}, "limit": {"100"}})
	if len(decodeChoiceHTTP(t, ok).Items) == 0 {
		t.Fatal("трёхсимвольный ввод обязан искать")
	}
}

// Подсказка уважает права: RLS фикстуры прячет чужие строки, и ввод по строке
// не должен показывать то, чего пользователь не видит в обычном подборе.
func TestRefOptionsByStringKeepsRowAccess(t *testing.T) {
	f := newChoiceHTTPFixture(t)

	found := f.serveRefOptions(t, f.target, url.Values{"q": {"needle"}, "by": {"name"}, "limit": {"100"}})
	for _, item := range decodeChoiceHTTP(t, found).Items {
		if label, _ := item["_label"].(string); label == "needle hidden" {
			t.Fatalf("ввод по строке выдал строку вне RLS: %v", item)
		}
	}
}

// refNameSearchField выбирает «Наименование», а у справочника без него —
// реквизит представления: подсказка не должна оставаться мёртвой.
func TestRefNameSearchFieldFallback(t *testing.T) {
	named := &metadata.Entity{Name: "Товар", Fields: []metadata.Field{
		{Name: "Артикул", Type: metadata.FieldTypeString},
		{Name: "Наименование", Type: metadata.FieldTypeString},
	}}
	if got := refNameSearchField(named); got != "Наименование" {
		t.Fatalf("выбран %q вместо «Наименование»", got)
	}
	renamed := &metadata.Entity{Name: "Товар", Fields: []metadata.Field{
		{Name: "Заголовок", Type: metadata.FieldTypeString},
	}}
	if got := refNameSearchField(renamed); got != "Заголовок" {
		t.Fatalf("без «Наименования» ожидался реквизит представления, а выбран %q", got)
	}
	if got := refNameSearchField(&metadata.Entity{Name: "Пусто"}); got != "" {
		t.Fatalf("у справочника без строковых реквизитов искать не по чему, а выбрано %q", got)
	}
}

// renderRefTypingForm рисует управляемую форму с одним ссылочным полем.
func renderRefTypingForm(t *testing.T, el *metadata.FormElement) string {
	t.Helper()
	entity := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Склад", Type: metadata.FieldType("reference:Склад"), RefEntity: "Склад"},
		},
	}
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: entity.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Elements:   []*metadata.FormElement{el},
	}
	data := map[string]any{
		"Entity": entity, "Form": form, "IsNew": true,
		"Values":        map[string]string{"Склад": "ref-1"},
		"RefOptions":    map[string][]map[string]any{"Склад": {{"id": "ref-1", "_label": "Основной"}}},
		"EnumOptions":   map[string]any{},
		"ChoiceOptions": loadChoiceOptions(form, "ru"),
		"TPRefOptions":  map[string]any{},
		"TPEnumLabels":  map[string]map[string]map[string]string{},
		"TPEnumOrder":   map[string]map[string][]string{},
		"TPRefMeta":     map[string]any{},
		"TablePartRows": map[string][]map[string]any{},
		"User":          nil, "Lang": "ru",
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	return buf.String()
}

// Элемент формы с input_by_string рендерится с маркером ввода по строке и
// видимым текстовым полем; select остаётся носителем значения, поэтому native
// required переезжает на текст — скрытый список браузер сфокусировать не может,
// и незаполненное обязательное поле блокировало бы отправку без объяснения.
func TestManagedRefInputByStringRendersTypingField(t *testing.T) {
	html := renderRefTypingForm(t, &metadata.FormElement{
		Kind: metadata.FormElementField, Name: "ПолеСклад",
		DataPath: "Объект.Склад", Required: true, InputByString: true,
	})

	if !strings.Contains(html, `data-ob-ref-typing="1"`) {
		t.Error("нет маркера ввода по строке у select")
	}
	if !strings.Contains(html, `data-ob-ref-text="ref-Склад"`) {
		t.Error("нет видимого поля ввода по строке")
	}
	if !strings.Contains(html, `value="Основной"`) {
		t.Error("в поле не подставлена подпись текущего значения")
	}
	// required на скрытом select заблокировал бы отправку без видимого
	// объяснения, поэтому его там быть не должно.
	start := strings.Index(html, `id="ref-Склад"`)
	if start < 0 {
		t.Fatal("пропал select ссылочного поля")
	}
	selectTag := html[start:]
	selectTag = selectTag[:strings.Index(selectTag, ">")]
	if strings.Contains(selectTag, ` required`) {
		t.Fatalf("required остался на скрытом select: %s", selectTag)
	}
}

// Без ключа разметка прежняя: ввода по строке нет, список работает как раньше.
func TestManagedRefWithoutInputByStringKeepsSelect(t *testing.T) {
	html := renderRefTypingForm(t, &metadata.FormElement{
		Kind: metadata.FormElementField, Name: "ПолеСклад", DataPath: "Объект.Склад",
	})
	if strings.Contains(html, "data-ob-ref-typing") || strings.Contains(html, "ob-ref-combo") {
		t.Error("без ключа поле получило ввод по строке")
	}
	if !strings.Contains(html, `id="ref-Склад"`) {
		t.Error("пропал обычный список ссылочного поля")
	}
}
