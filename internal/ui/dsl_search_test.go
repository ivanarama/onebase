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

// Проверяем именно регистрацию в наборе переменных: без неё функция есть в
// коде, но недоступна из модулей конфигурации.
func TestDSLFullTextSearch_RegisteredInVars(t *testing.T) {
	s, _, _ := newSearchTestServer(t)
	s.messages = NewMessageStore()
	vars := s.buildDSLVars(context.Background(), nil)
	for _, name := range []string{"ПолнотекстовыйПоиск", "FullTextSearch"} {
		if _, ok := vars[name]; !ok {
			t.Fatalf("функция %s не зарегистрирована в переменных DSL", name)
		}
	}
}
