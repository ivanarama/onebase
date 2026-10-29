package ui

import (
	"context"
	"fmt"
	"html"
	"html/template"
	"strings"
	"unicode"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/csssafe"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/report/compose"
	"github.com/ivantit66/onebase/internal/storage"
)

// Оформление строк выбора ссылок (choice_row_style справочника): выпадающий
// список, форма подбора, быстрый ввод, ячейки ТЧ. Стиль считается на сервере
// вместе с выдачей вариантов и едет рядом с id/_label:
//   - _style    — CSS строки (только csssafe-цвета, жирный, курсив);
//   - _style_bg — отдельно фон: подсветка активной строки в подборе и быстром
//     вводе перекрашивает фон и потом возвращает именно его, а не пустоту.
const (
	refOptionStyleKey   = "_style"
	refOptionStyleBgKey = "_style_bg"
)

// choiceRowStyler — функция стиля строки для refEntity и текущего
// пользователя, или nil: справочник без оформления либо пользователь не
// подходит под for_user.
func (s *Server) choiceRowStyler(ctx context.Context, refEntity *metadata.Entity) func(row map[string]any) metadata.FormCellStyle {
	if refEntity == nil || refEntity.ChoiceRowStyle == nil || len(refEntity.ChoiceRowStyle.Rules) == 0 {
		return nil
	}
	st := refEntity.ChoiceRowStyle
	if len(st.ForRoles) > 0 && !choiceStyleHasRole(ctx, st.ForRoles) {
		return nil
	}
	if st.ForUser != nil && st.ForUser.Func != "" {
		if !s.choiceStyleFuncMatches(ctx, refEntity, st.ForUser.Func) {
			return nil
		}
	} else if st.ForUser != nil && !s.choiceStyleUserMatches(ctx, st.ForUser) {
		return nil
	}
	var ev compose.Evaluator
	if s.interp != nil {
		ev = newInterpEvaluator(s.interp)
	}
	return func(row map[string]any) metadata.FormCellStyle {
		var out metadata.FormCellStyle
		for _, rule := range st.Rules {
			if choiceStyleWhen(ev, rule.When, row) {
				out = mergeFormConditionalStyle(out, rule.Style)
			}
		}
		return out
	}
}

// choiceStyleWhen — условие правила. Имя реквизита — его истинность (так же
// вычисляет и выражение; короткий путь нужен серверу без интерпретатора).
func choiceStyleWhen(ev compose.Evaluator, when string, row map[string]any) bool {
	if isPlainIdent(when) {
		if v, ok := row[when]; ok {
			return asBool(v)
		}
	}
	if ev == nil {
		return false
	}
	ok, err := ev.EvalBool(when, compose.Row(row))
	return err == nil && ok
}

func isPlainIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || unicode.IsLetter(r) || (i > 0 && unicode.IsDigit(r)) {
			continue
		}
		return false
	}
	return true
}

// choiceStyleHasRole — у текущего пользователя есть одна из ролей for_roles.
// Администратор без такой роли оформления не получает — как в 1С, где оно
// привязано к подразделению, а не к правам.
func choiceStyleHasRole(ctx context.Context, roles []string) bool {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return false
	}
	for _, have := range user.Roles {
		if have == nil {
			continue
		}
		for _, want := range roles {
			if strings.EqualFold(have.Name, want) {
				return true
			}
		}
	}
	return false
}

// choiceStyleFuncMatches — for_user.func: экспортная функция общего модуля
// без параметров вернула Истина. Окружение то же, что у choice_preview_proc
// (Запрос, Перечисления, ПредопределённыеЗначения, Константы,
// ТекущийПользователь() с полями ИД и Имя), база — только чтение, а
// транзакция хоста откатывается всегда. Ошибка, нет функции или не Булево —
// оформления нет: подсветка вторична и не должна ломать выбор.
func (s *Server) choiceStyleFuncMatches(ctx context.Context, refEntity *metadata.Entity, name string) bool {
	if s.interp == nil || s.store == nil || s.reg == nil {
		return false
	}
	mod, fn, ok := strings.Cut(strings.TrimSpace(name), ".")
	if !ok {
		return false
	}
	proc := s.reg.GetModuleNamespacedProc(strings.TrimSpace(mod), strings.TrimSpace(fn))
	if proc == nil || !proc.Export || len(proc.Params) != 0 {
		uiLog().Warn("choice_row_style: for_user.func не найдена, не Экспорт или с параметрами", "func", name, "entity", refEntity.Name)
		return false
	}
	tx, txCtx, err := s.store.BeginTx(ctx)
	if err != nil {
		return false
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	roCtx := storage.ReadOnlyContext(txCtx)
	vars := s.buildChoicePreviewVars(roCtx, nil, refEntity, "", nil)
	var result any
	if err := s.interp.RunWithResult(proc, &interpreter.MapThis{M: map[string]any{}}, &result, vars); err != nil {
		uiLog().Warn("choice_row_style: ошибка for_user.func", "func", name, "entity", refEntity.Name, "err", err)
		return false
	}
	b, isBool := result.(bool)
	if !isBool {
		uiLog().Warn("choice_row_style: for_user.func вернула не Булево", "func", name, "entity", refEntity.Name, "result", fmt.Sprintf("%T", result))
		return false
	}
	return b
}

// choiceStyleUserMatches — реквизит карточки текущего пользователя равен
// предопределённому элементу for_user.equals (как «Подразделение = Колл центр»
// у формы выбора 1С). Без пользователя, карточки или значения — не подходит.
func (s *Server) choiceStyleUserMatches(ctx context.Context, u *metadata.ChoiceStyleUser) bool {
	user := auth.UserFromContext(ctx)
	if user == nil || user.ID == "" || s.store == nil {
		return false
	}
	card := s.reg.GetEntity(u.Card)
	if card == nil {
		return false
	}
	account, err := u.CardAccountField(card)
	if err != nil {
		return false
	}
	catalog, name, ok := u.EqualsParts()
	if !ok {
		return false
	}
	want, err := s.store.GetPredefinedIDStr(ctx, catalog, name)
	if err != nil || want == "" {
		return false
	}
	rows, err := s.store.List(ctx, card.Name, card, storage.ListParams{
		Filters: map[string]storage.FilterValue{account: {Value: user.ID}},
		Limit:   1,
	})
	if err != nil || len(rows) == 0 {
		return false
	}
	return strings.EqualFold(refValueString(rows[0][u.Field]), want)
}

// applyChoiceRowStyles кладёт стиль в строки вариантов выбора.
func (s *Server) applyChoiceRowStyles(ctx context.Context, refEntity *metadata.Entity, rows []map[string]any) {
	styler := s.choiceRowStyler(ctx, refEntity)
	if styler == nil {
		return
	}
	for _, row := range rows {
		st := styler(row)
		if css := cssOfForm(st); css != "" {
			row[refOptionStyleKey] = css
			if bg := csssafe.Color(st.Background); bg != "" {
				row[refOptionStyleBgKey] = bg
			}
		}
	}
}

// refOptionStyle — атрибут style у <option> варианта выбора (функция шаблона).
func refOptionStyle(row any) template.HTMLAttr {
	m, _ := row.(map[string]any)
	css, _ := m[refOptionStyleKey].(string)
	if css == "" {
		return ""
	}
	return template.HTMLAttr(` style="` + html.EscapeString(css) + `"`) //nolint:gosec // G203: CSS собран из csssafe-значений и экранирован
}
