package query_test

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func qualifiedPhysicalFixture(t *testing.T, db *storage.DB) (query.CompileOpts, string) {
	t.Helper()
	str := func(name string) metadata.Field { return metadata.Field{Name: name, Type: metadata.FieldTypeString} }
	ref := func(name, entity string) metadata.Field {
		return metadata.Field{Name: name, Type: metadata.FieldType("reference:" + entity), RefEntity: entity}
	}
	employee := &metadata.Entity{Name: "Сотрудники", Kind: metadata.KindCatalog, Fields: []metadata.Field{str("Наименование")}}
	master := &metadata.Entity{Name: "Мастера", Kind: metadata.KindCatalog, Fields: []metadata.Field{str("Наименование"),
		{Name: "МенеджерКонсультант", Type: metadata.FieldTypeBool}, ref("Совпало", employee.Name), str("Текст")}}
	order := &metadata.Entity{Name: "Заявка", Kind: metadata.KindDocument, Fields: []metadata.Field{str("Номер"), ref("Мастер", master.Name),
		ref("МенеджерКонсультант", employee.Name), {Name: "Совпало", Type: metadata.FieldTypeBool},
		{Name: "Текст", Type: metadata.FieldTypeNumber}, ref("ТолькоДокумент", employee.Name)}}
	entities := []*metadata.Entity{employee, master, order}
	if err := db.Migrate(context.Background(), entities); err != nil {
		t.Fatal(err)
	}
	employeeID, yes, no := uuid.New(), uuid.New(), uuid.New()
	for _, row := range []struct {
		entity *metadata.Entity
		id     uuid.UUID
		values map[string]any
	}{
		{employee, employeeID, map[string]any{"Наименование": "Employee"}},
		{master, yes, map[string]any{"Наименование": "yes master", "МенеджерКонсультант": true, "Совпало": employeeID, "Текст": "word"}},
		{master, no, map[string]any{"Наименование": "no master", "МенеджерКонсультант": false, "Совпало": employeeID, "Текст": "2x"}},
		{order, uuid.New(), map[string]any{"Номер": "yes", "Мастер": yes, "МенеджерКонсультант": employeeID, "Совпало": true, "Текст": 2, "ТолькоДокумент": employeeID}},
		{order, uuid.New(), map[string]any{"Номер": "no", "Мастер": no, "МенеджерКонсультант": employeeID, "Совпало": false, "Текст": 2, "ТолькоДокумент": employeeID}},
		{order, uuid.New(), map[string]any{"Номер": "missing", "Мастер": nil, "МенеджерКонсультант": employeeID, "Совпало": false, "Текст": 2, "ТолькоДокумент": employeeID}},
	} {
		if err := db.Upsert(context.Background(), row.entity.Name, row.id, row.values, row.entity); err != nil {
			t.Fatal(err)
		}
	}
	return query.CompileOpts{Entities: entities, Dialect: db.Dialect(), Params: map[string]any{"Employee": employeeID.String()}}, employeeID.String()
}

func assertQualifiedRows(t *testing.T, db *storage.DB, opts query.CompileOpts, text string, columns, want []string) query.Result {
	t.Helper()
	result, err := query.Compile(text, opts)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	rows, gotColumns, err := query.Run(context.Background(), db, &result)
	if err != nil {
		t.Fatalf("Run: %v\nSQL: %s", err, result.SQL)
	}
	if !reflect.DeepEqual(gotColumns, columns) {
		t.Fatalf("columns=%v want=%v", gotColumns, columns)
	}
	got := []string{}
	for _, row := range rows {
		cells := []string{}
		for _, column := range columns {
			value := row[column]
			cell := "<NULL>"
			if value != nil {
				cell = fmt.Sprint(value)
				if column == "флаг" || column == "второй" {
					switch value := value.(type) {
					case bool:
						cell = fmt.Sprint(value)
					case int64:
						if value != 0 && value != 1 {
							t.Fatalf("invalid boolean %v", value)
						}
						cell = fmt.Sprint(value == 1)
					default:
						t.Fatalf("unexpected boolean type %T: %v", value, value)
					}
				}
			}
			cells = append(cells, cell)
		}
		got = append(got, strings.Join(cells, "|"))
	}
	gotWant := append([]string{}, want...)
	sort.Strings(got)
	sort.Strings(gotWant)
	if !reflect.DeepEqual(got, gotWant) {
		t.Fatalf("rows=%v want=%v\nSQL: %s", got, gotWant, result.SQL)
	}
	return result
}

func TestQualifiedPhysicalColumnsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		opts, employee := qualifiedPhysicalFixture(t, db)
		const join = ` ИЗ Документ.Заявка КАК З ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Мастера КАК М ПО М.Ссылка = З.Мастер`
		const base = `ВЫБРАТЬ З.Номер КАК Номер, М.МенеджерКонсультант КАК Флаг` + join
		all := []string{"yes|true", "no|false", "missing|<NULL>"}
		for _, tc := range []struct {
			name, text    string
			columns, rows []string
		}{
			{"local_raw_reference_column", `ВЫБРАТЬ З.Номер КАК Номер` + join + ` ГДЕ З.Номер В (ВЫБРАТЬ М.Номер ИЗ Документ.Заявка КАК М ГДЕ М.менеджерконсультант_id = &Employee)`, []string{"номер"}, []string{"yes", "no", "missing"}},
			{"local_derived_star_raw_reference_column", `ВЫБРАТЬ З.Номер КАК Номер` + join + ` ГДЕ З.Номер В (ВЫБРАТЬ М.Номер ИЗ (ВЫБРАТЬ Д.* ИЗ Документ.Заявка КАК Д) КАК М ГДЕ М.менеджерконсультант_id = &Employee)`, []string{"номер"}, []string{"yes", "no", "missing"}},
			{"projection", base, []string{"номер", "флаг"}, all},
			{"where_true", base + ` ГДЕ М.МенеджерКонсультант = ИСТИНА`, []string{"номер", "флаг"}, []string{"yes|true"}},
			{"where_false", base + ` ГДЕ М.МенеджерКонсультант = ЛОЖЬ`, []string{"номер", "флаг"}, []string{"no|false"}},
			{"on_preserves_null", base + ` И М.МенеджерКонсультант = ИСТИНА`, []string{"номер", "флаг"}, []string{"yes|true", "no|<NULL>", "missing|<NULL>"}},
			{"reverse_sources", `ВЫБРАТЬ З.Номер КАК Номер, М.МенеджерКонсультант КАК Флаг ИЗ Справочник.Мастера КАК М ЛЕВОЕ СОЕДИНЕНИЕ Документ.Заявка КАК З ПО З.Мастер = М.Ссылка`, []string{"номер", "флаг"}, []string{"yes|true", "no|false"}},
			{"mixed_alias_case", strings.ReplaceAll(strings.ReplaceAll(base, "М.", "mIx."), "КАК М ", "КАК Mix "), []string{"номер", "флаг"}, all},
			{"alias_named_foreign_field", strings.ReplaceAll(strings.ReplaceAll(base, "М.", "МенеджерКонсультант."), "КАК М ", "КАК МенеджерКонсультант "), []string{"номер", "флаг"}, all},
			{"derived_explicit", `ВЫБРАТЬ Д.Номер КАК Номер, Д.Флаг КАК Флаг ИЗ (` + base + `) КАК Д`, []string{"номер", "флаг"}, all},
			{"derived_star", `ВЫБРАТЬ Д.МенеджерКонсультант КАК Флаг ИЗ (ВЫБРАТЬ М.*` + join + ` ГДЕ З.Номер = "yes") КАК Д`, []string{"флаг"}, []string{"true"}},
			{"derived_named_after_unnamed_expression", `ВЫБРАТЬ З.Номер КАК Номер` + join + ` ГДЕ З.Номер В (ВЫБРАТЬ М.МенеджерКонсультант ИЗ (ВЫБРАТЬ КОЛИЧЕСТВО(*), "yes" КАК МенеджерКонсультант ИЗ Справочник.Сотрудники) КАК М)`, []string{"номер"}, []string{"yes"}},
			{"correlated_joined_owner", base + ` ГДЕ З.Номер В (ВЫБРАТЬ Д.Номер ИЗ Документ.Заявка КАК Д ГДЕ Д.Номер = З.Номер И М.МенеджерКонсультант = ИСТИНА)`, []string{"номер", "флаг"}, []string{"yes|true"}},
			{"union_sibling_types", `ВЫБРАТЬ З.Номер КАК Номер` + join + ` ГДЕ М.МенеджерКонсультант = ИСТИНА ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ М.Номер КАК Номер ИЗ Справочник.Мастера КАК З ЛЕВОЕ СОЕДИНЕНИЕ Документ.Заявка КАК М ПО М.Мастер = З.Ссылка ГДЕ М.Совпало = ЛОЖЬ`, []string{"номер"}, []string{"yes", "no"}},
			{"repeated_source", base + ` ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Мастера КАК М2 ПО М2.Ссылка = З.Мастер И М2.МенеджерКонсультант = ЛОЖЬ`, []string{"номер", "флаг"}, all},
			{"opposite_reference_predicate", `ВЫБРАТЬ З.Номер КАК Номер` + join + ` ГДЕ М.Совпало = &Employee`, []string{"номер"}, []string{"yes", "no"}},
			{"opposite_reference_id", `ВЫБРАТЬ З.Номер КАК Номер, М.Совпало.Ссылка КАК Ид` + join + ` ГДЕ М.Совпало = &Employee`, []string{"номер", "ид"}, []string{"yes|" + employee, "no|" + employee}},
			{"opposite_navigation", `ВЫБРАТЬ З.Номер КАК Номер, М.Совпало.Наименование КАК Имя` + join + ` ГДЕ М.Совпало.Наименование = "Employee"`, []string{"номер", "имя"}, []string{"yes|Employee", "no|Employee"}},
			{"text_does_not_borrow_numeric_cast", `ВЫБРАТЬ З.Номер КАК Номер, М.Текст КАК Текст` + join + ` ГДЕ М.Текст = "word"`, []string{"номер", "текст"}, []string{"yes|word"}},
		} {
			t.Run(tc.name, func(t *testing.T) { assertQualifiedRows(t, db, opts, tc.text, tc.columns, tc.rows) })
		}
		t.Run("read_gate_keeps_joined_source", func(t *testing.T) {
			result, err := query.Compile(base, opts)
			if err != nil {
				t.Fatal(err)
			}
			user := &auth.User{Login: "restricted", Roles: []*auth.Role{{Permissions: auth.Permission{Documents: map[string][]string{"Заявка": {"read"}}}}}}
			if denied := access.DeniedReadSource(user, result.Sources); !strings.Contains(denied, "Мастера") {
				t.Fatalf("joined source escaped read guard: %q, %+v", denied, result.Sources)
			}
		})
		t.Run("row_filters_preserve_left_and_on", func(t *testing.T) {
			filtered := opts
			filtered.RowFilters = map[query.SourceRef]*storage.Predicate{{Kind: "catalog", Name: "Мастера"}: {Field: "МенеджерКонсультант", Op: "eq", Value: false}}
			assertQualifiedRows(t, db, filtered, base, []string{"номер", "флаг"}, []string{"yes|<NULL>", "no|false", "missing|<NULL>"})
			assertQualifiedRows(t, db, filtered, base+` И М.МенеджерКонсультант = ИСТИНА`, []string{"номер", "флаг"}, []string{"yes|<NULL>", "no|<NULL>", "missing|<NULL>"})
		})
		for _, text := range []string{
			`ВЫБРАТЬ М.ТолькоДокумент КАК Х` + join,
			`ВЫБРАТЬ Чужой.МенеджерКонсультант КАК Х` + join,
			`ВЫБРАТЬ З.Номер КАК Номер` + join + ` ГДЕ З.Номер В (ВЫБРАТЬ М.МенеджерКонсультант ИЗ Справочник.Сотрудники КАК М)`,
			`ВЫБРАТЬ З.Номер КАК Номер` + join + ` ГДЕ З.Номер В (ВЫБРАТЬ М.МенеджерКонсультант ИЗ (ВЫБРАТЬ Ссылка ИЗ Справочник.Сотрудники) КАК М)`,
			`ВЫБРАТЬ М.Наименование КАК Имя` + join + ` ГДЕ М.Наименование В (ВЫБРАТЬ М.Наименование ИЗ (ВЫБРАТЬ КОЛИЧЕСТВО(*) ИЗ Справочник.Сотрудники) КАК М)`,
		} {
			t.Run("unknown_owner_or_missing_field", func(t *testing.T) {
				result, err := query.Compile(text, opts)
				if err != nil {
					return
				}
				for _, foreign := range []string{"м.толькодокумент_id", "чужой.менеджерконсультант_id", "м.менеджерконсультант_id"} {
					if strings.Contains(result.SQL, foreign) {
						t.Errorf("foreign mapping %s\nSQL: %s", foreign, result.SQL)
					}
				}
				if rows, _, err := query.Run(context.Background(), db, &result); err == nil {
					t.Fatalf("unknown field read foreign data: %v\n%s", rows, result.SQL)
				}
			})
		}
	})
}
