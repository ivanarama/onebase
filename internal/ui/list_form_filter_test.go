package ui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

func listFilterTestServer() (*Server, *metadata.Entity) {
	сотрудники := &metadata.Entity{
		Name: "Пользователи", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "УчётнаяЗапись", Type: "reference:_users", RefEntity: "_users"}},
	}
	задача := &metadata.Entity{
		Name: "А_Задача", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Инициатор", Type: "reference:Пользователи", RefEntity: "Пользователи"}},
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{задача, сотрудники}})
	return &Server{reg: reg}, задача
}

// Отбор по реквизиту СВЯЗАННОЙ записи: «Инициатор.УчётнаяЗапись = тот, кто
// смотрит» должен стать предикатом со ссылкой, а не сравнением строк.
func TestListFilterPredicate_RefAttribute(t *testing.T) {
	s, задача := listFilterTestServer()
	user := &auth.User{ID: "u-1", Login: "оператор"}
	pred, ok := s.listFilterPredicate(задача,
		metadata.FormListCondition{Field: "Инициатор.УчётнаяЗапись", Value: metadata.ListFilterCurrentUser}, user)
	if !ok {
		t.Fatal("условие не превратилось в предикат")
	}
	if pred.Field != "Инициатор" || pred.RefEntity == nil || pred.RefEntity.Name != "Пользователи" {
		t.Fatalf("ссылка разобрана неверно: %+v", pred)
	}
	if pred.RefPredicate == nil || pred.RefPredicate.Field != "УчётнаяЗапись" || pred.RefPredicate.Value != "u-1" {
		t.Fatalf("вложенное условие неверно: %+v", pred.RefPredicate)
	}
}

// Без пользователя подставить в @ТекущийПользователь нечего: условие
// пропускается целиком, иначе список выглядел бы пустым и «сломанным».
func TestListFilterPredicate_NoUserSkipsCondition(t *testing.T) {
	s, задача := listFilterTestServer()
	if _, ok := s.listFilterPredicate(задача,
		metadata.FormListCondition{Field: "Инициатор.УчётнаяЗапись", Value: metadata.ListFilterCurrentUser}, nil); ok {
		t.Fatal("условие применилось без пользователя")
	}
}

// Неизвестное поле молча не превращается в предикат: иначе список отобрал бы
// по несуществующей колонке и упал бы на SQL.
func TestListFilterPredicate_UnknownReferenceIgnored(t *testing.T) {
	s, задача := listFilterTestServer()
	if _, ok := s.listFilterPredicate(задача,
		metadata.FormListCondition{Field: "Неизвестное.Поле", Value: "x"}, &auth.User{ID: "u-1"}); ok {
		t.Fatal("условие по неизвестному полюприменилось")
	}
}

// Администратор видит список целиком: правило «мои записи» на него не
// распространяется — он разбирает чужие документы.
func TestListFilterPredicate_AdminSeesEverything(t *testing.T) {
	s, задача := listFilterTestServer()
	admin := &auth.User{ID: "root", Login: "admin", IsAdmin: true}
	if _, ok := s.listFilterPredicate(задача,
		metadata.FormListCondition{Field: "Инициатор.УчётнаяЗапись", Value: metadata.ListFilterCurrentUser}, admin); ok {
		t.Fatal("отбор «мои записи» применился к администратору")
	}
}

// Публичный путь списка: условие с roles отбирает «мои записи» только у
// пользователя с этой ролью. Оператор видит свою заявку, сотрудник ОТК и
// администратор — обе (конфигурация cc2: ОТК не видел заявок вовсе).
func TestUI_EntityList_FormFilterAppliesOnlyToListedRoles(t *testing.T) {
	заявка := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Автор", Type: metadata.FieldTypeString},
		},
	}
	заявка.Forms = []*metadata.FormModule{{
		Name: "ФормаСписка", Kind: "list", EntityName: заявка.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "КолонкаНаименование", DataPath: "Наименование"},
		},
		ListFilter: []metadata.FormListCondition{{
			Field: "Автор", Value: metadata.ListFilterCurrentUser, Roles: []string{"Оператор"},
		}},
	}}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{заявка})
	for _, row := range []map[string]any{
		{"Наименование": "Заявка оператора", "Автор": "u-op"},
		{"Наименование": "Чужая заявка", "Автор": "u-other"},
	} {
		if err := s.store.Upsert(ctx, заявка.Name, uuid.New(), row, заявка); err != nil {
			t.Fatal(err)
		}
	}
	роль := func(name string) []*auth.Role {
		return []*auth.Role{{Name: name, Permissions: auth.Permission{
			Catalogs: map[string][]string{заявка.Name: {"read"}},
		}}}
	}
	список := func(user *auth.User) string {
		t.Helper()
		req := reqWithChi(http.MethodGet, "/ui/catalog/"+заявка.Name, nil,
			map[string]string{"kind": "catalog", "entity": заявка.Name})
		req = req.WithContext(auth.ContextWithUser(req.Context(), user))
		rec := httptest.NewRecorder()
		s.list(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("list: status=%d body=%s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}

	оператор := список(&auth.User{ID: "u-op", Login: "operator", Roles: роль("Оператор")})
	if !strings.Contains(оператор, "Заявка оператора") || strings.Contains(оператор, "Чужая заявка") {
		t.Fatal("оператору — только свои записи")
	}
	отк := список(&auth.User{ID: "u-otk", Login: "otk", Roles: роль("СотрудникОТК")})
	if !strings.Contains(отк, "Заявка оператора") || !strings.Contains(отк, "Чужая заявка") {
		t.Fatal("роль вне roles условия видит весь список")
	}
	админ := список(&auth.User{ID: "root", Login: "admin", IsAdmin: true})
	if !strings.Contains(админ, "Заявка оператора") || !strings.Contains(админ, "Чужая заявка") {
		t.Fatal("администратор видит весь список")
	}
}

// Ревью #2172: постоянный отбор формы списка действует во всех путях этого
// списка — догрузка ветки дерева и выгрузка в Excel показывают ровно то же,
// что страница списка.
func TestUI_EntityList_FormFilterCoversTreeAndExcel(t *testing.T) {
	заявка := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Автор", Type: metadata.FieldTypeString},
		},
	}
	заявка.Forms = []*metadata.FormModule{{
		Name: "ФормаСписка", Kind: "list", EntityName: заявка.Name,
		LayoutKind: metadata.FormLayoutManaged,
		ListFilter: []metadata.FormListCondition{{
			Field: "Автор", Value: metadata.ListFilterCurrentUser, Roles: []string{"Оператор"},
		}},
	}}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{заявка})
	folder := uuid.New()
	if err := s.store.Upsert(ctx, заявка.Name, folder, map[string]any{"Наименование": "Папка", "is_folder": true}, заявка); err != nil {
		t.Fatal(err)
	}
	for _, row := range []map[string]any{
		{"Наименование": "MY-2172-ROW", "Автор": "u-op", "parent_id": folder},
		{"Наименование": "OTHER-2172-ROW", "Автор": "u-other", "parent_id": folder},
	} {
		if err := s.store.Upsert(ctx, заявка.Name, uuid.New(), row, заявка); err != nil {
			t.Fatal(err)
		}
	}
	оператор := &auth.User{ID: "u-op", Login: "operator", Roles: []*auth.Role{{Name: "Оператор", Permissions: auth.Permission{
		Catalogs: map[string][]string{заявка.Name: {"read"}},
	}}}}

	tree := serveTreeChildren(t, s, заявка.Name, "parent="+folder.String()+"&depth=0", оператор)
	if tree.Code != http.StatusOK {
		t.Fatalf("tree-children: %d %s", tree.Code, tree.Body.String())
	}
	if body := tree.Body.String(); !strings.Contains(body, "MY-2172-ROW") || strings.Contains(body, "OTHER-2172-ROW") {
		t.Fatalf("раскрытие папки обошло постоянный отбор: %s", body)
	}

	req := reqWithChi(http.MethodGet, "/ui/catalog/"+заявка.Name+"/excel", nil,
		map[string]string{"kind": "catalog", "entity": заявка.Name})
	req = req.WithContext(auth.ContextWithUser(req.Context(), оператор))
	rec := httptest.NewRecorder()
	s.listExcel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("excel: %d %s", rec.Code, rec.Body.String())
	}
	book, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := book.GetRows(book.GetSheetName(0))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, row := range rows[1:] {
		if len(row) > 0 && strings.Contains(row[0], "-2172-") {
			names = append(names, row[0])
		}
	}
	if strings.Join(names, ",") != "MY-2172-ROW" {
		t.Fatalf("выгрузка Excel обошла постоянный отбор: %v", rows)
	}
}

// Ревью #2172, круг 2: папки иерархического справочника постоянный отбор не
// скрывает — иначе папка без автора исчезала со страницы списка и из корня
// дерева, и до своей записи внутри неё было не дойти. Записи в папке отбор
// по-прежнему отбирает.
func TestUI_EntityList_FormFilterKeepsFoldersNavigable(t *testing.T) {
	заявка := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Автор", Type: metadata.FieldTypeString},
		},
	}
	заявка.Forms = []*metadata.FormModule{{
		Name: "ФормаСписка", Kind: "list", EntityName: заявка.Name,
		LayoutKind: metadata.FormLayoutManaged,
		ListFilter: []metadata.FormListCondition{{
			Field: "Автор", Value: metadata.ListFilterCurrentUser, Roles: []string{"Оператор"},
		}},
	}}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{заявка})
	folder := uuid.New()
	if err := s.store.Upsert(ctx, заявка.Name, folder, map[string]any{"Наименование": "FOLDER-2172", "is_folder": true}, заявка); err != nil {
		t.Fatal(err)
	}
	for _, row := range []map[string]any{
		{"Наименование": "MY-IN-FOLDER", "Автор": "u-op", "parent_id": folder},
		{"Наименование": "OTHER-IN-FOLDER", "Автор": "u-other", "parent_id": folder},
	} {
		if err := s.store.Upsert(ctx, заявка.Name, uuid.New(), row, заявка); err != nil {
			t.Fatal(err)
		}
	}
	оператор := &auth.User{ID: "u-op", Login: "operator", Roles: []*auth.Role{{Name: "Оператор", Permissions: auth.Permission{
		Catalogs: map[string][]string{заявка.Name: {"read"}},
	}}}}
	страница := func(query string) string {
		t.Helper()
		req := reqWithChi(http.MethodGet, "/ui/catalog/"+заявка.Name+"?"+query, nil,
			map[string]string{"kind": "catalog", "entity": заявка.Name})
		req = req.WithContext(auth.ContextWithUser(req.Context(), оператор))
		rec := httptest.NewRecorder()
		s.list(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("list %s: %d %s", query, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	for _, view := range []string{"view=list", "view=tree"} {
		if !strings.Contains(страница(view), "FOLDER-2172") {
			t.Fatalf("%s: папка без автора скрыта отбором — до своей записи не дойти", view)
		}
	}
	root := serveTreeChildren(t, s, заявка.Name, "", оператор)
	if root.Code != http.StatusOK || !strings.Contains(root.Body.String(), "FOLDER-2172") {
		t.Fatalf("корень дерева без папки: %d %s", root.Code, root.Body.String())
	}
	inside := страница("view=list&parent=" + folder.String())
	if !strings.Contains(inside, "MY-IN-FOLDER") || strings.Contains(inside, "OTHER-IN-FOLDER") {
		t.Fatal("в папке отбор обязан оставить только свою запись")
	}
}

// Ревью #2172, круг 2: условие по реквизиту связанной записи сравнивает ссылку
// внешней строки, даже когда у связанной сущности есть одноимённая ссылка.
func TestUI_EntityList_FormFilterReferenceKeepsOuterRow(t *testing.T) {
	владелец := &metadata.Entity{Name: "Владелец", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Account", Type: metadata.FieldTypeString},
		{Name: "Owner", Type: "reference:Владелец", RefEntity: "Владелец"},
	}}
	заявка := &metadata.Entity{Name: "Заявка", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "Owner", Type: "reference:Владелец", RefEntity: "Владелец"},
	}}
	заявка.Forms = []*metadata.FormModule{{
		Name: "ФормаСписка", Kind: "list", EntityName: заявка.Name,
		LayoutKind: metadata.FormLayoutManaged,
		ListFilter: []metadata.FormListCondition{{Field: "Owner.Account", Value: metadata.ListFilterCurrentUser}},
	}}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{заявка, владелец})
	mine, other := uuid.New(), uuid.New()
	// Порядок важен: «чужая» ссылается на «мою» (внешний ключ).
	for _, it := range []struct {
		id  uuid.UUID
		row map[string]any
	}{
		{mine, map[string]any{"Account": "u-op"}},
		{mine, map[string]any{"Account": "u-op", "Owner": mine.String()}},
		{other, map[string]any{"Account": "u-other", "Owner": mine.String()}},
	} {
		id, row := it.id, it.row
		if err := s.store.Upsert(ctx, владелец.Name, id, row, владелец); err != nil {
			t.Fatal(err)
		}
	}
	for name, owner := range map[string]uuid.UUID{"MY-REF-ROW": mine, "OTHER-REF-ROW": other} {
		if err := s.store.Upsert(ctx, заявка.Name, uuid.New(), map[string]any{"Наименование": name, "Owner": owner.String()}, заявка); err != nil {
			t.Fatal(err)
		}
	}
	user := &auth.User{ID: "u-op", Login: "operator", Roles: []*auth.Role{{Name: "Оператор", Permissions: auth.Permission{
		Catalogs: map[string][]string{заявка.Name: {"read"}, владелец.Name: {"read"}},
	}}}}
	req := reqWithChi(http.MethodGet, "/ui/catalog/"+заявка.Name, nil, map[string]string{"kind": "catalog", "entity": заявка.Name})
	req = req.WithContext(auth.ContextWithUser(req.Context(), user))
	rec := httptest.NewRecorder()
	s.list(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "MY-REF-ROW") || strings.Contains(body, "OTHER-REF-ROW") {
		t.Fatal("отбор Owner.Account = я пропустил чужую запись")
	}
}
