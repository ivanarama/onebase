package ui

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Объект.ПометкаУдаления = Истина + Объект.Записать() и
// Объект.УстановитьПометкуУдаления(...) у документа и справочника раньше молча
// не меняли deletion_mark — работал только Менеджер.ПометитьНаУдаление(Ссылка).
// Тест исполняет DSL-тело процедуры тем же путём, что обработка/модуль.
func TestDSLObjectDeletionMarkAssignmentAndMethod(t *testing.T) {
	ctx, db, s, dp, doc := newPostingDoc(t)
	_ = postOne(t, dp)
	cat := &metadata.Entity{Name: "Скан", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
	if err := db.Migrate(ctx, []*metadata.Entity{cat}); err != nil {
		t.Fatal(err)
	}
	s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{doc, cat}, Registers: s.reg.Registers()})

	mark := func(table string) (bool, bool) {
		t.Helper()
		var marked, posted bool
		q := "SELECT deletion_mark, " + map[bool]string{true: "posted", false: "FALSE"}[table == "поступлениетоваров"] + " FROM " + table + " LIMIT 1"
		if err := db.QueryRow(ctx, q).Scan(&marked, &posted); err != nil {
			t.Fatal(err)
		}
		return marked, posted
	}
	run := func(body string) []string {
		t.Helper()
		msgs, err := runDSLBody(t, s, body)
		if err != nil {
			t.Fatalf("DSL: %v\n%s", err, body)
		}
		return msgs
	}

	// Справочник: присваивание + Записать(); чтение после перечитывания.
	run(`Э = Справочники.Скан.Создать(); Э.Наименование = "скан-1"; Э.Записать();
Э.ПометкаУдаления = Истина; Э.Записать();`)
	if m, _ := mark("скан"); !m {
		t.Fatal("справочник: ПометкаУдаления = Истина + Записать() не пометил элемент")
	}
	msgs := run(`Об = Справочники.Скан.НайтиПоНаименованию("скан-1").ПолучитьОбъект(); Сообщить(Об.ПометкаУдаления);
Об.УстановитьПометкуУдаления(Ложь); Сообщить(Об.ПометкаУдаления);`)
	if strings.Join(msgs, "|") != "true|false" {
		t.Fatalf("справочник: ПометкаУдаления читается %q, ждали true|false", msgs)
	}
	if m, _ := mark("скан"); m {
		t.Fatal("справочник: УстановитьПометкуУдаления(Ложь) не сняла пометку")
	}

	// Проведённый документ: пометка присваиванием снимает проведение, как
	// ПометитьНаУдаление и как в 1С.
	run(`Д = Документы.ПоступлениеТоваров.НайтиПоНомеру("ПОС-001").ПолучитьОбъект();
Д.ПометкаУдаления = Истина; Д.Записать();`)
	if m, p := mark("поступлениетоваров"); !m || p {
		t.Fatalf("документ: marked=%t posted=%t, ждали помечен и не проведён", m, p)
	}
	// Провести помеченный присваиванием нельзя — ошибка, а не тихая запись.
	if _, err := runDSLBody(t, s, `Д = Документы.ПоступлениеТоваров.НайтиПоНомеру("ПОС-001").ПолучитьОбъект();
Д.ПометкаУдаления = Истина; Д.Провести();`); err == nil || !strings.Contains(err.Error(), "помеченный на удаление") {
		t.Fatalf("документ: Провести помеченного присваиванием: %v", err)
	}
	run(`Д = Документы.ПоступлениеТоваров.НайтиПоНомеру("ПОС-001").ПолучитьОбъект(); Д.УстановитьПометкуУдаления(Ложь);`)
	if m, _ := mark("поступлениетоваров"); m {
		t.Fatal("документ: УстановитьПометкуУдаления(Ложь) не сняла пометку")
	}
}
