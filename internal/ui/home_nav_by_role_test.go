package ui

// nav_by_role (config/home_page.yaml): левое меню «Главной» по ролям. Объект,
// который роль читает только ради ссылок и подбора (кладовщику — заявки из
// ордера), не должен попадать в её меню, а право чтения отнимать нельзя.

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

func homeNavByRoleServer(t *testing.T) *Server {
	t.Helper()
	doc := func(name string) *metadata.Entity {
		return &metadata.Entity{Name: name, Kind: metadata.KindDocument,
			Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}}}
	}
	s, _ := newSubmitTestServer(t, []*metadata.Entity{doc("Заявка"), doc("Звонок"), doc("Ордер")})
	s.reg.LoadHomePage(&metadata.HomePage{
		Nav: &metadata.SubsystemContents{Documents: []string{"Звонок", "Заявка"}},
		NavByRole: map[string]*metadata.SubsystemContents{
			"Кладовщик": {Documents: []string{"Ордер"}},
			"Гость":     {},
		},
	})
	return s
}

func navUser(login string, roles ...string) *auth.User {
	u := &auth.User{Login: login}
	for _, name := range roles {
		u.Roles = append(u.Roles, &auth.Role{Name: name, Permissions: auth.Permission{
			Documents: map[string][]string{"Заявка": {"read"}, "Звонок": {"read"}, "Ордер": {"read"}},
		}})
	}
	return u
}

func navLabels(groups []navGroup) string {
	var out []string
	for _, g := range groups {
		for _, it := range g.Items {
			out = append(out, it.Label)
		}
	}
	return strings.Join(out, ",")
}

func TestHomeNav_ByRole(t *testing.T) {
	s := homeNavByRoleServer(t)
	cases := []struct {
		name string
		user *auth.User
		want string
	}{
		// Заявку кладовщик читает (ордер ссылается на неё), но в меню — только ордер.
		// Порядок — как объявлено в nav.
		{"роль из nav_by_role", navUser("кладовщик", "Кладовщик"), "Ордер"},
		{"роль не указана — общий nav", navUser("оператор", "ОператорКЦ"), "Звонок,Заявка"},
		{"пустой состав роли — пустое меню", navUser("гость", "Гость"), ""},
		{"несколько ролей — объединение", navUser("двое", "Кладовщик", "Гость"), "Ордер"},
		{"имя роли без учёта регистра", navUser("к", "кладовщик"), "Ордер"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := navLabels(s.buildNav(reqWithUser("/ui/", tc.user), ""))
			if got != tc.want {
				t.Errorf("меню = %q, ждали %q", got, tc.want)
			}
		})
	}
	admin := &auth.User{Login: "админ", IsAdmin: true, Roles: []*auth.Role{{Name: "Кладовщик"}}}
	if got := navLabels(s.buildNav(reqWithUser("/ui/", admin), "")); got != "Звонок,Заявка" {
		t.Errorf("администратор видит общий nav, получено %q", got)
	}
}
