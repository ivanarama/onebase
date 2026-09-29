package configcheck

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

const accountingRegYAML = `name: Бух
accounts: Основной
resources:
  - {name: Сумма, type: number}
`

// План счетов из DEVELOPER.md с вложенными `children:` раньше загружался без
// субсчетов: у metadata.Account не было такого поля, yaml.v3 молча отбрасывал
// ключ, а onebase check отвечал «ошибок не найдено». Проверяем весь путь
// пользователя: onebase check → project.Load → синхронизация плана в _accounts.
func TestRunFull_ChartChildrenBecomeSubaccounts(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "accounts", "основной.yaml"), `name: Основной
title: Основной план счетов
accounts:
  - code: "10"
    name: Материалы
    kind: active
  - code: "90"
    name: Продажи
    kind: active_passive
    children:
      - code: "90.1"
        name: Выручка
        kind: passive
      - code: "90.2"
        name: Себестоимость
        kind: active
        children:
          - code: "90.2.1"
            name: Себестоимость товаров
`)
	mkFile(t, filepath.Join(dir, "accountregs", "бух.yaml"), accountingRegYAML)

	if result := RunFull(dir); !result.OK {
		t.Fatalf("план счетов из DEVELOPER.md не прошёл onebase check: %+v", result.Issues)
	}
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer proj.Close()

	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "acc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.EnsureAccountsTable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.SyncAccounts(ctx, proj.ChartsOfAccounts); err != nil {
		t.Fatal(err)
	}
	accounts, err := db.GetAccounts(ctx, "Основной")
	if err != nil {
		t.Fatal(err)
	}
	parents := map[string]string{}
	kinds := map[string]string{}
	for _, a := range accounts {
		code, _ := a["code"].(string)
		parent, _ := a["parent"].(string)
		kind, _ := a["kind"].(string)
		parents[code] = parent
		kinds[code] = kind
	}
	want := map[string]string{"10": "", "90": "", "90.1": "90", "90.2": "90", "90.2.1": "90.2"}
	if len(parents) != len(want) {
		t.Fatalf("счета в базе: %v, ожидались %v", parents, want)
	}
	for code, parent := range want {
		got, ok := parents[code]
		if !ok {
			t.Errorf("счёта %s нет в базе (все: %v)", code, parents)
			continue
		}
		if got != parent {
			t.Errorf("счёт %s: родитель %q, ожидался %q", code, got, parent)
		}
	}
	if kinds["90.2.1"] != "active_passive" {
		t.Errorf("вложенный счёт без kind получил вид %q, ожидался вид по умолчанию active_passive", kinds["90.2.1"])
	}
}

func TestRunFull_ChartOfAccountsIssues(t *testing.T) {
	cases := []struct {
		name, chart, reg string
		want             []string
	}{
		{
			name: "вид счёта через дефис",
			chart: `name: Основной
accounts:
  - {code: "60", name: Поставщики, kind: active-passive}
`,
			reg:  accountingRegYAML,
			want: []string{`"60"`, `"active-passive"`, "active_passive"},
		},
		{
			name: "повтор кода",
			chart: `name: Основной
accounts:
  - {code: "41", name: Товары, kind: active}
  - {code: "41", name: Товары ещё раз, kind: active}
`,
			reg:  accountingRegYAML,
			want: []string{`"41"`, "дважды"},
		},
		{
			name: "родитель не найден",
			chart: `name: Основной
accounts:
  - {code: "90.1", name: Выручка, kind: passive, parent: "90"}
`,
			reg:  accountingRegYAML,
			want: []string{`"90.1"`, `родитель "90"`},
		},
		{
			name: "вложенный счёт с чужим parent",
			chart: `name: Основной
accounts:
  - code: "90"
    name: Продажи
    children:
      - {code: "90.1", name: Выручка, parent: "91"}
`,
			reg:  accountingRegYAML,
			want: []string{`"90.1"`, `"90"`, `"91"`},
		},
		{
			name: "регистр ссылается на несуществующий план",
			chart: `name: Основной
accounts:
  - {code: "41", name: Товары, kind: active}
`,
			reg: `name: Бух
accounts: Основный
resources:
  - {name: Сумма, type: number}
`,
			want: []string{`"Основный"`, "не найден"},
		},
		{
			name: "имя плана в другом регистре букв",
			chart: `name: Основной
accounts:
  - {code: "41", name: Товары, kind: active}
`,
			reg: `name: Бух
accounts: основной
resources:
  - {name: Сумма, type: number}
`,
			want: []string{`"основной"`, `"Основной"`, "регистра букв"},
		},
		{
			name: "регистр без плана счетов",
			chart: `name: Основной
accounts:
  - {code: "41", name: Товары, kind: active}
`,
			reg: `name: Бух
resources:
  - {name: Сумма, type: number}
`,
			want: []string{"не указан план счетов"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			mkFile(t, filepath.Join(dir, "accounts", "основной.yaml"), c.chart)
			mkFile(t, filepath.Join(dir, "accountregs", "бух.yaml"), c.reg)
			result := RunFull(dir)
			if result.OK {
				t.Fatalf("onebase check пропустил конфигурацию: %s", c.name)
			}
			var all []string
			for _, is := range result.Issues {
				all = append(all, is.Message)
			}
			joined := strings.Join(all, "\n")
			for _, w := range c.want {
				if !strings.Contains(joined, w) {
					t.Errorf("в замечаниях нет %q:\n%s", w, joined)
				}
			}
		})
	}
}
