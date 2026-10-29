package metadata

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/csssafe"
)

// ChoiceRowStyle — оформление строк выбора ссылок на справочник
// (`choice_row_style`): выпадающий список, форма подбора, быстрый ввод. Как
// «ОформлениеСтрок» формы выбора 1С: элементы с особым признаком видны сразу,
// например направление с ограниченным приёмом заявок — красным текстом.
//
//	choice_row_style:
//	  for_roles: [ОператорКЦ]         # необязательно: только пользователям с этими ролями
//	  for_user:                       # необязательно: только этим пользователям
//	    card: Пользователи            # справочник карточки сотрудника
//	    account_field: УчётнаяЗапись  # reference:_users; можно опустить, если он один
//	    field: Подразделение
//	    equals: Подразделения.КоллЦентр   # <Справочник>.<предопределённый>
//	  # или условием конфигурации — функция без параметров, возвращает Булево:
//	  # for_user: {func: ОбщегоНазначения.ПользовательКоллЦентра}
//	  rules:
//	    - when: ОграниченПриемЗаявокДляКЦ
//	      color: "rgb(250,0,0)"
//	    - when: ЧастичноОграниченПриемЗаявокДляКЦ
//	      background: "rgb(255,255,0)"
//
// when — условие на реквизиты элемента, тот же язык выражений, что у
// `conditional` формы (имя булева реквизита — тоже условие). Подходят все
// правила, чьё условие истинно; их стили складываются по порядку. Цвета
// проходят csssafe.Color.
// for_roles и for_user необязательны; заданные вместе — оба условия сразу.
type ChoiceRowStyle struct {
	ForRoles []string
	ForUser  *ChoiceStyleUser
	Rules    []ChoiceStyleRule
}

// ChoiceStyleUser — оформление действует, только если реквизит Field
// карточки текущего пользователя (справочник Card, связь с учётной записью —
// AccountField) равен предопределённому элементу Equals. Либо, если задан
// Func, — если экспортная функция общего модуля «Модуль.Функция» без
// параметров вернула Истина (только чтение, один раз на выдачу; ошибка или
// нет функции — оформления нет).
type ChoiceStyleUser struct {
	Func         string
	Card         string
	AccountField string
	Field        string
	Equals       string
}

// ChoiceStyleRule — условие на элемент и его оформление.
type ChoiceStyleRule struct {
	When  string
	Style FormCellStyle
}

type rawChoiceRowStyle struct {
	ForRoles []string             `yaml:"for_roles"`
	ForUser  *rawChoiceStyleUser  `yaml:"for_user"`
	Rules    []rawChoiceStyleRule `yaml:"rules"`
}

type rawChoiceStyleUser struct {
	Func         string `yaml:"func"`
	Card         string `yaml:"card"`
	AccountField string `yaml:"account_field"`
	Field        string `yaml:"field"`
	Equals       string `yaml:"equals"`
}

type rawChoiceStyleRule struct {
	When       string `yaml:"when"`
	Color      string `yaml:"color"`
	Background string `yaml:"background"`
	Bold       bool   `yaml:"bold"`
	Italic     bool   `yaml:"italic"`
}

func parseChoiceRowStyle(raw *rawChoiceRowStyle) *ChoiceRowStyle {
	if raw == nil {
		return nil
	}
	out := &ChoiceRowStyle{}
	for _, r := range raw.ForRoles {
		if r = strings.TrimSpace(r); r != "" {
			out.ForRoles = append(out.ForRoles, r)
		}
	}
	if raw.ForUser != nil {
		out.ForUser = &ChoiceStyleUser{
			Func:         strings.TrimSpace(raw.ForUser.Func),
			Card:         strings.TrimSpace(raw.ForUser.Card),
			AccountField: strings.TrimSpace(raw.ForUser.AccountField),
			Field:        strings.TrimSpace(raw.ForUser.Field),
			Equals:       strings.TrimSpace(raw.ForUser.Equals),
		}
	}
	for _, r := range raw.Rules {
		out.Rules = append(out.Rules, ChoiceStyleRule{
			When:  strings.TrimSpace(r.When),
			Style: FormCellStyle{Color: strings.TrimSpace(r.Color), Background: strings.TrimSpace(r.Background), Bold: r.Bold, Italic: r.Italic},
		})
	}
	return out
}

// CardAccountField — реквизит карточки, связывающий её с учётной записью:
// заданный явно или единственный reference:_users.
func (u *ChoiceStyleUser) CardAccountField(card *Entity) (string, error) {
	if u.AccountField != "" {
		return u.AccountField, nil
	}
	found := ""
	for _, f := range card.Fields {
		if f.RefEntity == "_users" {
			if found != "" {
				return "", fmt.Errorf("у %s несколько реквизитов reference:_users — укажите account_field", card.Name)
			}
			found = f.Name
		}
	}
	if found == "" {
		return "", fmt.Errorf("у %s нет реквизита reference:_users", card.Name)
	}
	return found, nil
}

// EqualsParts — «Справочник.Предопределённый» из Equals.
func (u *ChoiceStyleUser) EqualsParts() (catalog, name string, ok bool) {
	catalog, name, ok = strings.Cut(u.Equals, ".")
	return strings.TrimSpace(catalog), strings.TrimSpace(name), ok && strings.TrimSpace(catalog) != "" && strings.TrimSpace(name) != ""
}

func validateChoiceRowStyle(e *Entity, byName map[string]*Entity) error {
	st := e.ChoiceRowStyle
	if st == nil {
		return nil
	}
	if len(st.Rules) == 0 {
		return fmt.Errorf("entity %s: choice_row_style: нет правил (rules)", e.Name)
	}
	for i, r := range st.Rules {
		if r.When == "" {
			return fmt.Errorf("entity %s: choice_row_style: правило %d без условия when", e.Name, i+1)
		}
		for _, c := range []struct{ key, value string }{{"color", r.Style.Color}, {"background", r.Style.Background}} {
			if c.value != "" && csssafe.Color(c.value) == "" {
				return fmt.Errorf("entity %s: choice_row_style: правило %d: недопустимый цвет %s %q (hex, rgb() или имя цвета)", e.Name, i+1, c.key, c.value)
			}
		}
		if r.Style.Color == "" && r.Style.Background == "" && !r.Style.Bold && !r.Style.Italic {
			return fmt.Errorf("entity %s: choice_row_style: правило %d ничего не оформляет", e.Name, i+1)
		}
	}
	u := st.ForUser
	if u == nil {
		return nil
	}
	if u.Func != "" {
		if u.Card != "" || u.Field != "" || u.Equals != "" || u.AccountField != "" {
			return fmt.Errorf("entity %s: choice_row_style.for_user: func задаётся вместо card/field/equals, не вместе", e.Name)
		}
		if mod, fn, ok := strings.Cut(u.Func, "."); !ok || strings.TrimSpace(mod) == "" || strings.TrimSpace(fn) == "" || strings.Contains(fn, ".") {
			return fmt.Errorf("entity %s: choice_row_style.for_user: func %q — ожидалось Модуль.Функция", e.Name, u.Func)
		}
		return nil
	}
	card := byName[u.Card]
	if card == nil {
		return fmt.Errorf("entity %s: choice_row_style.for_user: неизвестный справочник карточки %q", e.Name, u.Card)
	}
	account, err := u.CardAccountField(card)
	if err != nil {
		return fmt.Errorf("entity %s: choice_row_style.for_user: %w", e.Name, err)
	}
	if choiceStyleField(card, account) == nil || choiceStyleField(card, account).RefEntity != "_users" {
		return fmt.Errorf("entity %s: choice_row_style.for_user: %s.%s — не reference:_users", e.Name, card.Name, account)
	}
	field := choiceStyleField(card, u.Field)
	if field == nil {
		return fmt.Errorf("entity %s: choice_row_style.for_user: у %s нет реквизита %q", e.Name, card.Name, u.Field)
	}
	catalog, name, ok := u.EqualsParts()
	if !ok {
		return fmt.Errorf("entity %s: choice_row_style.for_user: equals %q — ожидалось <Справочник>.<предопределённый элемент>", e.Name, u.Equals)
	}
	if field.RefEntity != catalog {
		return fmt.Errorf("entity %s: choice_row_style.for_user: %s.%s ссылается на %s, а equals — на %s", e.Name, card.Name, u.Field, field.RefEntity, catalog)
	}
	target := byName[catalog]
	if target == nil {
		return fmt.Errorf("entity %s: choice_row_style.for_user: неизвестный справочник %q в equals", e.Name, catalog)
	}
	for _, p := range target.Predefined {
		if p != nil && p.Name == name {
			return nil
		}
	}
	return fmt.Errorf("entity %s: choice_row_style.for_user: у %s нет предопределённого %q", e.Name, catalog, name)
}

func choiceStyleField(e *Entity, name string) *Field {
	for i := range e.Fields {
		if e.Fields[i].Name == name {
			return &e.Fields[i]
		}
	}
	return nil
}
