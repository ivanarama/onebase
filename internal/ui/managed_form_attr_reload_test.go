package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"regexp"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Реквизит формы «Примечание» (save:false) переживает «Записать»: POST уходит с
// редиректом, и managed.js перед отправкой кладёт реквизиты формы в
// sessionStorage, а после загрузки новой страницы возвращает их в поля (#1759).
//
// Реквизит нарисован двумя копиями с взаимоисключающими hidden_when. Скрытая
// копия остаётся в DOM в disabled fieldset, поэтому первый контрол с этим name —
// не обязательно тот, который правил оператор. Раньше сохранялось значение
// первого контрола: при видимой второй копии в хранилище уходило старое значение
// скрытой, а после перезагрузки видимое поле оказывалось пустым.
//
// Путь пользователя: страница из обработчика GET → оператор меняет видимую копию
// old → new → «Записать» (сохранение реквизитов и публичный POST) → страница из
// GET заново → восстановление. Проверяем обе очерёдности копий, переключатель и
// обычный одиночный реквизит.
func TestРеквизитФормы_ПослеПерезагрузкиВидимаяКопияСохраняетВвод(t *testing.T) {
	cases := []struct {
		name       string
		kind       metadata.FormElementType
		copies     int
		shownFirst bool
	}{
		{"поле/видимая копия второй", metadata.FormElementField, 2, false},
		{"поле/видимая копия первой", metadata.FormElementField, 2, true},
		{"переключатель/видимая копия второй", metadata.FormElementSwitch, 2, false},
		{"переключатель/видимая копия первой", metadata.FormElementSwitch, 2, true},
		{"поле/одиночный реквизит", metadata.FormElementField, 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node, err := exec.LookPath("node")
			if err != nil {
				t.Skip("node is required for the managed form reload test")
			}
			ent := заявкаСПримечаниемВКопиях(tc.kind, tc.copies, tc.shownFirst)
			srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
			id := uuid.New()
			if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"СтадияОформления": "Черновик"}, ent); err != nil {
				t.Fatal(err)
			}

			// Открытая форма: значение «old» во всех копиях, оператор меняет
			// видимую на «new» и жмёт «Записать».
			html := открытьФормуОбъекта(t, srv, ent, id)
			before := пройтиСтраницуФормы(t, node, html, nil, []map[string]any{
				{"setAll": map[string]string{"Примечание": "old"}},
				{"edit": map[string]string{"Примечание": "new"}},
				{"stash": true},
			})
			if got := before.Values["Примечание"]; !reflect.DeepEqual(got, []string{"new"}) {
				t.Fatalf("форма отправляет Примечание=%q, ожидалось новое значение видимой копии", got)
			}
			var stashed map[string]string
			if before.Storage == nil || json.Unmarshal([]byte(*before.Storage), &stashed) != nil ||
				stashed["Примечание"] != "new" {
				t.Fatalf("в sessionStorage перед перезагрузкой %s, ожидалось Примечание=new", хранилище(before.Storage))
			}
			записатьЗаявку(t, srv, ent, id, before.Values)

			// Страница после редиректа: сервер реквизит формы не хранит, значение
			// возвращает клиент.
			after := пройтиСтраницуФормы(t, node, открытьФормуОбъекта(t, srv, ent, id), before.Storage,
				[]map[string]any{{"restore": true}})
			if got := after.Values["Примечание"]; !reflect.DeepEqual(got, []string{"new"}) {
				t.Fatalf("после перезагрузки видимое поле Примечание=%q, ожидалось «new»", got)
			}
			if after.Storage != nil {
				t.Fatalf("сохранённые реквизиты не удалены после восстановления: %s", *after.Storage)
			}
		})
	}
}

// Нет отправляемой копии — реквизит не сохраняется вовсе, и перезагрузка не
// возвращает значение скрытого поля.
func TestРеквизитФормы_БезВидимойКопииНичегоНеСохраняется(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the managed form reload test")
	}
	ent := заявкаСПримечаниемВКопиях(metadata.FormElementField, 1, false)
	ent.Forms[0].Elements[1].HiddenWhen = `СтадияОформления = "Черновик"`
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"СтадияОформления": "Черновик"}, ent); err != nil {
		t.Fatal(err)
	}
	page := пройтиСтраницуФормы(t, node, открытьФормуОбъекта(t, srv, ent, id), nil, []map[string]any{
		{"setAll": map[string]string{"Примечание": "old"}},
		{"stash": true},
	})
	if page.Storage != nil {
		t.Fatalf("реквизит без видимой копии сохранён: %s", *page.Storage)
	}
}

// заявкаСПримечаниемВКопиях — справочник, форма которого рисует реквизит формы
// «Примечание» (save:false) copies раз. Две копии взаимоисключающие: при
// стадии «Черновик» видна копия черновика. shownFirst ставит видимую копию
// первой в DOM.
func заявкаСПримечаниемВКопиях(kind metadata.FormElementType, copies int, shownFirst bool) *metadata.Entity {
	копия := func(name, hiddenWhen string) *metadata.FormElement {
		el := &metadata.FormElement{Kind: kind, Name: name, DataPath: "Примечание", HiddenWhen: hiddenWhen}
		if kind == metadata.FormElementSwitch {
			el.Options = []metadata.FormOption{{Value: "old"}, {Value: "new"}}
		}
		return el
	}
	elements := []*metadata.FormElement{fieldEl("ПолеСтадии", "Объект.СтадияОформления")}
	switch {
	case copies == 1:
		elements = append(elements, копия("ПолеПримечание", ""))
	case shownFirst:
		elements = append(elements,
			копия("ПолеПримечаниеЧерновика", `СтадияОформления = "Принята"`),
			копия("ПолеПримечаниеПринятой", `СтадияОформления <> "Принята"`))
	default:
		elements = append(elements,
			копия("ПолеПримечаниеПринятой", `СтадияОформления <> "Принята"`),
			копия("ПолеПримечаниеЧерновика", `СтадияОформления = "Принята"`))
	}
	form := managedObjectForm(elements...)
	form.Attributes = []*metadata.FormAttribute{{Name: "Примечание", TypeRef: "Строка", Save: false}}
	ent := &metadata.Entity{
		Name: "ЗаявкаСПримечанием", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "СтадияОформления", Type: metadata.FieldTypeString}},
	}
	ent.Forms = []*metadata.FormModule{form}
	return ent
}

// открытьФормуОбъекта — страница формы из публичного обработчика GET.
func открытьФормуОбъекта(t *testing.T, srv *Server, ent *metadata.Entity, id uuid.UUID) string {
	t.Helper()
	req := reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/"+id.String(), nil,
		map[string]string{"kind": "catalog", "entity": ent.Name, "id": id.String()})
	rec := httptest.NewRecorder()
	srv.formEdit(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("открытие формы: статус=%d body=%s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

var managedConfigScript = regexp.MustCompile(`(?s)<script type="application/json" id="ob-managed-config">(.*?)</script>`)

// хранилище — содержимое sessionStorage для сообщения теста.
func хранилище(s *string) string {
	if s == nil {
		return "пусто"
	}
	return *s
}

type formPageResult struct {
	Values  map[string][]string `json:"values"`
	Storage *string             `json:"storage"`
}

// пройтиСтраницуФормы разбирает настоящую разметку и конфиг страницы
// (ob-managed-config) и выполняет над ней действия кодом managed.js.
func пройтиСтраницуФормы(t *testing.T, node, html string, storage *string, actions []map[string]any) formPageResult {
	t.Helper()
	m := managedConfigScript.FindStringSubmatch(html)
	if m == nil {
		t.Fatal("на странице нет ob-managed-config")
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(m[1]), &config); err != nil {
		t.Fatalf("ob-managed-config: %v; %s", err, m[1])
	}
	if attrs, _ := config["formAttrs"].([]any); !reflect.DeepEqual(attrs, []any{"Примечание"}) {
		t.Fatalf("formAttrs страницы = %v, ожидался реквизит формы «Примечание»", config["formAttrs"])
	}
	payload, err := json.Marshal(map[string]any{
		"page": parseManagedPage(t, html), "config": config, "storage": storage, "actions": actions,
	})
	if err != nil {
		t.Fatal(err)
	}
	var result formPageResult
	runManagedPageHarness(t, node, payload, &result)
	return result
}
