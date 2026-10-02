package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

// Глубокий источник (план 183, срез B1) проверяется через публичный маршрут
// подбора: браузер присылает ТОЛЬКО ссылку ведущего поля, а реквизит за ней
// сервер читает сам. Поэтому проверки прав обязаны жить на сервере, и тест
// ходит тем же путём, что и форма, а не зовёт внутренний помощник.
type deepChoiceFixture struct {
	server     *Server
	target     *metadata.Entity
	owner      *metadata.Entity
	groupOne   uuid.UUID
	groupTwo   uuid.UUID
	directionA uuid.UUID
	directionB uuid.UUID
	directionC uuid.UUID
	directionD uuid.UUID
	ownerID    uuid.UUID
}

func deepChoiceUUID(prefix byte, number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("%02x000000-0000-0000-0000-%012d", prefix, number))
}

func newDeepChoiceFixture(t *testing.T) deepChoiceFixture {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "deep-choice.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	group := &metadata.Entity{
		Name: "ГруппаНеисправностей", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	direction := &metadata.Entity{
		Name: "Направление", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "ГруппаНеисправностей", Type: metadata.FieldType("reference:" + group.Name), RefEntity: group.Name},
			{Name: "Аудитория", Type: metadata.FieldTypeString},
		},
	}
	target := &metadata.Entity{
		Name: "Неисправность", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Группа", Type: metadata.FieldType("reference:" + group.Name), RefEntity: group.Name},
			{Name: "Секретный", Type: metadata.FieldTypeBool},
			{Name: "Аудитория", Type: metadata.FieldTypeString},
		},
	}
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", EntityName: "Заявка", Kind: "object", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{ID: "direction", Name: "ПолеНаправление", Kind: metadata.FormElementField, DataPath: "Объект.Направление"},
			{
				ID: "fault-picker", Name: "ПолеНеисправность", Kind: metadata.FormElementField,
				DataPath: "Объект.Неисправность",
				ChoiceFilter: []metadata.FormChoiceCondition{{
					Field: "Группа", Op: metadata.FormChoiceOpEqual,
					From: "Объект.Направление.ГруппаНеисправностей",
				}},
			},
		},
	}
	owner := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Направление", Type: metadata.FieldType("reference:" + direction.Name), RefEntity: direction.Name},
			{Name: "Неисправность", Type: metadata.FieldType("reference:" + target.Name), RefEntity: target.Name},
		},
		Forms: []*metadata.FormModule{form},
	}
	entities := []*metadata.Entity{group, direction, target, owner}
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatal(err)
	}

	fixture := deepChoiceFixture{
		target: target, owner: owner,
		groupOne: deepChoiceUUID(0x10, 1), groupTwo: deepChoiceUUID(0x10, 2),
		directionA: deepChoiceUUID(0x20, 1), directionB: deepChoiceUUID(0x20, 2),
		directionC: deepChoiceUUID(0x20, 3), directionD: deepChoiceUUID(0x20, 4),
		ownerID: deepChoiceUUID(0x40, 1),
	}

	for _, row := range []struct {
		id   uuid.UUID
		name string
	}{{fixture.groupOne, "Группа 1"}, {fixture.groupTwo, "Группа 2"}} {
		if err := db.Upsert(ctx, group.Name, row.id, map[string]any{"Наименование": row.name}, group); err != nil {
			t.Fatalf("seed group: %v", err)
		}
	}
	for _, row := range []struct {
		id       uuid.UUID
		name     string
		group    string
		audience string
	}{
		{fixture.directionA, "Направление A", fixture.groupOne.String(), "anna"},
		{fixture.directionB, "Направление B", fixture.groupTwo.String(), "anna"},
		{fixture.directionC, "Направление C", fixture.groupOne.String(), "bob"},
		{fixture.directionD, "Направление D", "", "anna"},
	} {
		if err := db.Upsert(ctx, direction.Name, row.id, map[string]any{
			"Наименование": row.name, "ГруппаНеисправностей": row.group, "Аудитория": row.audience,
		}, direction); err != nil {
			t.Fatalf("seed direction: %v", err)
		}
	}
	for _, row := range []struct {
		id       uuid.UUID
		name     string
		group    uuid.UUID
		secret   bool
		audience string
	}{
		{deepChoiceUUID(0x30, 1), "Не включается", fixture.groupOne, false, "anna"},
		{deepChoiceUUID(0x30, 2), "Течёт", fixture.groupTwo, true, "bob"},
		{deepChoiceUUID(0x30, 3), "Шумит", fixture.groupOne, false, "anna"},
	} {
		if err := db.Upsert(ctx, target.Name, row.id, map[string]any{
			"Наименование": row.name, "Группа": row.group.String(), "Секретный": row.secret, "Аудитория": row.audience,
		}, target); err != nil {
			t.Fatalf("seed fault: %v", err)
		}
	}
	if err := db.Upsert(ctx, owner.Name, fixture.ownerID, map[string]any{
		"Направление": fixture.directionA.String(),
	}, owner); err != nil {
		t.Fatalf("seed owner: %v", err)
	}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: entities})
	fixture.server = &Server{reg: reg, store: db}
	fixture.server.entitySvc = fixture.server.newEntityService(nil)
	return fixture
}

func TestManagedDeepChoiceInitialRenderUsesSavedDirection(t *testing.T) {
	f := newDeepChoiceFixture(t)
	options := f.initialOptions(t, deepChoiceUser(nil))
	if len(options) != 2 || !options[deepChoiceUUID(0x30, 1).String()] || !options[deepChoiceUUID(0x30, 3).String()] {
		t.Fatalf("initial deep choice options = %v, want only group 1", options)
	}
}

// initialOptions открывает сохранённую заявку и возвращает варианты, которые
// сервер отрисовал в поле подбора неисправности.
func (f deepChoiceFixture) initialOptions(t *testing.T, user *auth.User) map[string]bool {
	t.Helper()
	router := chi.NewRouter()
	f.server.Mount(router)
	request := httptest.NewRequest(http.MethodGet,
		"/ui/document/"+url.PathEscape(f.owner.Name)+"/"+f.ownerID.String(), nil)
	request = request.WithContext(auth.ContextWithUser(request.Context(), user))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("form status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	document, err := html.Parse(strings.NewReader(recorder.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	selectNode := findSelectByName(document, f.target.Name)
	if selectNode == nil {
		t.Fatalf("managed reference select %q not found", f.target.Name)
	}
	options := make(map[string]bool)
	for option := selectNode.FirstChild; option != nil; option = option.NextSibling {
		if option.Type != html.ElementNode || option.Data != "option" {
			continue
		}
		if value, ok := htmlAttribute(option, "value"); ok && value != "" {
			options[value] = true
		}
	}
	return options
}

// deepChoiceUser — оператор: читает все три справочника, но видит только свои
// направления. Направление C принадлежит другому оператору и закрыто строковым
// доступом.
func deepChoiceUser(policies auth.FieldPolicies) *auth.User {
	permission := auth.Permission{
		Catalogs:  map[string][]string{"Направление": {"read"}, "Неисправность": {"read"}, "ГруппаНеисправностей": {"read"}},
		Documents: map[string][]string{"Заявка": {"read", "write"}},
		RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
			"Направление": {"read": {Field: "Аудитория", Op: "eq", Value: auth.RowValue{User: "login"}}},
		}},
	}
	if len(policies) > 0 {
		permission.FieldAccess = auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{"Направление": policies}}
	}
	return &auth.User{Login: "anna", Roles: []*auth.Role{{Permissions: permission}}}
}

func (f deepChoiceFixture) request(t *testing.T, user *auth.User, sources map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return f.requestWith(t, user, sources, nil)
}

func (f deepChoiceFixture) requestWith(t *testing.T, user *auth.User, sources map[string]string, extra url.Values) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(sources)
	if err != nil {
		t.Fatal(err)
	}
	query := url.Values{
		"form_entity": {f.owner.Name},
		"form":        {"ФормаОбъекта"},
		"element":     {"fault-picker"},
		"sources":     {string(encoded)},
		"limit":       {"100"},
	}
	for key, values := range extra {
		query[key] = values
	}
	router := chi.NewRouter()
	f.server.Mount(router)
	request := httptest.NewRequest(http.MethodGet, "/ui/_ref-options/"+url.PathEscape(f.target.Name)+"?"+query.Encode(), nil)
	request = request.WithContext(auth.ContextWithUser(request.Context(), user))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func (f deepChoiceFixture) serve(t *testing.T, user *auth.User, source string) *httptest.ResponseRecorder {
	t.Helper()
	return f.request(t, user, map[string]string{"Объект.Направление.ГруппаНеисправностей": source})
}

func (f deepChoiceFixture) labels(t *testing.T, recorder *httptest.ResponseRecorder) []string {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeChoiceHTTP(t, recorder)
	labels := make([]string, 0, len(response.Items))
	for _, item := range response.Items {
		labels = append(labels, fmt.Sprint(item["_label"]))
	}
	if response.Total != len(labels) {
		t.Fatalf("total=%d, items=%d: %#v", response.Total, len(labels), response.Items)
	}
	return labels
}

func TestRefOptionsDeepChoiceSourceReadsIntermediateUnderUserRights(t *testing.T) {
	f := newDeepChoiceFixture(t)
	user := deepChoiceUser(nil)

	t.Run("отбор идёт по реквизиту выбранного направления", func(t *testing.T) {
		labels := f.labels(t, f.serve(t, user, f.directionA.String()))
		if len(labels) != 2 {
			t.Fatalf("ожидались неисправности группы 1, получено %v", labels)
		}
		for _, label := range labels {
			if label == "Течёт" {
				t.Fatalf("чужая группа попала в подбор: %v", labels)
			}
		}
		other := f.labels(t, f.serve(t, user, f.directionB.String()))
		if len(other) != 1 || other[0] != "Течёт" {
			t.Fatalf("смена ведущего поля не пересчитала отбор: %v", other)
		}
	})

	t.Run("закрытый строковым доступом посредник даёт пустую выдачу", func(t *testing.T) {
		if labels := f.labels(t, f.serve(t, user, f.directionC.String())); len(labels) != 0 {
			t.Fatalf("направление чужого оператора раскрыло свою группу: %v", labels)
		}
	})

	t.Run("несуществующий посредник неотличим от закрытого", func(t *testing.T) {
		if labels := f.labels(t, f.serve(t, user, deepChoiceUUID(0x20, 99).String())); len(labels) != 0 {
			t.Fatalf("выдача по несуществующей записи: %v", labels)
		}
	})

	t.Run("пустой реквизит посредника даёт пустую выдачу", func(t *testing.T) {
		if labels := f.labels(t, f.serve(t, user, f.directionD.String())); len(labels) != 0 {
			t.Fatalf("незаполненная группа отобрала весь справочник: %v", labels)
		}
	})

	t.Run("реквизит под полевой политикой не участвует в отборе", func(t *testing.T) {
		for _, strategy := range []string{"hide", "mask_all"} {
			masked := deepChoiceUser(auth.FieldPolicies{"ГруппаНеисправностей": auth.FieldPolicy{Read: strategy}})
			if labels := f.labels(t, f.serve(t, masked, f.directionA.String())); len(labels) != 0 {
				t.Fatalf("политика %q обойдена подбором: %v", strategy, labels)
			}
		}
	})

	t.Run("пустой источник — пустая выдача, а не весь справочник", func(t *testing.T) {
		if labels := f.labels(t, f.serve(t, user, "")); len(labels) != 0 {
			t.Fatalf("без ведущего значения показан весь справочник: %v", labels)
		}
	})

	t.Run("испорченное и постороннее значение источника — ошибка запроса", func(t *testing.T) {
		if recorder := f.serve(t, user, "не-uuid"); recorder.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		recorder := f.request(t, user, map[string]string{"Объект.Направление.Аудитория": f.directionA.String()})
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("необъявленный источник принят: status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

// Строковый конец пути: строковый реквизит цели сравнивается со строковым
// реквизитом посредника — так дом адресного классификатора подбирается по ИД
// улицы (ВладелецКод). Браузер по-прежнему присылает только ссылку ведущего
// поля, а права посредника и полевая маска действуют так же, как для ссылки.
func TestRefOptionsDeepChoiceStringSourceReadsIntermediateUnderUserRights(t *testing.T) {
	f := newDeepChoiceFixture(t)
	f.owner.Forms[0].Elements[1].ChoiceFilter = []metadata.FormChoiceCondition{{
		Field: "Аудитория", Op: metadata.FormChoiceOpEqual, From: "Объект.Направление.Аудитория",
	}}
	user := deepChoiceUser(nil)
	serve := func(user *auth.User, source string) *httptest.ResponseRecorder {
		return f.request(t, user, map[string]string{"Объект.Направление.Аудитория": source})
	}

	t.Run("отбор идёт по строковому реквизиту выбранного направления", func(t *testing.T) {
		labels := f.labels(t, serve(user, f.directionA.String()))
		sort.Strings(labels)
		if strings.Join(labels, ",") != "Не включается,Шумит" {
			t.Fatalf("ожидались неисправности аудитории anna, получено %v", labels)
		}
	})

	t.Run("текущее значение проверяется тем же отбором", func(t *testing.T) {
		sources := map[string]string{"Объект.Направление.Аудитория": f.directionA.String()}
		for selected, want := range map[uuid.UUID]bool{deepChoiceUUID(0x30, 3): true, deepChoiceUUID(0x30, 2): false} {
			response := decodeChoiceHTTP(t, f.requestWith(t, user, sources, url.Values{"selected_id": {selected.String()}}))
			if response.SelectedAllowed == nil || *response.SelectedAllowed != want {
				t.Fatalf("selected %s: selected_allowed=%v, want %v", selected, response.SelectedAllowed, want)
			}
		}
	})

	t.Run("первая отрисовка формы отбирает по строке сохранённого направления", func(t *testing.T) {
		// У направления B группа 2 и аудитория anna: отбор по группе дал бы
		// «Течёт», по аудитории — «Не включается» и «Шумит».
		if err := f.server.store.Upsert(context.Background(), f.owner.Name, f.ownerID, map[string]any{
			"Направление": f.directionB.String(),
		}, f.owner); err != nil {
			t.Fatal(err)
		}
		options := f.initialOptions(t, user)
		if len(options) != 2 || !options[deepChoiceUUID(0x30, 1).String()] || !options[deepChoiceUUID(0x30, 3).String()] {
			t.Fatalf("initial string choice options = %v, want audience anna", options)
		}
	})

	t.Run("закрытый строковым доступом посредник даёт пустую выдачу", func(t *testing.T) {
		// Аудитория направления C — bob: прочитай сервер его в обход прав,
		// в выдаче оказался бы «Течёт».
		if labels := f.labels(t, serve(user, f.directionC.String())); len(labels) != 0 {
			t.Fatalf("направление чужого оператора раскрыло свою аудиторию: %v", labels)
		}
	})

	t.Run("несуществующий посредник неотличим от закрытого", func(t *testing.T) {
		if labels := f.labels(t, serve(user, deepChoiceUUID(0x20, 99).String())); len(labels) != 0 {
			t.Fatalf("выдача по несуществующей записи: %v", labels)
		}
	})

	t.Run("реквизит под полевой политикой не участвует в отборе", func(t *testing.T) {
		for _, strategy := range []string{"hide", "mask_all"} {
			masked := deepChoiceUser(auth.FieldPolicies{"Аудитория": auth.FieldPolicy{Read: strategy}})
			if labels := f.labels(t, serve(masked, f.directionA.String())); len(labels) != 0 {
				t.Fatalf("политика %q обойдена подбором: %v", strategy, labels)
			}
		}
	})

	t.Run("браузер не подставляет строку вместо ссылки", func(t *testing.T) {
		if recorder := serve(user, "anna"); recorder.Code != http.StatusBadRequest {
			t.Fatalf("строка принята как значение источника: status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("пустой или NULL-реквизит посредника не отбирает записи с пустым реквизитом", func(t *testing.T) {
		ctx := context.Background()
		for number, audience := range map[int]string{7: "", 8: "   "} {
			if err := f.server.store.Upsert(ctx, f.target.Name, deepChoiceUUID(0x30, number), map[string]any{
				"Наименование": fmt.Sprintf("Аудитория %q", audience), "Аудитория": audience,
			}, f.target); err != nil {
				t.Fatal(err)
			}
		}
		// Строковый доступ снят, чтобы направление без аудитории было видно:
		// выдача пуста из-за пустого значения, а не из-за прав.
		open := deepChoiceUser(nil)
		open.Roles[0].Permissions.RowAccess = auth.RowAccess{}
		for number, audience := range map[int]any{5: "", 6: "   ", 10: nil} {
			empty := deepChoiceUUID(0x20, number)
			if err := f.server.store.Upsert(ctx, "Направление", empty, map[string]any{
				"Наименование": fmt.Sprintf("Направление %d", number), "Аудитория": audience,
			}, f.server.reg.GetEntity("Направление")); err != nil {
				t.Fatal(err)
			}
			if labels := f.labels(t, serve(open, empty.String())); len(labels) != 0 {
				t.Fatalf("аудитория %#v отобрала записи: %v", audience, labels)
			}
		}
	})

	t.Run("значение посредника сравнивается точно", func(t *testing.T) {
		padded := deepChoiceUUID(0x20, 9)
		if err := f.server.store.Upsert(context.Background(), "Направление", padded, map[string]any{
			"Наименование": "Направление F", "Аудитория": "anna ",
		}, f.server.reg.GetEntity("Направление")); err != nil {
			t.Fatal(err)
		}
		open := deepChoiceUser(nil)
		open.Roles[0].Permissions.RowAccess = auth.RowAccess{}
		if labels := f.labels(t, serve(open, padded.String())); len(labels) != 0 {
			t.Fatalf("«anna » с пробелом отобрала записи «anna»: %v", labels)
		}
	})

	// Последним: подтест меняет условие формы.
	t.Run("несовместимый род конца пути — ошибка до чтения записи", func(t *testing.T) {
		for _, condition := range []metadata.FormChoiceCondition{
			{Field: "Группа", Op: metadata.FormChoiceOpEqual, From: "Объект.Направление.Аудитория"},
			{Field: "Аудитория", Op: metadata.FormChoiceOpEqual, From: "Объект.Направление.ГруппаНеисправностей"},
		} {
			f.owner.Forms[0].Elements[1].ChoiceFilter = []metadata.FormChoiceCondition{condition}
			// Одинаковый ответ для существующей и несуществующей записи: иначе
			// ошибка выдавала бы, есть ли запись.
			for _, source := range []uuid.UUID{f.directionA, deepChoiceUUID(0x20, 99)} {
				recorder := f.request(t, user, map[string]string{condition.From: source.String()})
				if recorder.Code != http.StatusBadRequest {
					t.Fatalf("%s ← %s, источник %s: status=%d body=%s", condition.Field, condition.From, source, recorder.Code, recorder.Body.String())
				}
			}
		}
	})
}

func TestRefOptionsBooleanLiteralRespectsTargetFieldPolicy(t *testing.T) {
	f := newDeepChoiceFixture(t)
	value := false
	f.owner.Forms[0].Elements[1].ChoiceFilter = []metadata.FormChoiceCondition{{
		Field: "Секретный", Op: metadata.FormChoiceOpEqual, Value: &value,
	}}
	if got := decodeChoiceHTTP(t, f.request(t, deepChoiceUser(nil), map[string]string{})); got.Total != 2 || len(got.Items) != 2 {
		t.Fatalf("unmasked literal should find two rows: %#v", got)
	}

	var hiddenResponse string
	for _, strategy := range []string{"hide", "mask_all"} {
		user := deepChoiceUser(nil)
		user.Roles[0].Permissions.FieldAccess.Catalogs = map[string]auth.FieldPolicies{
			f.target.Name: {"Секретный": {Read: strategy}},
		}
		response := f.request(t, user, map[string]string{})
		if response.Code != http.StatusOK {
			t.Fatalf("policy %s: status=%d body=%s", strategy, response.Code, response.Body.String())
		}
		got := decodeChoiceHTTP(t, response)
		if got.Total != 0 || len(got.Items) != 0 {
			t.Fatalf("policy %s leaked the boolean via choice results: %#v", strategy, got)
		}
		if hiddenResponse != "" && response.Body.String() != hiddenResponse {
			t.Fatalf("hide and mask_all returned distinguishable results: %q vs %q", hiddenResponse, response.Body.String())
		}
		hiddenResponse = response.Body.String()
	}
}

func TestRefOptionsDeepChoiceStringTargetFieldPolicyNormalizesName(t *testing.T) {
	for _, field := range []string{"Аудитория", " Аудитория ", "\tаУдИтОрИя\u00a0"} {
		t.Run(field, func(t *testing.T) {
			f := newDeepChoiceFixture(t)
			f.owner.Forms[0].Elements[1].ChoiceFilter = []metadata.FormChoiceCondition{{
				Field: field, Op: metadata.FormChoiceOpEqual, From: "Объект.Направление.Аудитория",
			}}
			sources := map[string]string{"Объект.Направление.Аудитория": f.directionA.String()}
			extra := url.Values{"selected_id": {deepChoiceUUID(0x30, 1).String()}}
			open := deepChoiceUser(nil)
			response := f.requestWith(t, open, sources, extra)
			if response.Code != http.StatusOK {
				t.Fatalf("unmasked status=%d body=%s", response.Code, response.Body.String())
			}
			got := decodeChoiceHTTP(t, response)
			if got.Total != 2 || len(got.Items) != 2 || got.SelectedAllowed == nil || !*got.SelectedAllowed {
				t.Fatalf("unmasked string filter should find two rows and allow the selection: %#v", got)
			}
			if options := f.initialOptions(t, open); len(options) != 2 {
				t.Fatalf("unmasked initial options=%v, want two", options)
			}

			var hiddenResponse string
			for _, strategy := range []string{"hide", "mask_all"} {
				t.Run(strategy, func(t *testing.T) {
					user := deepChoiceUser(nil)
					user.Roles[0].Permissions.FieldAccess.Catalogs = map[string]auth.FieldPolicies{
						f.target.Name: {"Аудитория": {Read: strategy}},
					}
					response := f.requestWith(t, user, sources, extra)
					if response.Code != http.StatusOK {
						t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
					}
					got := decodeChoiceHTTP(t, response)
					if got.Total != 0 || len(got.Items) != 0 || got.SelectedAllowed == nil || *got.SelectedAllowed {
						t.Fatalf("masked target leaked its string via choice results: %#v", got)
					}
					if hiddenResponse != "" && response.Body.String() != hiddenResponse {
						t.Fatalf("hide and mask_all responses differ: %q vs %q", hiddenResponse, response.Body.String())
					}
					hiddenResponse = response.Body.String()
					if options := f.initialOptions(t, user); len(options) != 0 {
						t.Fatalf("masked target leaked its string via initial options: %v", options)
					}
				})
			}
		})
	}
}
