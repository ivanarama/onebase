package access

import (
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
)

func discloseTestEntity() *metadata.Entity {
	return &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Телефон", Type: metadata.FieldTypeString, PII: true},
		},
	}
}

func discloseUser(ops ...string) *auth.User {
	return &auth.User{ID: "u1", Login: "оператор", Roles: []*auth.Role{{
		Name:        "Оператор",
		Permissions: auth.Permission{Documents: map[string][]string{"Заявка": ops}},
	}}}
}

// Отбор по защищённому полю запрещён тому, кто не может увидеть значение.
func TestQueryMaskPlan_FilterDeniedWithoutDisclose(t *testing.T) {
	ent := discloseTestEntity()
	res, err := query.Compile(`ВЫБРАТЬ Номер ИЗ Документ.Заявка ГДЕ Телефон = &Т`,
		query.CompileOpts{Entities: []*metadata.Entity{ent}})
	if err != nil {
		t.Fatal(err)
	}
	plan := QueryMaskPlanFor(discloseUser("read"), res, func(kind, name string) *metadata.Entity { return ent })
	if plan.Denied == "" {
		t.Fatal("отбор по ПДн без права disclose должен запрещаться")
	}
}

// А тому, кто вправе раскрыть значение кнопкой, отбор по нему разрешён:
// подбор не даёт ему ничего нового, а без отбора он не найдёт заявку по
// телефону, который клиент только что назвал.
func TestQueryMaskPlan_FilterAllowedWithDisclose(t *testing.T) {
	ent := discloseTestEntity()
	res, err := query.Compile(`ВЫБРАТЬ Номер ИЗ Документ.Заявка ГДЕ Телефон = &Т`,
		query.CompileOpts{Entities: []*metadata.Entity{ent}})
	if err != nil {
		t.Fatal(err)
	}
	plan := QueryMaskPlanFor(discloseUser("read", "disclose"), res, func(kind, name string) *metadata.Entity { return ent })
	if plan.Denied != "" {
		t.Fatalf("отбор запрещён при наличии disclose: %s", plan.Denied)
	}
}

// Право разрешает ИСКАТЬ, но не ЧИТАТЬ: вывод самого поля остаётся под маской.
func TestQueryMaskPlan_DiscloseStillMasksOutput(t *testing.T) {
	ent := discloseTestEntity()
	res, err := query.Compile(`ВЫБРАТЬ Номер, Телефон ИЗ Документ.Заявка`,
		query.CompileOpts{Entities: []*metadata.Entity{ent}})
	if err != nil {
		t.Fatal(err)
	}
	plan := QueryMaskPlanFor(discloseUser("read", "disclose"), res, func(kind, name string) *metadata.Entity { return ent })
	rows := []map[string]any{{"номер": "Пл1", "телефон": "+79990000000"}}
	if _, err := plan.ApplyTracked(rows); err != nil {
		t.Fatal(err)
	}
	if rows[0]["телефон"] == "+79990000000" {
		t.Fatal("значение ПДн отдано столбцом без маски")
	}
}

// The compiler currently reports unqualified field names. A grant on one
// source must never authorize a protected field of another joined source.
func TestQueryMaskPlan_DiscloseDoesNotCrossSources(t *testing.T) {
	request := discloseTestEntity()
	client := &metadata.Entity{Name: "Клиент", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Телефон", Type: metadata.FieldTypeString, PII: true}}}
	lookup := func(_, name string) *metadata.Entity {
		if name == client.Name {
			return client
		}
		return request
	}
	for _, from := range []string{
		"Документ.Заявка КАК З ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Клиент КАК К ПО ИСТИНА",
		"Справочник.Клиент КАК К ЛЕВОЕ СОЕДИНЕНИЕ Документ.Заявка КАК З ПО ИСТИНА",
	} {
		for _, suffix := range []string{`ГДЕ З.Телефон = &Т`, `УПОРЯДОЧИТЬ ПО З.Телефон`, `УПОРЯДОЧИТЬ ПО Контакт`, `УПОРЯДОЧИТЬ ПО 2`} {
			res, err := query.Compile(`ВЫБРАТЬ З.Номер, З.Телефон КАК Контакт ИЗ `+from+" "+suffix, query.CompileOpts{Entities: []*metadata.Entity{request, client}})
			if err != nil {
				t.Fatal(err)
			}
			u := discloseUser("read")
			u.Roles[0].Permissions.Catalogs = map[string][]string{client.Name: {"read", "disclose"}}
			if plan := QueryMaskPlanFor(u, res, lookup); plan.Denied == "" {
				t.Errorf("disclose crossed sources: %s %s", from, suffix)
			}
			u.Roles[0].Permissions.Documents[request.Name] = []string{"read", "disclose"}
			if plan := QueryMaskPlanFor(u, res, lookup); plan.Denied != "" {
				t.Errorf("both sources disclosable: %s", plan.Denied)
			}
		}
	}
}
