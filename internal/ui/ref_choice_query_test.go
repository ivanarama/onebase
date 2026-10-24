package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// choice_filter запросом (как filter[].query формы списка, #2181): допустимы
// товарные группы, для которых в соответствии объектов 1С есть строка базы
// филиала. Объект соответствия — строка с UUID, база — строковый реквизит за
// ссылкой на филиал. Тест ходит публичным маршрутом подбора /ui/_ref-options:
// выпадающий список, ввод по строке и проверка выбранного значения.
type choiceQueryFixture struct {
	server                        *Server
	target, owner                 *metadata.Entity
	branchA, branchB, branchEmpty uuid.UUID
	groups                        map[string]uuid.UUID
}

const choiceQueryText = `ВЫБРАТЬ С.Объект ИЗ Справочник.Соответствие КАК С
ГДЕ С.ТипОбъекта = "ТГ" И С.База = &База И (С.Филиал = &Филиал ИЛИ С.Филиал ЕСТЬ NULL)`

func newChoiceQueryFixture(t *testing.T, db *storage.DB, extra ...metadata.FormChoiceCondition) choiceQueryFixture {
	t.Helper()
	ctx := context.Background()
	branch := &metadata.Entity{Name: "Филиал", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "СтрокаПодключения", Type: metadata.FieldTypeString},
	}}
	group := &metadata.Entity{Name: "ТоварнаяГруппа", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "Активна", Type: metadata.FieldTypeBool},
	}}
	mapping := &metadata.Entity{Name: "Соответствие", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "ТипОбъекта", Type: metadata.FieldTypeString},
		{Name: "База", Type: metadata.FieldTypeString},
		{Name: "Филиал", Type: metadata.FieldType("reference:" + branch.Name), RefEntity: branch.Name},
		{Name: "ДляВсех", Type: metadata.FieldTypeBool},
		{Name: "Объект", Type: metadata.FieldTypeString},
		{Name: "Аудитория", Type: metadata.FieldTypeString},
	}}
	conditions := append([]metadata.FormChoiceCondition{{
		Query:  choiceQueryText,
		Params: map[string]string{"База": "Объект.ФилиалАйсберг.СтрокаПодключения", "Филиал": "Объект.ФилиалАйсберг"},
	}}, extra...)
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", EntityName: "Заявка", Kind: "object", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{ID: "branch", Name: "ПолеФилиал", Kind: metadata.FormElementField, DataPath: "Объект.ФилиалАйсберг"},
			{ID: "group-picker", Name: "ПолеТГ", Kind: metadata.FormElementField, DataPath: "Объект.ТоварнаяГруппа", ChoiceFilter: conditions},
		},
	}
	owner := &metadata.Entity{Name: "Заявка", Kind: metadata.KindDocument, Fields: []metadata.Field{
		{Name: "ФилиалАйсберг", Type: metadata.FieldType("reference:" + branch.Name), RefEntity: branch.Name},
		{Name: "ТоварнаяГруппа", Type: metadata.FieldType("reference:" + group.Name), RefEntity: group.Name},
	}, Forms: []*metadata.FormModule{form}}
	entities := []*metadata.Entity{branch, group, mapping, owner}
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatal(err)
	}
	id := func(prefix byte, n int) uuid.UUID {
		return uuid.MustParse(fmt.Sprintf("%02x000000-0000-0000-0000-%012d", prefix, n))
	}
	f := choiceQueryFixture{target: group, owner: owner, branchA: id(0x10, 1), branchB: id(0x10, 2), branchEmpty: id(0x10, 3), groups: map[string]uuid.UUID{}}
	for _, b := range []struct {
		id         uuid.UUID
		name, conn string
	}{{f.branchA, "Москва", "srv/msk"}, {f.branchB, "Казань", "srv/kzn"}, {f.branchEmpty, "Без базы", ""}} {
		if err := db.Upsert(ctx, branch.Name, b.id, map[string]any{"Наименование": b.name, "СтрокаПодключения": b.conn}, branch); err != nil {
			t.Fatal(err)
		}
	}
	for i, g := range []struct {
		name   string
		active bool
	}{{"Холодильники", true}, {"Стиральные", true}, {"Плиты", false}, {"Чужая база", true}, {"Скрытая", true}, {"Нет в соответствии", true}} {
		gid := id(0x20, i+1)
		f.groups[g.name] = gid
		if err := db.Upsert(ctx, group.Name, gid, map[string]any{"Наименование": g.name, "Активна": g.active}, group); err != nil {
			t.Fatal(err)
		}
	}
	for i, m := range []struct {
		kind, base, group, audience string
		branch                      uuid.UUID
		all                         bool
	}{
		{"ТГ", "srv/msk", "Холодильники", "anna", f.branchA, false},
		{"ТГ", "srv/msk", "Стиральные", "anna", uuid.Nil, true}, // филиал пуст — для всех филиалов базы
		{"ТГ", "srv/msk", "Плиты", "anna", f.branchA, false},
		{"ТГ", "srv/kzn", "Чужая база", "anna", f.branchB, false},
		{"ТГ", "srv/msk", "Скрытая", "bob", f.branchA, false}, // закрыта строковым доступом
		{"Бренд", "srv/msk", "Нет в соответствии", "anna", f.branchA, false},
	} {
		values := map[string]any{"Наименование": fmt.Sprint(i), "ТипОбъекта": m.kind, "База": m.base,
			"ДляВсех": m.all, "Объект": f.groups[m.group].String(), "Аудитория": m.audience}
		if m.branch != uuid.Nil {
			values["Филиал"] = m.branch.String()
		}
		if err := db.Upsert(ctx, mapping.Name, id(0x30, i+1), values, mapping); err != nil {
			t.Fatal(err)
		}
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: entities})
	f.server = &Server{reg: reg, store: db}
	f.server.entitySvc = f.server.newEntityService(nil)
	return f
}

// choiceQueryUser читает все справочники, но строки соответствия видит только
// свои: запрос условия исполняется с правами пользователя.
func choiceQueryUser() *auth.User {
	return &auth.User{ID: "u-anna", Login: "anna", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs:  map[string][]string{"Филиал": {"read"}, "ТоварнаяГруппа": {"read"}, "Соответствие": {"read"}},
		Documents: map[string][]string{"Заявка": {"read", "write"}},
		RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
			"Соответствие": {"read": {Field: "Аудитория", Op: "eq", Value: auth.RowValue{User: "login"}}},
		}},
	}}}}
}

func (f choiceQueryFixture) fetch(t *testing.T, user *auth.User, branch string, extra url.Values) choiceHTTPResponse {
	t.Helper()
	sources, _ := json.Marshal(map[string]string{"Объект.ФилиалАйсберг": branch, "Объект.ФилиалАйсберг.СтрокаПодключения": branch})
	query := url.Values{"form_entity": {f.owner.Name}, "form": {"ФормаОбъекта"}, "element": {"group-picker"}, "sources": {string(sources)}, "limit": {"100"}}
	for k, v := range extra {
		query[k] = v
	}
	router := chi.NewRouter()
	f.server.Mount(router)
	request := httptest.NewRequest(http.MethodGet, "/ui/_ref-options/"+url.PathEscape(f.target.Name)+"?"+query.Encode(), nil)
	request = request.WithContext(auth.ContextWithUser(request.Context(), user))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	return decodeChoiceHTTP(t, recorder)
}

func choiceQueryLabels(r choiceHTTPResponse) []string {
	out := make([]string, 0, len(r.Items))
	for _, item := range r.Items {
		out = append(out, fmt.Sprint(item["_label"]))
	}
	sort.Strings(out)
	return out
}

func TestRefOptionsChoiceQueryFiltersByQueryResult(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newChoiceQueryFixture(t, db)
		user := choiceQueryUser()

		got := choiceQueryLabels(f.fetch(t, user, f.branchA.String(), nil))
		if fmt.Sprint(got) != "[Плиты Стиральные Холодильники]" {
			t.Fatalf("филиал Москва: %v — ждали группы своей базы без чужой базы, чужого типа и закрытой строки", got)
		}
		if got := choiceQueryLabels(f.fetch(t, user, f.branchB.String(), nil)); fmt.Sprint(got) != "[Чужая база]" {
			t.Fatalf("филиал Казань: %v", got)
		}
		// Ввод по строке ищет только среди допустимых.
		if got := choiceQueryLabels(f.fetch(t, user, f.branchA.String(), url.Values{"q": {"Чуж"}})); len(got) != 0 {
			t.Fatalf("автоподбор нашёл недопустимую группу: %v", got)
		}
		if got := choiceQueryLabels(f.fetch(t, user, f.branchA.String(), url.Values{"q": {"Холод"}})); fmt.Sprint(got) != "[Холодильники]" {
			t.Fatalf("автоподбор: %v", got)
		}
		// Проверка введённого значения: группа чужой базы не допустима.
		for name, want := range map[string]bool{"Холодильники": true, "Чужая база": false, "Скрытая": false} {
			r := f.fetch(t, user, f.branchA.String(), url.Values{"selected_id": {f.groups[name].String()}})
			if r.SelectedAllowed == nil || *r.SelectedAllowed != want {
				t.Errorf("selected %s: %v, ждали %v", name, r.SelectedAllowed, want)
			}
		}
		// Пустой источник и пустой реквизит за ссылкой — пустой подбор.
		if r := f.fetch(t, user, "", nil); r.Total != 0 {
			t.Fatalf("пустой филиал: %v", choiceQueryLabels(r))
		}
		if r := f.fetch(t, user, f.branchEmpty.String(), nil); r.Total != 0 {
			t.Fatalf("филиал без строки подключения: %v", choiceQueryLabels(r))
		}
	})
}

// Условие-запрос соединяется с остальными условиями через И.
func TestRefOptionsChoiceQueryCombinesWithOtherConditions(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		active := true
		f := newChoiceQueryFixture(t, db, metadata.FormChoiceCondition{Field: "Активна", Op: metadata.FormChoiceOpEqual, Value: &active})
		if got := choiceQueryLabels(f.fetch(t, choiceQueryUser(), f.branchA.String(), nil)); fmt.Sprint(got) != "[Стиральные Холодильники]" {
			t.Fatalf("query И Активна: %v", got)
		}
	})
}

// Сломанный запрос закрывает подбор, а не открывает весь справочник.
func TestRefOptionsChoiceQueryBrokenIsClosed(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newChoiceQueryFixture(t, db)
		f.owner.Forms[0].Elements[1].ChoiceFilter[0].Query = "ВЫБРАТЬ Х.Объект ИЗ Справочник.НетТакого КАК Х"
		if r := f.fetch(t, choiceQueryUser(), f.branchA.String(), nil); r.Total != 0 {
			t.Fatalf("сломанный запрос открыл подбор: %v", choiceQueryLabels(r))
		}
	})
}
