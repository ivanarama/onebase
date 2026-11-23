package configcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pii_open_when проверяется полным checker'ом (тем же, что onebase check):
// верное условие — без ошибок, опечатка в реквизите и синтаксис — ошибка
// field.pii-open-when, условие без pii: true — ошибка загрузки.
func TestRunFull_PIIOpenWhen(t *testing.T) {
	project := func(t *testing.T, fieldTail string) string {
		t.Helper()
		dir := t.TempDir()
		write := func(rel, body string) {
			p := filepath.Join(dir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		write("config/app.yaml", "name: piiopen\n")
		write("catalogs/Заявки.yaml", `name: Заявки
fields:
  - name: Наименование
    type: string
  - name: Статус
    type: string
  - name: Телефон
    type: string
`+fieldTail)
		return dir
	}
	codes := func(dir string) (piiOpen []string, load []string) {
		for _, is := range RunFull(dir).Issues {
			switch {
			case is.Code == "field.pii-open-when":
				piiOpen = append(piiOpen, is.Message)
			case strings.Contains(is.Message, "pii_open_when"):
				load = append(load, is.Message)
			}
		}
		return
	}
	if got, load := codes(project(t, "    pii: true\n    pii_open_when: 'Статус = \"НаОформлении\"'\n")); len(got)+len(load) != 0 {
		t.Fatalf("верное условие: ложные ошибки %v %v", got, load)
	}
	if got, _ := codes(project(t, "    pii: true\n    pii_open_when: 'Статуc = \"НаОформлении\"'\n")); len(got) != 1 {
		t.Fatalf("опечатка в имени реквизита не найдена: %v", got)
	}
	if got, _ := codes(project(t, "    pii: true\n    pii_open_when: 'Статус = '\n")); len(got) != 1 {
		t.Fatalf("синтаксическая ошибка не найдена: %v", got)
	}
	if _, load := codes(project(t, "    pii_open_when: 'Статус = \"НаОформлении\"'\n")); len(load) == 0 {
		t.Fatal("pii_open_when без pii: true принят молча")
	}
	// Табличная часть и регистры формы записи не имеют — ключ там отвергается.
	tp := project(t, `tableparts:
  - name: Контакты
    fields:
      - name: Номер
        type: string
        pii: true
        pii_open_when: 'Номер = "1"'
`)
	if _, load := codes(tp); len(load) == 0 {
		t.Fatal("pii_open_when у поля табличной части принят молча")
	}
	for _, reg := range []struct{ dir, body string }{
		{"registers", "name: Звонки\ndimensions:\n  - name: Телефон\n    type: string\n    pii: true\n    pii_open_when: 'Телефон = \"1\"'\nresources:\n  - name: Количество\n    type: number\n"},
		{"inforegs", "name: Контакты\ndimensions:\n  - name: Телефон\n    type: string\n    pii: true\n    pii_open_when: 'Телефон = \"1\"'\nresources:\n  - name: Имя\n    type: string\n"},
	} {
		dir := project(t, "")
		p := filepath.Join(dir, reg.dir, "Р.yaml")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(reg.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, load := codes(dir); len(load) == 0 {
			t.Fatalf("%s: pii_open_when у поля регистра принят молча", reg.dir)
		}
	}
}
