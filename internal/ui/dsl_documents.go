package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/exchange"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/webhook"
)

// dispatchDocWebhook публикует исходящий веб-хук (план 29) с DSL-пути документов.
// Путь Документы.X (docWriter/docProxy) не проходит через entityservice.Save, где
// живёт dispatchSaved, поэтому документы, записанные обработкой, HTTP-сервисом или
// регламентным заданием, событий не порождали — интеграции их не видели.
// Справочников это не касалось: catWriter сохраняется через entityservice.Save.
//
// Как и в entityservice: если запись идёт внутри явной DSL-транзакции, публикация
// откладывается до коммита — не сообщаем наружу о данных, которые ещё могут
// откатиться. record передаётся только там, где поля записи под рукой.
func (s *Server) dispatchDocWebhook(ctx context.Context, name string, entity *metadata.Entity, id uuid.UUID, fields map[string]any) {
	if !s.cfg.Webhooks.Enabled() {
		return
	}
	event := webhook.Event{
		Name:   name,
		Entity: entity.Name,
		ID:     id.String(),
		User:   storage.AuditUserLogin(ctx),
	}
	if fields != nil {
		event.Record = webhookDSLRecord(fields)
	}
	dispatch := func() { s.cfg.Webhooks.Dispatch(event) }
	if storage.DeferUntilTxCommit(ctx, dispatch) {
		return
	}
	dispatch()
}

// webhookDSLRecord — копия полей для тела хука без служебного псевдо-реквизита
// «Ссылка» (*interpreter.Ref в шаблоне бесполезен). Зеркалит
// entityservice.webhookRecord.
func webhookDSLRecord(fields map[string]any) map[string]any {
	rec := make(map[string]any, len(fields))
	for k, v := range fields {
		if low := strings.ToLower(k); low == "ссылка" || low == "reference" {
			continue
		}
		rec[k] = v
	}
	return rec
}

// docsCtxSource предоставляет «живой» контекст (с открытой DSL-транзакцией,
// если она есть). Реализуется *interpreter.TxState.
type docsCtxSource interface {
	Ctx() context.Context
}

type dslMessageCollectorContextKey struct{}

func withDSLMessageCollector(ctx context.Context, messages *[]string) context.Context {
	if messages == nil {
		return ctx
	}
	return context.WithValue(ctx, dslMessageCollectorContextKey{}, messages)
}

func dslMessageCollectorFromContext(ctx context.Context) *[]string {
	messages, _ := ctx.Value(dslMessageCollectorContextKey{}).(*[]string)
	return messages
}

func appendDSLMessages(ctx context.Context, messages []string) {
	if dst := dslMessageCollectorFromContext(ctx); dst != nil && len(messages) > 0 {
		*dst = append(*dst, messages...)
	}
}

// refManagerFor строит менеджера для ссылки на сущность по её метаданным:
// CatalogProxy для справочников, docProxy для документов. Используется в
// enrichHeaderRefs/enrichTPRowsWithRefs и dsl_object_attr, чтобы ссылки,
// собранные из значений колонок БД, несли менеджера — иначе у Удалить()
// и ПолучитьОбъект() на них не было бы привязки к типу.
func (s *Server) refManagerFor(entity *metadata.Entity, ctx context.Context) interpreter.RefManager {
	return s.refManagerForSrc(entity, interpreter.NewStaticCtx(ctx), ctx)
}

// refManagerForSrc — та же сборка менеджера, но с ЖИВЫМ источником контекста.
// Менеджер ссылки создаётся в момент первого чтения реквизита, то есть ДО того,
// как модуль откроет НачатьТранзакцию. Со снимком контекста его ПолучитьОбъект()
// уходил за ВТОРЫМ соединением, а пул SQLite — одно соединение, занятое той самой
// транзакцией: запрос вставал намертво до таймаута. С живым источником вызов
// выполняется внутри открытой транзакции.
func (s *Server) refManagerForSrc(entity *metadata.Entity, ctxSrc interpreter.CtxSource, ctx context.Context) interpreter.RefManager {
	if entity == nil {
		return nil
	}
	if ctxSrc == nil {
		ctxSrc = interpreter.NewStaticCtx(ctx)
	}
	switch entity.Kind {
	case metadata.KindCatalog:
		return interpreter.NewCatalogProxy(entity, s.store, ctxSrc).
			WithRowAccessChecker(s.dslRowAccessChecker()).
			WithFieldSearchChecker(s.dslFieldSearchChecker()).
			WithExchangeRegistrar(s.exchangeRegistrar()).
			WithObjectFactory(s.catObjectFactory(ctxSrc)).
			WithDeleter(dslCatalogDeleter{s: s})
	case metadata.KindDocument:
		return &docProxy{
			s:        s,
			ctxSrc:   ctxSrc,
			entity:   entity,
			messages: dslMessageCollectorFromContext(ctx),
		}
	}
	return nil
}

// docsRoot — DSL-глобал Документы / Documents (
// Документы.X.Создать() → пишущий объект документа с табличными частями
// и методами Записать()/Провести().
type docsRoot struct {
	s        *Server
	ctxSrc   docsCtxSource
	messages *[]string
}

func newDocsRoot(s *Server, ctxSrc docsCtxSource) *docsRoot {
	root := &docsRoot{s: s, ctxSrc: ctxSrc}
	if ctxSrc != nil {
		root.messages = dslMessageCollectorFromContext(ctxSrc.Ctx())
	}
	return root
}

// SetDSLMessageCollector connects document-hook messages to the collector of
// the processor or scheduled job that owns this Documents root.
func (d *docsRoot) SetDSLMessageCollector(messages *[]string) {
	d.messages = messages
}

func (d *docsRoot) Get(name string) any {
	entity := d.s.reg.GetEntity(name)
	if entity == nil || entity.Kind != metadata.KindDocument {
		return nil
	}
	return &docProxy{s: d.s, ctxSrc: d.ctxSrc, entity: entity, messages: d.messages}
}

func (d *docsRoot) Set(_ string, _ any) {}

// docProxy — Документы.ПоступлениеТоваров.
type docProxy struct {
	s        *Server
	ctxSrc   docsCtxSource
	entity   *metadata.Entity
	messages *[]string
}

func (p *docProxy) Get(_ string) any    { return nil }
func (p *docProxy) Set(_ string, _ any) {}

func (p *docProxy) ctx() context.Context {
	if p.ctxSrc != nil {
		return p.ctxSrc.Ctx()
	}
	return context.Background()
}

func (p *docProxy) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "создать", "create":
		return &docWriter{
			s:        p.s,
			ctxSrc:   p.ctxSrc,
			entity:   p.entity,
			messages: p.messages,
			obj: &runtime.Object{
				ID:            uuid.New(),
				Type:          p.entity.Name,
				Kind:          p.entity.Kind,
				Fields:        map[string]any{},
				TablePartRows: map[string][]map[string]any{},
			},
		}
	case "найтипономеру", "findbynumber":
		if len(args) == 0 {
			return nil
		}
		return p.findByField("Номер", fmt.Sprint(args[0]), args[0])
	case "найтипоидентификатору", "findbyid":
		// Ссылка по строковому UUID — для перебора документов из результата
		// запроса (р.Ссылка приходит UUID-строкой), когда номер неуникален и
		// НайтиПоНомеру() вернул бы не тот объект. Существование не проверяется:
		// .ПолучитьОбъект() поднимет понятную ошибку, если объекта нет.
		if len(args) == 0 {
			return nil
		}
		uuidStr := fmt.Sprint(args[0])
		if _, err := uuid.Parse(uuidStr); err != nil {
			interpreter.RaiseUserError("НайтиПоИдентификатору(" + p.entity.Name + "): неверный идентификатор ссылки: " + uuidStr)
		}
		return &interpreter.Ref{UUID: uuidStr, Name: uuidStr, Type: p.entity.Name, Manager: p}
	case "найтипореквизиту", "findbyattribute":
		if len(args) < 2 {
			interpreter.RaiseUserError("НайтиПоРеквизиту(" + p.entity.Name + "): нужны имя реквизита и значение")
		}
		field, ok := args[0].(string)
		if !ok {
			interpreter.RaiseUserError("НайтиПоРеквизиту(" + p.entity.Name + "): имя реквизита должно быть строкой")
		}
		return p.findByField(field, fmt.Sprint(args[1]), args[1])
	case "проверитьсовпадениепореквизиту", "matchbyattribute":
		if len(args) < 2 {
			interpreter.RaiseUserError("ПроверитьСовпадениеПоРеквизиту(" + p.entity.Name + "): нужны имя реквизита и значение")
		}
		field, ok := args[0].(string)
		if !ok {
			interpreter.RaiseUserError("ПроверитьСовпадениеПоРеквизиту(" + p.entity.Name + "): имя реквизита должно быть строкой")
		}
		return p.matchByField(field, args[1])
	case "удалить", "delete":
		if len(args) == 0 {
			interpreter.RaiseUserError("Удалить(" + p.entity.Name + "): не передана ссылка")
		}
		ref, ok := args[0].(*interpreter.Ref)
		if !ok {
			interpreter.RaiseUserError(fmt.Sprintf("Удалить(%s): ожидается ссылка, получено %T", p.entity.Name, args[0]))
		}
		if err := p.DeleteRef(ref.UUID); err != nil {
			interpreter.RaiseUserError("Удалить(" + p.entity.Name + "): " + err.Error())
		}
		return nil
	case "отменитьпроведение", "unpost":
		if len(args) == 0 {
			interpreter.RaiseUserError("ОтменитьПроведение(" + p.entity.Name + "): не передана ссылка")
		}
		ref, ok := args[0].(*interpreter.Ref)
		if !ok {
			interpreter.RaiseUserError(fmt.Sprintf("ОтменитьПроведение(%s): ожидается ссылка, получено %T", p.entity.Name, args[0]))
		}
		if err := p.unpostRef(ref.UUID); err != nil {
			interpreter.RaiseUserError("ОтменитьПроведение(" + p.entity.Name + "): " + err.Error())
		}
		return nil
	case "пометитьнаудаление", "markfordeletion":
		if len(args) == 0 {
			interpreter.RaiseUserError("ПометитьНаУдаление(" + p.entity.Name + "): не передана ссылка")
		}
		ref, ok := args[0].(*interpreter.Ref)
		if !ok {
			interpreter.RaiseUserError(fmt.Sprintf("ПометитьНаУдаление(%s): ожидается ссылка, получено %T", p.entity.Name, args[0]))
		}
		if err := p.markRef(ref.UUID, true); err != nil {
			interpreter.RaiseUserError("ПометитьНаУдаление(" + p.entity.Name + "): " + err.Error())
		}
		return nil
	case "снятьпометку", "unmarkdeletion":
		if len(args) == 0 {
			interpreter.RaiseUserError("СнятьПометку(" + p.entity.Name + "): не передана ссылка")
		}
		ref, ok := args[0].(*interpreter.Ref)
		if !ok {
			interpreter.RaiseUserError(fmt.Sprintf("СнятьПометку(%s): ожидается ссылка, получено %T", p.entity.Name, args[0]))
		}
		if err := p.markRef(ref.UUID, false); err != nil {
			interpreter.RaiseUserError("СнятьПометку(" + p.entity.Name + "): " + err.Error())
		}
		return nil
	}
	// Fallback на модуль менеджера: Документы.X.МойМетод(…).
	if result, found, err := p.s.callManagerProc(p.ctx(), p.entity.Name, method, args); found {
		if err != nil {
			interpreter.RaiseUserError(p.entity.Name + "." + method + ": " + err.Error())
		}
		return result
	}
	return nil
}

// findByField ищет документ по значению реквизита. raw — исходный аргумент DSL,
// чтобы при передаче ссылки искать по её наименованию.
func (p *docProxy) findByField(field, value string, raw any) any {
	if r, ok := raw.(*interpreter.Ref); ok {
		value = r.Name
	}
	if p.s.dslFieldSearchDenied(p.ctx(), p.entity, field) {
		// Поиск по защищённому реквизиту восстанавливает скрытое маской значение
		// перебором — как отбор ГДЕ в запросе, закрываем целиком (план 88E).
		interpreter.RaiseUserError("Найти(" + p.entity.Name + "." + field + "): реквизит защищён политикой поля")
	}
	if p.s.rowAccessRestricted(p.ctx(), p.entity, "read") {
		ids, displays, err := p.visibleMatches(field, value)
		if err != nil {
			interpreter.RaiseUserError("Найти(" + p.entity.Name + "." + field + "): " + err.Error())
		}
		if len(ids) == 0 {
			return nil
		}
		return &interpreter.Ref{UUID: ids[0], Name: displays[0], Type: p.entity.Name, Manager: p}
	}
	idStr, display, found, err := p.s.store.FindCatalogByField(p.ctx(), p.entity, field, value)
	if err != nil {
		interpreter.RaiseUserError("Найти(" + p.entity.Name + "." + field + "): " + err.Error())
	}
	if !found {
		return nil
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		interpreter.RaiseUserError("Найти(" + p.entity.Name + "." + field + "): неверный идентификатор найденной записи")
	}
	if err := p.s.checkDSLRowAccess(p.ctx(), p.entity, "read", id, nil); err != nil {
		if errors.Is(err, interpreter.ErrRowAccessDenied) {
			return nil
		}
		interpreter.RaiseUserError("Найти(" + p.entity.Name + "." + field + "): " + err.Error())
	}
	return &interpreter.Ref{UUID: idStr, Name: display, Type: p.entity.Name, Manager: p}
}

// matchByField — safe-match по реквизиту документа: Структура со Статусом,
// Ссылкой (только при ровно одном совпадении) и Количеством.
func (p *docProxy) matchByField(field string, raw any) any {
	value := interpreter.MatchValueString(raw)
	if p.s.dslFieldSearchDenied(p.ctx(), p.entity, field) {
		interpreter.RaiseUserError("ПроверитьСовпадениеПоРеквизиту(" + p.entity.Name + "." + field + "): реквизит защищён политикой поля")
	}
	if p.s.rowAccessRestricted(p.ctx(), p.entity, "read") {
		ids, displays, err := p.visibleMatches(field, value)
		if err != nil {
			interpreter.RaiseUserError("ПроверитьСовпадениеПоРеквизиту(" + p.entity.Name + "." + field + "): " + err.Error())
		}
		var ref *interpreter.Ref
		if len(ids) == 1 {
			ref = &interpreter.Ref{UUID: ids[0], Name: displays[0], Type: p.entity.Name, Manager: p}
		}
		return interpreter.NewMatchResultStruct(ref, len(ids))
	}
	idStr, display, count, err := p.s.store.MatchCatalogByField(p.ctx(), p.entity, field, value)
	if err != nil {
		interpreter.RaiseUserError("ПроверитьСовпадениеПоРеквизиту(" + p.entity.Name + "." + field + "): " + err.Error())
	}
	var ref *interpreter.Ref
	if count == 1 {
		id, err := uuid.Parse(idStr)
		if err != nil {
			interpreter.RaiseUserError("ПроверитьСовпадениеПоРеквизиту(" + p.entity.Name + "." + field + "): неверный идентификатор найденной записи")
		}
		if err := p.s.checkDSLRowAccess(p.ctx(), p.entity, "read", id, nil); err != nil {
			if errors.Is(err, interpreter.ErrRowAccessDenied) {
				return interpreter.NewMatchResultStruct(nil, 0)
			}
			interpreter.RaiseUserError("ПроверитьСовпадениеПоРеквизиту(" + p.entity.Name + "." + field + "): " + err.Error())
		}
		ref = &interpreter.Ref{UUID: idStr, Name: display, Type: p.entity.Name, Manager: p}
	}
	return interpreter.NewMatchResultStruct(ref, count)
}

func (p *docProxy) visibleMatches(field, value string) ([]string, []string, error) {
	ids, displays, err := p.s.store.ListCatalogMatchesByField(p.ctx(), p.entity, field, value)
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
		if err := p.s.checkDSLRowAccess(p.ctx(), p.entity, "read", id, nil); err != nil {
			if errors.Is(err, interpreter.ErrRowAccessDenied) {
				continue
			}
			return nil, nil, err
		}
		visibleIDs = append(visibleIDs, idStr)
		visibleDisplays = append(visibleDisplays, displays[i])
	}
	return visibleIDs, visibleDisplays, nil
}

// DeleteRef реализует interpreter.RefManager — удаление документа по UUID.
// Для проводимых документов сначала очищает движения по всем регистрам —
// иначе после DELETE останутся осиротевшие движения (recorder указывает на
// удалённый документ), которые раздувают остатки. То же делает UI-удаление
// (deleteRecord в handlers.go) и API; раньше DSL-путь это пропускал, из-за
// чего повторные запуски обработок накапливали движения.
func (p *docProxy) DeleteRef(uuidStr string) error {
	id, err := uuid.Parse(uuidStr)
	if err != nil {
		return fmt.Errorf("неверный идентификатор ссылки: %q", uuidStr)
	}
	ctx := p.ctx()
	if err := p.s.checkDSLRowAccess(ctx, p.entity, "delete", id, nil); err != nil {
		return err
	}
	var delBefore map[string]any
	if p.entity.NotifyChanges {
		delBefore, _ = p.s.store.GetByID(ctx, p.entity.Name, id, p.entity)
	}
	// Через entityservice — тот же путь, что у UI и REST: хуки модуля объекта
	// «ПередУдалением»/«ПослеУдаления», движения, ТЧ, планы обмена.
	res, err := p.s.entityService().Delete(ctx, p.entity, id)
	if err != nil {
		return err
	}
	if p.messages != nil {
		*p.messages = append(*p.messages, res.DSLMessages...)
	} else {
		appendDSLMessages(ctx, res.DSLMessages)
	}
	if res.DSLError != "" {
		// Хук отменил удаление. Для вызывающего DSL-кода это обычная
		// прикладная ошибка, ловится Попыткой.
		interpreter.RaiseUserError(res.DSLError)
	}
	p.s.publishDocChange(ctx, p.entity, id, "удалён", delBefore)
	// Веб-хук <kind>.delete (план 29) — как в UI-обработчике физического
	// удаления. Пометка на удаление обратима и событием не считается (markRef).
	p.s.dispatchDocWebhook(ctx, string(p.entity.Kind)+".delete", p.entity, id, nil)
	return nil
}

// unpostRef отменяет проведение документа через entityservice.Unpost — тем же
// путём, что UI-кнопка «Отменить проведение» и REST unpost: чистит движения,
// снимает posted и запускает ОбработкаУдаленияПроведения (OnUnpost) в одной
// транзакции (ошибка хука откатывает и движения, и признак проведения). Раньше
// DSL чистил движения и снимал posted напрямую, молча пропуская хук отката.
// WithTxIfNeeded внутри Unpost переиспользует открытую DSL-транзакцию, если она есть.
func (p *docProxy) unpostRef(uuidStr string) error {
	id, err := uuid.Parse(uuidStr)
	if err != nil {
		return fmt.Errorf("неверный идентификатор ссылки: %q", uuidStr)
	}
	ctx := p.ctx()
	if err := p.s.checkDSLRowAccess(ctx, p.entity, "unpost", id, nil); err != nil {
		return err
	}
	result, err := p.s.entitySvc.Unpost(ctx, p.entity, id)
	if err != nil {
		return err
	}
	if result.DSLError != "" {
		return fmt.Errorf("%s", result.DSLError)
	}
	// Веб-хук document.unpost (план 29) — как в UI-обработчике «Отменить проведение»
	// (entityservice.Unpost хуки не диспетчеризует, это делает вызывающий).
	p.s.dispatchDocWebhook(ctx, "document.unpost", p.entity, id, nil)
	return nil
}

// markRef помечает/снимает пометку на удаление (с авто-отменой проведения при
// пометке проведённого документа). Использует живой ctx.
func (p *docProxy) markRef(uuidStr string, mark bool) error {
	id, err := uuid.Parse(uuidStr)
	if err != nil {
		return fmt.Errorf("неверный идентификатор ссылки: %q", uuidStr)
	}
	if err := p.s.checkDSLRowAccess(p.ctx(), p.entity, "delete", id, nil); err != nil {
		return err
	}
	return p.s.markForDeletion(p.ctx(), p.entity, id, mark)
}

// LoadObject реализует interpreter.RefManager — загружает существующий документ
// (шапка + табличные части) по UUID и возвращает docWriter, через который
// Ссылка.ПолучитьОбъект().Поле = … → Записать()/Провести() обновят документ.
func (p *docProxy) LoadObject(uuidStr string) (any, error) {
	id, err := uuid.Parse(uuidStr)
	if err != nil {
		return nil, fmt.Errorf("неверный идентификатор ссылки: %q", uuidStr)
	}
	if err := p.s.checkDSLRowAccess(p.ctx(), p.entity, "read", id, nil); err != nil {
		return nil, err
	}
	// loadRuntimeObject грузит шапку + ТЧ и обогащает ссылочные поля до
	// *Ref{…,Manager}, чтобы DSL мог писать Док.СсылочноеПоле.ПолучитьОбъект().
	obj, err := p.s.loadRuntimeObject(p.ctx(), p.entity, id)
	if err != nil {
		return nil, err
	}
	version, err := p.s.store.EntityVersion(p.ctx(), p.entity.Name, id)
	if err != nil {
		return nil, err
	}
	return &docWriter{
		s:               p.s,
		ctxSrc:          p.ctxSrc,
		entity:          p.entity,
		obj:             obj,
		messages:        p.messages,
		loaded:          true,
		expectedVersion: &version,
	}, nil
}

// docWriter — записываемый/проводимый документ.
//
//	Док = Документы.ПоступлениеТоваров.Создать();
//	Док.Дата = ТекущаяДата();
//	Стр = Док.Товары.Добавить();
//	Стр.Номенклатура = Ном; Стр.Количество = 100; Стр.Цена = 500;
//	Док.Записать();
//	Док.Провести();
type docWriter struct {
	s        *Server
	ctxSrc   docsCtxSource
	entity   *metadata.Entity
	obj      *runtime.Object
	messages *[]string
	// loaded — объект получен из БД (Ссылка.ПолучитьОбъект), а не создан.
	// saved — объект уже записан в этой сессии. Оба используются ЭтоНовый().
	loaded          bool
	saved           bool
	expectedVersion *int64
	// assigned — реквизиты, присвоенные модулем в этой сессии: их чтение не
	// маскируется, значение принадлежит текущей операции (план 88E).
	assigned map[string]bool
}

func (w *docWriter) ctx() context.Context {
	if w.ctxSrc != nil {
		return w.ctxSrc.Ctx()
	}
	return context.Background()
}

// Get: имя табличной части → tpProxy, иначе значение поля шапки. Реквизит
// прочитанного из БД документа отдаётся по полевой политике роли (план 88E) —
// значение, присвоенное самим модулем, возвращается как есть.
func (w *docWriter) Get(name string) any {
	for _, tp := range w.entity.TableParts {
		if strings.EqualFold(tp.Name, name) {
			return &tpProxy{obj: w.obj, tpName: tp.Name}
		}
	}
	v := w.obj.Get(name)
	if !w.loaded || w.assigned[strings.ToLower(strings.TrimSpace(name))] {
		return v
	}
	return w.s.maskDSLValue(w.ctx(), w.entity, name, v)
}

func (w *docWriter) Set(name string, v any) {
	if w.assigned == nil {
		w.assigned = map[string]bool{}
	}
	w.assigned[strings.ToLower(strings.TrimSpace(name))] = true
	w.obj.Set(name, v)
}

func (w *docWriter) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "записать", "write":
		if err := w.write(); err != nil {
			interpreter.RaiseUserError("Записать(" + w.entity.Name + "): " + err.Error())
		}
		return w.ref()
	case "провести", "post":
		if err := w.conduct(); err != nil {
			interpreter.RaiseUserError("Провести(" + w.entity.Name + "): " + err.Error())
		}
		return w.ref()
	case "заполнить", "fill":
		if len(args) == 0 {
			interpreter.RaiseUserError("Заполнить(" + w.entity.Name + "): не передано основание")
		}
		if err := w.fill(args[0]); err != nil {
			interpreter.RaiseUserError("Заполнить(" + w.entity.Name + "): " + err.Error())
		}
		return nil
	case "установитьзначение", "setvalue":
		if len(args) >= 2 {
			if n, ok := args[0].(string); ok {
				w.Set(n, args[1])
			}
		}
	case "этоновый", "isnew":
		return !w.loaded && !w.saved
	case "прочитать", "read":
		if err := w.read(); err != nil {
			interpreter.RaiseUserError("Прочитать(" + w.entity.Name + "): " + err.Error())
		}
		return nil
	}
	return nil
}

// read перечитывает шапку и табличные части документа из БД
// (Документ.Прочитать()). Использует тот же путь загрузки, что и
// Ссылка.ПолучитьОбъект().
// forgetAssigned снимает признак «присвоено модулем» с перечисленных реквизитов:
// после этого Get() отдаёт их по полевой политике роли, а не как есть.
func (w *docWriter) forgetAssigned(names []string) {
	for _, n := range names {
		delete(w.assigned, strings.ToLower(strings.TrimSpace(n)))
	}
}

func (w *docWriter) read() error {
	if err := w.s.checkDSLRowAccess(w.ctx(), w.entity, "read", w.obj.ID, nil); err != nil {
		return err
	}
	row, err := w.s.store.GetByID(w.ctx(), w.entity.Name, w.obj.ID, w.entity)
	if err != nil {
		return err
	}
	fields := make(map[string]any, len(row))
	for _, f := range w.entity.Fields {
		if v, ok := row[f.Name]; ok && v != nil {
			fields[strings.ToLower(f.Name)] = v
		}
	}
	tpRows := make(map[string][]map[string]any, len(w.entity.TableParts))
	for _, tp := range w.entity.TableParts {
		rows, err := w.s.store.GetTablePartRows(w.ctx(), w.entity.Name, tp.Name, w.obj.ID, tp)
		if err != nil {
			return fmt.Errorf("табличная часть %s: %w", tp.Name, err)
		}
		tpRows[tp.Name] = rows
	}
	w.obj.Fields = fields
	w.obj.TablePartRows = tpRows
	// Прочитанный объект целиком приехал из БД: присвоенного модулем в нём
	// больше нет, а сохранённый признак снимал бы маску с реальных значений.
	w.assigned = nil
	w.s.enrichHeaderRefs(w.ctx(), w.entity, w.obj)
	for _, tp := range w.entity.TableParts {
		w.s.enrichTPRowsWithRefs(w.ctx(), tp, tpRows[tp.Name])
	}
	version, err := w.s.store.EntityVersion(w.ctx(), w.entity.Name, w.obj.ID)
	if err != nil {
		return err
	}
	w.expectedVersion = &version
	w.loaded = true
	return nil
}

// fill реализует Документы.X.СоздатьДокумент().Заполнить(Источник): запускает
// ОбработкаЗаполнения у приёмника, переносит результат в obj.Fields/TablePartRows.
// Источник — *interpreter.Ref или *runtime.Object. Делегирует entityservice.Fill,
// единая точка вызова OnFill вместе с UI-handler'ом.
func (w *docWriter) fill(src any) error {
	var srcType string
	var srcID uuid.UUID
	switch v := src.(type) {
	case *interpreter.Ref:
		if v == nil {
			return fmt.Errorf("ссылка пустая")
		}
		srcType = v.Type
		id, err := uuid.Parse(v.UUID)
		if err != nil {
			return fmt.Errorf("неверный UUID ссылки: %s", v.UUID)
		}
		srcID = id
	case *runtime.Object:
		if v == nil {
			return fmt.Errorf("объект-основание пустой")
		}
		srcType = v.Type
		srcID = v.ID
	default:
		return fmt.Errorf("ожидается ссылка или объект, получено %T", src)
	}
	result, err := w.s.entitySvc.Fill(w.ctx(), entityservice.FillRequest{
		Receiver:   w.entity,
		SourceType: srcType,
		SourceID:   srcID,
	})
	if err != nil {
		return err
	}
	if result.DSLError != "" {
		return fmt.Errorf("%s", result.DSLError)
	}
	for k, v := range result.Fields {
		w.obj.Fields[strings.ToLower(k)] = v
	}
	for tpName, rows := range result.TablePartRows {
		if rows != nil {
			w.obj.TablePartRows[tpName] = rows
		}
	}
	return nil
}

// write проставляет номер документа, вызывает ПриЗаписи (OnWrite), затем
// сохраняет шапку + табличные части. Автонумерация делегируется той же
// реализации entityservice, которой пользуются форма, ИИ и REST; вызов здесь
// остаётся нужен, потому что docWriter сохраняет документ напрямую.
// Использует живой ctx, поэтому при открытой DSL-транзакции запись
// участвует в ней; иначе автокоммит.
func (w *docWriter) write() error {
	return w.withLockScope(w.writeInContext)
}

// withLockScope — WithTxScope + LockCollector в контексте (если его ещё нет,
// как при вызове из обработки): внутрипроцессные мьютексы, взятые хуком через
// БлокировкаДанных без явного Разблокировать(), освобождаются после выхода из
// транзакции, а не утекают навсегда. Зеркалит entityservice.Save
// (service.go: lockCollector + defer ReleaseAll).
func (w *docWriter) withLockScope(fn func(ctx context.Context) error) error {
	base := w.ctx()
	if runtime.LockCollectorFromContext(base) != nil {
		return w.s.store.WithTxScope(base, fn)
	}
	lc := runtime.NewLockCollector()
	defer lc.ReleaseAll()
	return w.s.store.WithTxScope(base, func(ctx context.Context) error {
		return fn(runtime.ContextWithLockCollector(ctx, lc))
	})
}

func (w *docWriter) writeInContext(ctx context.Context) error {
	return w.writeInContextForAction(ctx, false)
}

func (w *docWriter) writeInContextForAction(ctx context.Context, posting bool) error {
	// Pre-образ живого списка (план 87): для существующего документа читаем строку
	// ДО записи, чтобы прежний владелец убрал её из списка при смене прав.
	var changeBefore map[string]any
	if w.entity.NotifyChanges && w.loaded {
		changeBefore, _ = w.s.store.GetByID(ctx, w.entity.Name, w.obj.ID, w.entity)
	}
	// Значения перечислений — до записи и до хука, тем же кодом, что и на пути
	// entityservice.Save (#962). Без этого модуль клал в реквизит-перечисление
	// произвольную строку: форма показывала пустой список, а сравнения вида
	// «Если Статус = "Закрыта"» молча не срабатывали. Правится это здесь, а не
	// копией проверки, потому что копий и так три.
	if msg := entityservice.ValidateEnumFields(w.s.reg, w.entity, w.obj.Fields, w.obj.TablePartRows); msg != "" {
		// Как на пути справочников (dsl_catalogs.go): текст проверки уходит
		// пользователю как есть, это не технический сбой.
		return fmt.Errorf("%s", msg)
	}
	// Number first so row-level write policy and the form/entity hook observe
	// the same value that will be persisted. writeInContext always owns a
	// transaction/savepoint, so rejection restores both counter and obj map.
	if err := w.s.entityService().EnsureAutoNumber(ctx, w.entity, w.obj); err != nil {
		return err
	}
	if w.accessID() == uuid.Nil {
		if err := w.s.autoFillRowAccessFields(ctx, w.entity, "write", w.obj.Fields); err != nil {
			return err
		}
		if posting {
			if err := w.s.autoFillRowAccessFields(ctx, w.entity, "post", w.obj.Fields); err != nil {
				return err
			}
		}
	}
	if err := w.s.checkDSLRowAccess(ctx, w.entity, "write", w.accessID(), w.obj.Fields); err != nil {
		return err
	}
	if posting {
		if err := w.s.checkDSLRowAccess(ctx, w.entity, "post", w.accessID(), w.obj.Fields); err != nil {
			return err
		}
	}
	// План 88E: реквизит, видный модулю только под маской, не перезаписывается —
	// тот же контракт, что у формы и REST («нельзя изменить то, что не видно»).
	if w.loaded || w.saved {
		restored, err := w.s.protectMaskedFieldsOnWrite(ctx, w.entity, w.obj.ID, w.obj.Fields)
		if err != nil {
			return err
		}
		// Восстановленное значение ложится в тот же набор, который читает
		// Get(): без снятия признака «присвоено модулем» защита записи сама
		// стала бы каналом раскрытия — после Записать() модуль прочитал бы
		// реальное значение, которого не видел до неё.
		w.forgetAssigned(restored)
	}
	// Псевдо-реквизит «Ссылка» самого документа — до запуска OnWrite, как это уже
	// делается на пути проведения (ensureSelfRef перед OnPost) и в entityservice.Save.
	// Без него this.Ссылка в ПриЗаписи был бы пуст на DSL-пути записи, из-за чего
	// запись ссылки на себя (Дв.X = this.Ссылка) или чтение пре-образа по своей
	// ссылке в хуке не работали. EnsureAutoNumber уже проставил Номер → displayName корректен.
	w.ensureSelfRef()
	mc := runtime.NewMovementsCollector(w.entity.Name, w.obj.ID).WillPersist()
	setPeriodFromFields(mc, w.entity, w.obj.Fields)
	errMsg, hookMessages := w.s.runOnWriteCtx(ctx, w.obj, mc)
	w.appendHookMessages(hookMessages)
	if errMsg != "" {
		return fmt.Errorf("%s", errMsg)
	}
	// Хук мог присвоить реквизиту-перечислению недопустимое значение (#977):
	// входная проверка была до него. Транзакция ещё открыта — откат бесплатен.
	if msg := entityservice.ValidateEnumFields(w.s.reg, w.entity, w.obj.Fields, w.obj.TablePartRows); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	// Validate the object only after auto-numbering, access preflight and
	// OnWrite have produced the values that will actually be persisted. The
	// storage backstop still protects direct writers; this object-level check
	// also covers table parts before either the header or its rows are written.
	if msg := storage.ValidateRequiredObjectValues(w.entity, w.obj.Fields, w.obj.TablePartRows, true); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	if w.expectedVersion == nil {
		if err := w.s.store.Upsert(ctx, w.entity.Name, w.obj.ID, w.obj.Fields, w.entity); err != nil {
			return err
		}
	} else {
		if err := w.s.store.UpsertVersioned(ctx, w.entity.Name, w.obj.ID, w.obj.Fields, w.entity, w.expectedVersion); err != nil {
			return err
		}
	}
	if err := w.s.saveTablePartsDirect(ctx, w.entity, w.obj.ID, w.obj.TablePartRows); err != nil {
		return err
	}
	// Регистрация изменения для планов обмена (план 86): запись документа из DSL
	// идёт мимо entityservice.Save. Провести() зовёт write() → регистрируется и оно.
	if err := exchange.RegisterOnSave(ctx, w.s.store, w.s.reg.ExchangePlans(), w.entity, w.obj.ID, false); err != nil {
		return err
	}
	// Для непроводимых документов движения, записанные в ПриЗаписи, фиксируем.
	// У проводимых документов движения формирует проведение (post).
	if !w.entity.Posting {
		if err := w.s.saveMovements(ctx, w.entity.Name, w.obj.ID, mc); err != nil {
			return err
		}
	}
	version, err := w.s.store.EntityVersion(ctx, w.entity.Name, w.obj.ID)
	if err != nil {
		return err
	}
	wasSaved, previousVersion := w.saved, w.expectedVersion
	w.saved = true
	w.expectedVersion = &version
	storage.DeferUntilTxRollback(ctx, func() {
		w.saved = wasSaved
		w.expectedVersion = previousVersion
	})
	// Живой список (план 87): отложенная до commit публикация «данные.<сущность>».
	w.s.publishDocChange(ctx, w.entity, w.obj.ID, "записан", changeBefore)
	// Веб-хук document.save (план 29) — Провести() зовёт write(), поэтому событие
	// записи приходит и перед document.post, как на пути entityservice.Save.
	w.s.dispatchDocWebhook(ctx, "document.save", w.entity, w.obj.ID, w.obj.Fields)
	return nil
}

func (w *docWriter) accessID() uuid.UUID {
	if w.loaded || w.saved {
		return w.obj.ID
	}
	return uuid.Nil
}

// conduct performs the implicit write and posting as one atomic operation.
// OnWrite, OnPost and all nested DSL writes share the same transaction/scope.
func (w *docWriter) conduct() error {
	return w.withLockScope(func(ctx context.Context) error {
		if err := w.writeInContextForAction(ctx, true); err != nil {
			return err
		}
		return w.postInContextAfterAccess(ctx)
	})
}

func (w *docWriter) post() error {
	return w.withLockScope(w.postInContext)
}

// postInContext запускает OnPost, собирает движения и фиксирует проведение —
// та же логика, что в postDocument (UI-проведение).
func (w *docWriter) postInContext(ctx context.Context) error {
	if err := w.s.checkDSLRowAccess(ctx, w.entity, "post", w.obj.ID, w.obj.Fields); err != nil {
		return err
	}
	return w.postInContextAfterAccess(ctx)
}

func (w *docWriter) postInContextAfterAccess(ctx context.Context) error {
	// Инвариант: помеченный на удаление документ нельзя провести (как в 1С).
	if marked, err := w.s.store.IsMarkedForDeletion(ctx, w.entity.Name, w.obj.ID); err != nil {
		return err
	} else if marked {
		return storage.ErrPostingDeletionMarked
	}
	w.ensureSelfRef()
	mc := runtime.NewMovementsCollector(w.entity.Name, w.obj.ID).WillPersist()
	setPeriodFromFields(mc, w.entity, w.obj.Fields)
	// Дата запрета проведения (свёртка базы, план 74).
	if mc.Period != nil {
		if lock, ok := w.s.store.GetPostingLockDate(ctx); ok && storage.PostingFrozen(lock, *mc.Period) {
			return storage.PostingFrozenError(lock)
		}
	}
	errMsg, hookMessages := w.s.runOnPostCtx(ctx, w.obj, mc)
	w.appendHookMessages(hookMessages)
	if errMsg != "" {
		return fmt.Errorf("%s", errMsg)
	}
	// Хук мог присвоить реквизиту-перечислению недопустимое значение (#977):
	// входная проверка была до него. Транзакция ещё открыта — откат бесплатен.
	if msg := entityservice.ValidateEnumFields(w.s.reg, w.entity, w.obj.Fields, w.obj.TablePartRows); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	// OnPost may change both header fields and table-part rows. Revalidate the
	// full, loaded document before persisting hook effects and movements.
	if msg := storage.ValidateRequiredObjectValues(w.entity, w.obj.Fields, w.obj.TablePartRows, true); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	// OnPost мог изменить реквизиты шапки (расчётные поля) — персистим их upsert'ом
	// после хука, как это делает entityservice.Save при проведении. writeInContext
	// уже создал ровно одну логическую версию этой операции, поэтому сохраняем
	// hook-поля без второго инкремента _version.
	if err := w.s.store.UpsertAfterVersionBump(ctx, w.entity.Name, w.obj.ID, w.obj.Fields, w.entity); err != nil {
		return err
	}
	if err := w.s.saveMovements(ctx, w.entity.Name, w.obj.ID, mc); err != nil {
		return err
	}
	if err := w.s.store.SetPosted(ctx, w.entity.Name, w.obj.ID, true); err != nil {
		return err
	}
	if err := exchange.RegisterOnSave(ctx, w.s.store, w.s.reg.ExchangePlans(), w.entity, w.obj.ID, false); err != nil {
		return err
	}
	// Живой список (план 87): «проведён» после успешного проведения из DSL.
	w.s.publishDocChange(ctx, w.entity, w.obj.ID, "проведён", nil)
	// Веб-хук document.post (план 29).
	w.s.dispatchDocWebhook(ctx, "document.post", w.entity, w.obj.ID, w.obj.Fields)
	return nil
}

func (w *docWriter) appendHookMessages(messages []string) {
	if w.messages != nil && len(messages) > 0 {
		*w.messages = append(*w.messages, messages...)
	}
}

// ensureSelfRef устанавливает псевдо-реквизит «Ссылка» самого документа, чтобы
// this.Ссылка в OnPost/OnWrite указывал на сам документ (нужно для записи
// DocumentRef на себя в регистр сведений: Дв.Спецификация = this.Ссылка).
func (w *docWriter) ensureSelfRef() {
	if w.obj.Fields == nil {
		w.obj.Fields = map[string]any{}
	}
	selfRef := &interpreter.Ref{UUID: w.obj.ID.String(), Name: w.displayName(), Type: w.entity.Name}
	w.obj.Fields["ссылка"] = selfRef
	w.obj.Fields["reference"] = selfRef
}

// ref строит ссылку на записанный документ с привязкой к менеджеру,
// чтобы Ссылка.Удалить() работала.
func (w *docWriter) ref() *interpreter.Ref {
	return &interpreter.Ref{
		UUID:    w.obj.ID.String(),
		Name:    w.displayName(),
		Type:    w.entity.Name,
		Manager: &docProxy{s: w.s, ctxSrc: w.ctxSrc, entity: w.entity, messages: w.messages},
	}
}

func (w *docWriter) displayName() string {
	if value, configured := explicitPresentationValue(w.entity, w.obj.Fields); configured {
		if value != "" {
			return value
		}
		id := w.obj.ID.String()
		if len(id) >= 8 {
			return w.entity.Name + ":" + id[:8]
		}
		return w.entity.Name
	}

	for _, k := range []string{"номер", "number"} {
		if v, ok := w.obj.Fields[k]; ok && v != nil {
			if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
				return s
			}
		}
	}
	id := w.obj.ID.String()
	if len(id) >= 8 {
		return w.entity.Name + ":" + id[:8]
	}
	return w.entity.Name
}

// tpProxy — табличная часть документа (Док.Товары).
// tpProxy — табличная часть записываемого объекта (документа или справочника).
type tpProxy struct {
	obj    *runtime.Object
	tpName string
}

func (t *tpProxy) Get(_ string) any    { return nil }
func (t *tpProxy) Set(_ string, _ any) {}

// IterateRows реализует контракт цикла «Для Каждого Стр Из Док.ТЧ» — отдаёт
// загруженные строки ТЧ. Без этого ТЧ документа, полученного из БД
// (Ссылка.ПолучитьОбъект / НайтиПоНомеру), нельзя было прочитать в DSL.
func (t *tpProxy) IterateRows() []map[string]any {
	return t.obj.TablePartRows[t.tpName]
}

// KnownMethods реализует interpreter.MethodLister: опечатка в имени метода
// обязана быть слышной. Список общий с обработчиками и событиями формы — у
// одной табличной части не должно быть трёх разных наборов методов в
// зависимости от того, откуда до неё добрались (issue #842).
func (t *tpProxy) KnownMethods() (string, []string) {
	return "ТабличнаяЧасть", runtime.TablePartMethods()
}

func (t *tpProxy) CallMethod(method string, args []any) any {
	rows := t.obj.TablePartRows[t.tpName]
	switch strings.ToLower(method) {
	case "добавить", "add":
		row := map[string]any{}
		t.obj.TablePartRows[t.tpName] = append(rows, row)
		return &interpreter.MapThis{M: row}
	case "очистить", "clear":
		t.obj.TablePartRows[t.tpName] = nil
	case "количество", "count":
		return float64(len(rows))
	case "получить", "get":
		if len(args) > 0 {
			if idx := runtime.RowIndexArg(args[0]); idx >= 0 && idx < len(rows) {
				return &interpreter.MapThis{M: rows[idx]}
			}
		}
	case "удалить", "delete":
		if len(args) > 0 {
			if idx := runtime.RowIndexArg(args[0]); idx >= 0 && idx < len(rows) {
				t.obj.TablePartRows[t.tpName] = append(rows[:idx], rows[idx+1:]...)
			}
		}
	case "итог", "total":
		if len(args) > 0 {
			return runtime.SumRowsColumn(rows, fmt.Sprintf("%v", args[0]))
		}
	}
	return nil
}

// dslFieldSearchChecker отдаёт полевую политику прокси справочников: поиск по
// защищённому реквизиту обязан отклоняться и там, а не только у документов,
// где гейт стоял с самого начала (план 88E).
type dslFieldSearch struct{ s *Server }

func (c dslFieldSearch) IsFieldSearchDenied(ctx context.Context, entity *metadata.Entity, field string) bool {
	return c.s.dslFieldSearchDenied(ctx, entity, field)
}

func (s *Server) dslFieldSearchChecker() interpreter.FieldSearchChecker { return dslFieldSearch{s: s} }
