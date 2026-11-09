package ui

import (
	"context"
	"fmt"
	"html"
	"html/template"
	"strings"
	"unicode"

	"github.com/google/uuid"
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
//   - \x00ref_style — CSS строки (только csssafe-цвета, жирный, курсив);
//   - \x00ref_style_bg — отдельно фон: подсветка активной строки в подборе и быстром
//     вводе перекрашивает фон и потом возвращает именно его, а не пустоту.
const (
	// NUL не может входить в SQL-имя: оформление не пересекается с реквизитами,
	// как и journalRowStyleKey. JSON передаёт NUL экранированным.
	refOptionStyleKey   = "\x00ref_style"
	refOptionStyleBgKey = "\x00ref_style_bg"
	// refOptionDisabledKey — строка видна, но не выбирается (правило с
	// disabled: true, например архивная реклама).
	refOptionDisabledKey = "\x00ref_disabled"
)

// Флаги оформления формы, где выбирают (choice_style_params поля → when_param
// правил справочника), едут в контексте запроса: выдача вариантов глубоко в
// referenceOptionsWithParams формы не знает.
type choiceStyleParamsKey struct{}

func withChoiceStyleParams(ctx context.Context, params map[string]bool) context.Context {
	if len(params) == 0 {
		return ctx
	}
	return context.WithValue(ctx, choiceStyleParamsKey{}, params)
}

func choiceStyleParamsFrom(ctx context.Context) map[string]bool {
	params, _ := ctx.Value(choiceStyleParamsKey{}).(map[string]bool)
	return params
}

// Только вычисленное оформление имеет этот тип. Одноимённые реквизиты
// справочника остаются данными и не могут стать CSS в шаблоне или проекции.
type refOptionCSS string

// choiceRowStyler — функция стиля строки для refEntity и текущего
// пользователя, или nil: справочник без оформления либо пользователь не
// подходит под for_user.
func (s *Server) choiceRowStyler(ctx context.Context, refEntity *metadata.Entity, rows []map[string]any) func(row map[string]any) (metadata.FormCellStyle, bool) {
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
	params := choiceStyleParamsFrom(ctx)
	inFolder := s.choiceStyleFolders(ctx, refEntity, rows)
	fromValues := s.choiceStyleFromValues(ctx, refEntity, rows)
	return func(row map[string]any) (metadata.FormCellStyle, bool) {
		// SQLite List возвращает boolean как int64, а GetByIDs — как bool.
		// Выражения DSL должны видеть одинаковые типы на обоих путях.
		values := make(map[string]any, len(row))
		for key, value := range row {
			values[key] = value
		}
		for _, field := range refEntity.Fields {
			if field.Type != metadata.FieldTypeBool {
				continue
			}
			for key, value := range values {
				if strings.EqualFold(key, field.Name) {
					values[key] = asBool(value)
				}
			}
		}
		var out metadata.FormCellStyle
		disabled := false
		id := strings.ToLower(refValueString(row["id"]))
		for _, rule := range st.Rules {
			if rule.When != "" && !choiceStyleWhen(ev, rule.When, values) {
				continue
			}
			if rule.WhenParam != "" && !params[strings.ToLower(rule.WhenParam)] {
				continue
			}
			if rule.WhenInFolder != "" && !inFolder[rule.WhenInFolder][id] {
				continue
			}
			style := rule.Style
			if rule.ColorFrom != "" {
				if c := csssafe.Color(fromValues[rule.ColorFrom][id]); c != "" {
					style.Color = c
				}
			}
			if rule.BackgroundFrom != "" {
				if c := csssafe.Color(fromValues[rule.BackgroundFrom][id]); c != "" {
					style.Background = c
				}
			}
			out = mergeFormConditionalStyle(out, style)
			disabled = disabled || rule.Disabled
		}
		return out, disabled
	}
}

// choiceStyleFolders — для правил when_in_folder: какие строки выдачи лежат в
// предопределённой группе (на любой глубине). Одна выборка на группу.
func (s *Server) choiceStyleFolders(ctx context.Context, refEntity *metadata.Entity, rows []map[string]any) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	if s.store == nil {
		return out
	}
	var ids []uuid.UUID
	for _, row := range rows {
		if id, err := uuid.Parse(refValueString(row["id"])); err == nil {
			ids = append(ids, id)
		}
	}
	for _, rule := range refEntity.ChoiceRowStyle.Rules {
		if rule.WhenInFolder == "" || out[rule.WhenInFolder] != nil {
			continue
		}
		out[rule.WhenInFolder] = map[string]bool{}
		if len(ids) == 0 {
			continue
		}
		for _, root := range s.choiceStyleFolderRoots(ctx, refEntity, rule.WhenInFolder) {
			set, err := s.store.IDsInSubtree(ctx, refEntity, root, ids)
			if err != nil {
				uiLog().Warn("choice_row_style: when_in_folder не вычислен", "entity", refEntity.Name, "folder", rule.WhenInFolder, "err", err)
				continue
			}
			for id := range set {
				out[rule.WhenInFolder][id] = true
			}
		}
	}
	return out
}

// choiceStyleFolderRoots — группы правила when_in_folder: предопределённая с
// этим именем, иначе все группы с таким наименованием.
func (s *Server) choiceStyleFolderRoots(ctx context.Context, refEntity *metadata.Entity, name string) []uuid.UUID {
	if raw, err := s.store.GetPredefinedIDStr(ctx, refEntity.Name, name); err == nil {
		if id, perr := uuid.Parse(strings.TrimSpace(raw)); perr == nil && id != uuid.Nil {
			return []uuid.UUID{id}
		}
	}
	roots, err := s.store.FolderIDsByName(ctx, refEntity, name)
	if err != nil {
		uiLog().Warn("choice_row_style: группа when_in_folder не найдена", "entity", refEntity.Name, "folder", name, "err", err)
		return nil
	}
	return roots
}

// choiceStyleFromValues — значения color_from/background_from по строкам:
// «Реквизит» — из самой строки, «Ссылка.Реквизит» — одной выборкой из
// справочника, на который ссылаются (с теми же правами чтения и масками, что у
// пользователя: цвет не должен быть каналом утечки закрытого реквизита).
func (s *Server) choiceStyleFromValues(ctx context.Context, refEntity *metadata.Entity, rows []map[string]any) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, rule := range refEntity.ChoiceRowStyle.Rules {
		for _, path := range []string{rule.ColorFrom, rule.BackgroundFrom} {
			if path == "" || out[path] != nil {
				continue
			}
			out[path] = s.choiceStylePathValues(ctx, refEntity, rows, path)
		}
	}
	return out
}

func (s *Server) choiceStylePathValues(ctx context.Context, refEntity *metadata.Entity, rows []map[string]any, path string) map[string]string {
	values := map[string]string{}
	ref, field := metadata.ChoiceStylePath(path)
	if ref == "" {
		for _, row := range rows {
			for key, value := range row {
				if strings.EqualFold(key, field) {
					if text, ok := value.(string); ok {
						values[strings.ToLower(refValueString(row["id"]))] = text
					}
				}
			}
		}
		return values
	}
	refField, ok := entityFieldByName(refEntity, ref)
	if !ok || s.store == nil || s.reg == nil {
		return values
	}
	holder := s.reg.GetEntity(refField.RefEntity)
	if holder == nil {
		return values
	}
	attr, ok := entityFieldByName(holder, field)
	if !ok || choiceAttrMasked(s.fieldDecisions(ctx, holder), attr.Name) {
		return values
	}
	byRow := map[string]string{}
	var ids []uuid.UUID
	seen := map[uuid.UUID]bool{}
	for _, row := range rows {
		var target string
		for key, value := range row {
			if strings.EqualFold(key, ref) {
				target = refValueString(value)
			}
		}
		id, err := uuid.Parse(strings.TrimSpace(target))
		if err != nil || id == uuid.Nil {
			continue
		}
		byRow[strings.ToLower(refValueString(row["id"]))] = id.String()
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return values
	}
	params, err := s.rowFilterFor(ctx, holder, "read", storage.ListParams{})
	if err != nil {
		return values
	}
	got, err := s.store.GetFieldsByIDsFiltered(ctx, holder, ids, []metadata.Field{attr}, params.RowFilter)
	if err != nil {
		uiLog().Warn("choice_row_style: цвет из реквизита не прочитан", "entity", refEntity.Name, "path", path, "err", err)
		return values
	}
	for rowID, targetID := range byRow {
		if r, ok := got[targetID]; ok {
			if text, ok := r[attr.Name].(string); ok {
				values[rowID] = text
			}
		}
	}
	return values
}

// choiceStyleWhen — условие правила. Имя реквизита — его истинность (так же
// вычисляет и выражение; короткий путь нужен серверу без интерпретатора).
func choiceStyleWhen(ev compose.Evaluator, when string, row map[string]any) bool {
	if isPlainIdent(when) {
		for key, value := range row {
			if strings.EqualFold(key, when) {
				return asBool(value)
			}
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
	styler := s.choiceRowStyler(ctx, refEntity, rows)
	for _, row := range rows {
		var st metadata.FormCellStyle
		disabled := false
		if styler != nil {
			st, disabled = styler(row)
		}
		// Удаляем только прежнее служебное оформление. Обычные реквизиты
		// сохраняются и для условий, и для статического choice_preview.
		delete(row, refOptionStyleKey)
		delete(row, refOptionStyleBgKey)
		delete(row, refOptionDisabledKey)
		if disabled {
			row[refOptionDisabledKey] = true
		}
		if css := cssOfForm(st); css != "" {
			row[refOptionStyleKey] = refOptionCSS(css)
			if bg := csssafe.Color(st.Background); bg != "" {
				row[refOptionStyleBgKey] = refOptionCSS(bg)
			}
		}
	}
}

// refOptionStyle — атрибуты <option> варианта выбора (функция шаблона): style
// оформления и маркер строки, которую выбирать нельзя.
func refOptionStyle(row any) template.HTMLAttr {
	m, _ := row.(map[string]any)
	attrs := ""
	if css, _ := m[refOptionStyleKey].(refOptionCSS); css != "" {
		attrs += ` style="` + html.EscapeString(string(css)) + `"`
	}
	if disabled, _ := m[refOptionDisabledKey].(bool); disabled {
		attrs += ` data-ob-ref-disabled="1"`
	}
	return template.HTMLAttr(attrs) //nolint:gosec // G203: CSS собран из csssafe-значений и экранирован
}

// refOptionDisabled — disabled у <option> строки, которую выбирать нельзя.
// Шаблон зовёт его только для НЕвыбранной строки: браузер не отправляет
// значение disabled-варианта, и сохранённая архивная ссылка молча стёрлась бы
// при записи формы.
func refOptionDisabled(row any) template.HTMLAttr {
	m, _ := row.(map[string]any)
	if disabled, _ := m[refOptionDisabledKey].(bool); disabled {
		return "disabled"
	}
	return ""
}

// choiceStyleParamValues — флаги choice_style_params поля из значений формы.
// Путь «Объект.Флаг» — значение самого поля; «Объект.Ссылка.Флаг» — булев
// реквизит записи, на которую ссылается поле (читается с правами и масками
// пользователя). Ошибка, пустая ссылка или закрытая запись — флаг ложен:
// оформление вторично и не должно ломать выбор.
func (s *Server) choiceStyleParamValues(ctx context.Context, owner *metadata.Entity, form *metadata.FormModule, element *metadata.FormElement, sources map[string]string) map[string]bool {
	if element == nil || len(element.ChoiceStyleParams) == 0 {
		return nil
	}
	out := make(map[string]bool, len(element.ChoiceStyleParams))
	for name, path := range element.ChoiceStyleParams {
		key := strings.ToLower(strings.TrimSpace(name))
		source, ok := metadata.ParseFormChoiceSource(path)
		if !ok {
			continue
		}
		raw := strings.TrimSpace(sources[strings.TrimSpace(path)])
		if !source.Deep() {
			out[key] = asBool(raw)
			continue
		}
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil || s.reg == nil || s.store == nil {
			continue
		}
		lead := s.reg.GetEntity(formChoiceRefEntity(owner, form, source.Root+"."+source.Field))
		if lead == nil {
			continue
		}
		attr, exists := entityFieldByName(lead, source.Attr)
		if !exists || attr.Type != metadata.FieldTypeBool || choiceAttrMasked(s.fieldDecisions(ctx, lead), attr.Name) {
			continue
		}
		params, err := s.rowFilterFor(ctx, lead, "read", storage.ListParams{})
		if err != nil {
			continue
		}
		rows, err := s.store.GetFieldsByIDsFiltered(ctx, lead, []uuid.UUID{id}, []metadata.Field{attr}, params.RowFilter)
		if err != nil {
			continue
		}
		if row, ok := rows[id.String()]; ok {
			out[key] = asBool(row[attr.Name])
		}
	}
	return out
}

// choiceValueCSS — оформление выбранного значения для самого поля
// (choice_style_value): CSS строки выбора с этим id, с «;» впереди, чтобы
// дописать к собственному style поля. Нет строки или оформления — пусто.
func choiceValueCSS(rows []map[string]any, value any) template.CSS {
	id := strings.ToLower(strings.TrimSpace(refValueString(value)))
	if id == "" {
		return ""
	}
	for _, row := range rows {
		if strings.ToLower(refValueString(row["id"])) != id {
			continue
		}
		if css, _ := row[refOptionStyleKey].(refOptionCSS); css != "" {
			return template.CSS(";" + string(css)) //nolint:gosec // G203: CSS собран из csssafe-значений
		}
		return ""
	}
	return ""
}
