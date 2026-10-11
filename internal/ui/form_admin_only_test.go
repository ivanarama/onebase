package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

// editable_admin_only — запрет по тому, КТО смотрит, а не по данным записи.
// Поэтому он проверяется во всех трёх местах, где может исчезнуть: в разметке,
// в ответе события формы и на записи. Последнее — главное: разметка запрета
// это подсказка интерфейсу, а не защита.
func adminOnlyFixture(t *testing.T) (*Server, *metadata.Entity, uuid.UUID) {
	t.Helper()
	typeField := &metadata.FormElement{
		Kind: metadata.FormElementField, Name: "ПолеТипЗвонка", DataPath: "Объект.ТипЗвонка",
		EditableAdminOnly: true,
		// Ложное readonly_when: по данным записи поле открыто, и именно этот
		// случай раньше снимал запрет после первого же события формы.
		ReadOnlyWhen: `Наименование = "заперт"`,
	}
	button := &metadata.FormElement{
		Kind: metadata.FormElementButton, Name: "Кн",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Пусто"},
	}
	form := managedObjectForm(
		fieldEl("ПолеНаименование", "Объект.Наименование"),
		typeField,
		fieldEl("ПолеКомментарий", "Объект.Комментарий"),
		button,
	)
	form.ProgramAST = mustParse(t, "Процедура Пусто() КонецПроцедуры")
	ent := &metadata.Entity{
		Name: "Звонок", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "ТипЗвонка", Type: metadata.FieldTypeString, Default: "Входящий по умолчанию"},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
		},
		Forms: []*metadata.FormModule{form},
	}
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	srv.authRepo = auth.NewRepo(srv.store)
	id := uuid.New()
	if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{
		"Наименование": "звонок", "ТипЗвонка": "Входящий", "Комментарий": "исходно",
	}, ent); err != nil {
		t.Fatal(err)
	}
	return srv, ent, id
}

func adminOnlyOperator(entityName string) *auth.User {
	return &auth.User{Login: "anna", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs: map[string][]string{entityName: {"read", "write"}},
	}}}}
}

func inputTagByName(t *testing.T, html, name string) string {
	t.Helper()
	marker := `name="` + name + `"`
	at := strings.Index(html, marker)
	if at < 0 {
		t.Fatalf("в разметке нет поля %q", name)
	}
	start := strings.LastIndex(html[:at], "<")
	end := strings.Index(html[at:], ">")
	if start < 0 || end < 0 {
		t.Fatalf("не удалось выделить тег поля %q", name)
	}
	return html[start : at+end]
}

func TestEditableAdminOnlyLocksMarkupForNonAdmin(t *testing.T) {
	_, ent, _ := adminOnlyFixture(t)
	form := ent.Forms[0]
	values := map[string]string{"Наименование": "звонок", "ТипЗвонка": "Входящий", "Комментарий": "исходно"}

	locked := renderFormKeysHTML(t, ent, form, values, nil, false)
	if !strings.Contains(inputTagByName(t, locked, "ТипЗвонка"), "readonly") {
		t.Error("неадминистратор получил редактируемое поле")
	}
	if strings.Contains(inputTagByName(t, locked, "Комментарий"), "readonly") {
		t.Error("заперлось соседнее поле, которое запрета не просило")
	}

	open := renderFormKeysHTML(t, ent, form, values, nil, true)
	if strings.Contains(inputTagByName(t, open, "ТипЗвонка"), "readonly") {
		t.Error("администратору поле осталось запертым")
	}
}

func TestEditableAdminOnlyDropsForgedValueOnWrite(t *testing.T) {
	srv, ent, id := adminOnlyFixture(t)

	// Подделанный POST: разметка запрета клиенту не указ, значение приходит.
	body := url.Values{
		"Наименование": {"звонок"},
		"ТипЗвонка":    {"Исходящий"},
		"Комментарий":  {"правка оператора"},
	}
	req := reqWithChi(http.MethodPost, "/ui/catalog/"+ent.Name+"/"+id.String(), body,
		map[string]string{"entity": ent.Name, "id": id.String()})
	req = req.WithContext(auth.ContextWithUser(req.Context(), adminOnlyOperator(ent.Name)))
	recorder := httptest.NewRecorder()
	srv.submitEdit(recorder, req)
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("запись не прошла: %d %s", recorder.Code, recorder.Body.String())
	}

	row, err := srv.store.GetByID(t.Context(), ent.Name, id, ent)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(row["ТипЗвонка"]); got != "Входящий" {
		t.Fatalf("подделанное значение запертого поля записалось: %q", got)
	}
	if got := fmt.Sprint(row["Комментарий"]); got != "правка оператора" {
		t.Fatalf("обычное поле не записалось: %q", got)
	}

	// Администратору то же поле доступно — запрет адресный, а не глухой.
	adminBody := url.Values{
		"Наименование": {"звонок"},
		"ТипЗвонка":    {"Исходящий"},
		"Комментарий":  {"правка оператора"},
	}
	adminReq := reqWithChi(http.MethodPost, "/ui/catalog/"+ent.Name+"/"+id.String(), adminBody,
		map[string]string{"entity": ent.Name, "id": id.String()})
	adminReq = adminReq.WithContext(auth.ContextWithUser(adminReq.Context(), &auth.User{Login: "root", IsAdmin: true}))
	adminRecorder := httptest.NewRecorder()
	srv.submitEdit(adminRecorder, adminReq)
	if adminRecorder.Code != http.StatusSeeOther {
		t.Fatalf("админская запись не прошла: %d %s", adminRecorder.Code, adminRecorder.Body.String())
	}
	row, err = srv.store.GetByID(t.Context(), ent.Name, id, ent)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(row["ТипЗвонка"]); got != "Исходящий" {
		t.Fatalf("администратор не смог изменить поле: %q", got)
	}
}

func TestEditableAdminOnlyForgedNewValuePreservesDefault(t *testing.T) {
	for _, test := range []struct {
		name string
		body url.Values
	}{
		{"поле не прислано", url.Values{"Наименование": {"новый"}, "Комментарий": {"обычное поле"}}},
		{"поле подделано", url.Values{"Наименование": {"новый"}, "ТипЗвонка": {"Подделка"}, "Комментарий": {"обычное поле"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			srv, ent, _ := adminOnlyFixture(t)
			req := reqWithChi(http.MethodPost, "/ui/catalog/"+ent.Name+"/new", test.body,
				map[string]string{"entity": ent.Name})
			req = req.WithContext(auth.ContextWithUser(req.Context(), adminOnlyOperator(ent.Name)))
			rec := httptest.NewRecorder()
			srv.submit(rec, req)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("запись не прошла: %d %s", rec.Code, rec.Body.String())
			}
			location, err := url.Parse(rec.Header().Get("Location"))
			if err != nil {
				t.Fatalf("некорректный адрес записи: %v", err)
			}
			id, err := uuid.Parse(path.Base(location.Path))
			if err != nil {
				t.Fatalf("нет id новой записи: %v", err)
			}
			row, err := srv.store.GetByID(t.Context(), ent.Name, id, ent)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprint(row["ТипЗвонка"]); got != "Входящий по умолчанию" {
				t.Fatalf("защищённый дефолт изменился: %q", got)
			}
			if got := fmt.Sprint(row["Комментарий"]); got != "обычное поле" {
				t.Fatalf("обычное поле не записалось: %q", got)
			}
		})
	}
}

func TestEditableAdminOnlyStaysLockedAfterFormEvent(t *testing.T) {
	srv, ent, id := adminOnlyFixture(t)
	body := url.Values{
		"_element": {"Кн"}, "_event": {string(metadata.FormEventOnClick)},
		"_kind": {"object"}, "_id": {id.String()},
		"Наименование": {"звонок"}, "ТипЗвонка": {"Входящий"}, "Комментарий": {"исходно"},
	}
	recorder := runFormEventAs(t, srv, ent, body, adminOnlyOperator(ent.Name))
	if recorder.Code != http.StatusOK {
		t.Fatalf("событие формы не отработало: %d %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		ElementStates *struct {
			ReadOnly map[string]bool `json:"readonly"`
		} `json:"elementStates"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("разбор ответа: %v; тело=%s", err, recorder.Body.String())
	}
	if response.ElementStates == nil || !response.ElementStates.ReadOnly[путьСостояния(t, ent.Forms[0], "ПолеТипЗвонка")] {
		t.Fatalf("после события запрет исчез: %s", recorder.Body.String())
	}
	if response.ElementStates.ReadOnly[путьСостояния(t, ent.Forms[0], "ПолеКомментарий")] {
		t.Fatalf("запрет расползся на соседнее поле: %s", recorder.Body.String())
	}

	adminRecorder := runFormEventAs(t, srv, ent, body, &auth.User{Login: "root", IsAdmin: true})
	if adminRecorder.Code != http.StatusOK {
		t.Fatalf("админское событие не отработало: %d %s", adminRecorder.Code, adminRecorder.Body.String())
	}
	var adminResponse struct {
		ElementStates *struct {
			ReadOnly map[string]bool `json:"readonly"`
		} `json:"elementStates"`
	}
	if err := json.Unmarshal(adminRecorder.Body.Bytes(), &adminResponse); err != nil {
		t.Fatalf("разбор админского ответа: %v", err)
	}
	if adminResponse.ElementStates != nil && adminResponse.ElementStates.ReadOnly[путьСостояния(t, ent.Forms[0], "ПолеТипЗвонка")] {
		t.Fatalf("администратору поле заперлось: %s", adminRecorder.Body.String())
	}
}

func TestEditableAdminOnlyRejectsForgedValueBeforeFormEventWrite(t *testing.T) {
	srv, ent, id := adminOnlyFixture(t)
	ent.Forms[0].ProgramAST = mustParse(t, `
Процедура Пусто()
    Объект.Записать();
КонецПроцедуры`)
	body := url.Values{
		"_element": {"Кн"}, "_event": {string(metadata.FormEventOnClick)},
		"_kind": {"object"}, "_id": {id.String()},
		"Наименование": {"звонок"}, "ТипЗвонка": {"Подделка"},
		"Комментарий": {"из события"},
	}
	rec := runFormEventAs(t, srv, ent, body, adminOnlyOperator(ent.Name))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"ok":false`) {
		t.Fatalf("событие не прошло: %d %s", rec.Code, rec.Body.String())
	}
	row, err := srv.store.GetByID(t.Context(), ent.Name, id, ent)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(row["ТипЗвонка"]); got != "Входящий" {
		t.Fatalf("обработчик записал подделанное значение: %q", got)
	}
	if got := fmt.Sprint(row["Комментарий"]); got != "из события" {
		t.Fatalf("обычное поле не записалось: %q", got)
	}
}

func TestEditableAdminOnlyRejectsForgedValueOnCloseIntent(t *testing.T) {
	srv, ent, id := adminOnlyFixture(t)
	body := closeIntentBody(uuid.NewString(), "ok", "звонок")
	body.Set("_close_mode", "save")
	body.Set("_id", id.String())
	body.Set("ТипЗвонка", "Подделка")
	body.Set("Комментарий", "при закрытии")
	rec := executeFormCloseIntentAsUser(t, srv, ent, body, adminOnlyOperator(ent.Name))
	if rec.Code != http.StatusOK {
		t.Fatalf("закрытие не прошло: %d %s", rec.Code, rec.Body.String())
	}
	row, err := srv.store.GetByID(t.Context(), ent.Name, id, ent)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(row["ТипЗвонка"]); got != "Входящий" {
		t.Fatalf("закрытие записало подделанное значение: %q", got)
	}
}

// Новая запись из события формы: восстанавливать значение неоткуда — строки ещё
// нет, а присланное запрещено. Без серверного умолчания запрет на правку
// оборачивался бы потерей объявленного значения: обработчик с Объект.Записать()
// сохранял NULL вместо «Входящий по умолчанию».
func TestEditableAdminOnlyKeepsDeclaredDefaultOnFormEventCreate(t *testing.T) {
	for _, test := range []struct {
		name  string
		field []string
	}{
		{"поле не прислано", nil},
		{"поле подделано", []string{"Подделка"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			srv, ent, _ := adminOnlyFixture(t)
			ent.Forms[0].ProgramAST = mustParse(t, `
Процедура Пусто()
    Объект.Записать();
КонецПроцедуры`)
			body := url.Values{
				"_element": {"Кн"}, "_event": {string(metadata.FormEventOnClick)},
				"_kind":        {"object"},
				"Наименование": {"новый из события"},
				"Комментарий":  {"обычное поле"},
			}
			if test.field != nil {
				body["ТипЗвонка"] = test.field
			}
			rec := runFormEventAs(t, srv, ent, body, adminOnlyOperator(ent.Name))
			if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"ok":false`) {
				t.Fatalf("событие не прошло: %d %s", rec.Code, rec.Body.String())
			}
			row := storedByName(t, srv, ent, "новый из события")
			if got := fmt.Sprint(row["ТипЗвонка"]); got != "Входящий по умолчанию" {
				t.Fatalf("новая запись потеряла защищённое умолчание: %q", got)
			}
			if got := fmt.Sprint(row["Комментарий"]); got != "обычное поле" {
				t.Fatalf("обычное поле не записалось: %q", got)
			}
		})
	}
}

// Тот же путь при закрытии формы с сохранением: close-intent пишет объект сам,
// и новая запись обязана получить то же умолчание.
func TestEditableAdminOnlyKeepsDeclaredDefaultOnCloseIntentCreate(t *testing.T) {
	srv, ent, _ := adminOnlyFixture(t)
	body := closeIntentBody(uuid.NewString(), "ok", "новый при закрытии")
	body.Set("_close_mode", "save")
	body.Set("Наименование", "новый при закрытии")
	body.Set("ТипЗвонка", "Подделка")
	body.Set("Комментарий", "обычное поле")
	rec := executeFormCloseIntentAsUser(t, srv, ent, body, adminOnlyOperator(ent.Name))
	if rec.Code != http.StatusOK {
		t.Fatalf("закрытие не прошло: %d %s", rec.Code, rec.Body.String())
	}
	row := storedByName(t, srv, ent, "новый при закрытии")
	if got := fmt.Sprint(row["ТипЗвонка"]); got != "Входящий по умолчанию" {
		t.Fatalf("новая запись при закрытии потеряла защищённое умолчание: %q", got)
	}
}

// storedByName находит записанный объект по наименованию: у новой записи id
// выдаёт сервер, и снаружи он известен только из ответа, который в этих
// сценариях не обязан его возвращать.
func storedByName(t *testing.T, srv *Server, ent *metadata.Entity, name string) map[string]any {
	t.Helper()
	rows, err := srv.store.List(t.Context(), ent.Name, ent, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if fmt.Sprint(row["Наименование"]) == name {
			return row
		}
	}
	t.Fatalf("запись %q не сохранилась: %+v", name, rows)
	return nil
}

// adminOnlyHierarchyFixture — иерархический справочник, у которого запертыми
// объявлены служебные ключи объекта: parent_id и is_folder. В entity.Fields их
// нет, поэтому formToFields их не приносит, а переносит отдельный
// mergeSubmittedEntityServiceFields — мимо которого запрет и обходился.
func adminOnlyHierarchyFixture(t *testing.T) (srv *Server, ent *metadata.Entity, groupA, groupB, item uuid.UUID) {
	t.Helper()
	form := managedObjectForm(
		fieldEl("ПолеНаименование", "Объект.Наименование"),
		&metadata.FormElement{
			Kind: metadata.FormElementField, Name: "ПолеРодитель", DataPath: "Объект.parent_id",
			EditableAdminOnly: true,
		},
		&metadata.FormElement{
			Kind: metadata.FormElementField, Name: "ПолеГруппа", DataPath: "Объект.is_folder",
			EditableAdminOnly: true,
		},
	)
	ent = &metadata.Entity{
		Name: "Папки", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
		},
		Forms: []*metadata.FormModule{form},
	}
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	srv.authRepo = auth.NewRepo(srv.store)
	groupA, groupB, item = uuid.New(), uuid.New(), uuid.New()
	for id, name := range map[uuid.UUID]string{groupA: "группа А", groupB: "группа Б"} {
		if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{
			"Наименование": name, "is_folder": true,
		}, ent); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.store.Upsert(ctx, ent.Name, item, map[string]any{
		"Наименование": "элемент", "Комментарий": "исходно",
		"parent_id": groupA.String(), "is_folder": false,
	}, ent); err != nil {
		t.Fatal(err)
	}
	return srv, ent, groupA, groupB, item
}

func submitHierarchyEdit(t *testing.T, srv *Server, ent *metadata.Entity, id uuid.UUID, body url.Values, user *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	req := reqWithChi(http.MethodPost, "/ui/catalog/"+ent.Name+"/"+id.String(), body,
		map[string]string{"entity": ent.Name, "id": id.String()})
	req = req.WithContext(auth.ContextWithUser(req.Context(), user))
	rec := httptest.NewRecorder()
	srv.submitEdit(rec, req)
	return rec
}

// Главный случай: обычный пользователь подделывает служебные ключи обычным
// публичным submit существующего элемента. Проверка формы такое размещение
// принимает, разметка поле запирает — но запрет обязан держать сервер. Раньше
// dropAdminOnlyFields видел только карту полей сущности, parent_id оставался в
// POST, и следующий mergeSubmittedEntityServiceFields переносил подделанное
// значение в сохраняемый объект: элемент менял родителя.
func TestEditableAdminOnlyKeepsHierarchyKeysOnForgedSubmit(t *testing.T) {
	srv, ent, groupA, groupB, item := adminOnlyHierarchyFixture(t)

	rec := submitHierarchyEdit(t, srv, ent, item, url.Values{
		"Наименование": {"элемент"},
		"Комментарий":  {"правка оператора"},
		"parent_id":    {groupB.String()},
		"is_folder":    {"true"},
	}, adminOnlyOperator(ent.Name))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("запись не прошла: %d %s", rec.Code, rec.Body.String())
	}

	row, err := srv.store.GetByID(t.Context(), ent.Name, item, ent)
	if err != nil {
		t.Fatal(err)
	}
	if got := refValueString(row["parent_id"]); got != groupA.String() {
		t.Fatalf("подделанный parent_id записался: %q, ожидалась прежняя группа %s", got, groupA)
	}
	if asBool(row["is_folder"]) {
		t.Fatal("подделанный is_folder записался: элемент стал группой")
	}
	// Запрет адресный: соседнее поле той же формы записалось как обычно.
	if got := fmt.Sprint(row["Комментарий"]); got != "правка оператора" {
		t.Fatalf("обычное поле не записалось: %q", got)
	}

	// Администратору те же ключи доступны — иначе «запрет» стал бы отказом всем.
	rec = submitHierarchyEdit(t, srv, ent, item, url.Values{
		"Наименование": {"элемент"},
		"Комментарий":  {"правка администратора"},
		"parent_id":    {groupB.String()},
		"is_folder":    {"true"},
	}, &auth.User{Login: "root", IsAdmin: true})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("админская запись не прошла: %d %s", rec.Code, rec.Body.String())
	}
	row, err = srv.store.GetByID(t.Context(), ent.Name, item, ent)
	if err != nil {
		t.Fatal(err)
	}
	if got := refValueString(row["parent_id"]); got != groupB.String() {
		t.Fatalf("администратор не смог перенести элемент: parent_id = %q", got)
	}
	if !asBool(row["is_folder"]) {
		t.Fatal("администратор не смог сделать элемент группой")
	}
}

// Обратная сторона запрета: без editable_admin_only служебные ключи обычного
// пользователя обязаны доезжать до записи. Иначе fail-closed превратился бы в
// «иерархию не меняет никто» и сломал обычный перенос элемента по группам.
func TestHierarchyKeysStayEditableWithoutAdminOnly(t *testing.T) {
	form := managedObjectForm(
		fieldEl("ПолеНаименование", "Объект.Наименование"),
		fieldEl("ПолеРодитель", "Объект.parent_id"),
	)
	ent := &metadata.Entity{
		Name: "Папки", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		Forms:  []*metadata.FormModule{form},
	}
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	srv.authRepo = auth.NewRepo(srv.store)
	groupB, item := uuid.New(), uuid.New()
	if err := srv.store.Upsert(ctx, ent.Name, groupB, map[string]any{
		"Наименование": "группа Б", "is_folder": true,
	}, ent); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.Upsert(ctx, ent.Name, item, map[string]any{
		"Наименование": "элемент", "is_folder": false,
	}, ent); err != nil {
		t.Fatal(err)
	}

	rec := submitHierarchyEdit(t, srv, ent, item, url.Values{
		"Наименование": {"элемент"},
		"parent_id":    {groupB.String()},
		"is_folder":    {"false"},
	}, adminOnlyOperator(ent.Name))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("запись не прошла: %d %s", rec.Code, rec.Body.String())
	}
	row, err := srv.store.GetByID(t.Context(), ent.Name, item, ent)
	if err != nil {
		t.Fatal(err)
	}
	if got := refValueString(row["parent_id"]); got != groupB.String() {
		t.Fatalf("обычный перенос элемента не сработал: parent_id = %q, ожидалась %s", got, groupB)
	}
}

// Список служебных ключей — один для переноса и для запрета. Если
// mergeSubmittedEntityServiceFields научат новому ключу, а entityServiceFormKeys
// забудут, запрет на нём молча перестанет действовать — этот тест ловит
// расхождение по факту: каждый ключ из списка доезжает до объекта и каждый
// отбирается серверным запретом.
func TestEntityServiceFormKeysMatchServerGuard(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Папки", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	for _, key := range entityServiceFormKeys {
		body := url.Values{"Наименование": {"элемент"}, key: {"true"}}
		req := reqWithChi(http.MethodPost, "/ui/catalog/"+ent.Name, body, nil)
		if err := req.ParseForm(); err != nil {
			t.Fatalf("%s: разбор формы: %v", key, err)
		}

		fields := map[string]any{}
		mergeSubmittedEntityServiceFields(req, ent, fields)
		if _, ok := fields[key]; !ok {
			t.Errorf("%s: в списке есть, а merge его не переносит", key)
		}

		form := managedObjectForm(&metadata.FormElement{
			Kind: metadata.FormElementField, Name: "Поле" + key, DataPath: "Объект." + key,
			EditableAdminOnly: true,
		})
		dropped := dropAdminOnlyFields(form, map[string]any{}, false)
		if len(dropped) != 1 || !strings.EqualFold(dropped[0], key) {
			t.Errorf("%s: серверный запрет его не отбирает (dropped = %v)", key, dropped)
		}
	}
}

// Публичная карточка и HTTP-событие передают настоящие пути в managed.js.
// Две копии закрытого поля и соседний открытый элемент могут иметь одно имя
// или вовсе не иметь имени: клиент обязан сохранить три разных состояния.
func TestEditableAdminOnlyPlacementPathsThroughHTTPAndClient(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for placement path regression")
	}
	for _, name := range []string{"Двойник", ""} {
		for _, admin := range []bool{false, true} {
			t.Run(fmt.Sprintf("name=%q/admin=%v", name, admin), func(t *testing.T) {
				srv, ent, id := adminOnlyFixture(t)
				form := ent.Forms[0]
				locked := form.Elements[1]
				locked.Name = name
				copy := *locked
				open := form.Elements[2]
				open.Name = name
				open.ReadOnlyWhen = `Наименование = "заперт"`
				group := &metadata.FormElement{
					Kind: metadata.FormElementGroupBox, Name: "Группа", Orientation: "horizontal", ScrollX: true,
					ReadOnlyWhen: `Наименование = "заперт"`,
					Children:     []*metadata.FormElement{locked, &copy},
				}
				button := form.Elements[3]
				button.Primary = true
				form.Elements = []*metadata.FormElement{group, open, button}
				user := adminOnlyOperator(ent.Name)
				if admin {
					user = &auth.User{Login: "root", IsAdmin: true}
				}
				req := reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/"+id.String(), nil,
					map[string]string{"kind": "catalog", "entity": ent.Name, "id": id.String()})
				req = req.WithContext(auth.ContextWithUser(req.Context(), user))
				rendered := httptest.NewRecorder()
				srv.formEdit(rendered, req)
				if rendered.Code != http.StatusOK {
					t.Fatalf("render: %d %s", rendered.Code, rendered.Body.String())
				}
				if !strings.Contains(rendered.Body.String(), "managed-group-scrollx") || !strings.Contains(rendered.Body.String(), "btn-primary managed-btn") {
					t.Fatal("lost group scrolling or primary button while reconciling templates")
				}
				event := runFormEventAs(t, srv, ent, url.Values{
					"_element": {"Кн"}, "_event": {string(metadata.FormEventOnClick)},
					"_kind": {"object"}, "_id": {id.String()}, "Наименование": {"звонок"},
				}, user)
				response := decodeFormEventResponse(t, event.Body.Bytes())
				if event.Code != http.StatusOK || !response.OK || response.ElementStates == nil {
					t.Fatalf("event: %d %s", event.Code, event.Body.String())
				}
				doc, err := html.Parse(strings.NewReader(rendered.Body.String()))
				if err != nil {
					t.Fatal(err)
				}
				fixture, err := json.Marshal(map[string]any{
					"tree":   domElementNode(findHTMLElement(doc, "body")),
					"states": response.ElementStates, "locked": !admin,
				})
				if err != nil {
					t.Fatal(err)
				}
				fixturePath := filepath.Join(t.TempDir(), "placements.json")
				if err := os.WriteFile(fixturePath, fixture, 0o600); err != nil {
					t.Fatal(err)
				}
				cmd := exec.CommandContext(t.Context(), node, "--test", "static/managed_placement_behavior_test.js") //nolint:gosec // test-only node resolved from PATH
				cmd.Env = append(os.Environ(), "ONEBASE_PLACEMENT_FIXTURE="+fixturePath)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("production client: %v\n%s", err, out)
				}
			})
		}
	}
}
