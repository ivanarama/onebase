package ui

// HTTP-обработчики регистров (накопления/сведений) и констант.
// Выделено из handlers.go (план 55, этап 1) — перенос as-is.

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/exchange"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func (s *Server) registerMovements(w http.ResponseWriter, r *http.Request) {
	name := capitalize(chi.URLParam(r, "name"))
	reg := s.reg.GetRegister(name)
	if reg == nil {
		http.Error(w, "unknown register: "+name, 404)
		return
	}
	if !s.requirePerm(w, r, "register", reg.Name, "read") {
		return
	}
	flt := parseRegFilter(r, reg.Dimensions, true /*periodic — у движений всегда есть период*/)
	var ok bool
	flt, ok = s.applyRegRowFilter(w, r, "register", reg.Name, "read", storage.RegisterPredicateEntity(reg), flt)
	if !ok {
		return
	}
	rows, err := s.store.GetMovements(r.Context(), name, reg, flt)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.resolveRegisterRows(r.Context(), rows, reg)
	s.render(w, r, "page-register-movements", map[string]any{
		"Register":   reg,
		"Rows":       rows,
		"Filter":     filterFormValues(r, reg.Dimensions),
		"RefOpts":    s.loadRefOpts(r.Context(), reg.Dimensions, filterFormValues(r, reg.Dimensions)),
		"HasFilters": !flt.IsEmpty(),
	})
}

func (s *Server) registerBalances(w http.ResponseWriter, r *http.Request) {
	name := capitalize(chi.URLParam(r, "name"))
	reg := s.reg.GetRegister(name)
	if reg == nil {
		http.Error(w, "unknown register: "+name, 404)
		return
	}
	if !s.requirePerm(w, r, "register", reg.Name, "read") {
		return
	}
	// Остатки: только «на дату» (to) + измерения; from игнорируется в storage.
	flt := parseRegFilter(r, reg.Dimensions, true)
	var ok bool
	flt, ok = s.applyRegRowFilter(w, r, "register", reg.Name, "read", storage.RegisterPredicateEntity(reg), flt)
	if !ok {
		return
	}
	rows, err := s.store.GetBalances(r.Context(), name, reg, flt)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.resolveRegisterRows(r.Context(), rows, reg)
	s.render(w, r, "page-register-balances", map[string]any{
		"Register":   reg,
		"Rows":       rows,
		"Filter":     filterFormValues(r, reg.Dimensions),
		"RefOpts":    s.loadRefOpts(r.Context(), reg.Dimensions, filterFormValues(r, reg.Dimensions)),
		"HasFilters": !flt.IsEmpty(),
	})
}

// parseRegFilter собирает storage.RegFilter из query-параметров формы отбора:
// flt_<ИмяИзмерения> для измерений (для ссылочных — UUID из select), from/to —
// границы периода (формат 2006-01-02 из <input type="date">). Период читается
// только если periodic. Имена измерений берутся из fields — это же страхует
// storage от инъекции (там имена ещё раз сверяются с метаданными). #45.
func parseRegFilter(r *http.Request, fields []metadata.Field, periodic bool) storage.RegFilter {
	q := r.URL.Query()
	f := storage.RegFilter{Dims: map[string]string{}}
	for _, fld := range fields {
		v := strings.TrimSpace(q.Get("flt_" + fld.Name))
		if v != "" {
			f.Dims[fld.Name] = v
		}
	}
	if periodic {
		if t := parseFilterDate(q.Get("from")); t != nil {
			f.From = t
		}
		if t := parseFilterDate(q.Get("to")); t != nil {
			// Конец выбранного дня (последняя наносекунда), чтобы отбор «по дату»
			// включал весь день: period <= to сравнивается с TIMESTAMP (#45).
			// Считаем как полночь следующего календарного дня минус наносекунду —
			// в зонах с переходом часов (DST) это корректнее, чем Add(24h).
			y, mo, da := t.Date()
			endOfDay := time.Date(y, mo, da+1, 0, 0, 0, 0, t.Location()).Add(-time.Nanosecond)
			f.To = &endOfDay
		}
	}
	return f
}

func parseFilterDate(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return &t
	}
	return nil
}

// filterFormValues возвращает текущие значения формы отбора (для сохранения
// выбора): flt_<Имя> по измерениям + from/to.
func filterFormValues(r *http.Request, fields []metadata.Field) map[string]string {
	q := r.URL.Query()
	vals := map[string]string{
		"from": strings.TrimSpace(q.Get("from")),
		"to":   strings.TrimSpace(q.Get("to")),
	}
	for _, fld := range fields {
		vals[fld.Name] = strings.TrimSpace(q.Get("flt_" + fld.Name))
	}
	return vals
}

// loadRefOpts загружает опции [{id,_label}] для ссылочных измерений (обобщение
// loadInfoRegRefOpts на произвольный набор полей; используется и для регистров
// накопления, и для регистров сведений). #45.
func (s *Server) loadRefOpts(ctx context.Context, fields []metadata.Field, values map[string]string) map[string][]map[string]any {
	opts := make(map[string][]map[string]any)
	for _, f := range fields {
		if f.RefEntity == "" {
			continue
		}
		opts[f.Name] = []map[string]any{}
		refEntity := s.reg.GetEntity(f.RefEntity)
		if refEntity == nil {
			continue
		}
		rows, err := s.initialReferenceOptions(ctx, refEntity, refOptionsFilter, []string{values[f.Name]})
		if err != nil {
			continue
		}
		opts[f.Name] = rows
	}
	return opts
}

func (s *Server) resolveRegisterRows(ctx context.Context, rows []map[string]any, reg *metadata.Register) {
	// Резолвим UUID и в измерениях, и в атрибутах: reference-атрибут
	// (например Организация) тоже хранит UUID и должен показываться именем.
	refFields := append(append([]metadata.Field{}, reg.Dimensions...), reg.Attributes...)
	cols := make([]refCol, len(refFields))
	for i, f := range refFields {
		cols[i] = refCol{Key: f.Name, RefEntity: f.RefEntity}
	}
	s.resolveRefColumns(ctx, rows, cols, "")

	// recorder label
	for _, row := range rows {
		recType, _ := row["recorder_type"].(string)
		recIDStr := asString(row["recorder"])
		if recType != "" && recIDStr != "" {
			if recID, err := uuid.Parse(recIDStr); err == nil {
				if entity := s.reg.GetEntityBySlug(recType); entity != nil {
					if docRow, err2 := s.store.GetByID(ctx, entity.Name, recID, entity); err2 == nil {
						// Представление регистратора едет в список движений, то
						// есть номер и дата документа обязаны подчиняться той же
						// полевой политике, что и список самого документа.
						s.maskRecord(ctx, entity, docRow)
						num := fmt.Sprintf("%v", docRow["Номер"])
						date := regFmtDate(docRow["Дата"])
						row["recorder_label"] = fmt.Sprintf("%s №%s от %s", entity.Name, num, date)
					}
				}
			}
		}
	}
}

// resolveInfoRegRows подменяет UUID ссылочных измерений и ресурсов регистра
// сведений на наименования (issue #44 — в списке регистра сведений показывались
// id вместо представлений). Зеркало resolveRegisterRows.
func (s *Server) resolveInfoRegRows(ctx context.Context, rows []map[string]any, ir *metadata.InfoRegister) {
	refFields := append(append([]metadata.Field{}, ir.Dimensions...), ir.Resources...)
	var cols []refCol
	for _, f := range refFields {
		if f.RefEntity == "" {
			continue
		}
		cols = append(cols, refCol{Key: f.Name, RefEntity: f.RefEntity})
	}
	if len(cols) == 0 {
		return
	}
	s.resolveRefColumns(ctx, rows, cols, "_label")
}

// resolveAccountRows резолвит reference-субконто (хранятся под ключами субконто<N>)
// в наименования. String/enum-субконто оставляет как есть.
func (s *Server) resolveAccountRows(ctx context.Context, rows []map[string]any, ar *metadata.AccountRegister) {
	var cols []refCol
	for i, f := range ar.Subconto {
		if f.RefEntity == "" {
			continue
		}
		cols = append(cols, refCol{Key: metadata.SubcontoColumn(i + 1), RefEntity: f.RefEntity})
	}
	if len(cols) == 0 {
		return
	}
	s.resolveRefColumns(ctx, rows, cols, "")
}

func (s *Server) getInfoReg(w http.ResponseWriter, r *http.Request) *metadata.InfoRegister {
	name := capitalize(chi.URLParam(r, "name"))
	ir := s.reg.GetInfoRegister(name)
	if ir == nil {
		http.Error(w, "unknown info register: "+name, 404)
	}
	return ir
}

func (s *Server) infoRegList(w http.ResponseWriter, r *http.Request) {
	ir := s.getInfoReg(w, r)
	if ir == nil {
		return
	}
	if !s.requirePerm(w, r, "inforeg", ir.Name, "read") {
		return
	}
	flt := parseRegFilter(r, ir.Dimensions, ir.Periodic)
	var ok bool
	flt, ok = s.applyRegRowFilter(w, r, "inforeg", ir.Name, "read", storage.InfoRegisterPredicateEntity(ir), flt)
	if !ok {
		return
	}
	rows, err := s.store.InfoRegList(r.Context(), ir, flt)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// Mask before resolving references: a protected UUID must not turn into a
	// readable label, and the detail-panel payload must receive only gated rows.
	s.maskInfoRegRecords(r.Context(), ir, rows)
	s.resolveInfoRegRows(r.Context(), rows, ir)
	s.render(w, r, "page-inforeg-list", map[string]any{
		"InfoReg":    ir,
		"Rows":       rows,
		"Filter":     filterFormValues(r, ir.Dimensions),
		"RefOpts":    s.loadRefOpts(r.Context(), ir.Dimensions, filterFormValues(r, ir.Dimensions)),
		"HasFilters": !flt.IsEmpty(),
	})
}

func (s *Server) loadInfoRegRefOpts(ctx context.Context, ir *metadata.InfoRegister, values map[string]string) map[string][]map[string]any {
	return s.loadRefOpts(ctx, ir.Dimensions, values)
}

func (s *Server) infoRegForm(w http.ResponseWriter, r *http.Request) {
	ir := s.getInfoReg(w, r)
	if ir == nil {
		return
	}
	if !s.requirePerm(w, r, "inforeg", ir.Name, "write") {
		return
	}
	now := time.Now().Format("2006-01-02")
	s.render(w, r, "page-inforeg-form", map[string]any{
		"InfoReg": ir,
		"Values":  map[string]string{"period": now},
		"Error":   "",
		"RefOpts": s.loadInfoRegRefOpts(r.Context(), ir, map[string]string{}),
	})
}

func (s *Server) infoRegSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	ir := s.getInfoReg(w, r)
	if ir == nil {
		return
	}
	if !s.requirePerm(w, r, "inforeg", ir.Name, "write") {
		return
	}
	s.limitMultipartRequest(w, r)
	if err := parseBoundedForm(r, defaultFormMemoryBytes); err != nil {
		http.Error(w, s.errText(r, err), uploadErrorStatus(err))
		return
	}

	var periodPtr *time.Time
	if ir.Periodic {
		pStr := r.FormValue("period")
		if pStr == "" {
			s.render(w, r, "page-inforeg-form", map[string]any{
				"InfoReg": ir,
				"Values":  formValuesFromRequest(r, ir),
				"Error":   "Период обязателен для периодического регистра",
				"RefOpts": s.loadInfoRegRefOpts(r.Context(), ir, formValuesFromRequest(r, ir)),
			})
			return
		}
		for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
			if t, err := time.ParseInLocation(layout, pStr, time.Local); err == nil {
				periodPtr = &t
				break
			}
		}
		if periodPtr == nil {
			s.render(w, r, "page-inforeg-form", map[string]any{
				"InfoReg": ir,
				"Values":  formValuesFromRequest(r, ir),
				"Error":   "Неверный формат даты периода",
				"RefOpts": s.loadInfoRegRefOpts(r.Context(), ir, formValuesFromRequest(r, ir)),
			})
			return
		}
	}

	dims := parseInfoRegFields(r, ir.Dimensions)
	resources := parseInfoRegFields(r, ir.Resources)

	// Общая точка записи (см. inforeg_write.go): строковая политика по
	// существующей и по новой записи, upsert и регистрация в планах обмена в
	// одной транзакции. DSL идёт туда же — копия разошлась бы именно в
	// проверке, которую легче всего забыть.
	denied := false
	allow := func(ctx context.Context, row map[string]any) error {
		if !s.rowAllowedFor(w, r, "inforeg", ir.Name, "write", storage.InfoRegisterPredicateEntity(ir), row) {
			denied = true
			return errRowPolicyDenied
		}
		return nil
	}
	if err := s.infoRegWrite(r.Context(), ir, dims, resources, periodPtr, allow); err != nil {
		if denied {
			return // rowAllowedFor уже отправил ответ
		}
		s.render(w, r, "page-inforeg-form", map[string]any{
			"InfoReg": ir,
			"Values":  formValuesFromRequest(r, ir),
			"Error":   err.Error(),
			"RefOpts": s.loadInfoRegRefOpts(r.Context(), ir, formValuesFromRequest(r, ir)),
		})
		return
	}
	http.Redirect(w, r, "/ui/inforeg/"+strings.ToLower(ir.Name), http.StatusFound)
}

func (s *Server) infoRegDelete(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	ir := s.getInfoReg(w, r)
	if ir == nil {
		return
	}
	if !s.requirePerm(w, r, "inforeg", ir.Name, "delete") {
		return
	}
	s.limitMultipartRequest(w, r)
	if err := parseBoundedForm(r, defaultFormMemoryBytes); err != nil {
		http.Error(w, s.errText(r, err), uploadErrorStatus(err))
		return
	}

	var periodPtr *time.Time
	if ir.Periodic {
		// Период берём из машинного ключа period_key (его кладёт InfoRegList в
		// hidden-поле списка). Если ключ не разобран — ОТКАЗЫВАЕМ в удалении:
		// иначе InfoRegDelete с nil-периодом снесёт все периоды комбинации
		// измерений (критическая потеря данных).
		t, ok := storage.ParseRegPeriod(r.FormValue("period"))
		if !ok {
			http.Error(w, s.tr(s.resolveLang(r), "Не удалось определить период записи для удаления"), http.StatusBadRequest)
			return
		}
		periodPtr = &t
	}
	// Ключ удаления не должен строиться из ОТОБРАЖАЕМЫХ значений: форма списка
	// кладёт в hidden-поля то, что видит пользователь, а под маской это «••••••».
	// DELETE сравнивал маску с реальным значением, не находил ни строки — и
	// молча отвечал успехом: пользователь считал запись удалённой, а она жива
	// (#861). Пока измерение замаскировано, ключа у этой роли попросту нет —
	// отказываем явно, как уже делаем при неразобранном периоде.
	if masked := s.maskedInfoRegDimensions(r.Context(), ir); len(masked) > 0 {
		http.Error(w, s.tr(s.resolveLang(r),
			"Удаление невозможно: у вашей роли закрыт доступ к ключевому измерению")+" ("+strings.Join(masked, ", ")+")",
			http.StatusForbidden)
		return
	}
	dims := parseInfoRegFields(r, ir.Dimensions)
	row, _ := s.infoRegExistingPolicyRow(r.Context(), ir, dims, periodPtr)
	// Удалять нечего — не успех. Прежде «не нашли ни строки» было неотличимо от
	// «удалили»: ответ один и тот же.
	if row == nil {
		http.Error(w, s.tr(s.resolveLang(r), "Запись не найдена: возможно, она уже удалена"), http.StatusNotFound)
		return
	}
	if !s.rowAllowedFor(w, r, "inforeg", ir.Name, "delete", storage.InfoRegisterPredicateEntity(ir), row) {
		return
	}
	plans := s.reg.ExchangePlans()
	if err := s.store.WithTx(r.Context(), func(ctx context.Context) error {
		if err := s.store.InfoRegDelete(ctx, ir, dims, periodPtr); err != nil {
			return err
		}
		return exchange.RegisterInfoRegOnSave(ctx, s.store, plans, ir, dims, true)
	}); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/ui/inforeg/"+strings.ToLower(ir.Name), http.StatusFound)
}

func parseInfoRegFields(r *http.Request, fields []metadata.Field) map[string]any {
	result := make(map[string]any, len(fields))
	for _, f := range fields {
		val := r.FormValue(f.Name)
		if val == "" {
			result[f.Name] = nil
			continue
		}
		result[f.Name] = parseInfoRegFieldValue(f, val)
	}
	return result
}

func infoRegPolicyRow(ir *metadata.InfoRegister, dims, resources map[string]any, period *time.Time) map[string]any {
	row := make(map[string]any, len(dims)+len(resources)+1)
	if period != nil {
		row["period"] = *period
	}
	for k, v := range dims {
		row[k] = v
	}
	for k, v := range resources {
		row[k] = v
	}
	return row
}

func (s *Server) infoRegExistingPolicyRow(ctx context.Context, ir *metadata.InfoRegister, dims map[string]any, period *time.Time) (map[string]any, bool) {
	flt := storage.RegFilter{Dims: map[string]string{}}
	for k, v := range dims {
		if v != nil {
			flt.Dims[k] = fmt.Sprintf("%v", v)
		}
	}
	if period != nil {
		flt.From = period
		flt.To = period
	}
	rows, err := s.store.InfoRegList(ctx, ir, flt)
	if err == nil && len(rows) > 0 {
		if period != nil {
			rows[0]["period"] = *period
		}
		return rows[0], true
	}
	return infoRegPolicyRow(ir, dims, nil, period), false
}

func parseInfoRegFieldValue(f metadata.Field, val string) any {
	switch f.Type {
	case metadata.FieldTypeDate:
		for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
			if t, err := time.ParseInLocation(layout, val, time.Local); err == nil {
				return t
			}
		}
		return val
	case metadata.FieldTypeBool:
		return val == "true" || val == "on"
	default:
		return val
	}
}

func (s *Server) constantsList(w http.ResponseWriter, r *http.Request) {
	values, _ := s.store.ListConstants(r.Context())
	valStrs := make(map[string]string, len(values))
	for k, v := range values {
		valStrs[k] = fmt.Sprintf("%v", v)
	}
	s.renderConstantsPage(w, r, valStrs, r.URL.Query().Get("saved") == "1", "")
}

// renderConstantsPage рендерит форму «Константы» с заданными значениями. Общий
// путь для GET (сохранённые значения) и для ошибки валидации при POST (введённые
// значения + сообщение errMsg), чтобы оператор не терял ввод.
func (s *Server) renderConstantsPage(w http.ResponseWriter, r *http.Request, valStrs map[string]string, saved bool, errMsg string) {
	consts := s.reg.Constants()
	sort.Slice(consts, func(i, j int) bool { return consts[i].Name < consts[j].Name })

	lang := s.resolveLang(r)
	// Опции выбора для ссылочных и enum-констант.
	refOpts := make(map[string][]map[string]any)
	enumOpts := make(map[string][]EnumOption)
	for _, c := range consts {
		switch {
		case c.RefEntity != "":
			refEntity := s.reg.GetEntity(c.RefEntity)
			if refEntity == nil {
				continue
			}
			rows, err := s.initialReferenceOptions(r.Context(), refEntity, refOptionsChoice, []string{valStrs[c.Name]})
			if err != nil {
				continue
			}
			refOpts[c.Name] = rows
		case c.EnumName != "":
			en := s.reg.GetEnum(c.EnumName)
			if en == nil {
				continue
			}
			list := make([]EnumOption, 0, len(en.Values))
			for _, v := range en.Values {
				list = append(list, EnumOption{Value: v, Label: en.ValueTitle(v, lang)})
			}
			enumOpts[c.Name] = list
		}
	}

	s.render(w, r, "page-constants", map[string]any{
		"Constants": consts,
		"Values":    valStrs,
		"RefOpts":   refOpts,
		"EnumOpts":  enumOpts,
		"Saved":     saved,
		"Error":     errMsg,
	})
}

func (s *Server) constantsSave(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, s.errText(r, err), 400)
		return
	}
	consts := s.reg.Constants()
	lang := s.resolveLang(r)

	// Сначала собираем и валидируем все значения — записываем, только если
	// всё корректно (иначе получился бы частичный upsert части констант).
	submitted := make(map[string]string, len(consts))
	for _, c := range consts {
		submitted[c.Name] = strings.TrimSpace(r.FormValue(c.Name))
	}
	for _, c := range consts {
		if msg := s.validateConstant(r.Context(), c, lang, submitted[c.Name]); msg != "" {
			s.renderConstantsPage(w, r, submitted, false, msg)
			return
		}
	}

	// Запись констант и регистрация изменений в планах обмена (план 86) — в одной
	// транзакции, чтобы сбой регистрации откатывал запись (как для сущностей).
	plans := s.reg.ExchangePlans()
	err := s.store.WithTx(r.Context(), func(ctx context.Context) error {
		for _, c := range consts {
			val := submitted[c.Name]
			var v any
			if val != "" {
				v = val
			}
			if err := s.store.SetConstant(ctx, c.Name, v); err != nil {
				return err
			}
			if err := exchange.RegisterConstantOnSave(ctx, s.store, plans, c.Name); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/ui/constants?saved=1", http.StatusSeeOther)
}

// validateConstant проверяет введённое значение константы против её описания:
// обязательность (required), принадлежность домену перечисления (enum) и
// существование элемента справочника (reference). Возвращает пустую строку,
// если значение допустимо, иначе — понятное сообщение об ошибке (на русском).
func (s *Server) validateConstant(ctx context.Context, c *metadata.Constant, lang, val string) string {
	label := c.DisplayLabel(lang)
	if val == "" {
		if c.Required {
			return fmt.Sprintf("Константа «%s» обязательна для заполнения", label)
		}
		return ""
	}
	switch {
	case c.EnumName != "":
		en := s.reg.GetEnum(c.EnumName)
		if en == nil {
			return fmt.Sprintf("Константа «%s»: перечисление %s не найдено", label, c.EnumName)
		}
		for _, v := range en.Values {
			if v == val {
				return ""
			}
		}
		return fmt.Sprintf("Константа «%s»: недопустимое значение «%s» (не входит в перечисление %s)", label, val, c.EnumName)
	case c.RefEntity != "":
		ent := s.reg.GetEntity(c.RefEntity)
		if ent == nil {
			return fmt.Sprintf("Константа «%s»: справочник %s не найден", label, c.RefEntity)
		}
		id, err := uuid.Parse(val)
		if err != nil {
			return fmt.Sprintf("Константа «%s»: неверная ссылка «%s»", label, val)
		}
		if _, err := s.store.GetByID(ctx, c.RefEntity, id, ent); err != nil {
			return fmt.Sprintf("Константа «%s»: выбранный элемент не найден", label)
		}
	}
	return ""
}

func formValuesFromRequest(r *http.Request, ir *metadata.InfoRegister) map[string]string {
	vals := map[string]string{"period": r.FormValue("period")}
	for _, f := range ir.Dimensions {
		vals[f.Name] = r.FormValue(f.Name)
	}
	for _, f := range ir.Resources {
		vals[f.Name] = r.FormValue(f.Name)
	}
	return vals
}
