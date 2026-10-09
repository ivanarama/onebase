package query_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Отбор виртуальной таблицы по ссылочному измерению — «Остатки(, Склад = &С)»,
// «СрезПоследних(, ТипЦен = &Т)» — встаёт в WHERE подзапроса над таблицей
// регистра, где колонка измерения — «склад_id». Раньше туда уходило логическое
// «склад» (псевдоним списка SELECT): SQLite видит псевдонимы в WHERE и прощал,
// PostgreSQL падал «column "склад" does not exist».
//
// Группировка по ссылке «СГРУППИРОВАТЬ ПО Д.Склад» шла по одному «склад_id»,
// а сортировка «УПОРЯДОЧИТЬ ПО Склад» — по наименованию из авто-JOIN: SQLite
// прощал, PostgreSQL требовал наименование в GROUP BY.
//
// Там же — строковое поле, в котором лежит UUID (реестр соответствий внешней
// системе): параметр-строку, похожую на UUID, платформа слала как «::uuid», и
// PostgreSQL не сравнивал «text = uuid» (#1981). Приведение «::uuid» при этом
// нужно остальным местам: без него «(&Склад ЕСТЬ ПУСТО ИЛИ …)» не типизируется.
func TestVirtualTableRefFilterAndUUIDStringMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ref := func(name, entity string) metadata.Field {
			return metadata.Field{Name: name, Type: metadata.FieldType("reference:" + entity), RefEntity: entity}
		}
		str := func(name string) metadata.Field { return metadata.Field{Name: name, Type: metadata.FieldTypeString} }
		num := func(name string) metadata.Field { return metadata.Field{Name: name, Type: metadata.FieldTypeNumber} }
		store := &metadata.Entity{Name: "СкладВТО", Kind: metadata.KindCatalog, Fields: []metadata.Field{str("Наименование")}}
		item := &metadata.Entity{Name: "ТоварВТО", Kind: metadata.KindCatalog, Fields: []metadata.Field{str("Наименование")}}
		kind := &metadata.Entity{Name: "ТипЦенВТО", Kind: metadata.KindCatalog, Fields: []metadata.Field{str("Наименование")}}
		doc := &metadata.Entity{Name: "ПриходВТО", Kind: metadata.KindDocument, Fields: []metadata.Field{str("Номер")}}
		master := &metadata.Entity{Name: "МастерВТО", Kind: metadata.KindCatalog, Fields: []metadata.Field{str("Наименование")}}
		entities := []*metadata.Entity{store, item, kind, doc, master}
		reg := &metadata.Register{
			Name:       "ОстаткиВТО",
			Dimensions: []metadata.Field{ref("Склад", store.Name), ref("Номенклатура", item.Name)},
			Resources:  []metadata.Field{num("Количество")},
		}
		prices := &metadata.InfoRegister{
			Name:       "ЦеныВТО",
			Periodic:   true,
			Dimensions: []metadata.Field{ref("Номенклатура", item.Name), ref("ТипЦен", kind.Name)},
			Resources:  []metadata.Field{num("Цена")},
		}
		mapping := &metadata.InfoRegister{
			Name:       "РеестрВТО",
			Dimensions: []metadata.Field{str("ГУИДВнешний")},
			Resources:  []metadata.Field{str("Объект")},
		}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{prices, mapping}); err != nil {
			t.Fatal(err)
		}
		mainID, otherID, itemID, retailID, masterID, docID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
		for _, row := range []struct {
			e  *metadata.Entity
			id uuid.UUID
			f  map[string]any
		}{
			{store, mainID, map[string]any{"Наименование": "Основной"}},
			{store, otherID, map[string]any{"Наименование": "Другой"}},
			{item, itemID, map[string]any{"Наименование": "Фильтр"}},
			{kind, retailID, map[string]any{"Наименование": "Розничная"}},
			{kind, masterID, map[string]any{"Наименование": "Мастера"}},
			{doc, docID, map[string]any{"Номер": "П-1"}},
			{master, uuid.New(), map[string]any{"Наименование": "Иванов"}},
		} {
			if err := db.Upsert(ctx, row.e.Name, row.id, row.f, row.e); err != nil {
				t.Fatal(err)
			}
		}
		period := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		if err := db.WriteMovements(ctx, reg.Name, doc.Name, docID, []map[string]any{
			{"ВидДвижения": "Приход", "Склад": mainID, "Номенклатура": itemID, "Количество": float64(5)},
			{"ВидДвижения": "Приход", "Склад": otherID, "Номенклатура": itemID, "Количество": float64(2)},
		}, reg, &period); err != nil {
			t.Fatal(err)
		}
		for _, p := range []struct {
			kind  uuid.UUID
			price float64
		}{{retailID, 300}, {masterID, 150}} {
			if err := db.InfoRegSet(ctx, prices, map[string]any{"Номенклатура": itemID, "ТипЦен": p.kind},
				map[string]any{"Цена": p.price}, &period); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.InfoRegSet(ctx, mapping, map[string]any{"ГУИДВнешний": "ext-1"},
			map[string]any{"Объект": itemID.String()}, nil); err != nil {
			t.Fatal(err)
		}
		if err := db.InfoRegSet(ctx, mapping, map[string]any{"ГУИДВнешний": "ext-пусто"},
			map[string]any{"Объект": ""}, nil); err != nil {
			t.Fatal(err)
		}

		for _, tc := range []struct {
			name   string
			src    string
			params map[string]any
			want   []string
		}{
			{
				name:   "Остатки с отбором по ссылочному измерению",
				src:    `ВЫБРАТЬ О.КоличествоОстаток КАК Метка, О.Склад КАК Место ИЗ РегистрНакопления.ОстаткиВТО.Остатки(, Склад = &Склад) КАК О`,
				params: map[string]any{"Склад": mainID.String()},
				want:   []string{"5:" + mainID.String()},
			},
			{
				name:   "Обороты с отбором по ссылочному измерению",
				src:    `ВЫБРАТЬ О.КоличествоОборот КАК Метка, О.Склад КАК Место ИЗ РегистрНакопления.ОстаткиВТО.Обороты(, , Склад = &Склад) КАК О`,
				params: map[string]any{"Склад": otherID.String()},
				want:   []string{"2:" + otherID.String()},
			},
			{
				name:   "СрезПоследних с отбором по ссылочному измерению",
				src:    `ВЫБРАТЬ Ц.Цена КАК Метка, Ц.ТипЦен КАК Место ИЗ РегистрСведений.ЦеныВТО.СрезПоследних(, ТипЦен = &Тип) КАК Ц`,
				params: map[string]any{"Тип": masterID.String()},
				want:   []string{"150:" + masterID.String()},
			},
			{
				name:   "строковое поле с UUID против параметра-UUID (#1981)",
				src:    `ВЫБРАТЬ Р.ГУИДВнешний КАК Метка, Р.Объект КАК Место ИЗ РегистрСведений.РеестрВТО КАК Р ГДЕ Р.Объект = &Объект`,
				params: map[string]any{"Объект": itemID.String()},
				want:   []string{"ext-1:" + itemID.String()},
			},
			{
				name:   "параметр-UUID первым против строкового поля (#1981)",
				src:    `ВЫБРАТЬ Р.ГУИДВнешний КАК Метка, Р.Объект КАК Место ИЗ РегистрСведений.РеестрВТО КАК Р ГДЕ &Объект = Р.Объект`,
				params: map[string]any{"Объект": itemID.String()},
				want:   []string{"ext-1:" + itemID.String()},
			},
			{
				// Без приведения PostgreSQL не типизирует «$1 IS NULL»: «could not
				// determine data type of parameter» — так падали все отчёты с
				// необязательным отбором по ссылке.
				name:   "необязательный отбор по ссылке: (&Склад ЕСТЬ ПУСТО ИЛИ Д.Склад = &Склад)",
				src:    `ВЫБРАТЬ СУММА(Д.Количество) КАК Метка, Д.Склад КАК Место ИЗ РегистрНакопления.ОстаткиВТО КАК Д ГДЕ (&Склад ЕСТЬ ПУСТО ИЛИ Д.Склад = &Склад) СГРУППИРОВАТЬ ПО Д.Склад`,
				params: map[string]any{"Склад": mainID.String()},
				want:   []string{"5:" + mainID.String()},
			},
			{
				name:   "строковое поле с UUID против списка UUID (#1981)",
				src:    `ВЫБРАТЬ Р.ГУИДВнешний КАК Метка, Р.Объект КАК Место ИЗ РегистрСведений.РеестрВТО КАК Р ГДЕ Р.Объект В (&Объекты)`,
				params: map[string]any{"Объекты": []any{itemID.String(), uuid.NewString()}},
				want:   []string{"ext-1:" + itemID.String()},
			},
			{
				name:   "пустая строка против ссылочного поля — не ошибка, не совпало",
				src:    `ВЫБРАТЬ О.КоличествоОстаток КАК Метка, О.Склад КАК Место ИЗ РегистрНакопления.ОстаткиВТО.Остатки() КАК О ГДЕ (&Все ИЛИ О.Склад = &Склад) И О.Склад = &Склад`,
				params: map[string]any{"Все": false, "Склад": ""},
				want:   nil,
			},
			{
				// УПОРЯДОЧИТЬ ПО Склад сортирует по наименованию из авто-JOIN, а
				// группировка по Д.Склад шла только по «склад_id»: PostgreSQL
				// отвергал запрос («must appear in the GROUP BY clause»).
				name:   "группировка по ссылке с сортировкой по её представлению",
				src:    `ВЫБРАТЬ СУММА(Д.Количество) КАК Метка, Д.Склад КАК Место ИЗ РегистрНакопления.ОстаткиВТО КАК Д СГРУППИРОВАТЬ ПО Д.Склад УПОРЯДОЧИТЬ ПО Склад`,
				params: map[string]any{},
				want:   []string{"2:" + otherID.String(), "5:" + mainID.String()},
			},
			{
				// Как в отчётах: подзапрос в соединении стоит между FROM и
				// группировкой, основной источник берётся из области SELECT.
				name: "группировка по ссылке рядом с подзапросом в соединении",
				src: `ВЫБРАТЬ СУММА(Д.Количество) КАК Метка, Д.Склад КАК Место ИЗ РегистрНакопления.ОстаткиВТО КАК Д
					ЛЕВОЕ СОЕДИНЕНИЕ (ВЫБРАТЬ РАЗЛИЧНЫЕ СМ.Наименование КАК Имя ИЗ Справочник.МастерВТО КАК СМ) КАК М
					ПО М.Имя = "Петров"
					ГДЕ М.Имя ЕСТЬ NULL СГРУППИРОВАТЬ ПО Д.Склад УПОРЯДОЧИТЬ ПО Склад`,
				params: map[string]any{},
				want:   []string{"2:" + otherID.String(), "5:" + mainID.String()},
			},
			{
				name:   "пустая строка против строкового поля — по-прежнему находит пустое",
				src:    `ВЫБРАТЬ Р.ГУИДВнешний КАК Метка, Р.Объект КАК Место ИЗ РегистрСведений.РеестрВТО КАК Р ГДЕ Р.Объект = &Пусто`,
				params: map[string]any{"Пусто": ""},
				want:   []string{"ext-пусто:"},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				res, err := query.Compile(tc.src, query.CompileOpts{
					Entities:  entities,
					Registers: []*metadata.Register{reg},
					InfoRegs:  []*metadata.InfoRegister{prices, mapping},
					Params:    tc.params,
					Dialect:   db.Dialect(),
				})
				if err != nil {
					t.Fatal(err)
				}
				rows, err := db.QueryAll(ctx, res.SQL, res.Args...)
				if err != nil {
					t.Fatalf("%v\nSQL: %s", err, res.SQL)
				}
				query.NormalizeColumns(&res, rows)
				var got []string
				for _, r := range rows {
					got = append(got, vtoLabel(r["метка"])+":"+vtoLabel(r["место"]))
				}
				if strings.Join(got, "|") != strings.Join(tc.want, "|") {
					t.Fatalf("строки %v, ожидалось %v\nSQL: %s", got, tc.want, res.SQL)
				}
			})
		}
	})
}

// vtoLabel — значение колонки строкой, одинаково на обоих движках: число без
// хвостовых нулей (PostgreSQL отдаёт numeric строкой «5.0000»), UUID как есть.
func vtoLabel(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return vtoLabel(string(x))
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case string:
		if f, err := strconv.ParseFloat(x, 64); err == nil && !strings.Contains(x, "-") {
			return strconv.FormatFloat(f, 'f', -1, 64)
		}
		return x
	}
	return vtoLabel(fmt.Sprint(v))
}
