package ui

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

// Встроенная функция DSL ПолнотекстовыйПоиск (план 82). Права — пользователя
// сессии: обработка не должна видеть больше, чем тот же пользователь в UI.

func dslSearchResults(t *testing.T, s *Server, ctx context.Context, args ...any) []any {
	t.Helper()
	got, err := s.dslFullTextSearch(ctx, args)
	if err != nil {
		t.Fatalf("ПолнотекстовыйПоиск: %v", err)
	}
	arr, ok := got.(*interpreter.Array)
	if !ok {
		t.Fatalf("ожидался массив, получено %T", got)
	}
	return arr.Iterate()
}

func TestDSLFullTextSearch_ReturnsStructuresWithRefs(t *testing.T) {
	ctx := context.Background()
	s, cat, doc := newSearchTestServer(t)

	id := uuid.New()
	if err := s.store.Upsert(ctx, cat.Name, id, map[string]any{"Наименование": "ООО Ромашка"}, cat); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Upsert(ctx, doc.Name, uuid.New(), map[string]any{
		"Номер": "РН-000012", "Основание": "договор с Ромашкой",
	}, doc); err != nil {
		t.Fatal(err)
	}

	items := dslSearchResults(t, s, ctx, "ромашк")
	if len(items) != 2 {
		t.Fatalf("ожидались два совпадения, получено %d: %+v", len(items), items)
	}
	first, ok := items[0].(*interpreter.Struct)
	if !ok {
		t.Fatalf("элемент выдачи должен быть структурой, получено %T", items[0])
	}
	if got := first.Get("Объект"); got != cat.Name {
		t.Fatalf("Объект = %v, ожидалось %s", got, cat.Name)
	}
	if got := first.Get("Представление"); got != "ООО Ромашка" {
		t.Fatalf("Представление = %v", got)
	}
	// Ссылка — полноценная ссылка DSL: с ней работают ПолучитьОбъект и запросы.
	ref, ok := first.Get("Ссылка").(*interpreter.Ref)
	if !ok {
		t.Fatalf("Ссылка должна быть ссылкой, получено %T", first.Get("Ссылка"))
	}
	if ref.UUID != id.String() || ref.Type != cat.Name {
		t.Fatalf("неверная ссылка: %+v", ref)
	}
}

func TestDSLFullTextSearch_RespectsUserRights(t *testing.T) {
	ctx := context.Background()
	s, cat, doc := newSearchTestServer(t)

	if err := s.store.Upsert(ctx, cat.Name, uuid.New(), map[string]any{"Наименование": "ООО Ромашка"}, cat); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Upsert(ctx, doc.Name, uuid.New(), map[string]any{
		"Номер": "РН-000012", "Основание": "договор с Ромашкой",
	}, doc); err != nil {
		t.Fatal(err)
	}

	userCtx := auth.ContextWithUser(ctx, &auth.User{Roles: []*auth.Role{{
		Permissions: auth.Permission{Catalogs: map[string][]string{cat.Name: {"read"}}},
	}}})
	items := dslSearchResults(t, s, userCtx, "ромашк")
	if len(items) != 1 {
		t.Fatalf("выдача должна ограничиваться правами пользователя: %+v", items)
	}
	if got := items[0].(*interpreter.Struct).Get("Объект"); got != cat.Name {
		t.Fatalf("в выдаче остался недоступный объект: %v", got)
	}
}

func TestDSLFullTextSearch_LimitAndEmptyQuery(t *testing.T) {
	ctx := context.Background()
	s, cat, _ := newSearchTestServer(t)

	for _, name := range []string{"ООО Ромашка", "ЗАО Ромашка-2", "ИП Ромашкин"} {
		if err := s.store.Upsert(ctx, cat.Name, uuid.New(), map[string]any{"Наименование": name}, cat); err != nil {
			t.Fatal(err)
		}
	}

	if items := dslSearchResults(t, s, ctx, "ромашк", 2); len(items) != 2 {
		t.Fatalf("лимит не применился: %+v", items)
	}
	if items := dslSearchResults(t, s, ctx, "   "); len(items) != 0 {
		t.Fatalf("пустой запрос должен давать пустую выдачу: %+v", items)
	}
	if _, err := s.dslFullTextSearch(ctx, nil); err == nil {
		t.Fatal("вызов без аргументов должен возвращать ошибку")
	}
	if _, err := s.dslFullTextSearch(ctx, []any{"ромашка", "много"}); err == nil {
		t.Fatal("нечисловой лимит должен возвращать ошибку")
	}
}

func TestDSLFullTextSearch_FieldAndEntityFilterBeforeTopN(t *testing.T) {
	ctx := context.Background()
	s, cat, _ := newSearchTestServer(t)
	const marker = "dslfilterboundarytoken"

	for i := 0; i < 31; i++ {
		if err := s.store.Upsert(ctx, cat.Name, uuid.New(), map[string]any{
			"Наименование": marker + " foreign",
			"Tenant":       "site-b",
		}, cat); err != nil {
			t.Fatal(err)
		}
	}
	localID := uuid.New()
	if err := s.store.Upsert(ctx, cat.Name, localID, map[string]any{
		"Наименование": "local",
		"Менеджер":     marker,
		"Tenant":       "site-a",
	}, cat); err != nil {
		t.Fatal(err)
	}

	items := dslSearchResults(t, s, ctx, marker, 30, "tenant", "site-a", []any{cat.Name})
	if len(items) != 1 {
		t.Fatalf("отбор DSL должен войти в запрос до top-N: %+v", items)
	}
	ref, ok := items[0].(*interpreter.Struct).Get("Ссылка").(*interpreter.Ref)
	if !ok || ref.UUID != localID.String() {
		t.Fatalf("отбор вернул не локальную запись: %+v", items[0])
	}

	if _, err := s.dslFullTextSearch(ctx, []any{marker, 30, "Tenant"}); err == nil {
		t.Fatal("поле отбора без значения должно отклоняться")
	}
	if _, err := s.dslFullTextSearch(ctx, []any{marker, 30, "Tenant", "site-a", []any{cat.Name}, "лишнее"}); err == nil {
		t.Fatal("лишние аргументы не должны молча игнорироваться")
	}
	if got := dslSearchResults(t, s, ctx, marker, 30, "НетТакогоПоля", "site-a"); len(got) != 0 {
		t.Fatalf("объекты без поля отбора не должны искаться глобально: %+v", got)
	}
}

func TestDSLFullTextSearch_MaskedFilterFieldDenied(t *testing.T) {
	ctx := context.Background()
	s, cat, _ := newSearchTestServer(t)
	user := &auth.User{Login: "operator", Roles: []*auth.Role{{
		Permissions: auth.Permission{
			Catalogs: map[string][]string{cat.Name: {"read"}},
			FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{
				cat.Name: {"Tenant": auth.FieldPolicy{Read: "hide"}},
			}},
		},
	}}}
	userCtx := auth.ContextWithUser(ctx, user)
	if _, err := s.dslFullTextSearch(userCtx, []any{
		"marker", 30, "Tenant", "site-a", []any{cat.Name},
	}); err == nil {
		t.Fatal("отбор по замаскированному полю создаёт guessing oracle и должен отклоняться")
	}
}

// Проверяем именно регистрацию в наборе переменных: без неё функция есть в
// коде, но недоступна из модулей конфигурации.
func TestDSLFullTextSearch_RegisteredInVars(t *testing.T) {
	s, _, _ := newSearchTestServer(t)
	s.messages = NewMessageStore()
	vars, _ := s.buildDSLVarsTx(context.Background(), nil)
	for _, name := range []string{"ПолнотекстовыйПоиск", "FullTextSearch"} {
		if _, ok := vars[name]; !ok {
			t.Fatalf("функция %s не зарегистрирована в переменных DSL", name)
		}
	}
}
