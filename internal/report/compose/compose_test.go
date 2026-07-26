package compose

import (
	"math"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/ivantit66/onebase/internal/report"
)

// noEval — заглушка Evaluator: оформление не применяется (для тестов без условий).
type noEval struct{}

func (noEval) EvalBool(string, Row) (bool, error)                 { return false, nil }
func (noEval) EvalNum(string, Row) (decimal.Decimal, bool, error) { return decimal.Zero, false, nil }

func decEq(t *testing.T, got any, want string) {
	t.Helper()
	d, ok := toDecimal(got)
	w, werr := decimal.NewFromString(want)
	if !ok || werr != nil || !d.Equal(w) {
		t.Fatalf("got %v (%T), want %s", got, got, want)
	}
}

func TestSingleGrouping(t *testing.T) {
	rows := []Row{
		{"Менеджер": "Иванов", "Сумма": "100"},
		{"Менеджер": "Иванов", "Сумма": "50"},
		{"Менеджер": "Петров", "Сумма": "30"},
	}
	spec := report.Composition{
		Groupings: []string{"Менеджер"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Totals:    report.Totals{Grand: true, Subtotals: true},
	}
	res, err := Compose(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 2 {
		t.Fatalf("groups=%d", len(res.Groups))
	}
	if res.Groups[0].Key != "Иванов" {
		t.Fatalf("order: %v", res.Groups[0].Key)
	}
	decEq(t, res.Groups[0].Subtotals["Сумма"], "150")
	decEq(t, res.Grand["Сумма"], "180")
	if res.RowCount != 3 || res.Capped {
		t.Fatalf("rowcount=%d capped=%v", res.RowCount, res.Capped)
	}
}

func TestGroupByDecimalKey(t *testing.T) {
	// Две равные по значению decimal должны попасть в одну группу: ключ
	// нормализуется в строку, а не сравнивается по указателю big.Int.
	rows := []Row{
		{"Год": decimal.NewFromInt(2026), "Сумма": "10"},
		{"Год": decimal.NewFromInt(2026), "Сумма": "20"},
		{"Год": decimal.NewFromInt(2025), "Сумма": "5"},
	}
	spec := report.Composition{
		Groupings: []string{"Год"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
	}
	res, _ := Compose(rows, spec, noEval{})
	if len(res.Groups) != 2 {
		t.Fatalf("ожидали 2 группы по равным decimal, получили %d", len(res.Groups))
	}
	decEq(t, res.Groups[0].Subtotals["Сумма"], "30")
}

func TestGroupByByteSliceKeyNoPanic(t *testing.T) {
	// Драйвер БД может вернуть значение колонки как []byte; []byte неэшируем как
	// ключ map → раньше группировка по такой колонке паниковала (issue #88).
	rows := []Row{
		{"Код": []byte("A"), "Сумма": "10"},
		{"Код": []byte("A"), "Сумма": "20"},
		{"Код": []byte("B"), "Сумма": "5"},
	}
	spec := report.Composition{
		Groupings: []string{"Код"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
	}
	res, err := Compose(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 2 {
		t.Fatalf("ожидали 2 группы по []byte-ключам, получили %d", len(res.Groups))
	}
	decEq(t, res.Groups[0].Subtotals["Сумма"], "30")
}

func TestGroupByMixedNumericKey(t *testing.T) {
	// Одно числовое значение, пришедшее как int64 и как decimal, должно попасть
	// в одну группу (issue #88): ключ канонизируется по значению, не по типу.
	rows := []Row{
		{"Год": int64(2026), "Сумма": "10"},
		{"Год": decimal.NewFromInt(2026), "Сумма": "20"},
		{"Год": int64(2025), "Сумма": "5"},
	}
	spec := report.Composition{
		Groupings: []string{"Год"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
	}
	res, _ := Compose(rows, spec, noEval{})
	if len(res.Groups) != 2 {
		t.Fatalf("int64 и decimal одного значения должны слиться, получили %d групп", len(res.Groups))
	}
	decEq(t, res.Groups[0].Subtotals["Сумма"], "30")
}

func TestNestedAndDetails(t *testing.T) {
	rows := []Row{
		{"М": "И", "К": "Р", "Сумма": "600"},
		{"М": "И", "К": "Р", "Сумма": "380"},
		{"М": "И", "К": "П", "Сумма": "270"},
	}
	spec := report.Composition{
		Groupings: []string{"М", "К"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Detail:    true,
	}
	res, err := Compose(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 1 || len(res.Groups[0].Children) != 2 {
		t.Fatalf("tree: %+v", res.Groups)
	}
	rom := res.Groups[0].Children[0]
	decEq(t, rom.Subtotals["Сумма"], "980")
	if len(rom.Details) != 2 {
		t.Fatalf("details=%d", len(rom.Details))
	}
	if rom.Details[0].Values["Сумма"] != "600" {
		t.Fatalf("detail val: %v", rom.Details[0].Values["Сумма"])
	}
}

func TestAggregates(t *testing.T) {
	rows := []Row{
		{"Г": "A", "X": "10.50"},
		{"Г": "A", "X": "4.50"},
	}
	mk := func(agg string) any {
		spec := report.Composition{Groupings: []string{"Г"}, Measures: []report.Measure{{Field: "X", Agg: agg}}}
		res, _ := Compose(rows, spec, noEval{})
		return res.Groups[0].Subtotals["X"]
	}
	decEq(t, mk("sum"), "15")
	decEq(t, mk("avg"), "7.5")
	decEq(t, mk("min"), "4.5")
	decEq(t, mk("max"), "10.5")
	if c, _ := mk("count").(int64); c != 2 {
		t.Fatalf("count=%v", mk("count"))
	}
}

func TestSort(t *testing.T) {
	rows := []Row{
		{"М": "А", "Сумма": "100"},
		{"М": "Б", "Сумма": "300"},
		{"М": "В", "Сумма": "200"},
	}
	spec := report.Composition{
		Groupings: []string{"М"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Sort:      []report.SortKey{{Field: "Сумма", Dir: "desc"}},
	}
	res, _ := Compose(rows, spec, noEval{})
	got := []any{res.Groups[0].Key, res.Groups[1].Key, res.Groups[2].Key}
	if got[0] != "Б" || got[1] != "В" || got[2] != "А" {
		t.Fatalf("order by subtotal desc: %v", got)
	}
}

func TestSortGroupsNilSubtotalLast(t *testing.T) {
	// Группа без числовых строк (min пустого набора = nil) при сортировке по
	// этому показателю не должна лексикографически прыгать впереди числовых
	// подытогов (issue #90): nil уходит в конец при возрастании.
	rows := []Row{
		{"М": "А", "Сумма": "100"},
		{"М": "Пусто", "Сумма": "нечисло"}, // min → nil
		{"М": "Б", "Сумма": "300"},
	}
	spec := report.Composition{
		Groupings: []string{"М"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "min"}},
		Sort:      []report.SortKey{{Field: "Сумма", Dir: "asc"}},
	}
	res, _ := Compose(rows, spec, noEval{})
	got := []any{res.Groups[0].Key, res.Groups[1].Key, res.Groups[2].Key}
	if got[0] != "А" || got[1] != "Б" || got[2] != "Пусто" {
		t.Fatalf("nil-подытог должен быть последним при asc: %v", got)
	}
}

// negEval: совпадение, когда Сумма отрицательна (имитация выражения "Сумма < 0").
type negEval struct{}

func (negEval) EvalBool(expr string, row Row) (bool, error) {
	d, ok := toDecimal(row["Сумма"])
	return ok && d.Sign() < 0, nil
}
func (negEval) EvalNum(string, Row) (decimal.Decimal, bool, error) { return decimal.Zero, false, nil }

// exprEval — фейковый Evaluator для теста вычисляемых показателей.
// Обрабатывает выражение "Сумма*2": берёт row["Сумма"] и умножает на 2.
type exprEval struct{}

func (exprEval) EvalBool(string, Row) (bool, error) { return false, nil }
func (exprEval) EvalNum(expr string, row Row) (decimal.Decimal, bool, error) {
	if expr == "Сумма*2" {
		d, ok := toDecimal(row["Сумма"])
		if !ok {
			return decimal.Zero, false, nil
		}
		return d.Mul(decimal.NewFromInt(2)), true, nil
	}
	return decimal.Zero, false, nil
}

func TestConditionalAndCap(t *testing.T) {
	rows := []Row{
		{"М": "A", "Сумма": "-45"},
		{"М": "A", "Сумма": "10"},
	}
	spec := report.Composition{
		Groupings: []string{"М"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Detail:    true,
		Conditional: []report.CondRule{
			{When: "Сумма < 0", Field: "", Style: report.CellStyle{Color: "#c00", Bold: true}},
		},
	}
	res, _ := Compose(rows, spec, negEval{})
	d := res.Groups[0].Details
	if d[0].Styles[""].Color != "#c00" || !d[0].Styles[""].Bold {
		t.Fatalf("styles[0]: %+v", d[0].Styles)
	}
	if _, ok := d[1].Styles[""]; ok {
		t.Fatalf("row 1 must be unstyled: %+v", d[1].Styles)
	}

	// потолок строк (логика уже в ComposeN)
	res2, _ := ComposeN(rows, spec, negEval{}, 1)
	if !res2.Capped || res2.RowCount != 1 {
		t.Fatalf("cap: capped=%v rowcount=%d", res2.Capped, res2.RowCount)
	}
}

func TestConditionalOnGroups(t *testing.T) {
	// Условное оформление должно срабатывать и на строках групп/подытогов
	// (по их подытогам), а не только на детальных строках — чтобы убыточную
	// группу можно было подсветить при detail:false (дизайн плана 59).
	rows := []Row{
		{"М": "Убыток", "Сумма": "-100"},
		{"М": "Прибыль", "Сумма": "50"},
	}
	spec := report.Composition{
		Groupings: []string{"М"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Conditional: []report.CondRule{
			{When: "Сумма < 0", Field: "", Style: report.CellStyle{Color: "#c00", Bold: true}},
		},
	}
	res, _ := Compose(rows, spec, negEval{})
	var loss, profit *Group
	for _, g := range res.Groups {
		switch g.Key {
		case "Убыток":
			loss = g
		case "Прибыль":
			profit = g
		}
	}
	if loss == nil || loss.Styles[""].Color != "#c00" || !loss.Styles[""].Bold {
		t.Fatalf("убыточная группа должна быть стилизована: %+v", loss)
	}
	if profit == nil || len(profit.Styles) != 0 {
		t.Fatalf("прибыльная группа не должна иметь стиль: %+v", profit)
	}
}

func TestExprMeasure(t *testing.T) {
	// Вычисляемый показатель Рент = Сумма*2 вычисляется ПОСЛЕ агрегации обычных
	// показателей (Сумма). Проверяем subtotal группы и grand-итог.
	rows := []Row{
		{"Менеджер": "Иванов", "Сумма": "60"},
		{"Менеджер": "Иванов", "Сумма": "40"},
		{"Менеджер": "Петров", "Сумма": "30"},
	}
	spec := report.Composition{
		Groupings: []string{"Менеджер"},
		Measures: []report.Measure{
			{Field: "Сумма", Agg: "sum"},
			{Field: "Рент", Expr: "Сумма*2"}, // вычисляемый: = Сумма * 2
		},
		Totals: report.Totals{Grand: true, Subtotals: true},
	}
	res, err := Compose(rows, spec, exprEval{})
	if err != nil {
		t.Fatal(err)
	}
	// Subtotal для Иванова: Сумма=100, Рент=200
	ivanov := res.Groups[0]
	if ivanov.Key != "Иванов" {
		t.Fatalf("первая группа: %v", ivanov.Key)
	}
	decEq(t, ivanov.Subtotals["Сумма"], "100")
	decEq(t, ivanov.Subtotals["Рент"], "200")

	// Subtotal для Петрова: Сумма=30, Рент=60
	petrov := res.Groups[1]
	decEq(t, petrov.Subtotals["Сумма"], "30")
	decEq(t, petrov.Subtotals["Рент"], "60")

	// Grand: Сумма=130, Рент=260 (вычисляется по grand-агрегату Сумма=130, не суммируется)
	decEq(t, res.Grand["Сумма"], "130")
	decEq(t, res.Grand["Рент"], "260")
}

// exprEvalMul — фейковый Evaluator для теста многоуровневой группировки с Expr.
// Поддерживает выражение "Сумма*2": берёт row["Сумма"] и умножает на 2.
// Отличается от exprEval только именем, чтобы тест был самодостаточен.
type exprEvalMul struct{}

func (exprEvalMul) EvalBool(string, Row) (bool, error) { return false, nil }
func (exprEvalMul) EvalNum(expr string, row Row) (decimal.Decimal, bool, error) {
	if expr == "Сумма*2" {
		d, ok := toDecimal(row["Сумма"])
		if !ok {
			return decimal.Zero, false, nil
		}
		return d.Mul(decimal.NewFromInt(2)), true, nil
	}
	return decimal.Zero, false, nil
}

func TestExprMeasureNested(t *testing.T) {
	// Два уровня группировки + вычисляемый показатель X=Сумма*2.
	// Цель: убедиться, что Expr вычисляется на подытоге КАЖДОГО уровня
	// (вложенная группа, верхняя группа и grand), а не только на верхнем.
	// Тест упадёт, если buildGroups не передаёт ev на вложенный уровень
	// (тогда вложенная группа получит nil вместо decimal).
	rows := []Row{
		{"Регион": "Север", "Менеджер": "Иванов", "Сумма": "60"},
		{"Регион": "Север", "Менеджер": "Иванов", "Сумма": "40"},
		{"Регион": "Север", "Менеджер": "Петров", "Сумма": "20"},
		{"Регион": "Юг", "Менеджер": "Сидоров", "Сумма": "30"},
	}
	spec := report.Composition{
		Groupings: []string{"Регион", "Менеджер"},
		Measures: []report.Measure{
			{Field: "Сумма", Agg: "sum"},
			{Field: "X", Expr: "Сумма*2"}, // вычисляемый: X = Сумма * 2
		},
		Totals: report.Totals{Grand: true, Subtotals: true},
	}
	res, err := Compose(rows, spec, exprEvalMul{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 2 {
		t.Fatalf("ожидали 2 верхние группы (Север/Юг), получили %d", len(res.Groups))
	}

	// Найдём группы Север и Юг
	var sever, yug *Group
	for _, g := range res.Groups {
		switch g.Key {
		case "Север":
			sever = g
		case "Юг":
			yug = g
		}
	}
	if sever == nil || yug == nil {
		t.Fatalf("не найдены ожидаемые группы, получили: %v, %v", res.Groups[0].Key, res.Groups[1].Key)
		return
	}

	// Верхний уровень (Север): Сумма=120, X=240
	decEq(t, sever.Subtotals["Сумма"], "120")
	decEq(t, sever.Subtotals["X"], "240")

	// Верхний уровень (Юг): Сумма=30, X=60
	decEq(t, yug.Subtotals["Сумма"], "30")
	decEq(t, yug.Subtotals["X"], "60")

	// Вложенный уровень (Север→Иванов): Сумма=100, X=200
	// Именно здесь тест упадёт, если ev не передаётся на вложенный уровень.
	if len(sever.Children) == 0 {
		t.Fatal("ожидали вложенные группы у Север")
	}
	var ivanov *Group
	for _, ch := range sever.Children {
		if ch.Key == "Иванов" {
			ivanov = ch
			break
		}
	}
	if ivanov == nil {
		t.Fatal("не найдена вложенная группа Иванов")
		return
	}
	decEq(t, ivanov.Subtotals["Сумма"], "100")
	decEq(t, ivanov.Subtotals["X"], "200") // ← упадёт, если ev=nil на вложенном уровне

	// Вложенный уровень (Север→Петров): Сумма=20, X=40
	var petrov *Group
	for _, ch := range sever.Children {
		if ch.Key == "Петров" {
			petrov = ch
			break
		}
	}
	if petrov == nil {
		t.Fatal("не найдена вложенная группа Петров")
		return
	}
	decEq(t, petrov.Subtotals["Сумма"], "20")
	decEq(t, petrov.Subtotals["X"], "40")

	// Grand: Сумма=150, X=300
	decEq(t, res.Grand["Сумма"], "150")
	decEq(t, res.Grand["X"], "300")
}

func TestComposeFieldsCaseInsensitive(t *testing.T) {
	// Компилятор запросов отдаёт имена колонок в нижнем регистре
	// (ВЫБРАТЬ Выручка КАК Выручка → колонка "выручка"), тогда как composition
	// ссылается на поля с заглавной. Сопоставление обязано быть
	// регистронезависимым — иначе все строки схлопываются в одну пустую группу,
	// а агрегаты выходят нулевыми.
	rows := []Row{
		{"организация": "Альфа", "выручка": "100", "валоваяприбыль": "40"},
		{"организация": "Альфа", "выручка": "50", "валоваяприбыль": "-10"},
		{"организация": "Бета", "выручка": "30", "валоваяприбыль": "30"},
	}
	spec := report.Composition{
		Groupings: []string{"Организация"},
		Measures: []report.Measure{
			{Field: "Выручка", Agg: "sum"},
			{Field: "ВаловаяПрибыль", Agg: "sum"},
		},
		Totals: report.Totals{Grand: true},
	}
	res, err := Compose(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 2 {
		t.Fatalf("ожидали 2 группы (Альфа, Бета), получили %d", len(res.Groups))
	}
	if res.Groups[0].Key != "Альфа" {
		t.Fatalf("ключ группы должен быть наименованием «Альфа», получили %q", res.Groups[0].Key)
	}
	decEq(t, res.Groups[0].Subtotals["Выручка"], "150")
	decEq(t, res.Grand["ВаловаяПрибыль"], "60")
}

// TestComposeNaNInfNoPanic: значения NaN/±Inf из REAL/float8 колонок не валят
// компоновку паникой decimal.NewFromFloat (issue #9). NaN/Inf выпадают из сумм
// (как «не число»), а группировка по такому ключу не падает.
func TestComposeNaNInfNoPanic(t *testing.T) {
	rows := []Row{
		{"Товар": "А", "Сумма": math.Inf(1)},
		{"Товар": "А", "Сумма": math.NaN()},
		{"Товар": "Б", "Сумма": 100.0},
		{"Товар": math.Inf(-1), "Сумма": math.NaN()}, // NaN/Inf и в ключе группировки
	}
	spec := report.Composition{
		Groupings: []string{"Товар"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Totals:    report.Totals{Grand: true},
	}
	// Главное — отсутствие паники.
	res, err := Compose(rows, spec, noEval{})
	if err != nil {
		t.Fatalf("Compose вернул ошибку: %v", err)
	}
	// NaN/Inf выпали из суммы → grand = 100 (только валидная строка Б).
	decEq(t, res.Grand["Сумма"], "100")
}

// TestComposeCrossNaNInfNoPanic: то же для кросс-режима (crossAgg/normalizeGroupKey).
func TestComposeCrossNaNInfNoPanic(t *testing.T) {
	rows := []Row{
		{"Товар": "А", "Месяц": "Янв", "Сумма": math.Inf(1)},
		{"Товар": "А", "Месяц": "Фев", "Сумма": 50.0},
	}
	spec := report.Composition{
		Groupings: []string{"Товар"},
		Columns:   []string{"Месяц"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
	}
	if _, err := ComposeCross(rows, spec, noEval{}); err != nil {
		t.Fatalf("ComposeCross вернул ошибку: %v", err)
	}
}
