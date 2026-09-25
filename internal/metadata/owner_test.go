package metadata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeYAML(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Подчинение объявляется ОДНОЙ строкой: реквизит «Владелец» платформа
// синтезирует сама. Иначе подчинение задавалось бы двумя местами — строкой
// owner и ссылочным реквизитом, который легко назвать иначе и потерять отбор.
func TestOwnerSynthesizesField(t *testing.T) {
	dir := t.TempDir()
	path := writeYAML(t, dir, "Договор.yaml", `name: Договор
owner: Контрагент
fields:
  - name: Наименование
    type: string
`)
	e, err := LoadFile(path, KindCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if e.Owner != "Контрагент" {
		t.Fatalf("Owner = %q, ждали Контрагент", e.Owner)
	}
	f := findEntityFieldFold(e, StandardOwnerField)
	if f == nil {
		t.Fatalf("реквизит %s не синтезирован: %+v", StandardOwnerField, e.Fields)
	}
	if f.RefEntity != "Контрагент" {
		t.Fatalf("тип реквизита владельца = %q, ждали ссылку на Контрагент", f.RefEntity)
	}
	// Устойчивый ID: без него миграция примет синтезированную колонку за новую
	// и запланирует снос старой вместе со связями.
	if f.ID != StandardOwnerFieldID {
		t.Fatalf("ID реквизита владельца = %q, ждали %q", f.ID, StandardOwnerFieldID)
	}
}

// Объявленный руками «Владелец» не перетирается: у него могут быть своя подпись
// и обязательность.
func TestOwnerKeepsExplicitField(t *testing.T) {
	dir := t.TempDir()
	path := writeYAML(t, dir, "Договор.yaml", `name: Договор
owner: Контрагент
fields:
  - name: Владелец
    type: reference:Контрагент
    label: Контрагент договора
    required: true
`)
	e, err := LoadFile(path, KindCatalog)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, f := range e.Fields {
		if strings.EqualFold(f.Name, StandardOwnerField) {
			count++
			if !f.Required || f.Title != "Контрагент договора" {
				t.Fatalf("объявленный реквизит владельца перетёрт: %+v", f)
			}
		}
	}
	if count != 1 {
		t.Fatalf("реквизитов «Владелец» = %d, ждали один", count)
	}
}

// Владелец обязан существовать и быть справочником, а документ владельцем быть
// не может: состав НСИ не должен зависеть от оперативных данных.
func TestOwnerValidation(t *testing.T) {
	contractor := &Entity{Name: "Контрагент", Kind: KindCatalog}
	order := &Entity{Name: "Заказ", Kind: KindDocument}
	sub := func(owner string) *Entity {
		return &Entity{
			Name: "Договор", Kind: KindCatalog, Owner: owner,
			Fields: []Field{{Name: StandardOwnerField, Type: FieldType("reference:" + owner), RefEntity: owner}},
		}
	}
	cases := []struct {
		name    string
		ents    []*Entity
		wantErr string
	}{
		{"владелец-справочник", []*Entity{contractor, sub("Контрагент")}, ""},
		// Несуществующий владелец ловится раньше — общей проверкой ссылочных
		// реквизитов: синтезированный «Владелец» ссылается на ту же сущность.
		{"неизвестный владелец", []*Entity{contractor, sub("Партнёр")}, "Партнёр"},
		{"владелец-документ", []*Entity{contractor, order, sub("Заказ")}, "только справочник"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.ents, nil)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ждали успех, получили %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ошибка = %v, ждали упоминание %q", err, tc.wantErr)
			}
		})
	}
}

// Справочник не может быть подчинён сам себе — такой отбор не сходится никогда.
func TestOwnerRejectsSelfReference(t *testing.T) {
	e := &Entity{
		Name: "Договор", Kind: KindCatalog, Owner: "Договор",
		Fields: []Field{{Name: StandardOwnerField, Type: "reference:Договор", RefEntity: "Договор"}},
	}
	err := Validate([]*Entity{e}, nil)
	if err == nil || !strings.Contains(err.Error(), "сам справочник") {
		t.Fatalf("ошибка = %v, ждали отказ на самоподчинение", err)
	}
}

// owner у документа не проглатывается молча: ключ, который ничего не делает,
// хуже ошибки — его пишут и ждут отбора.
func TestOwnerOnDocumentRejected(t *testing.T) {
	dir := t.TempDir()
	path := writeYAML(t, dir, "Заказ.yaml", `name: Заказ
owner: Контрагент
fields:
  - name: Сумма
    type: number
`)
	e, err := LoadFile(path, KindDocument)
	if err != nil {
		t.Fatal(err)
	}
	if e.Owner != "Контрагент" {
		t.Fatalf("Owner документа = %q — ключ потерян, Validate его не увидит", e.Owner)
	}
	contractor := &Entity{Name: "Контрагент", Kind: KindCatalog}
	if err := Validate([]*Entity{contractor, e}, nil); err == nil ||
		!strings.Contains(err.Error(), "только у справочника") {
		t.Fatalf("ошибка = %v, ждали отказ owner у документа", err)
	}
}
