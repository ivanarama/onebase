package query_test

import (
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestJoinedDocumentDateParamCompile(t *testing.T) {
	doc := &metadata.Entity{Name: "JoinedMoment", Kind: metadata.KindDocument, Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}, {Name: "Метка", Type: metadata.FieldTypeString}}, TableParts: []metadata.TablePart{{Name: "Вопросы", Fields: []metadata.Field{{Name: "Вопрос", Type: metadata.FieldTypeString}}}}}
	start := time.Date(2041, 6, 1, 0, 0, 0, 0, time.FixedZone("Europe/Moscow", 3*60*60))
	end := time.Date(2041, 6, 30, 23, 59, 59, 0, start.Location())
	sources := map[string]string{
		"table-part-first": "Документ.JoinedMoment.Вопросы КАК Т ВНУТРЕННЕЕ СОЕДИНЕНИЕ Документ.JoinedMoment КАК О ПО О.Ссылка = Т.Ссылка",
		"document-first":   "Документ.JoinedMoment КАК О ВНУТРЕННЕЕ СОЕДИНЕНИЕ Документ.JoinedMoment.Вопросы КАК Т ПО О.Ссылка = Т.Ссылка",
		"direct":           "Документ.JoinedMoment КАК О",
	}
	for name, source := range sources {
		for form, condition := range map[string]string{"comparisons": "О.Дата >= &Начало И О.Дата <= &Конец", "reversed": "&Начало <= О.Дата И &Конец >= О.Дата", "between": "О.Дата МЕЖДУ &Начало И &Конец"} {
			t.Run(name+"/"+form, func(t *testing.T) {
				compiled, err := query.Compile("ВЫБРАТЬ О.Метка ИЗ "+source+" ГДЕ "+condition, query.CompileOpts{Dialect: storage.SQLiteDialect{}, Entities: []*metadata.Entity{doc}, Params: map[string]any{"Начало": start, "Конец": end}})
				if err != nil {
					t.Fatal(err)
				}
				if len(compiled.Args) != 2 || compiled.Args[0] != "2041-05-31T21:00:00Z" || compiled.Args[1] != "2041-06-30T20:59:59Z" {
					t.Fatalf("wrong normalized arguments: %#v SQL=%s", compiled.Args, compiled.SQL)
				}
			})
		}
	}
	// A joined string field named Date must not borrow the main document's date type.
	text := &metadata.Entity{Name: "JoinedText", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeString}}}
	compiled, err := query.Compile("ВЫБРАТЬ О.Метка ИЗ Документ.JoinedMoment КАК О ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.JoinedText КАК С ПО ИСТИНА ГДЕ С.Дата >= &Начало", query.CompileOpts{Dialect: storage.SQLiteDialect{}, Entities: []*metadata.Entity{doc, text}, Params: map[string]any{"Начало": start}})
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Args) != 1 {
		t.Fatalf("arguments: %#v", compiled.Args)
	}
	if _, ok := compiled.Args[0].(time.Time); !ok {
		t.Fatalf("string field borrowed document date type: %#v", compiled.Args)
	}
	// The old register parameter path stays untouched; #2188 owns its format.
	reg := &metadata.InfoRegister{Name: "JoinedDates", Dimensions: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}}}
	compiled, err = query.Compile("ВЫБРАТЬ Р.Дата ИЗ РегистрСведений.JoinedDates КАК Р ГДЕ &Начало <= Р.Дата", query.CompileOpts{Dialect: storage.SQLiteDialect{}, InfoRegs: []*metadata.InfoRegister{reg}, Params: map[string]any{"Начало": start}})
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Args) != 1 {
		t.Fatalf("register arguments: %#v", compiled.Args)
	}
	if _, ok := compiled.Args[0].(time.Time); !ok {
		t.Fatalf("register binding changed: %#v", compiled.Args)
	}
}
