package access_test

import (
	"testing"

	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
)

func compileForMask(t *testing.T, src string) query.Result {
	t.Helper()
	res, err := query.Compile(src, query.CompileOpts{Entities: []*metadata.Entity{clientEntity()}})
	if err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	return res
}

func maskUser(fields auth.FieldPolicies) *auth.User {
	return &auth.User{Roles: []*auth.Role{catRole([]string{"read"}, fields)}}
}

func lookupClient(_, _ string) *metadata.Entity { return clientEntity() }

func TestQueryMaskPlan_MasksSimpleColumnsAndStar(t *testing.T) {
	u := maskUser(auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}})

	plan := access.QueryMaskPlanFor(u, compileForMask(t, `ВЫБРАТЬ Телефон КАК Контакт ИЗ Справочник.Клиент`), lookupClient)
	if plan.Denied != "" {
		t.Fatalf("простая колонка должна маскироваться, а не отклоняться: %s", plan.Denied)
	}
	rows := []map[string]any{{"контакт": "+79161234455"}}
	if err := plan.Apply(rows); err != nil {
		t.Fatal(err)
	}
	if rows[0]["контакт"] != "••••••••4455" {
		t.Fatalf("колонка под алиасом не замаскирована: %v", rows[0])
	}

	starPlan := access.QueryMaskPlanFor(u, compileForMask(t, `ВЫБРАТЬ * ИЗ Справочник.Клиент`), lookupClient)
	if starPlan.Denied != "" {
		t.Fatalf("«*» должен маскироваться по именам колонок: %s", starPlan.Denied)
	}
	starRows := []map[string]any{{"телефон": "+79161234455", "адрес": "Москва"}}
	if err := starPlan.Apply(starRows); err != nil {
		t.Fatal(err)
	}
	if starRows[0]["телефон"] != "••••••••4455" || starRows[0]["адрес"] != "Москва" {
		t.Fatalf("«*» замаскирован неверно: %v", starRows[0])
	}
}

// Отбор/группировка/агрегат по защищённому полю дают оракул перебора — маска на
// выходе там не защищает, запрос отклоняется целиком.
func TestQueryMaskPlan_DeniesFilterGroupAndAggregate(t *testing.T) {
	u := maskUser(auth.FieldPolicies{"Телефон": {Read: "mask_all"}})
	for _, src := range []string{
		`ВЫБРАТЬ Адрес ИЗ Справочник.Клиент ГДЕ Телефон = "+79161234455"`,
		`ВЫБРАТЬ Телефон, КОЛИЧЕСТВО(Возраст) КАК К ИЗ Справочник.Клиент СГРУППИРОВАТЬ ПО Телефон`,
		`ВЫБРАТЬ МАКСИМУМ(Телефон) КАК М ИЗ Справочник.Клиент`,
		`ВЫБРАТЬ Адрес ИЗ Справочник.Клиент УПОРЯДОЧИТЬ ПО Телефон`,
	} {
		if plan := access.QueryMaskPlanFor(u, compileForMask(t, src), lookupClient); plan.Denied == "" {
			t.Fatalf("%s: защищённое поле вне простой колонки должно отклонять запрос", src)
		}
	}
}

// Fail-closed: если запланированной колонки нет в результате (например SQLite
// назвал её иначе), строки отдавать нельзя — Apply обязан вернуть ошибку.
func TestQueryMaskPlan_ApplyFailsClosedOnMissingColumn(t *testing.T) {
	u := maskUser(auth.FieldPolicies{"Телефон": {Read: "mask_all"}})
	plan := access.QueryMaskPlanFor(u, compileForMask(t, `ВЫБРАТЬ Телефон ИЗ Справочник.Клиент`), lookupClient)
	rows := []map[string]any{{"phone": "+79161234455"}}
	if err := plan.Apply(rows); err == nil {
		t.Fatal("пропавшая колонка должна закрывать выдачу ошибкой")
	}
	if rows[0]["phone"] != "+79161234455" {
		t.Fatalf("строки не переписываются при ошибке: %v", rows[0])
	}
}

func TestQueryMaskPlan_HideBlanksValueKeepsColumn(t *testing.T) {
	u := maskUser(auth.FieldPolicies{"Паспорт": {Read: "hide"}})
	plan := access.QueryMaskPlanFor(u, compileForMask(t, `ВЫБРАТЬ Паспорт ИЗ Справочник.Клиент`), lookupClient)
	rows := []map[string]any{{"паспорт": "4509 123456"}}
	if err := plan.Apply(rows); err != nil {
		t.Fatal(err)
	}
	v, ok := rows[0]["паспорт"]
	if !ok {
		t.Fatal("колонка должна сохраниться, чтобы форма отчёта не зависела от роли")
	}
	if v != nil {
		t.Fatalf("hide обязан обнулить значение, получено %v", v)
	}
}

func TestQueryMaskPlan_NoPolicyNoAdminNoWork(t *testing.T) {
	res := compileForMask(t, `ВЫБРАТЬ Телефон ИЗ Справочник.Клиент`)
	if plan := access.QueryMaskPlanFor(nil, res, lookupClient); !plan.Empty() {
		t.Fatal("без пользователя маскировать нечего")
	}
	admin := &auth.User{IsAdmin: true, Roles: maskUser(auth.FieldPolicies{"Телефон": {Read: "mask_all"}}).Roles}
	if plan := access.QueryMaskPlanFor(admin, res, lookupClient); !plan.Empty() {
		t.Fatal("админ по умолчанию читает полное значение")
	}
	access.SetMaskAdmin(true)
	defer access.SetMaskAdmin(false)
	if plan := access.QueryMaskPlanFor(admin, res, lookupClient); plan.Empty() {
		t.Fatal("mask_admin подчиняет админа маске и в запросах")
	}
}

// Подзапрос/ОБЪЕДИНИТЬ поколоночно не разбираются — остаётся прежний отказ.
func TestQueryMaskPlan_ComplexQueryStaysDenied(t *testing.T) {
	u := maskUser(auth.FieldPolicies{"Телефон": {Read: "mask_all"}})
	src := `ВЫБРАТЬ Телефон ИЗ Справочник.Клиент ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Адрес ИЗ Справочник.Клиент`
	if plan := access.QueryMaskPlanFor(u, compileForMask(t, src), lookupClient); plan.Denied == "" {
		t.Fatal("ОБЪЕДИНИТЬ с защищённым полем должен отклоняться")
	}
}
