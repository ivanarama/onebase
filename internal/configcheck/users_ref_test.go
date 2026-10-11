package configcheck

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/project"
)

func usersRefProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "сотрудники.yaml"), `name: Сотрудники
fields:
  - name: Наименование
    type: string
  - name: УчётнаяЗапись
    type: reference:_users
`)
	return dir
}

// RunFull is the entry used by onebase check and the configurator. Both query
// paths must PREPARE against the real, empty auth schema, including user JOINs.
func TestRunFullUsersReferenceQueries(t *testing.T) {
	queries := []struct {
		name string
		text string
	}{
		{"presentation", "ВЫБРАТЬ УчётнаяЗапись КАК Значение ИЗ Справочник.Сотрудники"},
		{"login", "ВЫБРАТЬ УчётнаяЗапись.login КАК Значение ИЗ Справочник.Сотрудники"},
		{"full_name", "ВЫБРАТЬ УчётнаяЗапись.ПолноеИмя КАК Значение ИЗ Справочник.Сотрудники"},
		{"source_alias", "ВЫБРАТЬ С.УчётнаяЗапись.login КАК Значение ИЗ Справочник.Сотрудники КАК С"},
		{"control", "ВЫБРАТЬ Наименование КАК Значение ИЗ Справочник.Сотрудники"},
	}
	for _, source := range []string{"module", "report"} {
		for _, query := range queries {
			t.Run(source+"/"+query.name, func(t *testing.T) {
				dir := usersRefProject(t)
				writeUsersRefQuery(t, dir, source, query.text)
				res := RunFull(dir)
				if !res.OK {
					t.Fatalf("valid user reference query rejected: %+v", res.Issues)
				}
			})
		}
	}
}

func TestRunFullUsersReferenceUnknownField(t *testing.T) {
	for _, source := range []string{"module", "report"} {
		t.Run(source, func(t *testing.T) {
			dir := usersRefProject(t)
			writeUsersRefQuery(t, dir, source, "ВЫБРАТЬ УчётнаяЗапись.НетТакогоПоля КАК Значение ИЗ Справочник.Сотрудники")
			res := RunFull(dir)
			if res.OK {
				t.Fatal("unknown user field was accepted")
			}
			for _, issue := range res.Issues {
				if strings.Contains(strings.ToLower(issue.Message), "неттакогополя") {
					return
				}
			}
			t.Fatalf("unknown field diagnostic missing: %+v", res.Issues)
		})
	}
}

func writeUsersRefQuery(t *testing.T, dir, source, query string) {
	t.Helper()
	if source == "module" {
		mkFile(t, filepath.Join(dir, "src", "учётки.module.os"), fmt.Sprintf(`Функция Учётка() Экспорт
    Запрос = Новый Запрос;
    Запрос.Текст = "%s";
    Возврат Запрос.Выполнить();
КонецФункции
`, query))
	} else {
		mkFile(t, filepath.Join(dir, "reports", "учётки.yaml"), "name: Учётки\nquery: |\n  "+query+"\n")
	}
}

func TestBuildSchemaDBUsersRemainEmpty(t *testing.T) {
	proj, err := project.Load(usersRefProject(t))
	if err != nil {
		t.Fatal(err)
	}
	defer proj.Close()
	db, closeDB, err := BuildSchemaDB(proj)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	hasUsers, err := auth.NewRepo(db).HasUsers(context.Background())
	if err != nil {
		t.Fatalf("auth schema missing: %v", err)
	}
	if hasUsers {
		t.Fatal("configuration validation must not create user accounts")
	}
}
