package query_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func testReg() *metadata.Register {
	return &metadata.Register{
		Name: "ТоварноеДвижение",
		Dimensions: []metadata.Field{
			{Name: "Номенклатура"},
			{Name: "Склад"},
		},
		Resources: []metadata.Field{
			{Name: "Количество"},
			{Name: "Сумма"},
		},
	}
}

func testInfoReg(periodic bool) *metadata.InfoRegister {
	return &metadata.InfoRegister{
		Name:     "КурсыВалют",
		Periodic: periodic,
		Dimensions: []metadata.Field{
			{Name: "Валюта"},
		},
		Resources: []metadata.Field{
			{Name: "Курс"},
		},
	}
}

func TestCompile_Balances_NoDate(t *testing.T) {
	src := `ВЫБРАТЬ Номенклатура, КоличествоОстаток
ИЗ РегистрНакопления.ТоварноеДвижение.Остатки()`

	r, err := query.Compile(src, query.CompileOpts{
		Registers: []*metadata.Register{testReg()},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	if !strings.Contains(sql, "SUM(CASE WHEN вид_движения = 'Приход' THEN количество ELSE -количество END) AS количествоостаток") {
		t.Errorf("missing balance SUM for количество, got:\n%s", sql)
	}
	if !strings.Contains(sql, "FROM рег_товарноедвижение") {
		t.Errorf("missing FROM рег_товарноедвижение, got:\n%s", sql)
	}
	if !strings.Contains(sql, "GROUP BY номенклатура, склад") {
		t.Errorf("missing GROUP BY, got:\n%s", sql)
	}
	if strings.Contains(sql, "WHERE") {
		t.Errorf("unexpected WHERE clause when no date given, got:\n%s", sql)
	}
}

func TestCompile_Balances_WithDate(t *testing.T) {
	d := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	src := `ВЫБРАТЬ Номенклатура, КоличествоОстаток
ИЗ РегистрНакопления.ТоварноеДвижение.Остатки(&НаДату)`

	r, err := query.Compile(src, query.CompileOpts{
		Params:    map[string]any{"НаДату": d},
		Registers: []*metadata.Register{testReg()},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	if !strings.Contains(sql, "period <= $1::timestamptz") {
		t.Errorf("missing date condition, got:\n%s", sql)
	}
	if len(r.Args) != 1 {
		t.Errorf("expected 1 arg, got %d", len(r.Args))
	}
}

func TestCompile_Balances_WithDateAndFilter(t *testing.T) {
	d := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	src := `ВЫБРАТЬ Номенклатура, КоличествоОстаток
ИЗ РегистрНакопления.ТоварноеДвижение.Остатки(&НаДату, Склад = &Склад)`

	r, err := query.Compile(src, query.CompileOpts{
		Params:    map[string]any{"НаДату": d, "Склад": "Основной"},
		Registers: []*metadata.Register{testReg()},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	if !strings.Contains(sql, "period <= $1") {
		t.Errorf("missing date condition, got:\n%s", sql)
	}
	if !strings.Contains(sql, "склад = $2") {
		t.Errorf("missing filter condition, got:\n%s", sql)
	}
	if len(r.Args) != 2 {
		t.Errorf("expected 2 args, got %d", len(r.Args))
	}
}

func TestCompile_Turnovers(t *testing.T) {
	d1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	src := `ВЫБРАТЬ Номенклатура, КоличествоПриход, КоличествоРасход
ИЗ РегистрНакопления.ТоварноеДвижение.Обороты(&Начало, &Конец)`

	r, err := query.Compile(src, query.CompileOpts{
		Params:    map[string]any{"Начало": d1, "Конец": d2},
		Registers: []*metadata.Register{testReg()},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	if !strings.Contains(sql, "SUM(CASE WHEN вид_движения = 'Приход' THEN количество ELSE 0 END) AS количествоприход") {
		t.Errorf("missing приход column, got:\n%s", sql)
	}
	if !strings.Contains(sql, "SUM(CASE WHEN вид_движения = 'Расход' THEN количество ELSE 0 END) AS количестворасход") {
		t.Errorf("missing расход column, got:\n%s", sql)
	}
	if !strings.Contains(sql, "period >= $1") {
		t.Errorf("missing start condition, got:\n%s", sql)
	}
	if !strings.Contains(sql, "period <= $2") {
		t.Errorf("missing end condition, got:\n%s", sql)
	}
	if len(r.Args) != 2 {
		t.Errorf("expected 2 args, got %d: %v", len(r.Args), r.Args)
	}
}

func TestCompile_Turnovers_Oborot(t *testing.T) {
	src := `ВЫБРАТЬ Номенклатура, КоличествоОборот
ИЗ РегистрНакопления.ТоварноеДвижение.Обороты()`

	r, err := query.Compile(src, query.CompileOpts{
		Registers: []*metadata.Register{testReg()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.SQL, "SUM(CASE WHEN вид_движения = 'Приход' THEN количество ELSE -количество END) AS количествооборот") {
		t.Errorf("missing оборот column, got:\n%s", r.SQL)
	}
}

func TestCompile_LastSlice_Periodic_SQLite(t *testing.T) {
	d := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	src := `ВЫБРАТЬ Валюта, Курс
ИЗ РегистрСведений.КурсыВалют.СрезПоследних(&НаДату)`

	r, err := query.Compile(src, query.CompileOpts{
		Params:   map[string]any{"НаДату": d},
		InfoRegs: []*metadata.InfoRegister{testInfoReg(true)},
		Dialect:  storage.SQLiteDialect{},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	if strings.Contains(sql, "DISTINCT ON") {
		t.Errorf("SQLite: should NOT use DISTINCT ON, got:\n%s", sql)
	}
	if !strings.Contains(sql, "ROW_NUMBER() OVER (PARTITION BY валюта") {
		t.Errorf("SQLite: missing ROW_NUMBER() OVER, got:\n%s", sql)
	}
	if !strings.Contains(sql, "WHERE _rn = 1") {
		t.Errorf("SQLite: missing rn=1 filter, got:\n%s", sql)
	}
	if !strings.Contains(sql, "period <= ?") {
		t.Errorf("SQLite: should use ? placeholder, got:\n%s", sql)
	}
}

func TestCompile_LastSlice_Periodic(t *testing.T) {
	d := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	src := `ВЫБРАТЬ Валюта, Курс
ИЗ РегистрСведений.КурсыВалют.СрезПоследних(&НаДату)`

	r, err := query.Compile(src, query.CompileOpts{
		Params:   map[string]any{"НаДату": d},
		InfoRegs: []*metadata.InfoRegister{testInfoReg(true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	if !strings.Contains(sql, "DISTINCT ON") {
		t.Errorf("expected DISTINCT ON for periodic, got:\n%s", sql)
	}
	if !strings.Contains(sql, "FROM инфо_курсывалют") {
		t.Errorf("missing FROM инфо_курсывалют, got:\n%s", sql)
	}
	if !strings.Contains(sql, "period <= $1") {
		t.Errorf("missing date condition, got:\n%s", sql)
	}
	if !strings.Contains(sql, "ORDER BY валюта, period DESC") {
		t.Errorf("missing ORDER BY, got:\n%s", sql)
	}
}

func TestCompile_LastSlice_NonPeriodic(t *testing.T) {
	src := `ВЫБРАТЬ Валюта, Курс
ИЗ РегистрСведений.КурсыВалют.СрезПоследних()`

	r, err := query.Compile(src, query.CompileOpts{
		InfoRegs: []*metadata.InfoRegister{testInfoReg(false)},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	if strings.Contains(sql, "DISTINCT ON") {
		t.Errorf("unexpected DISTINCT ON for non-periodic, got:\n%s", sql)
	}
	if strings.Contains(sql, "ORDER BY") {
		t.Errorf("unexpected ORDER BY for non-periodic, got:\n%s", sql)
	}
	if !strings.Contains(sql, "FROM инфо_курсывалют") {
		t.Errorf("missing FROM, got:\n%s", sql)
	}
}

func TestCompile_LastSlice_WithFilter(t *testing.T) {
	d := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	src := `ВЫБРАТЬ Курс ИЗ РегистрСведений.КурсыВалют.СрезПоследних(&НаДату, Валюта = &Вал)`

	r, err := query.Compile(src, query.CompileOpts{
		Params:   map[string]any{"НаДату": d, "Вал": "USD"},
		InfoRegs: []*metadata.InfoRegister{testInfoReg(true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	if !strings.Contains(sql, "валюта = $2") {
		t.Errorf("missing filter, got:\n%s", sql)
	}
	if len(r.Args) != 2 {
		t.Errorf("expected 2 args, got %d", len(r.Args))
	}
}

func TestCompile_InfoReg_Direct(t *testing.T) {
	// РегистрСведений.X without virtual table → инфо_ prefix
	src := `ВЫБРАТЬ Валюта, Курс ИЗ РегистрСведений.КурсыВалют`
	r, err := query.Compile(src, query.CompileOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.SQL, "инфо_курсывалют") {
		t.Errorf("expected инфо_курсывалют, got: %s", r.SQL)
	}
}

func TestCompile_Balances_RefDim_Aliased(t *testing.T) {
	// Register with a reference dimension — ColumnName returns "номенклатура_id".
	// The VT subquery must alias it back to "номенклатура" so DSL code like
	// Стр.Номенклатура resolves correctly, and outer WHERE uses the logical name.
	reg := &metadata.Register{
		Name: "ПартииТоваров",
		Dimensions: []metadata.Field{
			{Name: "Номенклатура", RefEntity: "Номенклатура"},
			{Name: "ДатаПоставки"},
		},
		Resources: []metadata.Field{
			{Name: "Количество"},
			{Name: "Сумма"},
		},
	}
	src := `ВЫБРАТЬ Номенклатура, КоличествоОстаток
ИЗ РегистрНакопления.ПартииТоваров.Остатки()
ГДЕ Номенклатура В (&СписокНом)`

	r, err := query.Compile(src, query.CompileOpts{
		Params:    map[string]any{"СписокНом": []any{"uuid-1", "uuid-2"}},
		Registers: []*metadata.Register{reg},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	// VT subquery must alias the _id column back to the logical field name.
	if !strings.Contains(sql, "номенклатура_id AS номенклатура") {
		t.Errorf("expected 'номенклатура_id AS номенклатура' in VT subquery, got:\n%s", sql)
	}
	// GROUP BY must use the actual DB column name.
	if !strings.Contains(sql, "GROUP BY номенклатура_id") {
		t.Errorf("expected 'GROUP BY номенклатура_id', got:\n%s", sql)
	}
	// Outer WHERE must reference the aliased DSL name, not _id.
	if !strings.Contains(sql, "WHERE номенклатура IN") {
		t.Errorf("expected outer 'WHERE номенклатура IN', got:\n%s", sql)
	}
	if strings.Contains(sql, "WHERE номенклатура_id IN") {
		t.Errorf("outer WHERE should use aliased 'номенклатура', not 'номенклатура_id', got:\n%s", sql)
	}
}

// assertDocRefDisplay проверяет, что представление измерения-ссылки на документ
// разворачивается в .номер (а не .наименование), а ссылка на справочник — в
// .наименование. Это и есть инвариант фикса #21: displayCol() ветвится по типу
// сущности, а не валит все ссылки в одну колонку.
func assertDocRefDisplay(t *testing.T, sql, docAlias, catAlias string) {
	t.Helper()
	if !strings.Contains(sql, docAlias+".номер") {
		t.Errorf("document ref must resolve to %s.номер, got:\n%s", docAlias, sql)
	}
	if strings.Contains(sql, docAlias+".наименование") {
		t.Errorf("document ref must NOT use %s.наименование, got:\n%s", docAlias, sql)
	}
	if !strings.Contains(sql, catAlias+".наименование") {
		t.Errorf("catalog ref must still resolve to %s.наименование, got:\n%s", catAlias, sql)
	}
}

// TestCompile_VT_RefDim_Document_AccumGenerators расширяет регрессию
// TestCompile_VT_RefDim_Document (которая покрывает только .Остатки()) на
// остальные генераторы накопительного регистра: .Обороты() и .ОстаткиИОбороты()
// идут через тот же buildVTRefDimInfos/displayCol, поэтому документ-ссылка
// обязана давать .номер во всех трёх. Справочник-ссылка (Номенклатура) — контроль.
func TestCompile_VT_RefDim_Document_AccumGenerators(t *testing.T) {
	cases := []struct {
		name, vt, resource string
	}{
		{"Обороты", "Обороты()", "КоличествоОборот"},
		{"ОстаткиИОбороты", "ОстаткиИОбороты()", "КоличествоПриход"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := "ВЫБРАТЬ ЗаказПоставщику, Номенклатура, " + c.resource +
				" ИЗ РегистрНакопления.ТоварыВПути." + c.vt

			reg := &metadata.Register{
				Name: "ТоварыВПути",
				Dimensions: []metadata.Field{
					{Name: "ЗаказПоставщику", RefEntity: "ЗаказПоставщику"}, // документ
					{Name: "Номенклатура", RefEntity: "Номенклатура"},       // справочник
				},
				Resources: []metadata.Field{{Name: "Количество"}},
			}
			r, err := query.Compile(src, query.CompileOpts{
				Registers: []*metadata.Register{reg},
				Entities: []*metadata.Entity{
					{Name: "ЗаказПоставщику", Kind: metadata.KindDocument},
					{Name: "Номенклатура", Kind: metadata.KindCatalog},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			assertDocRefDisplay(t, r.SQL, "ref_заказпоставщику", "ref_номенклатура")
		})
	}
}

// TestCompile_VT_RefDim_Document_InfoSlices — то же для info-регистровых срезов:
// .СрезПоследних() и .СрезПервых() оба проходят через genInfoSlice, чей VT-предскан
// тоже зовёт buildVTRefDimInfos(ir.Dimensions, opts.Entities) (query.go:1246).
// Измерение-ссылка на документ → .номер, на справочник → .наименование.
func TestCompile_VT_RefDim_Document_InfoSlices(t *testing.T) {
	d := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, vt := range []string{"СрезПоследних", "СрезПервых"} {
		t.Run(vt, func(t *testing.T) {
			src := "ВЫБРАТЬ Заказ, Менеджер, Статус " +
				"ИЗ РегистрСведений.СостоянияЗаказов." + vt + "(&НаДату)"

			ir := &metadata.InfoRegister{
				Name:     "СостоянияЗаказов",
				Periodic: true,
				Dimensions: []metadata.Field{
					{Name: "Заказ", RefEntity: "ЗаказПокупателя"}, // документ
					{Name: "Менеджер", RefEntity: "Сотрудники"},   // справочник
				},
				Resources: []metadata.Field{{Name: "Статус"}},
			}
			r, err := query.Compile(src, query.CompileOpts{
				Params:   map[string]any{"НаДату": d},
				InfoRegs: []*metadata.InfoRegister{ir},
				Dialect:  storage.SQLiteDialect{},
				Entities: []*metadata.Entity{
					{Name: "ЗаказПокупателя", Kind: metadata.KindDocument},
					{Name: "Сотрудники", Kind: metadata.KindCatalog},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			assertDocRefDisplay(t, r.SQL, "ref_заказ", "ref_менеджер")
		})
	}
}

func TestCompile_MissingRegister_Error(t *testing.T) {
	src := `ВЫБРАТЬ Ном ИЗ РегистрНакопления.Неизвестный.Остатки()`
	_, err := query.Compile(src, query.CompileOpts{})
	if err == nil {
		t.Error("expected error for unknown register")
	}
	if !strings.Contains(err.Error(), "Неизвестный") {
		t.Errorf("error should mention register name, got: %v", err)
	}
}

// --- Обороты с периодичностью ---

func TestCompile_Turnovers_Periodicity_Month_PG(t *testing.T) {
	src := `ВЫБРАТЬ Номенклатура, КоличествоПриход
ИЗ РегистрНакопления.ТоварноеДвижение.Обороты(&Нач, &Кон, Месяц)`

	r, err := query.Compile(src, query.CompileOpts{
		Registers: []*metadata.Register{testReg()},
		Params:    map[string]any{"Нач": time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "Кон": time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	if !strings.Contains(sql, "date_trunc('month', period) AS period") {
		t.Errorf("expected date_trunc month in SELECT, got:\n%s", sql)
	}
	if !strings.Contains(sql, "GROUP BY date_trunc('month', period)") {
		t.Errorf("expected date_trunc month in GROUP BY, got:\n%s", sql)
	}
	if strings.Contains(sql, "WHERE") && strings.Contains(sql, "Месяц") {
		t.Errorf("Месяц should NOT appear as a filter condition, got:\n%s", sql)
	}
}

func TestCompile_Turnovers_Periodicity_Month_SQLite(t *testing.T) {
	src := `ВЫБРАТЬ Номенклатура, КоличествоПриход
ИЗ РегистрНакопления.ТоварноеДвижение.Обороты(, , Месяц)`

	r, err := query.Compile(src, query.CompileOpts{
		Registers: []*metadata.Register{testReg()},
		Dialect:   storage.SQLiteDialect{},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	if !strings.Contains(sql, "strftime('%Y-%m', COALESCE(ob_local_datetime(period), substr(period,1,19))) AS period") {
		t.Errorf("expected strftime month in SELECT, got:\n%s", sql)
	}
	if !strings.Contains(sql, "GROUP BY strftime('%Y-%m', COALESCE(ob_local_datetime(period), substr(period,1,19)))") {
		t.Errorf("expected strftime month in GROUP BY, got:\n%s", sql)
	}
}

func TestCompile_Turnovers_Periodicity_Day(t *testing.T) {
	src := `ВЫБРАТЬ Номенклатура
ИЗ РегистрНакопления.ТоварноеДвижение.Обороты(, , День)`

	r, err := query.Compile(src, query.CompileOpts{
		Registers: []*metadata.Register{testReg()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.SQL, "date_trunc('day', period) AS period") {
		t.Errorf("expected date_trunc day, got:\n%s", r.SQL)
	}
}

func TestCompile_Turnovers_Periodicity_Year(t *testing.T) {
	src := `ВЫБРАТЬ Номенклатура
ИЗ РегистрНакопления.ТоварноеДвижение.Обороты(, , Год)`

	r, err := query.Compile(src, query.CompileOpts{
		Registers: []*metadata.Register{testReg()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.SQL, "date_trunc('year', period) AS period") {
		t.Errorf("expected date_trunc year, got:\n%s", r.SQL)
	}
}

func TestCompile_Turnovers_Periodicity_Record(t *testing.T) {
	src := `ВЫБРАТЬ Номенклатура
ИЗ РегистрНакопления.ТоварноеДвижение.Обороты(, , Запись)`

	r, err := query.Compile(src, query.CompileOpts{
		Registers: []*metadata.Register{testReg()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.SQL, "period AS period") {
		t.Errorf("expected raw period (no truncation), got:\n%s", r.SQL)
	}
}

func TestCompile_Turnovers_PeriodicityWithFilter(t *testing.T) {
	src := `ВЫБРАТЬ Номенклатура, КоличествоПриход
ИЗ РегистрНакопления.ТоварноеДвижение.Обороты(, , Месяц, Склад = &С)`

	r, err := query.Compile(src, query.CompileOpts{
		Registers: []*metadata.Register{testReg()},
		Params:    map[string]any{"С": "Основной"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	// Periodicity should be present
	if !strings.Contains(sql, "date_trunc('month', period) AS period") {
		t.Errorf("expected period column, got:\n%s", sql)
	}
	// Filter should be applied from args[3]
	if !strings.Contains(sql, "склад = $") {
		t.Errorf("expected filter 'склад = $N', got:\n%s", sql)
	}
}

func TestCompile_Turnovers_FilterBackwardCompat(t *testing.T) {
	src := `ВЫБРАТЬ Номенклатура, КоличествоПриход
ИЗ РегистрНакопления.ТоварноеДвижение.Обороты(, , Склад = &С)`

	r, err := query.Compile(src, query.CompileOpts{
		Registers: []*metadata.Register{testReg()},
		Params:    map[string]any{"С": "Основной"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	// No period column — backward compatible
	if strings.Contains(sql, "AS period") {
		t.Errorf("should NOT have period column when no periodicity, got:\n%s", sql)
	}
	// Filter should still work from args[2]
	if !strings.Contains(sql, "склад = $") {
		t.Errorf("expected filter condition, got:\n%s", sql)
	}
}

func TestCompile_Turnovers_Periodicity_NoDate(t *testing.T) {
	src := `ВЫБРАТЬ Номенклатура, КоличествоПриход
ИЗ РегистрНакопления.ТоварноеДвижение.Обороты(, , Месяц)`

	r, err := query.Compile(src, query.CompileOpts{
		Registers: []*metadata.Register{testReg()},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	if !strings.Contains(sql, "date_trunc('month', period) AS period") {
		t.Errorf("expected period column even without dates, got:\n%s", sql)
	}
	if strings.Contains(sql, "period >=") || strings.Contains(sql, "period <=") {
		t.Errorf("no date args → no period WHERE conditions, got:\n%s", sql)
	}
}

// Round-trip: the outer query must be able to SELECT/ORDER BY the Период column.
// systemColAlias("Период") → "period", so the subquery must expose "AS period"
// (Latin), otherwise the outer reference is unresolvable on both engines.
func TestCompile_Turnovers_Periodicity_SelectPeriodColumn(t *testing.T) {
	for _, tc := range []struct {
		name  string
		dia   storage.Dialect
		trunc string
	}{
		{"pg", nil, "date_trunc('month', period)"},
		{"sqlite", storage.SQLiteDialect{}, "strftime('%Y-%m', COALESCE(ob_local_datetime(period), substr(period,1,19)))"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `ВЫБРАТЬ Период, Номенклатура, КоличествоПриход
ИЗ РегистрНакопления.ТоварноеДвижение.Обороты(, , Месяц)
УПОРЯДОЧИТЬ ПО Период`
			r, err := query.Compile(src, query.CompileOpts{
				Registers: []*metadata.Register{testReg()},
				Dialect:   tc.dia,
			})
			if err != nil {
				t.Fatal(err)
			}
			sql := r.SQL
			// Subquery exposes the period column under the Latin "period" alias.
			if !strings.Contains(sql, tc.trunc+" AS period") {
				t.Errorf("expected '%s AS period', got:\n%s", tc.trunc, sql)
			}
			// Outer query selects it as "period" (resolved via systemColAlias).
			if !strings.Contains(sql, "SELECT period,") {
				t.Errorf("outer SELECT should reference 'period', got:\n%s", sql)
			}
			// And orders by it — must resolve, not error.
			if !strings.Contains(sql, "ORDER BY period") {
				t.Errorf("outer ORDER BY should reference 'period', got:\n%s", sql)
			}
		})
	}
}
