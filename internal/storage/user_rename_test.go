package storage_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/navigation"
	"github.com/ivantit66/onebase/internal/storage"
)

// Переименование учётки переносит каждое семейство личных настроек и личные
// варианты отчётов — через публичные сеттеры и геттеры, на обоих диалектах.
// Чужие настройки не трогаются, а остатки удалённой учётки с новым именем не
// достаются переименованному пользователю.
func TestRenameUserData_AllUserKeyFamilies(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		const old, renamed, other = "Оператор", "Оператор_075", "Оператор2"
		navScope := func(user string) storage.NavigationSettingsScope {
			return storage.NavigationSettingsScope{Layer: navigation.UserLayer, Login: user, Context: "Склад"}
		}
		base := navSettingsBase("Склад")
		mode := map[string]string{old: "tabs", other: "tabs", renamed: "pages"}
		view := map[string]string{old: "tiles", other: "tiles", renamed: "list"}
		seed := func(user, tag string) {
			t.Helper()
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			must(db.SaveUserFormOpenMode(ctx, user, mode[user]))
			must(db.SaveFormGridOrder(ctx, "processor/Поиск/Форма/ТЧ", user, []string{tag}))
			must(db.SaveListFilterFields(ctx, "Заявка", user, []string{tag}))
			must(db.SaveReportUserSettings(ctx, "Продажи", user, `{"t":"`+tag+`"}`))
			must(db.SaveJournalUserSettings(ctx, "Звонки", user, `{"t":"`+tag+`"}`))
			must(db.SaveListViewUserSettings(ctx, "Номенклатура", user, view[user]))
			_, err := db.SaveNavigationSettings(ctx, navScope(user), base, navSettingsDelta(t, base, "nav-"+tag), "")
			must(err)
			_, err = db.SaveReportPreset(ctx, storage.ReportPreset{Report: "Продажи", User: user, Name: "вариант-" + tag, SettingsJSON: "{}"})
			must(err)
		}
		seed(old, "мой")
		seed(other, "чужой")
		seed(renamed, "остаток") // учётка с этим логином когда-то была и удалена

		if err := db.WithTx(ctx, func(tx context.Context) error { return db.RenameUserData(tx, old, renamed) }); err != nil {
			t.Fatal(err)
		}

		check := func(user, tag, wantMode, wantView string) {
			t.Helper()
			if got := db.GetUserFormOpenMode(ctx, user); got != wantMode {
				t.Errorf("%s: режим форм %q, ждали %q", user, got, wantMode)
			}
			wantList := []string(nil)
			if tag != "" {
				wantList = []string{tag}
			}
			if got := db.GetFormGridOrder(ctx, "processor/Поиск/Форма/ТЧ", user); !reflect.DeepEqual(got, wantList) {
				t.Errorf("%s: порядок колонок %v, ждали %v", user, got, wantList)
			}
			if got := db.GetListFilterFields(ctx, "Заявка", user); !reflect.DeepEqual(got, wantList) {
				t.Errorf("%s: поля отбора %v, ждали %v", user, got, wantList)
			}
			wantRaw := ""
			if tag != "" {
				wantRaw = `{"t":"` + tag + `"}`
			}
			if got, _ := db.GetReportUserSettings(ctx, "Продажи", user); got != wantRaw {
				t.Errorf("%s: настройки отчёта %q, ждали %q", user, got, wantRaw)
			}
			if got, _ := db.GetJournalUserSettings(ctx, "Звонки", user); got != wantRaw {
				t.Errorf("%s: настройки журнала %q, ждали %q", user, got, wantRaw)
			}
			if got, _ := db.GetListViewUserSettings(ctx, "Номенклатура", user); got != wantView {
				t.Errorf("%s: вид списка %q, ждали %q", user, got, wantView)
			}
			nav, err := db.GetNavigationSettings(ctx, navScope(user))
			if err != nil {
				t.Fatal(err)
			}
			if (tag == "") == nav.Exists || (tag != "" && !strings.Contains(nav.Raw, "nav-"+tag)) {
				t.Errorf("%s: личное меню %+v, ждали метку %q", user, nav, tag)
			}
			presets, err := db.ListReportPresets(ctx, "Продажи", user)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, p := range presets {
				names = append(names, p.Name)
			}
			wantNames := []string(nil)
			if tag != "" {
				wantNames = []string{"вариант-" + tag}
			}
			if !reflect.DeepEqual(names, wantNames) {
				t.Errorf("%s: варианты отчёта %v, ждали %v", user, names, wantNames)
			}
		}
		check(renamed, "мой", "tabs", "tiles")
		check(old, "", "", "")
		check(other, "чужой", "tabs", "tiles")
	})
}
