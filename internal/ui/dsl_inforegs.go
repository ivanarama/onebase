package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/exchange"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// infoRegsRoot — DSL-глобал РегистрыСведений / InfoRegisters (план 119A).
//
//	Запись = РегистрыСведений.СостояниеОбменов.СоздатьМенеджерЗаписи();
//	Запись.Узел = Узел;                     // измерение
//	Запись.Состояние = "Готов";             // ресурс
//	Запись.Записать();
//
//	Запись.Прочитать();                     // заполняет ресурсы по ключу
//	Если Запись.Выбран() Тогда … КонецЕсли;
//	Запись.Удалить();
//
// До этого записать регистр сведений из конфигурации было нечем: движения
// документа умеют только подчинённые регистратору, а независимый заполнялся
// руками через форму или обменом. Читать при этом можно было всегда
// (РегистрСведений.X.СрезПоследних в языке запросов) — асимметрия по записи.
type infoRegsRoot struct {
	s      *Server
	ctxSrc docsCtxSource
}

func newInfoRegsRoot(s *Server, ctxSrc docsCtxSource) *infoRegsRoot {
	return &infoRegsRoot{s: s, ctxSrc: ctxSrc}
}

func (r *infoRegsRoot) Get(name string) any {
	ir := r.s.reg.GetInfoRegister(name)
	if ir == nil {
		return nil
	}
	return &infoRegProxy{s: r.s, ctxSrc: r.ctxSrc, ir: ir}
}

func (r *infoRegsRoot) Set(_ string, _ any) {}

type infoRegProxy struct {
	s      *Server
	ctxSrc docsCtxSource
	ir     *metadata.InfoRegister
}

func (p *infoRegProxy) Get(_ string) any    { return nil }
func (p *infoRegProxy) Set(_ string, _ any) {}

func (p *infoRegProxy) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "создатьменеджерзаписи", "createrecordmanager":
		// Регистр, подчинённый регистратору, программной записью не трогаем:
		// его строки принадлежат проведению документа, и ближайшее
		// перепроведение снесло бы записанное без предупреждения. Отказ на
		// создании менеджера, а не на Записать(), — ошибка приходит там, где
		// написана неверная строка.
		if p.ir.Recorder {
			interpreter.RaiseUserError("РегистрыСведений." + p.ir.Name +
				": регистр подчинён регистратору, пишите движениями в ОбработкаПроведения документа")
		}
		return newInfoRegRecord(p.s, p.ctxSrc, p.ir)
	case "создатьнаборзаписей", "createrecordset":
		if p.ir.Recorder {
			interpreter.RaiseUserError("РегистрыСведений." + p.ir.Name +
				": регистр подчинён регистратору, пишите движениями в ОбработкаПроведения документа")
		}
		return newInfoRegRecordSet(p.s, p.ctxSrc, p.ir)
	}
	interpreter.RaiseUserError("РегистрыСведений." + p.ir.Name + ": неизвестный метод «" + method +
		"» (доступны СоздатьМенеджерЗаписи, СоздатьНаборЗаписей)")
	return nil
}

// infoRegRecord — менеджер записи. Значения измерений/ресурсов/периода
// набираются присваиванием, как у объекта документа.
type infoRegRecord struct {
	s        *Server
	ctxSrc   docsCtxSource
	ir       *metadata.InfoRegister
	values   map[string]any
	period   *time.Time
	selected bool // Прочитать() нашла запись
}

func newInfoRegRecord(s *Server, ctxSrc docsCtxSource, ir *metadata.InfoRegister) *infoRegRecord {
	return &infoRegRecord{s: s, ctxSrc: ctxSrc, ir: ir, values: map[string]any{}}
}

func (r *infoRegRecord) ctx() context.Context {
	if r.ctxSrc != nil {
		return r.ctxSrc.Ctx()
	}
	return context.Background()
}

func (r *infoRegRecord) Get(name string) any {
	if isInfoRegSystemPeriod(r.ir, name) {
		if r.period == nil {
			return time.Time{}
		}
		return *r.period
	}
	if f := infoRegField(r.ir, name); f != nil {
		return r.values[f.Name]
	}
	return nil
}

func (r *infoRegRecord) Set(name string, v any) {
	if isInfoRegSystemPeriod(r.ir, name) {
		if t, ok := v.(time.Time); ok {
			r.period = &t
			return
		}
		if v == nil {
			r.period = nil
			return
		}
		interpreter.RaiseUserError("РегистрыСведений." + r.ir.Name + ".Период: ожидается дата")
		return
	}
	f := infoRegField(r.ir, name)
	if f == nil {
		// Опечатка в имени измерения тихо ушла бы в никуда, а запись легла бы
		// с пустым ключом — то есть не туда, где её потом ищут.
		interpreter.RaiseUserError("РегистрыСведений." + r.ir.Name + ": нет измерения или ресурса «" + name + "»")
		return
	}
	r.values[f.Name] = v
}

func (r *infoRegRecord) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "записать", "write":
		dims, res := r.split()
		if err := r.s.infoRegWrite(r.ctx(), r.ir, dims, res, r.period, r.access("write")); err != nil {
			interpreter.RaiseUserError("Записать(" + r.ir.Name + "): " + err.Error())
		}
		r.selected = true
		return nil
	case "прочитать", "read":
		// Единственный метод менеджера, который не проверял период: у
		// Записать()/Удалить() проверка была, а Прочитать() без Периода на
		// периодическом регистре молча читал произвольную строку — то есть
		// отдавал не ошибку, а чужие данные (#857).
		if err := infoRegCheckPeriod(r.ir, r.period); err != nil {
			interpreter.RaiseUserError("Прочитать(" + r.ir.Name + "): " + err.Error())
		}
		dims, _ := r.split()
		row, err := r.s.store.InfoRegGetExact(r.ctx(), r.ir, dims, r.period)
		if err != nil {
			interpreter.RaiseUserError("Прочитать(" + r.ir.Name + "): " + err.Error())
		}
		if row == nil {
			r.selected = false
			return false
		}
		if err := r.checkRead(row); err != nil {
			interpreter.RaiseUserError("Прочитать(" + r.ir.Name + "): " + err.Error())
		}
		for _, f := range r.ir.Resources {
			if v, ok := row[f.Name]; ok {
				r.values[f.Name] = v
			}
		}
		r.selected = true
		return true
	case "удалить", "delete":
		dims, _ := r.split()
		if err := r.s.infoRegRemove(r.ctx(), r.ir, dims, r.period, r.access("delete")); err != nil {
			interpreter.RaiseUserError("Удалить(" + r.ir.Name + "): " + err.Error())
		}
		r.selected = false
		return nil
	case "выбран", "selected":
		return r.selected
	}
	interpreter.RaiseUserError("МенеджерЗаписи(" + r.ir.Name + "): неизвестный метод «" + method +
		"» (доступны Записать, Прочитать, Удалить, Выбран)")
	return nil
}

// split разделяет набранные значения на измерения и ресурсы.
func (r *infoRegRecord) split() (dims, resources map[string]any) {
	dims = make(map[string]any, len(r.ir.Dimensions))
	resources = make(map[string]any, len(r.ir.Resources))
	for _, f := range r.ir.Dimensions {
		dims[f.Name] = r.values[f.Name]
	}
	for _, f := range r.ir.Resources {
		resources[f.Name] = r.values[f.Name]
	}
	return dims, resources
}

// access отдаёт проверку строковой политики для записи/удаления. В доверенном
// контексте (проведение, миграции) проверка не нужна — как у регистров
// накопления.
func (r *infoRegRecord) access(op string) infoRegAccess {
	if isTrustedDSLContext(r.ctx()) {
		return nil
	}
	return func(ctx context.Context, row map[string]any) error {
		dec, err := r.s.rowDecisionFor(ctx, "inforeg", r.ir.Name, op,
			storage.InfoRegisterPredicateEntity(r.ir))
		if err != nil {
			return err
		}
		if !dec.Allowed {
			return interpreter.ErrRowAccessDenied
		}
		if dec.Unrestricted || dec.Predicate == nil {
			return nil
		}
		if !r.s.matchRowPredicate(ctx, row, dec.Predicate) {
			return interpreter.ErrRowAccessDenied
		}
		return nil
	}
}

// checkRead применяет строковую политику к прочитанной записи: чтение через
// объект не должно показывать то, что скрыто в списке и в запросах.
func (r *infoRegRecord) checkRead(row map[string]any) error {
	allow := r.access("read")
	if allow == nil {
		return nil
	}
	return allow(r.ctx(), row)
}

func infoRegField(ir *metadata.InfoRegister, name string) *metadata.Field {
	for i := range ir.Dimensions {
		if strings.EqualFold(ir.Dimensions[i].Name, name) {
			return &ir.Dimensions[i]
		}
	}
	for i := range ir.Resources {
		if strings.EqualFold(ir.Resources[i].Name, name) {
			return &ir.Resources[i]
		}
	}
	return nil
}

func isPeriodName(name string) bool {
	low := strings.ToLower(name)
	return low == "период" || low == "period"
}

// isInfoRegSystemPeriod distinguishes the synthetic key component from a real
// metadata field on a non-periodic register. Periodic metadata is validated to
// reserve both spellings, while the historical non-periodic field Период stays
// addressable through the record manager and record set.
func isInfoRegSystemPeriod(ir *metadata.InfoRegister, name string) bool {
	return isPeriodName(name) && (ir.Periodic || infoRegField(ir, name) == nil)
}

// ─── Набор записей (план 119B) ────────────────────────────────────────────────
//
//	Набор = РегистрыСведений.ЛогОбмена.СоздатьНаборЗаписей();
//	Набор.Отбор.Узел = Узел;      // отбор — только по измерениям
//	Набор.Прочитать();            // текущее содержимое по отбору
//	Набор.Очистить();
//	Стр = Набор.Добавить();
//	Стр.Узел = Узел;
//	Стр.Событие = "Старт";
//	Набор.Записать();             // замещает содержимое по отбору
//
// «Записать» — это «удалить по отбору и вставить накопленное», одной
// транзакцией. Так же ведёт себя набор записей в 1С, и так же это спасает от
// полузаписи: сбой на середине откатывает всё, а не оставляет регистр с
// половиной старых и половиной новых строк.
//
// Отбор обязателен. Запись набора без отбора означала бы «снести регистр
// целиком» — самая дорогая опечатка из возможных.

type infoRegRecordSet struct {
	s      *Server
	ctxSrc docsCtxSource
	ir     *metadata.InfoRegister
	filter *infoRegFilter
	rows   []map[string]any
}

func newInfoRegRecordSet(s *Server, ctxSrc docsCtxSource, ir *metadata.InfoRegister) *infoRegRecordSet {
	return &infoRegRecordSet{s: s, ctxSrc: ctxSrc, ir: ir, filter: &infoRegFilter{ir: ir, values: map[string]any{}}}
}

func (rs *infoRegRecordSet) ctx() context.Context {
	if rs.ctxSrc != nil {
		return rs.ctxSrc.Ctx()
	}
	return context.Background()
}

func (rs *infoRegRecordSet) Get(name string) any {
	if strings.EqualFold(name, "Отбор") || strings.EqualFold(name, "Filter") {
		return rs.filter
	}
	return nil
}

func (rs *infoRegRecordSet) Set(string, any) {}

func (rs *infoRegRecordSet) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "прочитать", "read":
		_, rowFilter, err := rs.access("read")
		if err != nil {
			interpreter.RaiseUserError("Прочитать(" + rs.ir.Name + "): " + err.Error())
		}
		f := rs.regFilter()
		f.RowFilter = rowFilter
		rows, err := rs.s.store.InfoRegList(rs.ctx(), rs.ir, f)
		if err != nil {
			interpreter.RaiseUserError("Прочитать(" + rs.ir.Name + "): " + err.Error())
		}
		rs.rows = infoRegDSLRows(rs.ir, rows)
		return float64(len(rs.rows))
	case "очистить", "clear":
		rs.rows = nil
		return nil
	case "добавить", "add":
		row := map[string]any{}
		// Значения отбора проставляются сразу: строка набора вне своего отбора
		// не имеет смысла, а забытое измерение дало бы запись с пустым ключом.
		for name, v := range rs.filter.values {
			row[name] = v
		}
		rs.rows = append(rs.rows, row)
		return &interpreter.MapThis{M: row}
	case "количество", "count":
		return float64(len(rs.rows))
	case "получить", "get":
		if len(args) == 0 {
			interpreter.RaiseUserError("Получить(" + rs.ir.Name + "): не указан индекс строки")
		}
		// Индекс приходит из DSL числом; decimal и строка проходят тем же
		// путём, что у остальных методов коллекций.
		idx := runtime.RowIndexArg(args[0])
		if idx < 0 || idx >= len(rs.rows) {
			interpreter.RaiseUserError(fmt.Sprintf(
				"Получить(%s): индекс %d вне набора (строк %d)", rs.ir.Name, idx, len(rs.rows)))
		}
		return &interpreter.MapThis{M: rs.rows[idx]}
	case "записать", "write":
		rs.write()
		return nil
	}
	interpreter.RaiseUserError("НаборЗаписей(" + rs.ir.Name + "): неизвестный метод «" + method +
		"» (доступны Прочитать, Очистить, Добавить, Количество, Получить, Записать)")
	return nil
}

// IterateRows делает набор перебираемым: `Для Каждого Стр Из Набор Цикл`.
//
// Без него прочитанные строки были для прикладного кода непрозрачны: их можно
// было сосчитать и перезаписать целиком, но не посмотреть и не поправить — то
// есть типичный цикл «прочитал срез, поправил один ресурс, записал» приходилось
// подменять на «прочитал запросом, собрал набор заново» (#905).
//
// Отдаётся тот же срез строк, что правит Добавить(): правка строки в цикле
// должна попадать в Записать(), иначе перебор был бы обманчиво бесполезным.
func (rs *infoRegRecordSet) IterateRows() []map[string]any { return rs.rows }

// write замещает содержимое по отбору: удаление и вставка в одной транзакции.
//
// Каждая строка пишется через общую точку infoRegWrite — тот же контракт, что у
// менеджера записи (119A) и у формы: проверка политик, валидация и РЕГИСТРАЦИЯ
// изменения в планах обмена. Прежде набор звал storage.InfoRegSet напрямую,
// поэтому узлы обмена об изменениях не узнавали и репликация молча расходилась
// (#856). Удаляемые строки регистрируются так же — иначе на узлах остались бы
// записи, которых здесь уже нет.
func (rs *infoRegRecordSet) write() {
	ctx := rs.ctx()
	// Object permissions are checked before inspecting the target slice or the
	// number of buffered rows. Thus an empty/missing slice cannot be used as an
	// existence oracle by a role lacking write or delete.
	allowWrite, writeFilter, err := rs.access("write")
	if err != nil {
		interpreter.RaiseUserError("Записать(" + rs.ir.Name + "): " + err.Error())
	}
	allowDelete, deleteFilter, err := rs.access("delete")
	if err != nil {
		interpreter.RaiseUserError("Записать(" + rs.ir.Name + "): " + err.Error())
	}
	f := rs.regFilter()
	existingFilter := allInfoRegPredicates(writeFilter, deleteFilter)
	f.RowFilter = existingFilter
	type preparedRow struct {
		dims      map[string]any
		resources map[string]any
		period    *time.Time
	}
	prepared := make([]preparedRow, 0, len(rs.rows))

	// Чистая валидация ДО транзакции: незачем открывать её, чтобы тут же
	// откатить. Проверяем здесь и период каждой строки — если оставить его в
	// цикле записи, поздняя ошибка способна прийти уже после DELETE и нескольких
	// успешных INSERT.
	// Опечатка в имени поля прежде молча писала мусор — менеджер записи для той
	// же ошибки честно поднимает исключение.
	for i, row := range rs.rows {
		for name := range row {
			if isPeriodName(name) && (rs.ir.Periodic || infoRegField(rs.ir, name) == nil) {
				continue
			}
			if infoRegField(rs.ir, name) == nil {
				interpreter.RaiseUserError(fmt.Sprintf(
					"Записать(%s): строка %d — нет измерения или ресурса «%s»", rs.ir.Name, i+1, name))
			}
		}
		// Строка не должна противоречить своему отбору: иначе она «сбегает» из
		// него и затирает чужой срез — при том что удаление шло по отбору.
		for name, want := range rs.filter.values {
			got := rowValueFold(row, name)
			if got == nil {
				interpreter.RaiseUserError(fmt.Sprintf(
					"Записать(%s): строка %d — не задано измерение «%s» из отбора",
					rs.ir.Name, i+1, name))
			}
			if !infoRegFilterValuesEqual(infoRegField(rs.ir, name), got, want) {
				interpreter.RaiseUserError(fmt.Sprintf(
					"Записать(%s): строка %d — измерение «%s» = %v не совпадает с отбором (%v)",
					rs.ir.Name, i+1, name, got, want))
			}
		}

		dims := make(map[string]any, len(rs.ir.Dimensions))
		resources := make(map[string]any, len(rs.ir.Resources))
		for _, d := range rs.ir.Dimensions {
			dims[d.Name] = rowValueFold(row, d.Name)
		}
		for _, r := range rs.ir.Resources {
			resources[r.Name] = rowValueFold(row, r.Name)
		}
		period, err := infoRegRowPeriod(rs.ir, row)
		if err != nil {
			interpreter.RaiseUserError(fmt.Sprintf("Записать(%s): строка %d — %s", rs.ir.Name, i+1, err))
		}
		prepared = append(prepared, preparedRow{dims: dims, resources: resources, period: period})
	}

	// WithTxScope даёт операции собственную границу атомарности. Если DSL уже
	// открыл транзакцию, это savepoint: ловимое Попытка/Исключение не сможет
	// затем зафиксировать DELETE и только часть новых строк.
	err = rs.s.store.WithTxScope(ctx, func(txCtx context.Context) error {
		// PostgreSQL needs an explicit table write lock: a row/predicate lock
		// cannot protect a key which does not exist yet. SQLite's following
		// DELETE is the first write statement and serializes writers itself.
		if err := rs.s.store.LockInfoRegisterForPolicyWrite(txCtx, rs.ir); err != nil {
			return err
		}
		// DELETE ... RETURNING — единый источник истины для delete-RLS и
		// tombstone'ов. SELECT до транзакции оставлял окно, в котором конкурентная
		// строка удалялась без проверки и без регистрации в обмене.
		deleted, err := rs.s.store.InfoRegDeleteByFilterReturning(txCtx, rs.ir, f)
		if err != nil {
			return err
		}
		plans := rs.s.reg.ExchangePlans()
		for _, row := range deleted {
			// Сохраняем контракт обычной записи: существующая строка также
			// должна быть доступна по write-RLS. Набор дополнительно требует
			// delete, потому что замещение физически удаляет эту строку.
			if allowWrite != nil {
				if err := allowWrite(txCtx, row); err != nil {
					return err
				}
			}
			// Замещение физически удаляет прежние строки, поэтому ему требуется
			// отдельное право delete. Право write не должно неявно его выдавать.
			if allowDelete != nil {
				if err := allowDelete(txCtx, row); err != nil {
					return err
				}
			}
			dims := make(map[string]any, len(rs.ir.Dimensions))
			for _, d := range rs.ir.Dimensions {
				dims[d.Name] = rowValueFold(row, d.Name)
			}
			if err := exchange.RegisterInfoRegOnSave(txCtx, rs.s.store, plans, rs.ir, dims, true); err != nil {
				return err
			}
		}
		for _, row := range prepared {
			// The proposed row must satisfy write-RLS. The storage upsert also
			// predicates its conflict UPDATE on the existing row. A hidden row
			// which survived the filtered DELETE is therefore a silent no-op,
			// not an overwrite and not an existence signal.
			if err := rs.s.infoRegWriteRecordSet(txCtx, rs.ir, row.dims, row.resources,
				row.period, allowWrite, writeFilter, existingFilter); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		interpreter.RaiseUserError("Записать(" + rs.ir.Name + "): " + err.Error())
	}
}

// regFilter переводит Отбор в storage.RegFilter. Пустой отбор отклоняется.
func (rs *infoRegRecordSet) regFilter() storage.RegFilter {
	dims := map[string]any{}
	for name, v := range rs.filter.values {
		if v == nil {
			interpreter.RaiseUserError("Отбор(" + rs.ir.Name + "): не задано значение измерения «" + name + "»")
		}
		if text, ok := v.(string); ok && text == "" {
			interpreter.RaiseUserError("Отбор(" + rs.ir.Name + "): пустое значение измерения «" + name + "»")
		}
		dims[name] = v
	}
	if len(dims) == 0 {
		interpreter.RaiseUserError("НаборЗаписей(" + rs.ir.Name +
			"): не задан Отбор — запись набора без отбора снесла бы регистр целиком")
	}
	return storage.RegFilter{DimValues: dims}
}

func (rs *infoRegRecordSet) access(op string) (infoRegAccess, *storage.Predicate, error) {
	if isTrustedDSLContext(rs.ctx()) {
		return nil, nil, nil
	}
	dec, err := rs.s.rowDecisionFor(rs.ctx(), "inforeg", rs.ir.Name, op,
		storage.InfoRegisterPredicateEntity(rs.ir))
	if err != nil {
		return nil, nil, err
	}
	if !dec.Allowed {
		return nil, nil, interpreter.ErrRowAccessDenied
	}
	if dec.Unrestricted || dec.Predicate == nil {
		return nil, nil, nil
	}
	predicate := dec.Predicate
	allow := func(ctx context.Context, row map[string]any) error {
		if !rs.s.matchRowPredicate(ctx, row, predicate) {
			return interpreter.ErrRowAccessDenied
		}
		return nil
	}
	return allow, predicate, nil
}

func allInfoRegPredicates(predicates ...*storage.Predicate) *storage.Predicate {
	items := make([]storage.Predicate, 0, len(predicates))
	for _, predicate := range predicates {
		if predicate != nil {
			items = append(items, *predicate)
		}
	}
	switch len(items) {
	case 0:
		return nil
	case 1:
		return &items[0]
	default:
		return &storage.Predicate{All: items}
	}
}

// infoRegFilter — объект Отбор набора записей. Принимает только измерения:
// отбирать по ресурсу нельзя, ключ записи составляют измерения.
type infoRegFilter struct {
	ir     *metadata.InfoRegister
	values map[string]any
}

func (f *infoRegFilter) Get(name string) any { return f.values[name] }

func (f *infoRegFilter) Set(name string, v any) {
	for i := range f.ir.Dimensions {
		if strings.EqualFold(f.ir.Dimensions[i].Name, name) {
			f.values[f.ir.Dimensions[i].Name] = v
			return
		}
	}
	interpreter.RaiseUserError("Отбор(" + f.ir.Name + "): «" + name +
		"» не измерение регистра — отбирать можно только по измерениям")
}

// rowValueFold достаёт значение из строки без учёта регистра имени.
func rowValueFold(row map[string]any, name string) any {
	if v, ok := row[name]; ok {
		return v
	}
	low := strings.ToLower(name)
	for k, v := range row {
		if strings.ToLower(k) == low {
			return v
		}
	}
	return nil
}

// infoRegFilterValuesEqual compares a row dimension with its typed DSL filter.
// In particular, *interpreter.Ref.String() is a display name, not identity;
// reference dimensions must compare by UUID or two different objects with the
// same name become indistinguishable.
func infoRegFilterValuesEqual(field *metadata.Field, left, right any) bool {
	canonical := func(v any) string {
		if field != nil && field.RefEntity != "" {
			if ref, ok := v.(interface{ GetRefUUID() string }); ok {
				return strings.ToLower(ref.GetRefUUID())
			}
			return strings.ToLower(fmt.Sprintf("%v", v))
		}
		switch value := v.(type) {
		case time.Time:
			return value.UTC().Format(time.RFC3339Nano)
		case *time.Time:
			if value == nil {
				return ""
			}
			return value.UTC().Format(time.RFC3339Nano)
		default:
			return fmt.Sprintf("%v", v)
		}
	}
	return canonical(left) == canonical(right)
}

// infoRegDSLRows переводит строки хранилища в строки набора для DSL.
//
// InfoRegList отдаёт период ДВУМЯ ключами и оба — для интерфейса: `period` —
// человекочитаемое «02.01.2006» для ячейки списка, `period_key` — машинный
// ключ для round-trip через HTML-форму удаления. Набор записей клал их в
// строку как есть, поэтому цикл «Прочитать() → Добавить() → Записать()» на
// периодическом регистре падал всегда, когда в регистре была хоть одна
// строка: обратно приезжала строка «02.01.2006», а запись ждёт дату (#857).
//
// Коду на DSL нужен один ключ и типизированное значение: Период — Дата.
func infoRegDSLRows(ir *metadata.InfoRegister, rows []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		r := make(map[string]any, len(row))
		for k, v := range row {
			// period/period_key — транспортные поля только у периодического
			// регистра. В непериодическом это допустимые имена пользовательских
			// измерений/ресурсов, и удалять их при чтении нельзя.
			if ir.Periodic && (k == "period" || k == "period_key") {
				continue
			}
			r[k] = v
		}
		if ir.Periodic {
			key, _ := row["period_key"].(string)
			t, ok := storage.ParseRegPeriod(key)
			if !ok {
				// Молча отдать строку без периода нельзя: она доедет до
				// Записать() и снесёт не тот срез.
				interpreter.RaiseUserError("Прочитать(" + ir.Name +
					"): не удалось разобрать период строки «" + key + "»")
			}
			r["Период"] = t
		}
		out = append(out, r)
	}
	return out
}

// infoRegRowPeriod достаёт период строки набора для периодического регистра.
// Принимает и дату, и строковое представление: строку мог положить как сам
// прикладной код, так и прежнее поведение Прочитать().
func infoRegRowPeriod(ir *metadata.InfoRegister, row map[string]any) (*time.Time, error) {
	if !ir.Periodic {
		for name := range row {
			if isPeriodName(name) && infoRegField(ir, name) == nil {
				// Тот же контракт, что у менеджера записи: служебный Период у
				// непериодического регистра не является безобидным лишним полем.
				dummy := time.Time{}
				return nil, infoRegCheckPeriod(ir, &dummy)
			}
		}
		return nil, nil
	}
	v := rowValueFold(row, "Период")
	if v == nil {
		v = rowValueFold(row, "period")
	}
	switch p := v.(type) {
	case time.Time:
		return &p, nil
	case *time.Time:
		if p != nil {
			return p, nil
		}
	case string:
		if t, ok := storage.ParseRegPeriod(p); ok {
			return &t, nil
		}
		// «02.01.2006» — представление, которое отдавал прежний Прочитать().
		if t, err := time.ParseInLocation("02.01.2006", strings.TrimSpace(p), time.Local); err == nil {
			return &t, nil
		}
		return nil, fmt.Errorf("регистр сведений %s: период строки набора «%s» не распознан", ir.Name, p)
	}
	return nil, fmt.Errorf("регистр сведений %s периодический: у строки набора не задан Период", ir.Name)
}
