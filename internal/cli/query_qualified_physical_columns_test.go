package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestQueryQualifiedPhysicalColumnsCLIMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		dir := t.TempDir()
		for name, text := range map[string]string{
			"config/app.yaml":          "name: qualified-source-physical-columns\n",
			"catalogs/Сотрудники.yaml": "name: Сотрудники\nfields:\n  - name: Наименование\n    type: string\n",
			"catalogs/Мастера.yaml":    "name: Мастера\nfields:\n  - name: Наименование\n    type: string\n  - name: МенеджерКонсультант\n    type: bool\n",
			"documents/Заявка.yaml":    "name: Заявка\nfields:\n  - name: Номер\n    type: string\n  - name: Мастер\n    type: reference:Мастера\n  - name: МенеджерКонсультант\n    type: reference:Сотрудники\n",
		} {
			writeProcrunFixture(t, dir, name, text)
		}
		cmd := userReferenceCLICommand(t, db, dir, false)
		if _, err := captureStdout(t, func() error { return migrateCmd.RunE(cmd, nil) }); err != nil {
			t.Fatal(err)
		}
		proj, err := project.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(proj.Close)
		entities := map[string]*metadata.Entity{}
		for _, entity := range proj.Entities {
			entities[entity.Name] = entity
		}
		employee, yes, no := uuid.New(), uuid.New(), uuid.New()
		for _, row := range []struct {
			name   string
			id     uuid.UUID
			values map[string]any
		}{
			{"Сотрудники", employee, map[string]any{"Наименование": "Employee"}},
			{"Мастера", yes, map[string]any{"Наименование": "yes master", "МенеджерКонсультант": true}},
			{"Мастера", no, map[string]any{"Наименование": "no master", "МенеджерКонсультант": false}},
			{"Заявка", uuid.New(), map[string]any{"Номер": "yes", "Мастер": yes, "МенеджерКонсультант": employee}},
			{"Заявка", uuid.New(), map[string]any{"Номер": "no", "Мастер": no, "МенеджерКонсультант": employee}},
			{"Заявка", uuid.New(), map[string]any{"Номер": "missing", "Мастер": nil, "МенеджерКонсультант": employee}},
		} {
			if err := db.Upsert(context.Background(), row.name, row.id, row.values, entities[row.name]); err != nil {
				t.Fatal(err)
			}
		}
		args := []string{"query", "--project", dir, "--json", "--sql", "--limit", "0"}
		for _, flag := range []string{"sqlite", "db"} {
			value, err := cmd.Flags().GetString(flag)
			if err != nil {
				t.Fatal(err)
			}
			if value != "" {
				args = append(args, "--"+flag, value)
			}
		}
		const base = `ВЫБРАТЬ З.Номер КАК Номер, М.МенеджерКонсультант КАК Флаг ИЗ Документ.Заявка КАК З ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Мастера КАК М ПО М.Ссылка = З.Мастер`
		for _, tc := range []struct {
			name, text string
			want       []string
		}{
			{"projection", base, []string{"yes|true", "no|false", "missing|<NULL>"}},
			{"where_true", base + ` ГДЕ М.МенеджерКонсультант = ИСТИНА`, []string{"yes|true"}},
			{"where_false", base + ` ГДЕ М.МенеджерКонсультант = ЛОЖЬ`, []string{"no|false"}},
			{"on_true", base + ` И М.МенеджерКонсультант = ИСТИНА`, []string{"yes|true", "no|<NULL>", "missing|<NULL>"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				out, err := executeQueryCommand(t, append(append([]string{}, args...), tc.text)...)
				if err != nil {
					t.Fatalf("public query CLI: %v\n%s", err, out)
				}
				var got queryOutput
				if err := json.Unmarshal([]byte(out), &got); err != nil {
					t.Fatalf("JSON: %v\n%s", err, out)
				}
				if !reflect.DeepEqual(got.Columns, []string{"номер", "флаг"}) {
					t.Fatalf("columns=%v", got.Columns)
				}
				values := []string{}
				for _, row := range got.Rows {
					flag := "<NULL>"
					if row["флаг"] != nil {
						value, ok := row["флаг"].(bool)
						if !ok {
							t.Fatalf("JSON bool has type %T", row["флаг"])
						}
						flag = fmt.Sprint(value)
					}
					values = append(values, fmt.Sprint(row["номер"])+"|"+flag)
				}
				want := append([]string{}, tc.want...)
				sort.Strings(values)
				sort.Strings(want)
				if !reflect.DeepEqual(values, want) {
					t.Fatalf("rows=%v want=%v\n%s", values, want, out)
				}
			})
		}
	})
}
