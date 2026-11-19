package ui

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestReportWorkbookFromProcessor(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"catalogs/entry.yaml": `name: Запись
fields:
  - {name: Наименование, type: string}
  - {name: Группа, type: string}
  - {name: День, type: date}
  - {name: Сумма, type: number}
`,
		"reports/month.yaml": `name: Итоги
params:
  - {name: Начало, type: date}
query: |
  ВЫБРАТЬ Группа КАК Группа, Сумма КАК Сумма
  ИЗ Справочник.Запись ГДЕ День >= &Начало
variants:
  - name: По группам
    composition:
      groupings: [Группа]
      measures:
        - {field: Сумма, agg: sum, title: Итого}
      totals: {grand: true}
`,
		"processors/seed.yaml": "name: seed\n",
		"src/seed.proc.os": `Процедура Выполнить()
  Факт(Дата(2026, 5, 10), 3);
  Факт(Дата(2026, 5, 11), 4);
  Факт(Дата(2026, 4, 30), 100);
КонецПроцедуры

Процедура Факт(День, Сумма)
  С = Справочники.Запись.Создать();
  С.Наименование = "Строка";
  С.Группа = "Север";
  С.День = День;
  С.Сумма = Сумма;
  С.Записать();
КонецПроцедуры
`,
		"processors/export.yaml": "name: export\nparams:\n  - {name: Вариант, type: string}\n",
		"src/export.proc.os": `Процедура Выполнить(Вариант)
  Сообщить(СформироватьОтчетXLSX("Итоги", Вариант, Новый Структура("начало", Дата(2026, 5, 1))));
КонецПроцедуры
`,
	}
	for filename, body := range files {
		path := filepath.Join(dir, filename)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	proj, err := project.Load(dir)
	require.NoError(t, err)
	t.Cleanup(func() { proj.Close() })

	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := t.Context()
		require.NoError(t, db.Migrate(ctx, proj.Entities))
		server, reg, err := NewOfflineServer(proj, db)
		require.NoError(t, err)
		_, runErr, err := server.RunProcessor(ctx, reg, "seed", nil, nil, nil)
		require.NoError(t, err)
		require.NoError(t, runErr)

		user := &auth.User{Login: "worker", Roles: []*auth.Role{{
			Permissions: auth.Permission{
				Catalogs: map[string][]string{"Запись": {"read"}},
				Reports:  map[string][]string{"Итоги": {"run"}},
			},
		}}}
		userCtx := auth.ContextWithUser(ctx, user)
		_, err = db.SaveReportPreset(ctx, storage.ReportPreset{
			Report: "Итоги", User: user.Login, Name: "Личный", IsDefault: true,
			SettingsJSON: `{"filters":[{"field":"Группа","op":"eq","value":"Исключить"}]}`,
		})
		require.NoError(t, err)

		params := map[string]string{"Вариант": "По группам"}
		messages, runErr, err := server.RunProcessor(userCtx, reg, "export", params, nil, nil)
		require.NoError(t, err)
		require.NoError(t, runErr)
		require.Len(t, messages, 1)
		data, err := base64.StdEncoding.DecodeString(messages[0])
		require.NoError(t, err)
		book, err := excelize.OpenReader(bytes.NewReader(data))
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, book.Close()) })
		rows, err := book.GetRows(book.GetSheetName(0))
		require.NoError(t, err)
		require.Contains(t, rows, []string{"Север", "7"})
		require.Contains(t, rows[0], "Итого")

		_, runErr, err = server.RunProcessor(userCtx, reg, "export", map[string]string{"Вариант": "Опечатка"}, nil, nil)
		require.NoError(t, err)
		require.ErrorContains(t, runErr, "вариант")
		_, runErr, err = server.RunProcessor(auth.ContextWithUser(ctx, &auth.User{Login: "denied"}), reg, "export", params, nil, nil)
		require.NoError(t, err)
		require.ErrorContains(t, runErr, "отчёт недоступен")
	})
}
