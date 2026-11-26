package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Складской отчёт с подзапросом и соединением справочника филиалов, у которого
// есть защищённое поле (пароль почты), строится под ролью без доступа к этому
// полю, пока поле в запросе не упомянуто. Умножение «Количество * Цена» — не
// выборка всех полей: раньше оно давало отказ «защищённое поле: *» (регресс
// выпуска 26 cc2).
func TestReportMask_SubqueryWithUnmentionedMaskedField(t *testing.T) {
	branch := &metadata.Entity{Name: "ФилиалОтчёта", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "ПарольПочты", Type: metadata.FieldTypeString},
	}}
	stock := &metadata.Entity{Name: "ОстатокОтчёта", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "Филиал", Type: "reference:ФилиалОтчёта", RefEntity: "ФилиалОтчёта"},
		{Name: "Количество", Type: metadata.FieldTypeNumber},
		{Name: "Цена", Type: metadata.FieldTypeNumber},
	}}
	const sub = `(ВЫБРАТЬ О.Филиал КАК Филиал, СУММА(О.Количество * О.Цена) КАК Сумма
		ИЗ Справочник.ОстатокОтчёта КАК О СГРУППИРОВАТЬ ПО О.Филиал) КАК Т
		ЛЕВОЕ СОЕДИНЕНИЕ Справочник.ФилиалОтчёта КАК Ф ПО Т.Филиал = Ф.Ссылка`
	reports := map[string]string{
		"Склад":           `ВЫБРАТЬ Ф.Наименование КАК Филиал, Т.Сумма КАК Сумма ИЗ ` + sub,
		"СклВыборкаПарол": `ВЫБРАТЬ Ф.Наименование КАК Филиал, Т.Пароль КАК Пароль ИЗ (ВЫБРАТЬ Ф2.ПарольПочты КАК Пароль, Ф2.Ссылка КАК Филиал ИЗ Справочник.ФилиалОтчёта КАК Ф2) КАК Т ЛЕВОЕ СОЕДИНЕНИЕ Справочник.ФилиалОтчёта КАК Ф ПО Т.Филиал = Ф.Ссылка`,
		"СклОтборПарол":   `ВЫБРАТЬ Т.Наименование КАК Филиал ИЗ (ВЫБРАТЬ Ф2.Наименование КАК Наименование ИЗ Справочник.ФилиалОтчёта КАК Ф2 ГДЕ Ф2.ПарольПочты <> "") КАК Т`,
		"СклЗвёздочка":    `ВЫБРАТЬ Т.Наименование КАК Филиал ИЗ (ВЫБРАТЬ * ИЗ Справочник.ФилиалОтчёта) КАК Т`,
	}
	var reps []*reportpkg.Report
	for name, q := range reports {
		reps = append(reps, &reportpkg.Report{Name: name, Query: q})
	}
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{branch, stock})
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{branch, stock}, Reports: reps})
	srv.reg = registry
	srv.interp = interpreter.New()
	srv.ops = newOperationLimiter()
	branchID := uuid.New()
	if err := srv.store.Upsert(ctx, branch.Name, branchID, map[string]any{"Наименование": "Север", "ПарольПочты": "секрет-123"}, branch); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.Upsert(ctx, stock.Name, uuid.New(), map[string]any{
		"Наименование": "Деталь", "Филиал": branchID.String(), "Количество": 2, "Цена": 50,
	}, stock); err != nil {
		t.Fatal(err)
	}
	reportRun := map[string][]string{}
	for name := range reports {
		reportRun[name] = []string{"run"}
	}
	user := &auth.User{ID: "kl", Login: "kladovshik", Roles: []*auth.Role{{
		Name: "Кладовщик",
		Permissions: auth.Permission{
			Catalogs: map[string][]string{branch.Name: {"read"}, stock.Name: {"read"}},
			FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{
				branch.Name: {"ПарольПочты": {Read: "hide"}},
			}},
			Reports: reportRun,
		},
	}}}
	run := func(name string) string {
		t.Helper()
		r := reqWithChi(http.MethodPost, "/ui/report/"+name, url.Values{}, map[string]string{"name": name})
		r = r.WithContext(auth.ContextWithUser(r.Context(), user))
		w := httptest.NewRecorder()
		srv.reportRun(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: код %d: %s", name, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	const deny = "защищённое поле"
	if body := run("Склад"); strings.Contains(body, deny) || !strings.Contains(body, "Север") || strings.Contains(body, "секрет-123") {
		t.Fatalf("отчёт без упоминания защищённого поля должен строиться: %s", body)
	}
	for _, name := range []string{"СклВыборкаПарол", "СклОтборПарол", "СклЗвёздочка"} {
		if body := run(name); !strings.Contains(body, deny) || strings.Contains(body, "секрет-123") {
			t.Fatalf("%s: ожидался отказ по защищённому полю: %s", name, body)
		}
	}
}
