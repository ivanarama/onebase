package metadata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// choice_row_style читается из YAML справочника и проверяется Validate:
// опечатка в цвете, карточке или предопределённом падает при загрузке, а не
// выглядит в рантайме как «почему-то не красится».
func TestChoiceRowStyle_ParseAndValidate(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) *Entity {
		t.Helper()
		p := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		e, err := LoadFile(p, KindCatalog)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	deps := write("Подразделения", "name: Подразделения\nfields:\n  - {name: Наименование, type: string}\npredefined:\n  - name: КоллЦентр\n    fields: {Наименование: Колл центр}\n")
	card := write("Пользователи", "name: Пользователи\nfields:\n  - {name: УчётнаяЗапись, type: reference:_users}\n  - {name: Подразделение, type: reference:Подразделения}\n")
	dirs := write("Направления", `name: Направления
fields:
  - {name: Наименование, type: string}
  - {name: Огр, type: bool}
  - {name: ЧастОгр, type: bool}
choice_row_style:
  for_roles: [ОператорКЦ]
  for_user: {card: Пользователи, field: Подразделение, equals: Подразделения.КоллЦентр}
  rules:
    - {when: Огр, color: "rgb(250,0,0)"}
    - {when: ЧастОгр, background: "rgb(255,255,0)", bold: true}
`)
	st := dirs.ChoiceRowStyle
	if st == nil || len(st.Rules) != 2 || st.ForUser == nil || st.ForUser.Equals != "Подразделения.КоллЦентр" || len(st.ForRoles) != 1 {
		t.Fatalf("разбор: %+v", st)
	}
	if st.Rules[1].Style.Background != "rgb(255,255,0)" || !st.Rules[1].Style.Bold {
		t.Fatalf("стиль правила: %+v", st.Rules[1])
	}
	if err := Validate([]*Entity{deps, card, dirs}, nil); err != nil {
		t.Fatalf("корректное объявление отвергнуто: %v", err)
	}
	for _, c := range []struct {
		name string
		mut  func(*ChoiceRowStyle)
		want string
	}{
		{"цвет", func(s *ChoiceRowStyle) { s.Rules[0].Style.Color = "red;background:url(x)" }, "недопустимый цвет"},
		{"пустое правило", func(s *ChoiceRowStyle) { s.Rules[0] = ChoiceStyleRule{When: "Огр"} }, "ничего не оформляет"},
		{"карточка", func(s *ChoiceRowStyle) { s.ForUser.Card = "Сотрудники" }, "неизвестный справочник карточки"},
		{"реквизит", func(s *ChoiceRowStyle) { s.ForUser.Field = "Отдел" }, "нет реквизита"},
		{"предопределённый", func(s *ChoiceRowStyle) { s.ForUser.Equals = "Подразделения.Склад" }, "нет предопределённого"},
		{"func с card", func(s *ChoiceRowStyle) { s.ForUser.Func = "Модуль.Функция" }, "вместо card"},
	} {
		orig := *dirs.ChoiceRowStyle
		orig.Rules = append([]ChoiceStyleRule(nil), dirs.ChoiceRowStyle.Rules...)
		u := *dirs.ChoiceRowStyle.ForUser
		orig.ForUser = &u
		bad := *dirs
		bad.ChoiceRowStyle = &orig
		c.mut(bad.ChoiceRowStyle)
		err := Validate([]*Entity{deps, card, &bad}, nil)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: ждали ошибку %q, получили %v", c.name, c.want, err)
		}
	}
}
