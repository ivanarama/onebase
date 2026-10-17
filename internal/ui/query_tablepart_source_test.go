package ui

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Табличная часть как источник запроса из модуля (публичный путь — Запрос.
// Выполнить() под пользователем): право чтения проверяется у владельца, а
// полевая политика — у полей самой табличной части, иначе её ПДн уходили бы из
// запроса без маски.
func TestDSLQuery_TablePartSourceUsesOwnerAccessAndTablePartMasks(t *testing.T) {
	тч := metadata.TablePart{Name: "Телефоны", Fields: []metadata.Field{
		{Name: "Номер", Type: metadata.FieldTypeString, PII: true},
	}}
	клиент := &metadata.Entity{
		Name: "КлиентТЧ", Kind: metadata.KindCatalog,
		Fields:     []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{тч},
	}
	товар := &metadata.Entity{
		Name: "ТоварТЧ", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{клиент, товар})
	ид := uuid.New()
	if err := s.store.Upsert(ctx, клиент.Name, ид, map[string]any{"Наименование": "Иванов"}, клиент); err != nil {
		t.Fatal(err)
	}
	if err := s.store.UpsertTablePartRows(ctx, клиент.Name, тч.Name, ид,
		[]map[string]any{{"Номер": "+79161234455"}}, тч); err != nil {
		t.Fatal(err)
	}
	const src = `Функция Проверка() Экспорт
		З = Новый Запрос;
		З.Текст = "ВЫБРАТЬ Т.Ссылка КАК Клиент, Т.Номер КАК Номер ИЗ Справочник.КлиентТЧ.Телефоны КАК Т";
		Р = З.Выполнить();
		Возврат Строка(Р.Количество()) + "|" + Строка(Р[0].Номер) + "|" + ТипЗнч(Р[0].Клиент);
	КонецФункции`
	run := func(user *auth.User) (any, error) {
		prog := mustParse(t, src)
		var result any
		vars, _ := s.buildDSLVarsTx(auth.ContextWithUser(ctx, user), runtime.NewMovementsCollector("test", uuid.Nil))
		err := s.interp.RunWithResult(prog.Procedures[0], nil, &result, vars)
		return result, err
	}

	// Номер — ПДн табличной части (pii: true); права disclose у роли нет.
	маска := &auth.User{ID: "op", Login: "operator", Roles: []*auth.Role{{
		Name:        "Оператор",
		Permissions: auth.Permission{Catalogs: map[string][]string{клиент.Name: {"read"}}},
	}}}
	got, err := run(маска)
	if err != nil {
		t.Fatalf("запрос к табличной части с правом чтения владельца: %v", err)
	}
	if got != "1|••••••|СправочникСсылка.КлиентТЧ" {
		t.Fatalf("строка ТЧ, маска её поля и ссылка на владельца: %q", got)
	}

	безПрава := &auth.User{ID: "x", Login: "other", Roles: []*auth.Role{{
		Name:        "Склад",
		Permissions: auth.Permission{Catalogs: map[string][]string{товар.Name: {"read"}}},
	}}}
	if _, err := run(безПрава); err == nil || !strings.Contains(err.Error(), клиент.Name) {
		t.Fatalf("без права чтения владельца запрос к его табличной части должен отклоняться: %v", err)
	}
}
