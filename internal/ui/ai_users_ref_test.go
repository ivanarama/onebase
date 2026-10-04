package ui

import (
	"context"
	"encoding/base64"
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

// ИИ-помощник исполняет запрос от имени пользователя (план 54), а права
// сверяет по объектам-источникам. Ссылка на учётную запись (reference:_users,
// #1646) системную таблицу источником не регистрирует — значит, служебные
// колонки учётки обязан закрыть сам язык запросов. Иначе пользователю с правом
// чтения одного документа хватало попросить ассистента выполнить
// «ВЫБРАТЬ Автор.password_hash …» — и хеш пароля администратора уходил в чат.
func TestAIRunQuery_UsersRefDoesNotExposeAuthColumns(t *testing.T) {
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
	admin, err := repo.Create(ctx, "boss", "S3cret-Passw0rd!", "Директор", true)
	if err != nil {
		t.Fatal(err)
	}
	заказ := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Автор", Type: "reference:_users", RefEntity: metadata.SystemUsersEntity},
		},
	}
	if err := metadata.Validate([]*metadata.Entity{заказ}, nil); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := db.Migrate(ctx, []*metadata.Entity{заказ}); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, заказ.Name, uuid.New(), map[string]any{"Номер": "1", "Автор": admin.ID}, заказ); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{заказ}})
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

	for _, q := range []string{
		"ВЫБРАТЬ Автор.password_hash КАК Х ИЗ Документ.Заказ",
		"ВЫБРАТЬ Автор.totp_secret КАК Т ИЗ Документ.Заказ",
		"ВЫБРАТЬ Автор.is_admin КАК А ИЗ Документ.Заказ",
		"ВЫБРАТЬ Автор.* ИЗ Документ.Заказ",
		"ВЫБРАТЬ Номер ИЗ Документ.Заказ ГДЕ Автор.password_hash ПОДОБНО \"$2%\"",
		// Соединение с _users появляется ради разрешённого Автор.Логин, а его
		// служебный псевдоним открыл бы соседние колонки той же строки.
		"ВЫБРАТЬ ref_автор.password_hash КАК Х ИЗ Документ.Заказ ГДЕ Автор.Логин <> \"\"",
	} {
		res := run(q)
		if !res.IsError {
			t.Fatalf("%s: запрос выполнен, служебная колонка учётки ушла ассистенту: %s", q, res.Content)
		}
		if !strings.Contains(res.Content, "недоступно") {
			t.Errorf("%s: отказ не называет причину: %s", q, res.Content)
		}
	}

	// Звёздочка без квалификатора разворачивает и авто-JOIN учётки, а он
	// появляется, как только запрос упоминает ссылку (#1752). Запрос законный
	// и выполняется, но служебных колонок учётки в ответе нет.
	var hash []byte
	if err := db.QueryRow(ctx, `SELECT password_hash FROM _users WHERE login = 'boss'`).Scan(&hash); err != nil || len(hash) == 0 {
		t.Fatalf("хеш пароля: %v (%d байт)", err, len(hash))
	}
	for _, q := range []string{
		"ВЫБРАТЬ * ИЗ Документ.Заказ ГДЕ Автор.Логин <> \"\"",
		"ВЫБРАТЬ * ИЗ Документ.Заказ УПОРЯДОЧИТЬ ПО Автор.Наименование",
	} {
		res := run(q)
		if res.IsError {
			t.Fatalf("%s: запрос отклонён: %s", q, res.Content)
		}
		for _, secret := range []string{"password_hash", "totp_secret", "is_admin", "auth_subject", string(hash), base64.StdEncoding.EncodeToString(hash)} {
			if strings.Contains(res.Content, secret) {
				t.Fatalf("%s: в ответе ассистенту %q: %s", q, secret, res.Content)
			}
		}
		if !strings.Contains(res.Content, "boss") {
			t.Fatalf("%s: звёздочка потеряла разрешённый логин учётки: %s", q, res.Content)
		}
	}

	// Объявленные реквизиты учётки читаются как раньше.
	res := run("ВЫБРАТЬ Автор.Логин КАК Л, Автор.Наименование КАК Н ИЗ Документ.Заказ")
	if res.IsError {
		t.Fatalf("Логин и Наименование учётки должны читаться: %s", res.Content)
	}
	if !strings.Contains(res.Content, "boss") || !strings.Contains(res.Content, "Директор") {
		t.Fatalf("ответ без логина и представления учётки: %s", res.Content)
	}
}
