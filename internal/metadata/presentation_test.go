package metadata

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Явное представление объекта (#846), пришло из вопроса #819: «в справочнике
// Наименование и Код, а показывать нужно Код».
//
// Правило по именам (LabelFields) сознательно ставит «Код» последним: конвертер
// 1С кладёт его перед «Наименованием», и без правила у импортированных
// конфигураций везде показывался бы код. Но там, где объект ПРИНЯТО
// представлять артикулом, табельным номером или шифром, правилу помочь нечем —
// если «Наименование» есть, победит оно.
func presentationEntity(presentation ...string) *Entity {
	return &Entity{
		Name: "Номенклатура",
		Kind: KindCatalog,
		Fields: []Field{
			{Name: "Код", Type: FieldTypeString},
			{Name: "Наименование", Type: FieldTypeString},
			{Name: "Артикул", Type: FieldTypeString},
			{Name: "Остаток", Type: FieldTypeNumber},
		},
		Presentation: presentation,
	}
}

func TestLabelFields_БезКлючаПравилоПрежнее(t *testing.T) {
	got := LabelFields(presentationEntity())
	if len(got) == 0 || got[0].Name != "Наименование" {
		t.Fatalf("представление по умолчанию изменилось: %+v", got)
	}
	// «Код» остаётся последним — ради этого правило и писалось.
	if got[len(got)-1].Name != "Код" {
		t.Errorf("«Код» перестал быть последним: %+v", got)
	}
}

func TestLabelFields_ЯвноеПредставлениеПеребиваетПравило(t *testing.T) {
	got := LabelFields(presentationEntity("Артикул"))
	if len(got) != 1 || got[0].Name != "Артикул" {
		t.Fatalf("явное представление не применилось: %+v", got)
	}
}

// Список задаёт запасной вариант: RowLabel берёт первый НЕПУСТОЙ.
func TestLabelFields_СписокЗадаётПорядок(t *testing.T) {
	got := LabelFields(presentationEntity("Артикул", "Наименование"))
	if len(got) != 2 || got[0].Name != "Артикул" || got[1].Name != "Наименование" {
		t.Fatalf("порядок списка не сохранён: %+v", got)
	}
}

// Пустой артикул → подпись берётся из следующего кандидата.
func TestRowLabel_ЗапаснойКандидатИзСписка(t *testing.T) {
	e := presentationEntity("Артикул", "Наименование")
	row := map[string]any{"Артикул": "", "Наименование": "Стул", "Код": "К-1"}
	if got := RowLabel(row, e); got != "Стул" {
		t.Fatalf("RowLabel = %q, ожидалось «Стул»", got)
	}
	row["Артикул"] = "А-42"
	if got := RowLabel(row, e); got != "А-42" {
		t.Fatalf("RowLabel = %q, ожидалось «А-42»", got)
	}
}

// Опечатка ловится проверкой конфигурации, а не в рантайме: представление
// меняется сразу везде, и «вдруг стало другим» — худший способ узнать.
func TestValidate_ПредставлениеПроверяется(t *testing.T) {
	err := Validate([]*Entity{presentationEntity("Артикулл")}, nil)
	if err == nil || !strings.Contains(err.Error(), "presentation") {
		t.Fatalf("опечатка в presentation принята: %v", err)
	}

	err = Validate([]*Entity{presentationEntity("Остаток")}, nil)
	if err == nil || !strings.Contains(err.Error(), "строковым") {
		t.Fatalf("нестроковый реквизит принят: %v", err)
	}

	if err := Validate([]*Entity{presentationEntity("Артикул")}, nil); err != nil {
		t.Fatalf("корректное представление отклонено: %v", err)
	}

	err = Validate([]*Entity{presentationEntity("Артикул", "артикул")}, nil)
	if err == nil || !strings.Contains(err.Error(), "повтор") {
		t.Fatalf("дубль presentation принят: %v", err)
	}
	if got := LabelFields(presentationEntity("Артикул", "артикул")); len(got) != 1 {
		t.Fatalf("runtime не убрал дубль presentation: %+v", got)
	}

	err = Validate([]*Entity{presentationEntity("")}, nil)
	if err == nil || !strings.Contains(err.Error(), "пустое имя") {
		t.Fatalf("пустой presentation принят: %v", err)
	}
}

func TestLoadFile_ExplicitEmptyPresentationFailsValidation(t *testing.T) {
	for name, value := range map[string]string{
		"пустая строка":         "''",
		"пустой список":         "[]",
		"пустой элемент списка": "[Артикул, '']",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "номенклатура.yaml")
			body := "name: Номенклатура\npresentation: " + value + `
fields:
  - name: Артикул
    type: string
`
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			e, err := LoadFile(path, KindCatalog)
			if err != nil {
				t.Fatalf("LoadFile: %v", err)
			}
			if err := Validate([]*Entity{e}, nil); err == nil || !strings.Contains(err.Error(), "пустое имя") {
				t.Fatalf("явно пустой presentation прошёл Validate: %v", err)
			}
		})
	}
}

// Ключ читается и как строка, и как список: «одно поле» — частый случай, и
// требовать список из одного элемента значило бы гнуть формат под код.
func TestLoadFile_PresentationСтрокаИСписок(t *testing.T) {
	cases := map[string]struct {
		yaml string
		want []string
	}{
		"строка":    {"presentation: '  Артикул  '\n", []string{"Артикул"}},
		"список":    {"presentation: ['  Артикул  ', ' Наименование ']\n", []string{"Артикул", "Наименование"}},
		"нет ключа": {"", nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "номенклатура.yaml")
			body := "name: Номенклатура\n" + tc.yaml + `fields:
  - name: Артикул
    type: string
  - name: Наименование
    type: string
`
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			e, err := LoadFile(path, KindCatalog)
			if err != nil {
				t.Fatalf("LoadFile: %v", err)
			}
			if !reflect.DeepEqual(e.Presentation, tc.want) {
				t.Fatalf("Presentation = %#v, ожидалось %#v", e.Presentation, tc.want)
			}
			if err := Validate([]*Entity{e}, nil); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}
