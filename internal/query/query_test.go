package query_test

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestCompile_BalancesQuery(t *testing.T) {
	src := `ВЫБРАТЬ
  Номенклатура,
  СУММА(Количество) КАК Количество
ИЗ РегистрНакопления.ТоварноеДвижение
СГРУППИРОВАТЬ ПО Номенклатура
УПОРЯДОЧИТЬ ПО Номенклатура`

	r, err := query.Compile(src, query.CompileOpts{})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	if !strings.Contains(sql, "SELECT") {
		t.Errorf("expected SELECT, got: %s", sql)
	}
	if !strings.Contains(sql, "SUM(количество)") {
		t.Errorf("expected SUM(количество), got: %s", sql)
	}
	if !strings.Contains(sql, "рег_товарноедвижение") {
		t.Errorf("expected рег_товарноедвижение, got: %s", sql)
	}
	if !strings.Contains(sql, "GROUP BY") {
		t.Errorf("expected GROUP BY, got: %s", sql)
	}
	if !strings.Contains(sql, "ORDER BY") {
		t.Errorf("expected ORDER BY, got: %s", sql)
	}
	if len(r.Args) != 0 {
		t.Errorf("expected 0 args, got %d", len(r.Args))
	}
}

func TestCompile_WithParam(t *testing.T) {
	src := `ВЫБРАТЬ Номенклатура ИЗ РегистрНакопления.ТоварноеДвижение ГДЕ вид_движения = &ВидДвижения`

	r, err := query.Compile(src, query.CompileOpts{Params: map[string]any{"ВидДвижения": "Приход"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.SQL, "$1") {
		t.Errorf("expected $1 placeholder, got: %s", r.SQL)
	}
	if len(r.Args) != 1 || r.Args[0] != "Приход" {
		t.Errorf("expected args=[Приход], got %v", r.Args)
	}
}

func TestCompile_WithUUIDParam(t *testing.T) {
	src := `ВЫБРАТЬ Номенклатура ИЗ РегистрНакопления.ТоварноеДвижение ГДЕ Номенклатура = &Ном`

	r, err := query.Compile(src, query.CompileOpts{Params: map[string]any{"Ном": "4e582af9-cd26-4af0-a244-d282e02a5603"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.SQL, "::uuid") {
		t.Errorf("expected ::uuid cast for UUID param, got: %s", r.SQL)
	}
	if len(r.Args) != 1 || r.Args[0] != "4e582af9-cd26-4af0-a244-d282e02a5603" {
		t.Errorf("expected UUID arg, got %v", r.Args)
	}
}

func TestCompile_WithNonUUIDStringParam(t *testing.T) {
	src := `ВЫБРАТЬ Номенклатура ИЗ РегистрНакопления.ТоварноеДвижение ГДЕ вид_движения = &Вид`

	r, err := query.Compile(src, query.CompileOpts{Params: map[string]any{"Вид": "Приход"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.SQL, "::text") {
		t.Errorf("expected ::text cast for regular string param, got: %s", r.SQL)
	}
}

func TestCompile_StringLiteral(t *testing.T) {
	src := `ВЫБРАТЬ Номенклатура ИЗ РегистрНакопления.ТоварноеДвижение ГДЕ вид_движения = "Приход"`

	r, err := query.Compile(src, query.CompileOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.SQL, "'Приход'") {
		t.Errorf("expected single-quoted string, got: %s", r.SQL)
	}
}

func TestCompile_InnerJoin(t *testing.T) {
	src := `ВЫБРАТЬ
  Прод.Номер,
  Клиент.Наименование,
  Прод.Сумма
ИЗ Документ.Реализация КАК Прод
  ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.Клиент КАК Клиент
  ПО Прод.Покупатель = Клиент.Ссылка`

	r, err := query.Compile(src, query.CompileOpts{})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	if !strings.Contains(sql, "INNER JOIN") {
		t.Errorf("expected INNER JOIN, got: %s", sql)
	}
	if !strings.Contains(sql, "реализация") {
		t.Errorf("expected реализация table, got: %s", sql)
	}
	if !strings.Contains(sql, "клиент") {
		t.Errorf("expected клиент table, got: %s", sql)
	}
	// .Ссылка should map to .id
	if !strings.Contains(sql, "клиент.id") {
		t.Errorf("expected клиент.id (Ссылка→id), got: %s", sql)
	}
	if !strings.Contains(sql, "ON") {
		t.Errorf("expected ON clause, got: %s", sql)
	}
}

func TestCompile_LeftJoin_WithGroupBy(t *testing.T) {
	src := `ВЫБРАТЬ
  Н.Наименование,
  СУММА(Д.Количество) КАК Итог
ИЗ Справочник.Номенклатура КАК Н
  ЛЕВОЕ СОЕДИНЕНИЕ РегистрНакопления.ОстаткиТоваров КАК Д
  ПО Н.Ссылка = Д.Номенклатура
СГРУППИРОВАТЬ ПО Н.Наименование
УПОРЯДОЧИТЬ ПО Н.Наименование`

	r, err := query.Compile(src, query.CompileOpts{})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	if !strings.Contains(sql, "LEFT JOIN") {
		t.Errorf("expected LEFT JOIN, got: %s", sql)
	}
	if !strings.Contains(sql, "GROUP BY") {
		t.Errorf("expected GROUP BY, got: %s", sql)
	}
	if !strings.Contains(sql, "ORDER BY") {
		t.Errorf("expected ORDER BY, got: %s", sql)
	}
	// .Ссылка → .id
	if !strings.Contains(sql, "н.id") {
		t.Errorf("expected н.id (Ссылка→id), got: %s", sql)
	}
	// ON keyword (not BY)
	if !strings.Contains(sql, "ON") {
		t.Errorf("expected ON clause, got: %s", sql)
	}
}

func TestCompile_EnglishJoin(t *testing.T) {
	src := `SELECT P.Number, C.Name
FROM Document.Sale AS P
  LEFT JOIN Catalog.Client AS C
  ON P.Client = C.Reference`

	r, err := query.Compile(src, query.CompileOpts{})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	if !strings.Contains(sql, "LEFT JOIN") {
		t.Errorf("expected LEFT JOIN, got: %s", sql)
	}
	if !strings.Contains(sql, "c.id") {
		t.Errorf("expected c.id (Reference→id), got: %s", sql)
	}
}

func TestCompile_RightJoin(t *testing.T) {
	src := `ВЫБРАТЬ К.Наименование
ИЗ Документ.Заказ КАК З
  ПРАВОЕ СОЕДИНЕНИЕ Справочник.Клиент КАК К
  ПО З.Клиент = К.Ссылка`

	r, err := query.Compile(src, query.CompileOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.SQL, "RIGHT JOIN") {
		t.Errorf("expected RIGHT JOIN, got: %s", r.SQL)
	}
}

func TestCompile_Ssylka_InSelect(t *testing.T) {
	src := `ВЫБРАТЬ Н.Ссылка, Н.Наименование ИЗ Справочник.Номенклатура КАК Н`

	r, err := query.Compile(src, query.CompileOpts{})
	if err != nil {
		t.Fatal(err)
	}
	// Н.Ссылка → н.id
	if !strings.Contains(r.SQL, "н.id") {
		t.Errorf("expected н.id, got: %s", r.SQL)
	}
}

// Регрессия для замечания #18: bare-Ссылка (без алиаса) тоже должна
// транслироваться в id.
func TestCompile_Ssylka_Bare(t *testing.T) {
	src := `ВЫБРАТЬ Ссылка, Наименование ИЗ Справочник.ТипЦен`

	r, err := query.Compile(src, query.CompileOpts{})
	if err != nil {
		t.Fatal(err)
	}
	// Ссылка → id, без префикса
	if !strings.Contains(r.SQL, "SELECT id") {
		t.Errorf("expected SELECT id, got: %s", r.SQL)
	}
	if strings.Contains(r.SQL, "ссылка") {
		t.Errorf("bare Ссылка leaked into SQL: %s", r.SQL)
	}
}

// системные колонки регистра должны быть доступны
// под PascalCase русскоязычными именами.
func TestCompile_SystemCols_BareAndDotted(t *testing.T) {
	src := `ВЫБРАТЬ Р.Период, Р.ВидДвижения, Период
ИЗ РегистрНакопления.ТоварноеДвижение КАК Р
ГДЕ Период >= &Дата`

	r, err := query.Compile(src, query.CompileOpts{
		Params: map[string]any{"Дата": "2026-01-01"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	// alias-форма
	if !strings.Contains(sql, "р.period") {
		t.Errorf("ожидалось р.period, получили: %s", sql)
	}
	if !strings.Contains(sql, "р.вид_движения") {
		t.Errorf("ожидалось р.вид_движения, получили: %s", sql)
	}
	// bare и в WHERE
	if !strings.Contains(sql, "period >=") {
		t.Errorf("ожидалось period >= в WHERE, получили: %s", sql)
	}
}

// Имена системных колонок регистра не являются зарезервированными для
// документов и справочников: там это обычные прикладные поля с кириллическими
// физическими именами.
func TestCompile_SystemColNamesRemainEntityFields(t *testing.T) {
	for _, source := range []string{"Документ.НачислениеВзноса", "Справочник.Периоды"} {
		src := `ВЫБРАТЬ Д.Период, Д.ВидДвижения, Д.Регистратор, Д.НомерСтроки, Период
ИЗ ` + source + ` КАК Д
ГДЕ Д.Период >= &Дата`

		r, err := query.Compile(src, query.CompileOpts{
			Params: map[string]any{"Дата": "2026-01-01"},
		})
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		for _, want := range []string{
			"д.период",
			"д.виддвижения",
			"д.регистратор",
			"д.номерстроки",
			"период",
		} {
			if !strings.Contains(r.SQL, want) {
				t.Errorf("%s: ожидалось %q, получили: %s", source, want, r.SQL)
			}
		}
		for _, unwanted := range []string{
			"д.period",
			"д.вид_движения",
			"д.recorder",
			"д.line_number",
		} {
			if strings.Contains(r.SQL, unwanted) {
				t.Errorf("%s: системная колонка %q не должна применяться к сущности: %s", source, unwanted, r.SQL)
			}
		}
	}
}

// В смешанном запросе тип определяется по квалификатору: одно и то же имя
// «Период» у документа и регистра должно вести к разным физическим колонкам.
func TestCompile_SystemColsUseQualifiedSourceType(t *testing.T) {
	src := `ВЫБРАТЬ Д.Период, Р.Период
ИЗ Документ.НачислениеВзноса КАК Д
ЛЕВОЕ СОЕДИНЕНИЕ РегистрНакопления.Взносы КАК Р
ПО Р.Регистратор = Д.Ссылка`

	r, err := query.Compile(src, query.CompileOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.SQL, "д.период") {
		t.Errorf("поле документа должно остаться кириллическим: %s", r.SQL)
	}
	if !strings.Contains(r.SQL, "р.period") {
		t.Errorf("поле регистра должно разрешиться в period: %s", r.SQL)
	}
	if !strings.Contains(r.SQL, "р.recorder") {
		t.Errorf("Регистратор регистра должен разрешиться в recorder: %s", r.SQL)
	}
}

func TestCompile_SystemColsAreScopedToNestedSelect(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "outer register inner document",
			src: `ВЫБРАТЬ Период,
    (ВЫБРАТЬ Период ИЗ Документ.НачислениеВзноса КАК Д) КАК ПериодДокумента
ИЗ РегистрНакопления.Взносы КАК Р`,
			want: []string{"SELECT period,", "(SELECT период FROM начислениевзноса AS д)"},
		},
		{
			name: "outer document inner register",
			src: `ВЫБРАТЬ Период,
    (ВЫБРАТЬ Период ИЗ РегистрНакопления.Взносы КАК Р) КАК ПериодРегистра
ИЗ Документ.НачислениеВзноса КАК Д`,
			want: []string{"SELECT период,", "(SELECT period FROM рег_взносы AS р)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := query.Compile(tt.src, query.CompileOpts{})
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				if !strings.Contains(r.SQL, want) {
					t.Errorf("ожидалось %q, получили: %s", want, r.SQL)
				}
			}
		})
	}
}

// Поле сущности, прочитанное через ссылочное измерение регистра, также не
// должно ошибочно считаться системной колонкой самого регистра.
func TestCompile_SystemColNameThroughReferenceRemainsEntityField(t *testing.T) {
	src := `ВЫБРАТЬ ДокументНачисления.Период
ИЗ РегистрНакопления.Взносы КАК Р`
	reg := &metadata.Register{
		Name: "Взносы",
		Dimensions: []metadata.Field{
			{Name: "ДокументНачисления", RefEntity: "НачислениеВзноса"},
		},
	}
	doc := &metadata.Entity{
		Name: "НачислениеВзноса",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Период", Type: metadata.FieldTypeDate},
		},
	}

	r, err := query.Compile(src, query.CompileOpts{
		Registers: []*metadata.Register{reg},
		Entities:  []*metadata.Entity{doc},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.SQL, "ref_документначисления.период") {
		t.Errorf("поле документа через ссылку должно остаться кириллическим: %s", r.SQL)
	}
	if strings.Contains(r.SQL, "ref_документначисления.period") {
		t.Errorf("поле документа через ссылку ошибочно разрешилось как системное: %s", r.SQL)
	}
}

// функции даты в DSL должны транслироваться в SQL-эквиваленты
// под нужный диалект. SQL case-insensitive — сравниваем в lowercase, поскольку
// транслятор лоуэркейсит идентификаторы.
func TestCompile_DateFuncs_SQLite(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`ВЫБРАТЬ Год(Период) ИЗ РегистрНакопления.Х`, "cast(strftime('%Y', period) AS integer)"},
		{`ВЫБРАТЬ Месяц(Период) ИЗ РегистрНакопления.Х`, "cast(strftime('%m', period) AS integer)"},
		{`ВЫБРАТЬ День(Период) ИЗ РегистрНакопления.Х`, "cast(strftime('%d', period) AS integer)"},
		{`ВЫБРАТЬ НачалоДня(Период) ИЗ РегистрНакопления.Х`, "date(period)"},
		{`ВЫБРАТЬ НачалоМесяца(Период) ИЗ РегистрНакопления.Х`, "date(period, 'start of month')"},
	}
	for _, c := range cases {
		r, err := query.Compile(c.src, query.CompileOpts{Dialect: storage.SQLiteDialect{}})
		if err != nil {
			t.Errorf("Compile(%q): %v", c.src, err)
			continue
		}
		if !strings.Contains(r.SQL, c.want) {
			t.Errorf("Compile(%q):\n  got: %s\n want substring: %s", c.src, r.SQL, c.want)
		}
	}
}

func TestCompile_DateFuncs_PG(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`ВЫБРАТЬ Год(Период) ИЗ РегистрНакопления.Х`, "cast(extract(year FROM period) AS integer)"},
		{`ВЫБРАТЬ Месяц(Период) ИЗ РегистрНакопления.Х`, "cast(extract(month FROM period) AS integer)"},
		{`ВЫБРАТЬ НачалоДня(Период) ИЗ РегистрНакопления.Х`, "date_trunc('day', period)"},
		{`ВЫБРАТЬ НачалоМесяца(Период) ИЗ РегистрНакопления.Х`, "date_trunc('month', period)"},
	}
	for _, c := range cases {
		r, err := query.Compile(c.src, query.CompileOpts{}) // default = PG
		if err != nil {
			t.Errorf("Compile(%q): %v", c.src, err)
			continue
		}
		if !strings.Contains(r.SQL, c.want) {
			t.Errorf("Compile(%q):\n  got: %s\n want substring: %s", c.src, r.SQL, c.want)
		}
	}
}

// Вложенные date-функции тоже должны разворачиваться: Месяц(НачалоМесяца(x)).
func TestCompile_DateFuncs_Nested(t *testing.T) {
	src := `ВЫБРАТЬ Месяц(НачалоМесяца(Период)) ИЗ РегистрНакопления.Х`
	r, err := query.Compile(src, query.CompileOpts{Dialect: storage.SQLiteDialect{}})
	if err != nil {
		t.Fatal(err)
	}
	// внутри Месяц(...) должно быть date(period, ...)
	if !strings.Contains(r.SQL, "strftime('%m', date(period, 'start of month'))") {
		t.Errorf("вложенность не развернулась: %s", r.SQL)
	}
}

// Английские алиасы Year/Month/Day тоже должны работать.
func TestCompile_DateFuncs_EnglishAliases(t *testing.T) {
	r, err := query.Compile(
		`ВЫБРАТЬ Year(Period), Month(Period) ИЗ РегистрНакопления.Х`,
		query.CompileOpts{Dialect: storage.SQLiteDialect{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.SQL, "strftime('%Y'") {
		t.Errorf("Year не развернулся: %s", r.SQL)
	}
	if !strings.Contains(r.SQL, "strftime('%m'") {
		t.Errorf("Month не развернулся: %s", r.SQL)
	}
}

// Скалярные математические 1С-функции (ОКР/АБС/ЦЕЛ) должны транслироваться в
// нативный SQL, а не уходить в БД сырым русским именем (issue #39).
// SQL case-insensitive — идентификаторы лоуэркейсятся транслятором, ключевые
// слова (AS) остаются в верхнем регистре.
func TestCompile_MathFuncs_SQLite(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`ВЫБРАТЬ ОКР(Сумма, 2) ИЗ РегистрНакопления.Х`, "round(сумма, 2)"},
		{`ВЫБРАТЬ ОКР(Сумма) ИЗ РегистрНакопления.Х`, "round(сумма)"},
		{`ВЫБРАТЬ АБС(Сумма) ИЗ РегистрНакопления.Х`, "abs(сумма)"},
		{`ВЫБРАТЬ ЦЕЛ(Сумма) ИЗ РегистрНакопления.Х`, "cast(сумма AS integer)"},
		// пример из issue: ОКР поверх агрегата СУММА.
		{`ВЫБРАТЬ ОКР(СУММА(Выручка), 0) КАК Значение ИЗ РегистрНакопления.Х`, "round(SUM(выручка), 0)"},
	}
	for _, c := range cases {
		r, err := query.Compile(c.src, query.CompileOpts{Dialect: storage.SQLiteDialect{}})
		if err != nil {
			t.Errorf("Compile(%q): %v", c.src, err)
			continue
		}
		if !strings.Contains(r.SQL, c.want) {
			t.Errorf("Compile(%q):\n  got: %s\n want substring: %s", c.src, r.SQL, c.want)
		}
	}
}

func TestCompile_MathFuncs_PG(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`ВЫБРАТЬ ОКР(Сумма, 2) ИЗ РегистрНакопления.Х`, "round(сумма, 2)"},
		{`ВЫБРАТЬ АБС(Сумма) ИЗ РегистрНакопления.Х`, "abs(сумма)"},
		// На PG усечение к нулю — TRUNC, а не CAST AS INTEGER (тот округлял бы).
		{`ВЫБРАТЬ ЦЕЛ(Сумма) ИЗ РегистрНакопления.Х`, "trunc(сумма)"},
	}
	for _, c := range cases {
		r, err := query.Compile(c.src, query.CompileOpts{}) // default = PG
		if err != nil {
			t.Errorf("Compile(%q): %v", c.src, err)
			continue
		}
		if !strings.Contains(r.SQL, c.want) {
			t.Errorf("Compile(%q):\n  got: %s\n want substring: %s", c.src, r.SQL, c.want)
		}
	}
}

// Английские алиасы математики (round/abs/int) тоже должны разворачиваться, в
// частности int → усечение, согласованное с DSL-builtin int/цел.
func TestCompile_MathFuncs_EnglishAliases(t *testing.T) {
	r, err := query.Compile(
		`ВЫБРАТЬ int(Сумма) ИЗ РегистрНакопления.Х`,
		query.CompileOpts{Dialect: storage.SQLiteDialect{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.SQL, "cast(сумма AS integer)") {
		t.Errorf("int не развернулся в усечение: %s", r.SQL)
	}
}

// вариант 1 — внешний ГДЕ на атрибут поверх .Остатки().
// Inner subquery должен экспортировать атрибут, чтобы outer WHERE сработал.
func TestCompile_Balances_OuterWhereOnAttribute(t *testing.T) {
	src := `ВЫБРАТЬ Контрагент, СуммаОстаток ИЗ РегистрНакопления.Взаиморасчёты.Остатки() ГДЕ ТипКонтрагента = &ТипК`
	reg := &metadata.Register{
		Name: "Взаиморасчёты",
		Dimensions: []metadata.Field{
			{Name: "Контрагент", Type: metadata.FieldTypeString},
		},
		Resources: []metadata.Field{
			{Name: "Сумма", Type: metadata.FieldTypeNumber},
		},
		Attributes: []metadata.Field{
			{Name: "ТипКонтрагента", Type: metadata.FieldTypeString},
		},
	}
	r, err := query.Compile(src, query.CompileOpts{
		Registers: []*metadata.Register{reg},
		Params:    map[string]any{"ТипК": "Поставщик"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// inner subquery теперь экспортирует атрибут через MIN(),
	// поэтому outer WHERE на типконтрагента работает.
	if !strings.Contains(r.SQL, "MIN(типконтрагента)") {
		t.Errorf("атрибут не агрегирован в inner SELECT: %s", r.SQL)
	}
	if !strings.Contains(r.SQL, "WHERE типконтрагента") {
		t.Errorf("outer WHERE на атрибут не сгенерирован: %s", r.SQL)
	}
}

// SELECT атрибута поверх .Остатки() должен работать после фикса.
func TestCompile_Balances_OuterSelectOnAttribute(t *testing.T) {
	src := `ВЫБРАТЬ Контрагент, ТипКонтрагента, СуммаОстаток ИЗ РегистрНакопления.Взаиморасчёты.Остатки()`
	reg := &metadata.Register{
		Name: "Взаиморасчёты",
		Dimensions: []metadata.Field{
			{Name: "Контрагент", Type: metadata.FieldTypeString},
		},
		Resources: []metadata.Field{
			{Name: "Сумма", Type: metadata.FieldTypeNumber},
		},
		Attributes: []metadata.Field{
			{Name: "ТипКонтрагента", Type: metadata.FieldTypeString},
		},
	}
	r, err := query.Compile(src, query.CompileOpts{Registers: []*metadata.Register{reg}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.SQL, "MIN(типконтрагента)") {
		t.Errorf("атрибут не агрегирован: %s", r.SQL)
	}
}

// атрибуты регистров должны быть доступны в фильтрах .Остатки().
func TestCompile_Balances_FilterByAttribute(t *testing.T) {
	src := `ВЫБРАТЬ * ИЗ РегистрНакопления.Взаиморасчёты.Остатки(, ТипКонтрагента = &ТипК)`
	reg := &metadata.Register{
		Name: "Взаиморасчёты",
		Dimensions: []metadata.Field{
			{Name: "Контрагент", Type: metadata.FieldTypeString},
		},
		Resources: []metadata.Field{
			{Name: "Сумма", Type: metadata.FieldTypeNumber},
		},
		Attributes: []metadata.Field{
			{Name: "ТипКонтрагента", Type: metadata.FieldTypeString},
		},
	}
	r, err := query.Compile(src, query.CompileOpts{
		Registers: []*metadata.Register{reg},
		Params:    map[string]any{"ТипК": "Поставщик"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// в WHERE должно быть условие на типконтрагента (lowercase)
	if !strings.Contains(r.SQL, "типконтрагента") {
		t.Errorf("атрибут не попал в WHERE: %s", r.SQL)
	}
	t.Logf("compiled SQL: %s", r.SQL)
}

func TestCompile_Ssylka_InWhere(t *testing.T) {
	src := `ВЫБРАТЬ Наименование ИЗ Справочник.ТипЦен ГДЕ Ссылка = &ИД`

	r, err := query.Compile(src, query.CompileOpts{
		Params: map[string]any{"ИД": "00000000-0000-0000-0000-000000000000"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.SQL, "WHERE id =") {
		t.Errorf("expected WHERE id =, got: %s", r.SQL)
	}
}

func TestCompile_RefDim_AutoJoin(t *testing.T) {
	// When a register has a reference-type dimension, the query compiler should:
	// • SELECT:   Номенклатура → ref_номенклатура.наименование AS номенклатура
	// • FROM:     inject LEFT JOIN номенклатура ref_номенклатура ON ...
	// • WHERE:    Номенклатура → номенклатура_id
	// • GROUP BY: Номенклатура → ref_номенклатура.наименование
	src := `ВЫБРАТЬ
  Номенклатура,
  СУММА(Выручка) КАК Выручка
ИЗ РегистрНакопления.ВаловаяПрибыль
ГДЕ (&Номенклатура ЕСТЬ ПУСТО ИЛИ Номенклатура = &Номенклатура)
СГРУППИРОВАТЬ ПО Номенклатура`

	reg := &metadata.Register{
		Name: "ВаловаяПрибыль",
		Dimensions: []metadata.Field{
			{Name: "Номенклатура", RefEntity: "Номенклатура"},
		},
		Resources: []metadata.Field{
			{Name: "Выручка"},
		},
	}

	r, err := query.Compile(src, query.CompileOpts{
		Registers: []*metadata.Register{reg},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL

	// SELECT must reference наименование from the join alias, not the raw dim name
	if !strings.Contains(sql, "ref_номенклатура.наименование") {
		t.Errorf("expected ref_номенклатура.наименование in SELECT, got: %s", sql)
	}
	// Auto-JOIN must be present
	if !strings.Contains(sql, "LEFT JOIN номенклатура ref_номенклатура") {
		t.Errorf("expected LEFT JOIN, got: %s", sql)
	}
	// WHERE must use the _id column
	if !strings.Contains(sql, "номенклатура_id") {
		t.Errorf("expected номенклатура_id in WHERE, got: %s", sql)
	}
	// GROUP BY must use the join expression (not _id)
	if !strings.Contains(sql, "GROUP BY ref_номенклатура.наименование") {
		t.Errorf("expected GROUP BY ref_номенклатура.наименование, got: %s", sql)
	}
}

func TestCompile_RefField_SsylkaSelectsUUID(t *testing.T) {
	src := `ВЫБРАТЬ Проект.Ссылка КАК ПроектСсылка ИЗ Документ.Приход`

	r, err := query.Compile(src, query.CompileOpts{
		Entities: []*metadata.Entity{
			{
				Name: "Приход",
				Kind: metadata.KindDocument,
				Fields: []metadata.Field{
					{Name: "Проект", Type: "reference:Проект", RefEntity: "Проект"},
				},
			},
			{
				Name:   "Проект",
				Kind:   metadata.KindCatalog,
				Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	if !strings.Contains(sql, "ref_проект.id AS проектссылка") {
		t.Errorf("expected Проект.Ссылка to select ref_проект.id, got: %s", sql)
	}
	if strings.Contains(sql, "ref_проект.наименование AS проектссылка") {
		t.Errorf("Проект.Ссылка must not use display name: %s", sql)
	}
}

// TestCompile_StringDim_NoIdSuffix ensures that a string-type dimension
// in one register is not incorrectly mapped to _id when another register
// in opts has a reference-type dimension with the same name.
func TestCompile_StringDim_NoIdSuffix(t *testing.T) {
	src := `ВЫБРАТЬ
  Номенклатура,
  Склад,
  СУММА(Количество) КАК Количество
ИЗ РегистрНакопления.ОстаткиТоваров
СГРУППИРОВАТЬ ПО Номенклатура, Склад`

	// ОстаткиТоваров has Номенклатура as plain string
	regStock := &metadata.Register{
		Name: "ОстаткиТоваров",
		Dimensions: []metadata.Field{
			{Name: "Номенклатура", Type: "string"},
			{Name: "Склад", Type: "string"},
		},
		Resources: []metadata.Field{{Name: "Количество"}},
	}
	// ВаловаяПрибыль has Номенклатура as reference — should NOT pollute colMap
	regProfit := &metadata.Register{
		Name: "ВаловаяПрибыль",
		Dimensions: []metadata.Field{
			{Name: "Номенклатура", Type: "reference:Номенклатура", RefEntity: "Номенклатура"},
		},
		Resources: []metadata.Field{{Name: "Выручка"}},
	}

	r, err := query.Compile(src, query.CompileOpts{
		Registers: []*metadata.Register{regStock, regProfit},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	if strings.Contains(sql, "номенклатура_id") {
		t.Errorf("string-type dim must NOT become _id; got: %s", sql)
	}
	if !strings.Contains(sql, "номенклатура") {
		t.Errorf("expected plain 'номенклатура' column in SQL, got: %s", sql)
	}
}

// TestCompile_BareCatalogInFrom ensures that a bare catalog name in FROM clause
// is not replaced by colMap entry from a reference dimension in another register.
// Regression: "ИЗ Номенклатура" was compiled to "FROM номенклатура_id" because
// colMap fallback mapped it from a register with reference:Номенклатура.
func TestCompile_BareCatalogInFrom(t *testing.T) {
	src := "ВЫБРАТЬ Наименование, ЦенаПродажи ИЗ Номенклатура"

	regProfit := &metadata.Register{
		Name: "ВаловаяПрибыль",
		Dimensions: []metadata.Field{
			{Name: "Номенклатура", Type: "reference:Номенклатура", RefEntity: "Номенклатура"},
		},
		Resources: []metadata.Field{{Name: "Выручка"}},
	}

	r, err := query.Compile(src, query.CompileOpts{
		Registers: []*metadata.Register{regProfit},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	if !strings.Contains(sql, "FROM номенклатура") {
		t.Errorf("expected FROM номенклатура, got: %s", sql)
	}
	if strings.Contains(sql, "FROM номенклатура_id") {
		t.Errorf("bare catalog name must NOT be replaced by _id column; got: %s", sql)
	}
}

// TestCompile_VT_RefDim_AutoJoin ensures that VT queries (Остатки, etc.) also
// get auto-JOINs for reference dimensions so results show names, not UUIDs.
func TestCompile_VT_RefDim_AutoJoin(t *testing.T) {
	src := "ВЫБРАТЬ Номенклатура КАК Ном, КоличествоОстаток КАК Количество " +
		"ИЗ РегистрНакопления.ПартииТоваров.Остатки() " +
		"ГДЕ (&Номенклатура ЕСТЬ ПУСТО ИЛИ Номенклатура = &Номенклатура) " +
		"УПОРЯДОЧИТЬ ПО Ном"

	reg := &metadata.Register{
		Name: "ПартииТоваров",
		Dimensions: []metadata.Field{
			{Name: "Номенклатура", RefEntity: "Номенклатура"},
		},
		Resources: []metadata.Field{{Name: "Количество"}},
	}

	r, err := query.Compile(src, query.CompileOpts{
		Registers: []*metadata.Register{reg},
		Params:    map[string]any{"Номенклатура": nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL

	// SELECT: must resolve ref dim via JOIN, not double AS
	if !strings.Contains(sql, "ref_номенклатура.наименование AS ном") {
		t.Errorf("expected ref_номенклатура.наименование AS ном, got: %s", sql)
	}
	// LEFT JOIN must be present in outer query
	if !strings.Contains(sql, "LEFT JOIN номенклатура ref_номенклатура") {
		t.Errorf("expected LEFT JOIN for VT outer query, got: %s", sql)
	}
	// WHERE: outer query must use logical name (aliased from VT subquery)
	if !strings.Contains(sql, "номенклатура = NULL") {
		t.Errorf("expected logical name in outer WHERE, got: %s", sql)
	}
	// ORDER BY: alias is fine
	if !strings.Contains(sql, "ORDER BY ном") {
		t.Errorf("expected ORDER BY ном (alias), got: %s", sql)
	}
}

// TestCompile_VT_RefDim_Document verifies that a dimension referencing a document
// resolves to .номер (not .наименование) in a virtual-table (Остатки) query —
// documents have no наименование column. Regression for buildVTRefDimInfos not
// propagating refIsDoc.
func TestCompile_VT_RefDim_Document(t *testing.T) {
	src := "ВЫБРАТЬ ЗаказПоставщику, КоличествоОстаток " +
		"ИЗ РегистрНакопления.ТоварыВПути.Остатки()"

	reg := &metadata.Register{
		Name: "ТоварыВПути",
		Dimensions: []metadata.Field{
			{Name: "ЗаказПоставщику", RefEntity: "ЗаказПоставщику"},
		},
		Resources: []metadata.Field{{Name: "Количество"}},
	}

	r, err := query.Compile(src, query.CompileOpts{
		Registers: []*metadata.Register{reg},
		Entities: []*metadata.Entity{
			{Name: "ЗаказПоставщику", Kind: metadata.KindDocument},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL

	if !strings.Contains(sql, "ref_заказпоставщику.номер") {
		t.Errorf("expected document ref to resolve to .номер, got: %s", sql)
	}
	if strings.Contains(sql, "ref_заказпоставщику.наименование") {
		t.Errorf("document ref must not use .наименование, got: %s", sql)
	}
}

// TestCompile_VT_WithUserAlias verifies that КАК after a VT subquery is consumed
// and the user-provided alias is used in the auto-JOIN ON clause.
func TestCompile_VT_WithUserAlias(t *testing.T) {
	src := "ВЫБРАТЬ Номенклатура ИЗ РегистрНакопления.ПартииТоваров.Остатки() КАК Пар"

	reg := &metadata.Register{
		Name: "ПартииТоваров",
		Dimensions: []metadata.Field{
			{Name: "Номенклатура", RefEntity: "Номенклатура"},
		},
		Resources: []metadata.Field{{Name: "Количество"}},
	}

	r, err := query.Compile(src, query.CompileOpts{
		Registers: []*metadata.Register{reg},
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL

	if !strings.Contains(sql, "AS пар") {
		t.Errorf("expected user alias 'пар', got: %s", sql)
	}
	if strings.Contains(sql, "AS остатки_партиитоваров") {
		t.Errorf("default alias must not appear when user provides КАК, got: %s", sql)
	}
	if !strings.Contains(sql, "пар.номенклатура") {
		t.Errorf("JOIN ON must use user alias, got: %s", sql)
	}
}
