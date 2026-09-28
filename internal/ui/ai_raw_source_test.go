package ui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/llm"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// ИИ-помощник в режиме rbac сверяет права по источникам запроса (план 54).
// Имя в ИЗ, которого нет в метаданных, источником не регистрировалось, и
// проверке нечего было отклонять: пользователь с правом чтения одного
// документа получал через ассистента логины и хеши паролей всех учёток, а
// голым именем сущности («ИЗ Секрет») — справочник, на который прав нет.
func TestAIRunQuery_SourceOutsideConfigurationIsRejected(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(ctx, "boss", "S3cret-Passw0rd!", "Директор", true); err != nil {
		t.Fatal(err)
	}
	заказ := &metadata.Entity{Name: "Заказ", Kind: metadata.KindDocument, Fields: []metadata.Field{
		{Name: "Номер", Type: metadata.FieldTypeString},
	}}
	секрет := &metadata.Entity{Name: "Секрет", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
	}}
	ents := []*metadata.Entity{заказ, секрет}
	if err := metadata.Validate(ents, nil); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := db.Migrate(ctx, ents); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, заказ.Name, uuid.New(), map[string]any{"Номер": "1"}, заказ); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, секрет.Name, uuid.New(), map[string]any{"Наименование": "код сейфа"}, секрет); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: ents})
	s := &Server{store: db, reg: registry, interp: interpreter.New(), lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = s.newEntityService(nil)
	if err := s.store.SaveAIDataScope(ctx, storage.AIDataScopeRBAC); err != nil {
		t.Fatal(err)
	}
	clerk := &auth.User{ID: uuid.NewString(), Login: "clerk", Roles: []*auth.Role{{
		Permissions: auth.Permission{Documents: map[string][]string{"Заказ": {"read"}}},
	}}}
	run := func(q string) llm.ToolResult {
		return s.aiRunQuery(auth.ContextWithUser(ctx, clerk), llm.ToolCall{ID: "q", Input: map[string]any{"запрос": q}})
	}

	for _, c := range []struct{ query, reason string }{
		{"ВЫБРАТЬ login, password_hash ИЗ _users", "не объект конфигурации"},
		{"ВЫБРАТЬ З.Номер, У.password_hash ИЗ Документ.Заказ КАК З ЛЕВОЕ СОЕДИНЕНИЕ _users КАК У ПО У.login <> З.Номер", "не объект конфигурации"},
		// Скобки на месте источника и известный вид с чужим именем — те же
		// служебные таблицы другой записью.
		{"ВЫБРАТЬ login, password_hash ИЗ (_users)", "не объект конфигурации"},
		{"ВЫБРАТЬ login, password_hash ИЗ Справочник._users", "такого объекта в конфигурации нет"},
	} {
		res := run(c.query)
		if !res.IsError {
			t.Fatalf("%s: запрос выполнен, служебная таблица ушла ассистенту: %s", c.query, res.Content)
		}
		if strings.Contains(res.Content, "boss") || !strings.Contains(res.Content, c.reason) {
			t.Fatalf("%s: отказ без причины или с данными учёток: %s", c.query, res.Content)
		}
	}

	// Голое имя сущности — источник, и право на него проверяется.
	if res := run("ВЫБРАТЬ Наименование ИЗ Секрет"); !res.IsError || strings.Contains(res.Content, "код сейфа") {
		t.Fatalf("справочник без права чтения прочитан голым именем: %s", res.Content)
	}
	if res := run("ВЫБРАТЬ Номер ИЗ Заказ"); res.IsError {
		t.Fatalf("документ с правом чтения должен читаться и голым именем: %s", res.Content)
	}
}
