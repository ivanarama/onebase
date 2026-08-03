package metadata

import (
	"os"
	"path/filepath"
	"testing"
)

// Блок `fulltext:` управляет составом полнотекстового индекса (план 82).

func writeEntityYAML(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "e.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFile_FullTextList(t *testing.T) {
	path := writeEntityYAML(t, `
name: Контрагент
fields:
  - {name: Наименование, type: string}
  - {name: Комментарий, type: string}
fulltext: [Наименование, Комментарий]
`)
	e, err := LoadFile(path, KindCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if !e.FullTextSet || len(e.FullText) != 2 {
		t.Fatalf("ожидался явный список полей, получено %+v (set=%v)", e.FullText, e.FullTextSet)
	}
	if got := FullTextFields(e); len(got) != 2 || got[0].Name != "Наименование" {
		t.Fatalf("неожиданный состав индекса: %+v", got)
	}
}

// Отсутствие ключа и явный пустой список — разные вещи: первое означает
// умолчание (все строковые реквизиты), второе — объект вне поиска.
func TestLoadFile_FullTextDefaultVsEmpty(t *testing.T) {
	noKey := writeEntityYAML(t, `
name: Контрагент
fields:
  - {name: Наименование, type: string}
  - {name: Дата, type: date}
`)
	e, err := LoadFile(noKey, KindCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if e.FullTextSet {
		t.Fatalf("без ключа fulltext флаг не должен взводиться")
	}
	if got := FullTextFields(e); len(got) != 1 || got[0].Name != "Наименование" {
		t.Fatalf("по умолчанию индексируются строковые реквизиты, получено %+v", got)
	}

	empty := writeEntityYAML(t, `
name: Контрагент
fields:
  - {name: Наименование, type: string}
fulltext: []
`)
	e2, err := LoadFile(empty, KindCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if !e2.FullTextSet {
		t.Fatalf("явный fulltext: [] должен взводить флаг")
	}
	if got := FullTextFields(e2); len(got) != 0 {
		t.Fatalf("явный пустой список означает «вне поиска», получено %+v", got)
	}
}

// У документа синтезируется реквизит Номер — он должен попадать в индекс по
// умолчанию, иначе документ без прочих строковых реквизитов не найти вовсе.
func TestFullTextFields_DocumentNumber(t *testing.T) {
	path := writeEntityYAML(t, `
name: РасходнаяНакладная
numerator: {prefix: "РН-", length: 8}
fields:
  - {name: Сумма, type: number}
`)
	e, err := LoadFile(path, KindDocument)
	if err != nil {
		t.Fatal(err)
	}
	got := FullTextFields(e)
	if len(got) != 1 || got[0].Name != "Номер" {
		t.Fatalf("ожидался Номер в индексе по умолчанию, получено %+v", got)
	}
}

func TestValidate_FullTextRejectsBadFields(t *testing.T) {
	cases := []struct {
		name   string
		entity *Entity
	}{
		{
			name: "неизвестный реквизит",
			entity: &Entity{
				Name:        "Контрагент",
				Fields:      []Field{{Name: "Наименование", Type: FieldTypeString}},
				FullText:    []string{"Опечатка"},
				FullTextSet: true,
			},
		},
		{
			name: "нетекстовый тип",
			entity: &Entity{
				Name:        "Контрагент",
				Fields:      []Field{{Name: "Сумма", Type: FieldTypeNumber}},
				FullText:    []string{"Сумма"},
				FullTextSet: true,
			},
		},
		{
			name: "ссылочный реквизит",
			entity: &Entity{
				Name:        "Контрагент",
				Fields:      []Field{{Name: "Родитель", Type: "reference:Контрагент", RefEntity: "Контрагент"}},
				FullText:    []string{"Родитель"},
				FullTextSet: true,
			},
		},
		{
			name: "дубль",
			entity: &Entity{
				Name:        "Контрагент",
				Fields:      []Field{{Name: "Наименование", Type: FieldTypeString}},
				FullText:    []string{"Наименование", "наименование"},
				FullTextSet: true,
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Validate([]*Entity{c.entity}, nil); err == nil {
				t.Fatalf("ожидалась ошибка валидации")
			}
		})
	}
}

func TestValidate_FullTextAcceptsStringAndRichText(t *testing.T) {
	e := &Entity{
		Name: "Контрагент",
		Fields: []Field{
			{Name: "Наименование", Type: FieldTypeString},
			{Name: "Описание", Type: FieldTypeRichText},
		},
		FullText:    []string{"наименование", "Описание"},
		FullTextSet: true,
	}
	if err := Validate([]*Entity{e}, nil); err != nil {
		t.Fatalf("string и richtext должны приниматься: %v", err)
	}
	if got := FullTextFields(e); len(got) != 2 {
		t.Fatalf("имя реквизита должно сопоставляться без учёта регистра: %+v", got)
	}
}
