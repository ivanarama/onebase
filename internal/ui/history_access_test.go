package ui

// История объекта подчиняется тем же правам, что список и форма (план 121,
// раздел RBAC/маска). До этого она читалась напрямую из журнала регистрации и
// обходила обе проверки: маску ПДн (план 88) и построчный доступ (план 79).

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

func historyClientEntity() *metadata.Entity {
	return &metadata.Entity{
		Name: "Клиент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Телефон", Type: metadata.FieldTypeString},
		},
	}
}

func historyReader(entityName string, policies auth.FieldPolicies) *auth.User {
	return &auth.User{ID: uuid.NewString(), Login: "reader", Roles: []*auth.Role{{
		Name: "Читатель",
		Permissions: auth.Permission{
			Catalogs: map[string][]string{entityName: {"read"}},
			FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{
				entityName: policies,
			}},
		},
	}}}
}

// Замаскированный реквизит не должен раскрываться в истории: там лежат его
// прежнее и новое значения целиком, и до этой правки страница «История»
// показывала их пользователю, которому в форме тот же телефон показан как
// «•••••1122».
func TestRecordHistory_MasksFieldValues(t *testing.T) {
	cat := historyClientEntity()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{cat})
	if err := s.store.EnsureAuditSchema(ctx); err != nil {
		t.Fatal(err)
	}
	const oldPhone = "79990000001"
	const newPhone = "79990000002"
	id := uuid.New()
	if err := s.store.Upsert(ctx, cat.Name, id, map[string]any{
		"Наименование": "Клиент", "Телефон": oldPhone}, cat); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Upsert(ctx, cat.Name, id, map[string]any{
		"Наименование": "Клиент", "Телефон": newPhone}, cat); err != nil {
		t.Fatal(err)
	}

	user := historyReader(cat.Name, auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}})
	r := reqWithChi(http.MethodGet, "/ui/catalog/клиент/"+id.String()+"/history", nil,
		map[string]string{"entity": "клиент", "id": id.String()})
	r = r.WithContext(auth.ContextWithUser(r.Context(), user))
	w := httptest.NewRecorder()
	s.recordHistory(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("код ответа %d: %s", w.Code, w.Body.String())
	}
	page := w.Body.String()
	if strings.Contains(page, oldPhone) {
		t.Fatal("история раскрыла прежнее значение замаскированного реквизита")
	}
	if strings.Contains(page, newPhone) {
		t.Fatal("история раскрыла новое значение замаскированного реквизита")
	}
	if !strings.Contains(page, "0002") {
		t.Fatalf("замаскированное значение вообще не показано: %s", page)
	}
}

// Скрытый (hide) реквизит не оставляет в истории и строки: сам факт «здесь
// что-то менялось» сообщает, что поле есть и заполнено.
func TestRecordHistory_DropsHiddenFieldEvents(t *testing.T) {
	cat := historyClientEntity()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{cat})
	if err := s.store.EnsureAuditSchema(ctx); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := s.store.Upsert(ctx, cat.Name, id, map[string]any{
		"Наименование": "Клиент", "Телефон": "79990000001"}, cat); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Upsert(ctx, cat.Name, id, map[string]any{
		"Наименование": "Клиент", "Телефон": "79990000002"}, cat); err != nil {
		t.Fatal(err)
	}

	user := historyReader(cat.Name, auth.FieldPolicies{"Телефон": {Read: "hide"}})
	r := reqWithChi(http.MethodGet, "/ui/catalog/клиент/"+id.String()+"/history", nil,
		map[string]string{"entity": "клиент", "id": id.String()})
	r = r.WithContext(auth.ContextWithUser(r.Context(), user))
	w := httptest.NewRecorder()
	s.recordHistory(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("код ответа %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "Телефон") {
		t.Fatal("скрытый реквизит упомянут в истории")
	}
}

// Без права чтения сущности история недоступна: раньше обработчик только
// находил сущность по ссылке и сразу шёл в журнал.
func TestRecordHistory_RequiresReadPermission(t *testing.T) {
	cat := historyClientEntity()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{cat})
	if err := s.store.EnsureAuditSchema(ctx); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := s.store.Upsert(ctx, cat.Name, id, map[string]any{"Наименование": "Клиент"}, cat); err != nil {
		t.Fatal(err)
	}

	stranger := &auth.User{ID: uuid.NewString(), Login: "stranger", Roles: []*auth.Role{{
		Name:        "Чужой",
		Permissions: auth.Permission{Catalogs: map[string][]string{"Другой": {"read"}}},
	}}}
	r := reqWithChi(http.MethodGet, "/ui/catalog/клиент/"+id.String()+"/history", nil,
		map[string]string{"entity": "клиент", "id": id.String()})
	r = r.WithContext(auth.ContextWithUser(r.Context(), stranger))
	w := httptest.NewRecorder()
	s.recordHistory(w, r)
	if w.Code == http.StatusOK {
		t.Fatalf("история отдана без права чтения сущности: %d", w.Code)
	}
}
