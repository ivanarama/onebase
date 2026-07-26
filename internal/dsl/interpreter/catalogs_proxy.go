package interpreter

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Статусы safe-match API (ПроверитьСовпадениеПоРеквизиту): кладутся в поле
// Статус результата, чтобы прикладной код мог явно различать create/update/conflict.
const (
	MatchStatusNone     = "НеНайдено"
	MatchStatusOne      = "НайденаОдна"
	MatchStatusMultiple = "НайденоНесколько"
)

var ErrRowAccessDenied = errors.New("row access denied")

// NewMatchResultStruct собирает результат safe-match в Структуру с полями
// Статус / Ссылка / Количество. ref задаётся только при ровно одном совпадении.
// Экспортируется, чтобы тем же результатом пользовался путь Документы.X в ui.
func NewMatchResultStruct(ref *Ref, count int) *Struct {
	status := MatchStatusNone
	switch {
	case count == 1:
		status = MatchStatusOne
	case count >= 2:
		status = MatchStatusMultiple
	}
	var refVal any // nil → Неопределено при отсутствии/неоднозначности
	if ref != nil {
		refVal = ref
	}
	s := &Struct{vals: map[string]any{}}
	s.Set("Статус", status)
	s.Set("Ссылка", refVal)
	s.Set("Количество", float64(count))
	return s
}

// MatchValueString приводит DSL-значение к строке для поиска по реквизиту:
// у ссылки берётся наименование, числа форматируются без экспоненты (как Строка()).
// Экспортируется для пути Документы.X в пакете ui.
func MatchValueString(raw any) string {
	if r, ok := raw.(*Ref); ok {
		return r.Name
	}
	if s, err := builtinToString([]any{raw}, "", 0); err == nil {
		if str, ok := s.(string); ok {
			return str
		}
	}
	return fmt.Sprintf("%v", raw)
}

// CatalogsDB extends PredefinedDB with field-based lookups and writes.
// Returns ("", "", false, nil) on not-found so the DSL can compare against nil.
type CatalogsDB interface {
	PredefinedDB
	FindCatalogByField(ctx context.Context, entity *metadata.Entity, fieldName, value string) (idStr, display string, ok bool, err error)
	ListCatalogMatchesByField(ctx context.Context, entity *metadata.Entity, fieldName, value string) (ids, displays []string, err error)
	// MatchCatalogByField — safe-match: количество совпадений и (при ровно
	// одном) id/представление найденной записи.
	MatchCatalogByField(ctx context.Context, entity *metadata.Entity, fieldName, value string) (idStr, display string, count int, err error)
	// WriteCatalogRecord upserts a record. idStr пустой →
	// генерируется новый UUID. Возвращает UUID записанной записи.
	WriteCatalogRecord(ctx context.Context, entity *metadata.Entity, idStr string, fields map[string]any) (string, error)
	// Delete удаляет запись справочника/документа по идентификатору.
	Delete(ctx context.Context, entityName string, id uuid.UUID) error
	// GetByID загружает запись по UUID. Возвращает поля шапки (включая
	// id, _version, deletion_mark и т.д.). Используется Ссылка.ПолучитьОбъект()
	// для редактирования существующих записей справочников.
	GetByID(ctx context.Context, entityName string, id uuid.UUID, entity *metadata.Entity) (map[string]any, error)
}

// EntityLookup resolves an entity name (case-insensitive) to its metadata.
type EntityLookup interface {
	GetEntity(name string) *metadata.Entity
}

// ManagerCaller — необязательный «вызыватель» процедур модуля менеджера
// (X.manager.os). Опционально цепляется к CatalogsRoot через
// WithManagerCaller — если не задан, CatalogProxy остаётся прежним.
//
// Семантика found: процедура была найдена в модуле менеджера. Если false —
// proxy продолжает обработку (например, возвращает nil как раньше).
type ManagerCaller interface {
	CallManager(entityName, method string, args []any) (result any, found bool, err error)
}

// RowAccessChecker is provided by the host runtime to make DSL data proxies
// respect the same row-level access decisions as UI/REST. A nil checker keeps
// legacy trusted/server-code behavior for tests and headless callers.
type RowAccessChecker interface {
	CheckRowAccess(ctx context.Context, entity *metadata.Entity, op string, id uuid.UUID, fields map[string]any) error
	IsRowAccessRestricted(ctx context.Context, entity *metadata.Entity, op string) bool
	AutoFillRowAccess(ctx context.Context, entity *metadata.Entity, op string, fields map[string]any) error
}

// CtxSource предоставляет «живой» контекст. Для обычного запуска это
// статический контекст; при активной DSL-транзакции — *TxState, чей
// Ctx() несёт открытую транзакцию — запись справочника
// из обработки участвует в ней.
type CtxSource interface {
	Ctx() context.Context
}

// staticCtx — CtxSource c фиксированным контекстом (нет транзакции).
type staticCtx struct{ ctx context.Context }

func (s staticCtx) Ctx() context.Context { return s.ctx }

// NewStaticCtx wraps a plain context as a CtxSource.
func NewStaticCtx(ctx context.Context) CtxSource { return staticCtx{ctx: ctx} }

// ExchangeRegistrar регистрирует изменение объекта в планах обмена (план 86)
// после прямой записи из DSL (Справочники.X.Создать().Записать()), которая идёт
// мимо entityservice.Save. nil — обмен не подключён (тесты/headless). Замыкание
// строит host-слой (ui), где доступны store и реестр планов.
type ExchangeRegistrar func(ctx context.Context, entity *metadata.Entity, id uuid.UUID, deletion bool) error

type optionalTxRunner interface {
	WithTxIfNeeded(ctx context.Context, fn func(context.Context) error) error
}

func withOptionalCatalogTx(db CatalogsDB, ctx context.Context, fn func(context.Context) error) error {
	if tx, ok := db.(optionalTxRunner); ok {
		return tx.WithTxIfNeeded(ctx, fn)
	}
	return fn(ctx)
}

// CatalogsRoot is the DSL global Справочники / Catalogs.
// CatalogObjectFactory — необязательная фабрика объектных обёрток справочника.
// Позволяет вышестоящему слою (ui) подменить объекты, возвращаемые
// Справочники.X.Создать() и Ссылка.ПолучитьОбъект(), на полнофункциональные —
// с табличными частями и DSL-хуком ПриЗаписи (как у документов). Без фабрики
// используется встроенный CatalogRecordWriter (только поля шапки).
type CatalogObjectFactory interface {
	NewCatalogObject(entity *metadata.Entity) any
	LoadCatalogObject(entity *metadata.Entity, uuidStr string) (any, error)
}

type CatalogsRoot struct {
	db         CatalogsDB
	lookup     EntityLookup
	ctxSrc     CtxSource
	caller     ManagerCaller // optional — fallback к модулю менеджера в CallMethod
	access     RowAccessChecker
	registrar  ExchangeRegistrar
	objFactory CatalogObjectFactory
}

// NewCatalogsRoot creates the root object for injection as DSL extraVar.
// ctxSrc — источник живого контекста (staticCtx или *TxState).
func NewCatalogsRoot(ctxSrc CtxSource, db CatalogsDB, lookup EntityLookup) *CatalogsRoot {
	return &CatalogsRoot{db: db, lookup: lookup, ctxSrc: ctxSrc}
}

// WithManagerCaller подключает обработчик пользовательских методов
// модуля менеджера. Возвращает себя для цепочки.
func (r *CatalogsRoot) WithManagerCaller(c ManagerCaller) *CatalogsRoot {
	r.caller = c
	return r
}

// WithRowAccessChecker attaches host row-level access checks to all catalog
// proxies created from this root.
func (r *CatalogsRoot) WithRowAccessChecker(c RowAccessChecker) *CatalogsRoot {
	r.access = c
	return r
}

// WithExchangeRegistrar подключает регистрацию изменений в планах обмена для
// прямых записей справочников из DSL. Возвращает себя для цепочки.
func (r *CatalogsRoot) WithExchangeRegistrar(reg ExchangeRegistrar) *CatalogsRoot {
	r.registrar = reg
	return r
}

// WithObjectFactory подключает фабрику объектных обёрток (см.
// CatalogObjectFactory). Возвращает себя для цепочки.
func (r *CatalogsRoot) WithObjectFactory(f CatalogObjectFactory) *CatalogsRoot {
	r.objFactory = f
	return r
}

func (r *CatalogsRoot) Get(entityName string) any {
	entity := r.lookup.GetEntity(entityName)
	if entity == nil || entity.Kind != metadata.KindCatalog {
		return nil
	}
	return &CatalogProxy{entity: entity, db: r.db, ctxSrc: r.ctxSrc, caller: r.caller, access: r.access, registrar: r.registrar, objFactory: r.objFactory}
}

func (r *CatalogsRoot) Set(_ string, _ any) {}

// CatalogProxy resolves predefined items, runtime lookups, and record creation.
//
//	Справочники.ТипЦен.Закупочная                  → *Ref to predefined item
//	Справочники.ТипЦен.НайтиПоНаименованию("X")     → *Ref or nil
//	Справочники.Контрагент.Создать()                → *CatalogRecordWriter
type CatalogProxy struct {
	entity     *metadata.Entity
	db         CatalogsDB
	ctxSrc     CtxSource
	caller     ManagerCaller // optional — для вызовов методов модуля менеджера
	access     RowAccessChecker
	registrar  ExchangeRegistrar
	objFactory CatalogObjectFactory
}

// NewCatalogProxy создаёт менеджера справочника для привязки к ссылкам,
// приходящим из БД (см. enrichHeaderRefs/enrichTPRowsWithRefs в ui).
// Так Ссылка.Удалить()/ПолучитьОбъект() работают на ссылках реквизитов
// шапки/ТЧ, а не только на ссылках, созданных через Справочники.X.НайтиПо…
func NewCatalogProxy(entity *metadata.Entity, db CatalogsDB, ctxSrc CtxSource) *CatalogProxy {
	return &CatalogProxy{entity: entity, db: db, ctxSrc: ctxSrc}
}

// WithRowAccessChecker attaches host row-level access checks to a standalone
// catalog proxy, usually one used as a Ref manager for values loaded from DB.
func (p *CatalogProxy) WithRowAccessChecker(c RowAccessChecker) *CatalogProxy {
	p.access = c
	return p
}

// WithExchangeRegistrar подключает регистрацию изменений в планах обмена к
// standalone-прокси (обычно менеджеру ссылки на справочник). Для цепочки.
func (p *CatalogProxy) WithExchangeRegistrar(reg ExchangeRegistrar) *CatalogProxy {
	p.registrar = reg
	return p
}

// WithObjectFactory подключает фабрику объектных обёрток к standalone-прокси
// (менеджеру ссылки). Для цепочки.
func (p *CatalogProxy) WithObjectFactory(f CatalogObjectFactory) *CatalogProxy {
	p.objFactory = f
	return p
}

func (p *CatalogProxy) ctx() context.Context {
	if p.ctxSrc != nil {
		return p.ctxSrc.Ctx()
	}
	return context.Background()
}

// Get is called for foo.Bar attribute access — predefined item lookup.
func (p *CatalogProxy) Get(itemName string) any {
	for _, item := range p.entity.Predefined {
		if strings.EqualFold(item.Name, itemName) {
			id, err := p.db.GetPredefinedIDStr(p.ctx(), p.entity.Name, item.Name)
			if err != nil || id == "" {
				return nil
			}
			parsed, err := uuid.Parse(id)
			if err != nil {
				return nil
			}
			if err := p.checkRowAccess("read", parsed, nil); err != nil {
				if errors.Is(err, ErrRowAccessDenied) {
					return nil
				}
				RaiseUserError("Доступ к " + p.entity.Name + "." + item.Name + ": " + err.Error())
			}
			return &Ref{UUID: id, Name: item.Name, Type: p.entity.Name, Manager: p}
		}
	}
	return nil
}

func (p *CatalogProxy) Set(_ string, _ any) {}

// CallMethod implements MethodCallable for method-style invocation.
func (p *CatalogProxy) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "найтипонаименованию", "findbyname":
		return p.findByField("Наименование", args)
	case "найтипокоду", "findbycode":
		return p.findByField("Код", args)
	case "найтипоидентификатору", "findbyid":
		// Ссылка по строковому UUID — для строк результата запроса
		// (ВЫБРАТЬ Поле.Ссылка КАК Ид), когда наименование/код не уникальны
		// или лишний поиск по реквизиту нежелателен. Существование записи
		// проверит ПолучитьОбъект().
		if len(args) == 0 {
			return nil
		}
		uuidStr := fmt.Sprint(args[0])
		if _, err := uuid.Parse(uuidStr); err != nil {
			RaiseUserError("НайтиПоИдентификатору(" + p.entity.Name + "): неверный идентификатор ссылки: " + uuidStr)
		}
		return &Ref{UUID: uuidStr, Name: uuidStr, Type: p.entity.Name, Manager: p}
	case "найтипореквизиту", "findbyattribute":
		if len(args) < 2 {
			RaiseUserError("НайтиПоРеквизиту(" + p.entity.Name + "): нужны имя реквизита и значение")
		}
		field, ok := args[0].(string)
		if !ok {
			RaiseUserError("НайтиПоРеквизиту(" + p.entity.Name + "): имя реквизита должно быть строкой")
		}
		return p.findByField(field, args[1:])
	case "проверитьсовпадениепореквизиту", "matchbyattribute":
		if len(args) < 2 {
			RaiseUserError("ПроверитьСовпадениеПоРеквизиту(" + p.entity.Name + "): нужны имя реквизита и значение")
		}
		field, ok := args[0].(string)
		if !ok {
			RaiseUserError("ПроверитьСовпадениеПоРеквизиту(" + p.entity.Name + "): имя реквизита должно быть строкой")
		}
		return p.matchByField(field, args[1])
	case "создать", "create":
		if p.objFactory != nil {
			return p.objFactory.NewCatalogObject(p.entity)
		}
		return &CatalogRecordWriter{
			entity:    p.entity,
			db:        p.db,
			ctxSrc:    p.ctxSrc,
			access:    p.access,
			registrar: p.registrar,
			fields:    map[string]any{},
		}
	case "удалить", "delete":
		if len(args) == 0 {
			RaiseUserError("Удалить(" + p.entity.Name + "): не передана ссылка")
		}
		ref, ok := args[0].(*Ref)
		if !ok {
			RaiseUserError(fmt.Sprintf("Удалить(%s): ожидается ссылка, получено %T", p.entity.Name, args[0]))
		}
		if err := p.DeleteRef(ref.UUID); err != nil {
			RaiseUserError("Удалить(" + p.entity.Name + "): " + err.Error())
		}
		return nil
	}
	// Fallback на модуль менеджера: Справочники.X.МойМетод(…). Если caller не
	// подключён или процедура не объявлена — ведёт себя как раньше (nil).
	if p.caller != nil {
		if result, found, err := p.caller.CallManager(p.entity.Name, method, args); found {
			if err != nil {
				RaiseUserError(p.entity.Name + "." + method + ": " + err.Error())
			}
			return result
		}
	}
	return nil
}

// DeleteRef реализует RefManager — удаление записи справочника по UUID.
func (p *CatalogProxy) DeleteRef(uuidStr string) error {
	id, err := uuid.Parse(uuidStr)
	if err != nil {
		return i18nerr.Errorf("неверный идентификатор ссылки: %q", uuidStr)
	}
	if err := p.checkRowAccess("delete", id, nil); err != nil {
		return err
	}
	return withOptionalCatalogTx(p.db, p.ctx(), func(ctx context.Context) error {
		if p.registrar != nil {
			if err := p.registrar(ctx, p.entity, id, true); err != nil {
				return fmt.Errorf("регистрация удаления в обмене: %w", err)
			}
		}
		return p.db.Delete(ctx, p.entity.Name, id)
	})
}

// LoadObject реализует RefManager — загружает существующую запись справочника
// по UUID и возвращает CatalogRecordWriter с предзаполненными полями, так что
// Ссылка.ПолучитьОбъект().Поле = … → Записать() обновит запись по тому же id.
// При подключённой фабрике объект строит она (с табличными частями и хуками).
func (p *CatalogProxy) LoadObject(uuidStr string) (any, error) {
	if p.objFactory != nil {
		return p.objFactory.LoadCatalogObject(p.entity, uuidStr)
	}
	id, err := uuid.Parse(uuidStr)
	if err != nil {
		return nil, i18nerr.Errorf("неверный идентификатор ссылки: %q", uuidStr)
	}
	if err := p.checkRowAccess("read", id, nil); err != nil {
		return nil, err
	}
	row, err := p.db.GetByID(p.ctx(), p.entity.Name, id, p.entity)
	if err != nil {
		return nil, err
	}
	fields := make(map[string]any, len(row))
	for _, f := range p.entity.Fields {
		if v, ok := row[f.Name]; ok && v != nil {
			fields[strings.ToLower(f.Name)] = v
		}
	}
	return &CatalogRecordWriter{
		entity:    p.entity,
		db:        p.db,
		ctxSrc:    p.ctxSrc,
		access:    p.access,
		registrar: p.registrar,
		idStr:     uuidStr,
		fields:    fields,
	}, nil
}

func (p *CatalogProxy) findByField(field string, args []any) any {
	if len(args) == 0 {
		return nil
	}
	value, ok := args[0].(string)
	if !ok {
		if r, ok := args[0].(*Ref); ok {
			value = r.Name
		} else {
			return nil
		}
	}
	if p.rowAccessRestricted("read") {
		ids, displays, err := p.visibleMatches(field, value)
		if err != nil {
			RaiseUserError("Найти(" + p.entity.Name + "." + field + "): " + err.Error())
		}
		if len(ids) == 0 {
			return nil
		}
		return &Ref{UUID: ids[0], Name: displays[0], Type: p.entity.Name, Manager: p}
	}
	idStr, display, found, err := p.db.FindCatalogByField(p.ctx(), p.entity, field, value)
	if err != nil || !found {
		return nil
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		RaiseUserError("Найти(" + p.entity.Name + "." + field + "): неверный идентификатор найденной записи")
	}
	if err := p.checkRowAccess("read", id, nil); err != nil {
		if errors.Is(err, ErrRowAccessDenied) {
			return nil
		}
		RaiseUserError("Найти(" + p.entity.Name + "." + field + "): " + err.Error())
	}
	return &Ref{UUID: idStr, Name: display, Type: p.entity.Name, Manager: p}
}

// matchByField — safe-match по реквизиту: возвращает Структуру со Статусом,
// Ссылкой (только при ровно одном совпадении) и Количеством.
func (p *CatalogProxy) matchByField(field string, raw any) any {
	value := MatchValueString(raw)
	if p.rowAccessRestricted("read") {
		ids, displays, err := p.visibleMatches(field, value)
		if err != nil {
			RaiseUserError("ПроверитьСовпадениеПоРеквизиту(" + p.entity.Name + "." + field + "): " + err.Error())
		}
		var ref *Ref
		if len(ids) == 1 {
			ref = &Ref{UUID: ids[0], Name: displays[0], Type: p.entity.Name, Manager: p}
		}
		return NewMatchResultStruct(ref, len(ids))
	}
	idStr, display, count, err := p.db.MatchCatalogByField(p.ctx(), p.entity, field, value)
	if err != nil {
		RaiseUserError("ПроверитьСовпадениеПоРеквизиту(" + p.entity.Name + "." + field + "): " + err.Error())
	}
	var ref *Ref
	if count == 1 {
		id, err := uuid.Parse(idStr)
		if err != nil {
			RaiseUserError("ПроверитьСовпадениеПоРеквизиту(" + p.entity.Name + "." + field + "): неверный идентификатор найденной записи")
		}
		if err := p.checkRowAccess("read", id, nil); err != nil {
			if errors.Is(err, ErrRowAccessDenied) {
				return NewMatchResultStruct(nil, 0)
			}
			RaiseUserError("ПроверитьСовпадениеПоРеквизиту(" + p.entity.Name + "." + field + "): " + err.Error())
		}
		ref = &Ref{UUID: idStr, Name: display, Type: p.entity.Name, Manager: p}
	}
	return NewMatchResultStruct(ref, count)
}

func (p *CatalogProxy) visibleMatches(field, value string) ([]string, []string, error) {
	ids, displays, err := p.db.ListCatalogMatchesByField(p.ctx(), p.entity, field, value)
	if err != nil {
		return nil, nil, err
	}
	visibleIDs := make([]string, 0, len(ids))
	visibleDisplays := make([]string, 0, len(displays))
	for i, idStr := range ids {
		id, err := uuid.Parse(idStr)
		if err != nil {
			return nil, nil, fmt.Errorf("неверный идентификатор найденной записи")
		}
		if err := p.checkRowAccess("read", id, nil); err != nil {
			if errors.Is(err, ErrRowAccessDenied) {
				continue
			}
			return nil, nil, err
		}
		visibleIDs = append(visibleIDs, idStr)
		visibleDisplays = append(visibleDisplays, displays[i])
	}
	return visibleIDs, visibleDisplays, nil
}

func (p *CatalogProxy) checkRowAccess(op string, id uuid.UUID, fields map[string]any) error {
	if p.access == nil {
		return nil
	}
	return p.access.CheckRowAccess(p.ctx(), p.entity, op, id, fields)
}

func (p *CatalogProxy) rowAccessRestricted(op string) bool {
	return p.access != nil && p.access.IsRowAccessRestricted(p.ctx(), p.entity, op)
}

// CatalogRecordWriter — записываемый объект справочника/документа,
// созданный через Справочники.X.Создать().
//
//	Зап = Справочники.Контрагент.Создать();
//	Зап.Наименование = "ООО Ромашка";
//	Зап.ИНН = "7701234567";
//	Ссыл = Зап.Записать();   // → *Ref на записанную запись
type CatalogRecordWriter struct {
	entity    *metadata.Entity
	db        CatalogsDB
	ctxSrc    CtxSource
	access    RowAccessChecker
	registrar ExchangeRegistrar
	idStr     string
	fields    map[string]any
}

func (w *CatalogRecordWriter) ctx() context.Context {
	if w.ctxSrc != nil {
		return w.ctxSrc.Ctx()
	}
	return context.Background()
}

// Get — чтение установленного значения поля (case-insensitive).
func (w *CatalogRecordWriter) Get(name string) any {
	low := strings.ToLower(name)
	for k, v := range w.fields {
		if strings.ToLower(k) == low {
			return v
		}
	}
	return nil
}

// Set — установка значения поля (Зап.Поле = значение).
func (w *CatalogRecordWriter) Set(name string, v any) {
	w.fields[strings.ToLower(name)] = v
}

// Fields — имена заполненных полей объекта. Позволяет использовать объект
// как источник в ЗаполнитьЗначенияСвойств(Приёмник, Объект).
func (w *CatalogRecordWriter) Fields() []string {
	names := make([]string, 0, len(w.fields))
	for k := range w.fields {
		names = append(names, k)
	}
	return names
}

// CallMethod — Записать() / УстановитьЗначение().
func (w *CatalogRecordWriter) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "записать", "write":
		if err := w.checkWriteAccess(); err != nil {
			RaiseUserError("Записать(" + w.entity.Name + "): " + err.Error())
		}
		var id string
		err := withOptionalCatalogTx(w.db, w.ctx(), func(ctx context.Context) error {
			var err error
			id, err = w.db.WriteCatalogRecord(ctx, w.entity, w.idStr, w.fields)
			if err != nil {
				return err
			}
			if w.registrar != nil {
				parsed, err := uuid.Parse(id)
				if err != nil {
					return err
				}
				if err := w.registrar(ctx, w.entity, parsed, false); err != nil {
					return fmt.Errorf("регистрация обмена: %w", err)
				}
			}
			return nil
		})
		if err != nil {
			RaiseUserError("Записать(" + w.entity.Name + "): " + err.Error())
		}
		w.idStr = id
		name := ""
		if v := w.Get("Наименование"); v != nil {
			name = fmt.Sprintf("%v", v)
		}
		return &Ref{
			UUID: id, Name: name, Type: w.entity.Name,
			Manager: &CatalogProxy{entity: w.entity, db: w.db, ctxSrc: w.ctxSrc, access: w.access, registrar: w.registrar},
		}
	case "установитьзначение", "setvalue":
		if len(args) >= 2 {
			if n, ok := args[0].(string); ok {
				w.Set(n, args[1])
			}
		}
	case "этоновый", "isnew":
		return w.idStr == ""
	case "прочитать", "read":
		w.read()
		return nil
	}
	return nil
}

// read перечитывает поля объекта из БД (Объект.Прочитать()). Требует, чтобы
// объект уже был записан (иначе нечего читать).
func (w *CatalogRecordWriter) read() {
	if w.idStr == "" {
		RaiseUserError("Прочитать(" + w.entity.Name + "): объект ещё не записан")
	}
	id, err := uuid.Parse(w.idStr)
	if err != nil {
		RaiseUserError("Прочитать(" + w.entity.Name + "): неверный идентификатор")
	}
	if err := w.checkReadAccess(id); err != nil {
		RaiseUserError("Прочитать(" + w.entity.Name + "): " + err.Error())
	}
	row, err := w.db.GetByID(w.ctx(), w.entity.Name, id, w.entity)
	if err != nil {
		RaiseUserError("Прочитать(" + w.entity.Name + "): " + err.Error())
	}
	w.fields = make(map[string]any, len(row))
	for _, f := range w.entity.Fields {
		if v, ok := row[f.Name]; ok && v != nil {
			w.fields[strings.ToLower(f.Name)] = v
		}
	}
}

func (w *CatalogRecordWriter) checkReadAccess(id uuid.UUID) error {
	if w.access == nil {
		return nil
	}
	return w.access.CheckRowAccess(w.ctx(), w.entity, "read", id, nil)
}

func (w *CatalogRecordWriter) checkWriteAccess() error {
	if w.access == nil {
		return nil
	}
	if strings.TrimSpace(w.idStr) == "" {
		if err := w.access.AutoFillRowAccess(w.ctx(), w.entity, "write", w.fields); err != nil {
			return err
		}
	}
	id := uuid.Nil
	if strings.TrimSpace(w.idStr) != "" {
		parsed, err := uuid.Parse(w.idStr)
		if err != nil {
			return i18nerr.Errorf("неверный идентификатор ссылки: %q", w.idStr)
		}
		id = parsed
	}
	return w.access.CheckRowAccess(w.ctx(), w.entity, "write", id, w.fields)
}
