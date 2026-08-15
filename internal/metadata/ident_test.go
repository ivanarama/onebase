package metadata

import (
	"strings"
	"testing"
)

func TestValidIdent(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"Номенклатура", true},
		{"Counterparty", true},
		{"СтавкаНДС", true},
		{"_служебное", true},
		{"Поле1", true},
		{"a", true},

		{"", false},
		{"Дата платежа", false}, // пробел
		{"Поле-2", false},       // дефис
		{"1Поле", false},        // начинается с цифры
		{"Контрагент;DROP", false},
		{`Имя"`, false},
		{"Контр.агент", false}, // точка
		{"Сумма(руб)", false},  // скобки
	}
	for _, c := range cases {
		if got := ValidIdent(c.name); got != c.want {
			t.Errorf("ValidIdent(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestValidateIdentifiers_OK(t *testing.T) {
	entities := []*Entity{
		{Name: "Контрагент", Kind: KindCatalog, Fields: []Field{{Name: "Наименование", Type: FieldTypeString}}},
		{
			Name: "Поступление", Kind: KindDocument,
			Fields:     []Field{{Name: "Номер", Type: FieldTypeString}},
			TableParts: []TablePart{{Name: "Товары", Fields: []Field{{Name: "Количество", Type: FieldTypeNumber}}}},
		},
	}
	registers := []*Register{{
		Name:       "ОстаткиТоваров",
		Dimensions: []Field{{Name: "Номенклатура", Type: FieldTypeString}},
		Resources:  []Field{{Name: "Количество", Type: FieldTypeNumber}},
	}}
	inforegs := []*InfoRegister{{
		Name:       "ЦеныНоменклатуры",
		Dimensions: []Field{{Name: "Номенклатура", Type: FieldTypeString}},
		Resources:  []Field{{Name: "Цена", Type: FieldTypeNumber}},
	}}
	enums := []*Enum{{Name: "ВидКонтрагента", Values: []string{"Поставщик"}}}
	constants := []*Constant{{Name: "ОсновнаяВалюта", Type: FieldTypeString}}

	if err := ValidateIdentifiers(entities, registers, inforegs, nil, enums, constants); err != nil {
		t.Fatalf("корректная конфигурация не должна давать ошибку: %v", err)
	}
}

func TestValidateIdentifiers_Rejects(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
	}{
		{"имя справочника с пробелом", func() error {
			return ValidateIdentifiers([]*Entity{{Name: "Мой Справочник", Kind: KindCatalog}}, nil, nil, nil, nil, nil)
		}},
		{"реквизит с дефисом", func() error {
			e := &Entity{Name: "Контрагент", Kind: KindCatalog, Fields: []Field{{Name: "ИНН-КПП", Type: FieldTypeString}}}
			return ValidateIdentifiers([]*Entity{e}, nil, nil, nil, nil, nil)
		}},
		{"реквизит совпадает со служебной колонкой", func() error {
			e := &Entity{Name: "Контрагент", Kind: KindCatalog, Fields: []Field{{Name: "id", Type: FieldTypeString}}}
			return ValidateIdentifiers([]*Entity{e}, nil, nil, nil, nil, nil)
		}},
		{"табличная часть с инъекцией", func() error {
			e := &Entity{Name: "Поступление", Kind: KindDocument, TableParts: []TablePart{{Name: "Товары; DROP TABLE"}}}
			return ValidateIdentifiers([]*Entity{e}, nil, nil, nil, nil, nil)
		}},
		{"измерение регистра с кавычкой", func() error {
			r := &Register{Name: "Остатки", Dimensions: []Field{{Name: `Ном"`, Type: FieldTypeString}}}
			return ValidateIdentifiers(nil, []*Register{r}, nil, nil, nil, nil)
		}},
		{"перечисление с точкой", func() error {
			return ValidateIdentifiers(nil, nil, nil, nil, []*Enum{{Name: "Вид.Контрагента"}}, nil)
		}},
		{"константа начинается с цифры", func() error {
			return ValidateIdentifiers(nil, nil, nil, nil, nil, []*Constant{{Name: "1Валюта"}})
		}},
	}
	for _, tt := range tests {
		if err := tt.run(); err == nil {
			t.Errorf("%s: ожидалась ошибка валидации, получено nil", tt.name)
		}
	}
}

// Регрессия (план критических фиксов, #3): systemColumns не содержал _version,
// recorder_type, line_number, вид_движения, updated_at — поле/измерение с таким
// именем проходило валидацию и ломало DDL/UPDATE (duplicate column либо двойное
// присваивание _version). Колонка ТЧ «строка» проверяется отдельно.
func TestValidateIdentifiers_ReservedColumns(t *testing.T) {
	reject := []struct {
		name string
		run  func() error
	}{
		{"реквизит _version", func() error {
			e := &Entity{Name: "Контрагент", Kind: KindCatalog, Fields: []Field{{Name: "_version", Type: FieldTypeNumber}}}
			return ValidateIdentifiers([]*Entity{e}, nil, nil, nil, nil, nil)
		}},
		{"реквизит recorder_type (регистронезависимо)", func() error {
			e := &Entity{Name: "Контрагент", Kind: KindCatalog, Fields: []Field{{Name: "Recorder_Type", Type: FieldTypeString}}}
			return ValidateIdentifiers([]*Entity{e}, nil, nil, nil, nil, nil)
		}},
		{"измерение регистра вид_движения", func() error {
			r := &Register{Name: "Остатки", Dimensions: []Field{{Name: "вид_движения", Type: FieldTypeString}}}
			return ValidateIdentifiers(nil, []*Register{r}, nil, nil, nil, nil)
		}},
		{"измерение регистра line_number", func() error {
			r := &Register{Name: "Остатки", Dimensions: []Field{{Name: "line_number", Type: FieldTypeNumber}}}
			return ValidateIdentifiers(nil, []*Register{r}, nil, nil, nil, nil)
		}},
		{"измерение инфорегистра updated_at", func() error {
			ir := &InfoRegister{Name: "Цены", Dimensions: []Field{{Name: "updated_at", Type: FieldTypeString}}}
			return ValidateIdentifiers(nil, nil, []*InfoRegister{ir}, nil, nil, nil)
		}},
		{"реквизит ТЧ Строка", func() error {
			e := &Entity{Name: "Поступление", Kind: KindDocument, TableParts: []TablePart{
				{Name: "Товары", Fields: []Field{{Name: "Строка", Type: FieldTypeString}}}}}
			return ValidateIdentifiers([]*Entity{e}, nil, nil, nil, nil, nil)
		}},
	}
	for _, tt := range reject {
		if err := tt.run(); err == nil {
			t.Errorf("%s: ожидалась ошибка валидации, получено nil", tt.name)
		}
	}

	// «Строка» как реквизит обычной сущности (НЕ ТЧ) должна оставаться допустимой —
	// такой колонки в таблице сущности нет, глобально запрещать имя не нужно.
	e := &Entity{Name: "Заметка", Kind: KindCatalog, Fields: []Field{{Name: "Строка", Type: FieldTypeString}}}
	if err := ValidateIdentifiers([]*Entity{e}, nil, nil, nil, nil, nil); err != nil {
		t.Errorf("«Строка» как реквизит сущности должна быть допустима: %v", err)
	}
}

func TestValidateIdentifiers_PeriodicInfoRegisterReservesPeriodTransportFields(t *testing.T) {
	for _, name := range []string{"Период", "period", "ПЕРИОД", "Period", "period_key", "PERIOD_KEY"} {
		t.Run(name, func(t *testing.T) {
			ir := &InfoRegister{
				Name:       "История",
				Periodic:   true,
				Dimensions: []Field{{Name: name, Type: FieldTypeString}},
			}
			err := ValidateIdentifiers(nil, nil, []*InfoRegister{ir}, nil, nil, nil)
			if err == nil || !strings.Contains(err.Error(), "системного периода") {
				t.Fatalf("periodic field %q must be rejected by the explicit DSL-period contract: %v", name, err)
			}
		})
	}

	// A non-periodic register has no synthetic period or period_key transport
	// field. Both names remain ordinary, addressable metadata fields for
	// backward compatibility.
	ir := &InfoRegister{
		Name: "Настройки",
		Dimensions: []Field{
			{Name: "Период", Type: FieldTypeString},
			{Name: "period_key", Type: FieldTypeString},
		},
	}
	if err := ValidateIdentifiers(nil, nil, []*InfoRegister{ir}, nil, nil, nil); err != nil {
		t.Fatalf("non-periodic real field Период must remain valid: %v", err)
	}
}
