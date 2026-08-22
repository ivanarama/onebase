package metadata_test

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestParseDefault_Sources(t *testing.T) {
	cases := []struct {
		raw      string
		wantKind metadata.DefaultKind
		wantArg  string
	}{
		{"", "", ""},
		{"   ", "", ""},
		{"сегодня", metadata.DefaultToday, ""},
		{"ToDay", metadata.DefaultToday, ""},
		{"сейчас", metadata.DefaultNow, ""},
		{"NOW", metadata.DefaultNow, ""},
		{"ТекущийПользователь", metadata.DefaultCurrentUser, ""},
		{"единственный", metadata.DefaultSingle, ""},
		{"single", metadata.DefaultSingle, ""},
		{"Константа.НашаОрганизация", metadata.DefaultConstant, "НашаОрганизация"},
		{"constant.Warehouse", metadata.DefaultConstant, "Warehouse"},
		{"ВТомЧисле", metadata.DefaultLiteral, ""},
		{"12.5", metadata.DefaultLiteral, ""},
	}
	for _, tc := range cases {
		spec, ok, err := metadata.ParseDefault(tc.raw)
		if err != nil {
			t.Fatalf("ParseDefault(%q): неожиданная ошибка %v", tc.raw, err)
		}
		if tc.wantKind == "" {
			if ok {
				t.Errorf("ParseDefault(%q): ожидалось «дефолта нет»", tc.raw)
			}
			continue
		}
		if !ok {
			t.Errorf("ParseDefault(%q): ожидался дефолт", tc.raw)
			continue
		}
		if spec.Kind != tc.wantKind {
			t.Errorf("ParseDefault(%q).Kind = %q, ожидалось %q", tc.raw, spec.Kind, tc.wantKind)
		}
		if spec.Constant != tc.wantArg {
			t.Errorf("ParseDefault(%q).Constant = %q, ожидалось %q", tc.raw, spec.Constant, tc.wantArg)
		}
	}
}

// Опечатка в имени источника обязана падать, а не превращаться в литерал:
// молча не сработавший дефолт ищут в рантайме, и ищут долго.
func TestParseDefault_TypoInSourceIsError(t *testing.T) {
	for _, raw := range []string{"Константы.НашаОрганизация", "constants.X", "Справочники.Склады", "константа."} {
		if _, _, err := metadata.ParseDefault(raw); err == nil {
			t.Errorf("ParseDefault(%q): ожидалась ошибка, дефолт принят", raw)
		}
	}
}

func entityWithDefault(fieldType metadata.FieldType, def string) []*metadata.Entity {
	f := metadata.Field{Name: "Реквизит", Type: fieldType, Default: def}
	switch {
	case strings.HasPrefix(string(fieldType), "reference:"):
		f.Type = metadata.FieldType(fieldType)
		f.RefEntity = strings.TrimPrefix(string(fieldType), "reference:")
	case strings.HasPrefix(string(fieldType), "enum:"):
		f.EnumName = strings.TrimPrefix(string(fieldType), "enum:")
	}
	return []*metadata.Entity{{
		Name:   "Документ",
		Kind:   metadata.KindDocument,
		Fields: []metadata.Field{f},
	}}
}

func TestValidateDefaults(t *testing.T) {
	enums := []*metadata.Enum{{Name: "СпособУчета", Values: []string{"ВТомЧисле", "Сверху"}}}
	constants := []*metadata.Constant{
		{Name: "НашаОрганизация", Type: "reference:Организация", RefEntity: "Организация"},
		{Name: "Порог", Type: metadata.FieldTypeNumber},
	}
	cases := []struct {
		name      string
		fieldType metadata.FieldType
		def       string
		wantErr   string
	}{
		{"дата сейчас", metadata.FieldTypeDate, "сейчас", ""},
		{"строка литерал", metadata.FieldTypeString, "Основной", ""},
		{"число литерал", metadata.FieldTypeNumber, "12,5", ""},
		{"булево литерал", metadata.FieldTypeBool, "Истина", ""},
		{"перечисление литерал", "enum:СпособУчета", "ВТомЧисле", ""},
		{"ссылка на константу", "reference:Организация", "Константа.НашаОрганизация", ""},
		{"ссылка единственный", "reference:Организация", "единственный", ""},
		{"логин в строку", metadata.FieldTypeString, "ТекущийПользователь", ""},

		{"сейчас не на дате", metadata.FieldTypeString, "сейчас", "только к реквизиту типа date"},
		{"единственный не на ссылке", metadata.FieldTypeString, "единственный", "только к ссылочному реквизиту"},
		{"логин на ссылке", "reference:Пользователи", "ТекущийПользователь", "ПриСозданииНового"},
		{"нет такой константы", metadata.FieldTypeString, "Константа.Неведомая", "несуществующую константу"},
		{"константа другого типа", metadata.FieldTypeString, "Константа.Порог", "константа имеет тип"},
		{"константа-ссылка на чужую сущность", "reference:Склад", "Константа.НашаОрганизация", "константа ссылается на"},
		{"нет такого значения перечисления", "enum:СпособУчета", "Сбоку", "нет такого значения"},
		{"дата литералом", metadata.FieldTypeDate, "2026-01-01", "допустимы только"},
		{"число не число", metadata.FieldTypeNumber, "много", "не число"},
		{"булево не булево", metadata.FieldTypeBool, "возможно", "не булево"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := metadata.ValidateDefaults(entityWithDefault(tc.fieldType, tc.def), enums, constants)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ожидался успех, получено: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ожидалась ошибка со словами %q, получен успех", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ошибка %q не содержит %q", err.Error(), tc.wantErr)
			}
			if !strings.Contains(err.Error(), "Реквизит") {
				t.Errorf("в сообщении нет имени реквизита: %q", err.Error())
			}
		})
	}
}

// В табличной части ключ пока не поддерживается, и `check` говорит это прямо:
// принять его и не применять — молча неработающий дефолт. Сообщение обязано
// называть и ТЧ, и реквизит, иначе поиск в конфигурации с двумя десятками
// табличных частей превращается в перебор.
func TestValidateDefaults_TablePartRejected(t *testing.T) {
	ents := []*metadata.Entity{{
		Name: "РеализацияТоваров",
		Kind: metadata.KindDocument,
		TableParts: []metadata.TablePart{{
			Name:   "Товары",
			Fields: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber, Default: "1"}},
		}},
	}}
	err := metadata.ValidateDefaults(ents, nil, nil)
	if err == nil {
		t.Fatal("ожидалась ошибка")
	}
	if !strings.Contains(err.Error(), "не поддерживается в табличной части") {
		t.Errorf("сообщение не объясняет причину: %q", err.Error())
	}
	for _, want := range []string{"Товары", "Количество"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ошибка %q не содержит %q", err.Error(), want)
		}
	}
}
