package ui

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Модуль под ролью с маской ПДн читает телефон клиента как «••••••». Раньше
// ничто не мешало присвоить заглушку реквизиту нового документа и записать —
// так в базу оператора КЦ попали телефоны «••••••». Путь пользовательский:
// DSL-окружение запроса, Документы.Заявка.Создать() / Записать().
func TestDSL_MaskedPlaceholderCannotBeWrittenToAnotherObject(t *testing.T) {
	client, order := dslMaskEntities()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client, order})
	clientID := uuid.New()
	if err := s.store.Upsert(ctx, "Клиент", clientID, map[string]any{
		"Наименование": "Иванов", "Телефон": "+79161234455",
	}, client); err != nil {
		t.Fatal(err)
	}
	user := &auth.User{ID: "op", Login: "operator", Roles: []*auth.Role{{Name: "Оператор", Permissions: auth.Permission{
		Catalogs:    map[string][]string{"Клиент": {"read"}},
		Documents:   map[string][]string{"Заявка": {"read", "write"}},
		FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{"Клиент": {"Телефон": {Read: "mask_all"}}}},
	}}}}
	uctx := auth.ContextWithUser(ctx, user)

	run := func(phone string) error {
		t.Helper()
		var msgs []string
		vars, tx := s.buildDSLVarsWithMessagesTx(uctx, runtime.NewMovementsCollector("test", uuid.Nil), &msgs)
		defer rollbackDSLExecution(tx)
		vars["Клиент"] = &interpreter.Ref{UUID: clientID.String(), Type: "Клиент", AttrResolver: s.newDSLRefAttrResolver(uctx)}
		prog, err := parser.New(lexer.New(`Процедура Скопировать()
	З = Документы.Заявка.Создать();
	З.Номер = "1";
	З.Телефон = `+phone+`;
	З.Записать();
КонецПроцедуры`, "copy.os")).ParseProgram()
		if err != nil {
			t.Fatal(err)
		}
		return finishDSLExecution(tx, interpreter.New().Run(prog.Procedures[0], nil, vars))
	}

	err := run("Клиент.Телефон")
	if err == nil || !strings.Contains(err.Error(), "скрытое значение нельзя записать") {
		t.Fatalf("заглушка маски записана в другой объект: err=%v", err)
	}
	if rows, _ := s.store.List(ctx, "Заявка", order, storage.ListParams{}); len(rows) != 0 {
		t.Fatalf("документ с заглушкой сохранён: %v", rows)
	}
	// Обычная запись не затронута.
	if err := run(`"+79990001122"`); err != nil {
		t.Fatalf("обычная запись отвергнута: %v", err)
	}
}
