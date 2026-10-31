package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/langref"
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

// Исполняем JOIN из текста справки, чтобы её псевдоним не расходился с тестом.
func TestDSLQuery_TablePartOwnerJoinFromLangref(t *testing.T) {
	doc, ok := langref.ByName("из")
	if !ok {
		t.Fatal("нет справки ИЗ")
	}
	_, join, ok := strings.Cut(doc.Doc, "соедините источник с владельцем: ")
	if !ok {
		t.Fatal("нет примера JOIN с владельцем")
	}
	join = strings.TrimSuffix(join, ".")
	for _, kind := range []metadata.Kind{metadata.KindCatalog, metadata.KindDocument} {
		t.Run(string(kind), func(t *testing.T) {
			tp := metadata.TablePart{Name: "Строки", Fields: []metadata.Field{{Name: "Текст", Type: metadata.FieldTypeString}}}
			owner := &metadata.Entity{Name: "ВладелецТЧ", Kind: kind, TableParts: []metadata.TablePart{tp}}
			prefix, field := "Справочник", "Метка"
			if kind == metadata.KindDocument {
				prefix = "Документ"
			}
			owner.Fields = []metadata.Field{{Name: field, Type: metadata.FieldTypeString}}
			s, ctx := newSubmitTestServer(t, []*metadata.Entity{owner})
			id := uuid.New()
			if err := s.store.Upsert(ctx, owner.Name, id, map[string]any{field: "Владелец"}, owner); err != nil {
				t.Fatal(err)
			}
			if err := s.store.UpsertTablePartRows(ctx, owner.Name, tp.Name, id, []map[string]any{{"Текст": "Строка"}}, tp); err != nil {
				t.Fatal(err)
			}
			source := prefix + "." + owner.Name
			text := fmt.Sprintf("ВЫБРАТЬ Вл.%s КАК Имя ИЗ %s.%s КАК С %s", field, source, tp.Name,
				strings.ReplaceAll(join, "Справочник.Имя", source))
			prog := mustParse(t, fmt.Sprintf(`Функция Проверка() Экспорт
				З = Новый Запрос;
				З.Текст = "%s";
				Р = З.Выполнить();
				Возврат Строка(Р.Количество()) + "|" + Строка(Р[0].Имя);
			КонецФункции`, text))
			vars, _ := s.buildDSLVarsTx(ctx, runtime.NewMovementsCollector("test", uuid.Nil))
			var got any
			if err := s.interp.RunWithResult(prog.Procedures[0], nil, &got, vars); err != nil {
				t.Fatalf("пример из справки %q: %v", text, err)
			}
			if got != "1|Владелец" {
				t.Fatalf("JOIN из справки: %v", got)
			}
		})
	}
}
