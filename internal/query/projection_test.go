package query

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func projectionOf(t *testing.T, src string, opts CompileOpts) ProjectionPlan {
	t.Helper()
	res, err := Compile(src, opts)
	if err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	return res.Projection
}

func clientEntityForProjection() *metadata.Entity {
	return &metadata.Entity{
		Name: "Клиент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Телефон", Type: metadata.FieldTypeString},
			{Name: "Сумма", Type: metadata.FieldTypeNumber},
		},
	}
}

func TestAnalyzeProjection_SimpleColumns(t *testing.T) {
	opts := CompileOpts{Entities: []*metadata.Entity{clientEntityForProjection()}}
	plan := projectionOf(t, `ВЫБРАТЬ Наименование, Телефон КАК Контакт ИЗ Справочник.Клиент`, opts)
	if !plan.Simple {
		t.Fatal("одиночный SELECT должен разбираться поколоночно")
	}
	if len(plan.Columns) != 2 {
		t.Fatalf("ожидалось 2 колонки, получено %d: %+v", len(plan.Columns), plan.Columns)
	}
	if plan.Columns[0].Output != "наименование" || !containsFold(plan.Columns[0].Fields, "Наименование") {
		t.Fatalf("колонка без алиаса разобрана неверно: %+v", plan.Columns[0])
	}
	// Алиас КАК не должен стирать происхождение поля — иначе маска обходится
	// переименованием колонки.
	if plan.Columns[1].Output != "контакт" || !containsFold(plan.Columns[1].Fields, "Телефон") {
		t.Fatalf("алиас разобран неверно: %+v", plan.Columns[1])
	}
	if len(plan.UnmaskableFields) != 0 {
		t.Fatalf("простые колонки не должны попадать в UnmaskableFields: %v", plan.UnmaskableFields)
	}
}

func TestAnalyzeProjection_Star(t *testing.T) {
	opts := CompileOpts{Entities: []*metadata.Entity{clientEntityForProjection()}}
	plan := projectionOf(t, `ВЫБРАТЬ * ИЗ Справочник.Клиент`, opts)
	if !plan.Simple || len(plan.Columns) != 1 || !plan.Columns[0].Star {
		t.Fatalf("«*» разобран неверно: %+v", plan)
	}
}

func TestAnalyzeProjection_ExpressionsAreUnmaskable(t *testing.T) {
	opts := CompileOpts{Entities: []*metadata.Entity{clientEntityForProjection()}}
	for _, src := range []string{
		`ВЫБРАТЬ СУММА(Сумма) КАК Итого, Телефон + "!" КАК Контакт ИЗ Справочник.Клиент`,
		`ВЫБРАТЬ Наименование ИЗ Справочник.Клиент ГДЕ Телефон <> ""`,
		`ВЫБРАТЬ Наименование ИЗ Справочник.Клиент УПОРЯДОЧИТЬ ПО Телефон`,
		`ВЫБРАТЬ Наименование, СУММА(Сумма) КАК С ИЗ Справочник.Клиент СГРУППИРОВАТЬ ПО Телефон, Наименование`,
	} {
		plan := projectionOf(t, src, opts)
		if !containsFold(plan.UnmaskableFields, "Телефон") {
			t.Fatalf("%s: Телефон должен быть немаскируемым, получено %v", src, plan.UnmaskableFields)
		}
	}
}

func TestAnalyzeProjection_SubqueryAndUnionNotSimple(t *testing.T) {
	opts := CompileOpts{Entities: []*metadata.Entity{clientEntityForProjection()}}
	for _, src := range []string{
		`ВЫБРАТЬ Наименование ИЗ Справочник.Клиент ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Телефон ИЗ Справочник.Клиент`,
		`ВЫБРАТЬ Наименование ИЗ Справочник.Клиент ГДЕ Телефон В (ВЫБРАТЬ Телефон ИЗ Справочник.Клиент)`,
	} {
		if plan := projectionOf(t, src, opts); plan.Simple {
			t.Fatalf("%s: несколько SELECT не должны считаться простой проекцией", src)
		}
	}
}

// Ссылочное измерение выводит не идентификатор, а Наименование/Номер связанной
// сущности — маска связанной сущности обязана дойти до этой колонки.
func TestAnalyzeProjection_RefDimAddsDisplayField(t *testing.T) {
	client := clientEntityForProjection()
	order := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Клиент", Type: "reference:Клиент", RefEntity: "Клиент"},
		},
	}
	opts := CompileOpts{Entities: []*metadata.Entity{client, order}}
	plan := projectionOf(t, `ВЫБРАТЬ Клиент ИЗ Документ.Заказ`, opts)
	if len(plan.Columns) != 1 {
		t.Fatalf("ожидалась одна колонка: %+v", plan.Columns)
	}
	if !containsFold(plan.Columns[0].Fields, "Клиент") || !containsFold(plan.Columns[0].Fields, "Наименование") {
		t.Fatalf("ссылочное измерение должно тянуть за собой Наименование: %+v", plan.Columns[0])
	}
}

// Имя колонки в плане должно совпадать с именем колонки в результате: иначе
// маска не найдёт значение и путь чтения обязан упасть, а не отдать данные.
func TestAnalyzeProjection_OutputNameMatchesSQL(t *testing.T) {
	opts := CompileOpts{Entities: []*metadata.Entity{clientEntityForProjection()}}
	res, err := Compile(`ВЫБРАТЬ Телефон КАК Контакт, Наименование ИЗ Справочник.Клиент`, opts)
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(res.SQL)
	for _, col := range res.Projection.Columns {
		if col.Output != "" && !strings.Contains(sql, col.Output) {
			t.Fatalf("колонка %q не найдена в SQL: %s", col.Output, res.SQL)
		}
	}
}
