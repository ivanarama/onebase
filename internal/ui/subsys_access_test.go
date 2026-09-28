package ui

// Видимость разделов по правам и ролям (subsys_access.go): панель показывает
// раздел, только если пользователю доступен хотя бы один объект contents и
// пройден whitelist roles; скрытый раздел не открывается прямой ссылкой.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/widget"
)

func subsysAccessFixture() []*metadata.Subsystem {
	return []*metadata.Subsystem{
		{Name: "Склад", Contents: metadata.SubsystemContents{
			Documents: []string{"ПоступлениеТоваров"},
			Registers: []string{"ОстаткиТоваров"},
		}},
		{Name: "Продажи", Contents: metadata.SubsystemContents{
			Documents: []string{"РеализацияТоваров"},
			Reports:   []string{"ВаловаяПрибыль"},
		}},
	}
}

func storekeeperUser() *auth.User {
	return &auth.User{Login: "кладовщик", Roles: []*auth.Role{{
		Name: "Кладовщик",
		Permissions: auth.Permission{
			Documents:  map[string][]string{"ПоступлениеТоваров": {"read", "write"}},
			Registers:  map[string][]string{"ОстаткиТоваров": {"read"}},
			Processors: map[string][]string{},
		},
	}}}
}

func reqWithUser(target string, u *auth.User) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if u != nil {
		req = req.WithContext(auth.ContextWithUser(req.Context(), u))
	}
	return req
}

func subsysNames(subs []*metadata.Subsystem) []string {
	var names []string
	for _, s := range subs {
		names = append(names, s.Name)
	}
	return names
}

func TestVisibleSubsystems_FiltersByPermissions(t *testing.T) {
	s := newServerForFormMode(t)
	s.reg.LoadSubsystems(subsysAccessFixture())

	// Кладовщик: прав на объекты «Продаж» нет → раздел скрыт.
	got := s.visibleSubsystems(reqWithUser("/ui/", storekeeperUser()))
	if names := subsysNames(got); len(names) != 1 || names[0] != "Склад" {
		t.Fatalf("кладовщик должен видеть только Склад, получено %v", names)
	}

	// Открытый деплой (нет пользователя) и админ видят всё.
	if got := s.visibleSubsystems(reqWithUser("/ui/", nil)); len(got) != 2 {
		t.Fatalf("без пользователя должны быть видны все разделы, получено %v", subsysNames(got))
	}
	admin := &auth.User{Login: "root", IsAdmin: true}
	if got := s.visibleSubsystems(reqWithUser("/ui/", admin)); len(got) != 2 {
		t.Fatalf("админ должен видеть все разделы, получено %v", subsysNames(got))
	}
}

func TestVisibleSubsystems_RolesWhitelist(t *testing.T) {
	s := newServerForFormMode(t)
	subs := subsysAccessFixture()
	// «Склад» доступен кладовщику по правам, но whitelist пускает только менеджера.
	subs[0].Roles = []string{"Менеджер"}
	s.reg.LoadSubsystems(subs)

	if got := s.visibleSubsystems(reqWithUser("/ui/", storekeeperUser())); len(got) != 0 {
		t.Fatalf("whitelist roles должен скрыть раздел, получено %v", subsysNames(got))
	}

	// Роль из whitelist + право хотя бы на один объект → раздел виден.
	manager := &auth.User{Login: "менеджер", Roles: []*auth.Role{{
		Name: "Менеджер",
		Permissions: auth.Permission{
			Documents: map[string][]string{"ПоступлениеТоваров": {"read"}},
		},
	}}}
	got := s.visibleSubsystems(reqWithUser("/ui/", manager))
	if names := subsysNames(got); len(names) != 1 || names[0] != "Склад" {
		t.Fatalf("менеджер должен видеть Склад по whitelist, получено %v", names)
	}

	// Роль подходит, но прав ни на один объект contents нет → пустой раздел скрыт.
	bare := &auth.User{Login: "пустой", Roles: []*auth.Role{{Name: "Менеджер"}}}
	if got := s.visibleSubsystems(reqWithUser("/ui/", bare)); len(got) != 0 {
		t.Fatalf("без прав на объекты раздел должен быть скрыт, получено %v", subsysNames(got))
	}
}

func TestVisibleSubsystems_DashboardOnlySection(t *testing.T) {
	s := newServerForFormMode(t)
	// Раздел без contents (только рабочий стол) — гейтить нечем, виден всем.
	s.reg.LoadSubsystems([]*metadata.Subsystem{{Name: "Мониторинг"}})
	if got := s.visibleSubsystems(reqWithUser("/ui/", storekeeperUser())); len(got) != 1 {
		t.Fatalf("раздел без contents должен быть виден, получено %v", subsysNames(got))
	}
}

func TestVisibleSubsystems_JournalByDocuments(t *testing.T) {
	s := newServerForFormMode(t)
	s.reg.LoadJournals([]*metadata.Journal{{Name: "Складские", Documents: []string{"ПоступлениеТоваров", "СписаниеТоваров"}}})
	s.reg.LoadSubsystems([]*metadata.Subsystem{{Name: "Журналы", Contents: metadata.SubsystemContents{
		Journals: []string{"Складские"},
	}}})

	// Кладовщик читает ПоступлениеТоваров → журнал (и раздел) доступен.
	if got := s.visibleSubsystems(reqWithUser("/ui/", storekeeperUser())); len(got) != 1 {
		t.Fatalf("раздел с журналом по читаемому документу должен быть виден, получено %v", subsysNames(got))
	}
	stranger := &auth.User{Login: "чужой", Roles: []*auth.Role{{
		Name:        "Чужой",
		Permissions: auth.Permission{Processors: map[string][]string{}},
	}}}
	if got := s.visibleSubsystems(reqWithUser("/ui/", stranger)); len(got) != 0 {
		t.Fatalf("журнал без читаемых документов не должен показывать раздел, получено %v", subsysNames(got))
	}
}

func TestIndex_HiddenSubsystem_Forbidden(t *testing.T) {
	s := newServerForFormMode(t)
	s.reg.LoadSubsystems(subsysAccessFixture())

	rec := httptest.NewRecorder()
	s.index(rec, reqWithUser("/ui/?subsystem=Продажи", storekeeperUser()))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("прямой заход в скрытый раздел должен давать 403, получено %d", rec.Code)
	}

	// Свой раздел открывается.
	rec = httptest.NewRecorder()
	s.index(rec, reqWithUser("/ui/?subsystem=Склад", storekeeperUser()))
	if rec.Code != http.StatusOK {
		t.Fatalf("доступный раздел должен открываться, получено %d", rec.Code)
	}
}

func TestAppShell_HiddenSubsystem_Forbidden(t *testing.T) {
	s := newServerForFormMode(t)
	s.reg.LoadSubsystems(subsysAccessFixture())

	rec := httptest.NewRecorder()
	s.appShell(rec, reqWithUser("/ui/app?subsystem=Продажи", storekeeperUser()))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("оболочка вкладок тоже должна гейтить скрытый раздел, получено %d", rec.Code)
	}
}

func TestIndex_SubsystemUsesSameLocalizedObjectTitlesAsGlobalHome(t *testing.T) {
	s := newServerForFormMode(t)
	s.cfg.Bundle = mustBundle(t, `"Save"`)
	infoReg := &metadata.InfoRegister{
		Name: "StockState", Title: "Состояние запасов",
		Titles: map[string]string{"en": "Stock state"},
	}
	rep := &report.Report{
		Name: "SalesReport", Title: "Отчёт о продажах",
		Titles: map[string]string{"en": "Sales report"},
	}
	proc := &processor.Processor{
		Name: "Reconcile", Title: "Сверка остатков",
		Titles: map[string]string{"en": "Reconcile stock"},
	}
	s.reg.Load(runtime.LoadOptions{
		InfoRegs: []*metadata.InfoRegister{infoReg},
		Reports:  []*report.Report{rep},
	})
	s.reg.LoadProcessors([]*processor.Processor{proc})
	s.reg.LoadSubsystems([]*metadata.Subsystem{{
		Name: "Operations",
		Contents: metadata.SubsystemContents{
			InfoRegs:   []string{infoReg.Name},
			Reports:    []string{rep.Name},
			Processors: []string{proc.Name},
		},
	}})

	tests := []struct {
		name string
		lang string
		want []string
	}{
		{name: "base titles", lang: "ru", want: []string{"Состояние запасов", "Отчёт о продажах", "Сверка остатков"}},
		{name: "localized titles", lang: "en", want: []string{"Stock state", "Sales report", "Reconcile stock"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s.cfg.Lang = tc.lang
			for _, target := range []string{"/ui/", "/ui/?subsystem=Operations"} {
				rec := httptest.NewRecorder()
				s.index(rec, httptest.NewRequest(http.MethodGet, target, nil))
				if rec.Code != http.StatusOK {
					t.Fatalf("GET %s: status = %d, want 200", target, rec.Code)
				}
				body := rec.Body.String()
				for _, label := range tc.want {
					if !strings.Contains(body, `title="`+label+`"`) {
						t.Errorf("GET %s: menu does not contain title %q", target, label)
					}
				}
			}
		})
	}
}

// Дашборд не рендерит виджеты, чей источник пользователю недоступен: карточка
// пропадает целиком (вместе с опустевшим рядом), а не показывает «нет доступа».
func TestHomeDashboard_SkipsAccessDeniedWidgets(t *testing.T) {
	s := newServerForFormMode(t)
	ent := &metadata.Entity{Name: "Товар", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
	s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
	s.reg.LoadWidgets([]*metadata.Widget{
		{Name: "Секретный", Type: metadata.WidgetTypeKPI, Query: "ВЫБРАТЬ Наименование ИЗ Справочник.Товар"},
		{Name: "Битый", Type: metadata.WidgetTypeKPI, Query: "ВЫБРАТЬ Наименование ИЗ"},
	})
	// Первый ряд опустеет целиком, во втором останется настоящая ошибка.
	s.reg.LoadHomePage(&metadata.HomePage{Layout: "rows", Rows: []metadata.HomePageRow{
		{Widgets: []string{"Секретный"}},
		{Widgets: []string{"Битый"}},
	}})

	noRights := &auth.User{Login: "гость", Roles: []*auth.Role{{
		Name:        "Гость",
		Permissions: auth.Permission{Processors: map[string][]string{}},
	}}}
	data := s.homeDashboardData(reqWithUser("/ui/", noRights))
	rows := data["WidgetRows"].([][]widget.Result)
	if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0].Name != "Битый" {
		t.Fatalf("ожидался один ряд с одним виджетом «Битый», получено %+v", rows)
	}
	if rows[0][0].Error == "" || rows[0][0].AccessDenied {
		t.Fatalf("настоящая ошибка должна остаться видимой, получено %+v", rows[0][0])
	}
	flat := data["WidgetResults"].([]widget.Result)
	if len(flat) != 1 {
		t.Fatalf("denied-виджет не должен попадать в WidgetResults, получено %+v", flat)
	}

	// Админ видит обе карточки (у «Секретного» — SQL/данные, не отказ).
	admin := &auth.User{Login: "root", IsAdmin: true}
	data = s.homeDashboardData(reqWithUser("/ui/", admin))
	if rows := data["WidgetRows"].([][]widget.Result); len(rows) != 2 {
		t.Fatalf("админ должен видеть оба ряда, получено %+v", rows)
	}
}

func TestHiddenHomeRedirect_TargetsFirstVisible(t *testing.T) {
	s := newServerForFormMode(t)
	s.reg.LoadSubsystems(subsysAccessFixture())
	s.reg.LoadHomePage(&metadata.HomePage{Hidden: true})

	// Первый по порядку раздел («Склад») кладовщику доступен — редирект в него.
	rec := httptest.NewRecorder()
	s.index(rec, reqWithUser("/ui/", storekeeperUser()))
	want := "/ui/?" + url.Values{"subsystem": {"Склад"}}.Encode()
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != want {
		t.Fatalf("ожидался редирект в Склад (%s), получено %d → %q", want, rec.Code, rec.Header().Get("Location"))
	}

	// Пользователь без прав вообще: видимых разделов нет → фейл-сейф, без редиректа.
	bare := &auth.User{Login: "никто", Roles: []*auth.Role{{
		Name:        "Никто",
		Permissions: auth.Permission{Processors: map[string][]string{}},
	}}}
	rec = httptest.NewRecorder()
	s.index(rec, reqWithUser("/ui/", bare))
	if rec.Code == http.StatusSeeOther {
		t.Fatalf("без видимых разделов редиректа быть не должно, получено %d → %q", rec.Code, rec.Header().Get("Location"))
	}
}
