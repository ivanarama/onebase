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

// newEntityService собирает entityservice.Service, привязанный к серверу:
// PrepareHook/EnrichTPRows — обогащение ссылок, BuildVars — полный DSL-набор,
// MakeThis — обёртка с методами ТЧ. Используется и полным сервером (New), и
// offline-запуском обработок (RunProcessorOffline).
func (s *Server) newEntityService(hooks *webhook.Dispatcher) *entityservice.Service {
	// Пустое хранилище кладём нетипизированным nil. Server штатно поднимается
	// без БД (ui.New(reg, nil, ...)), а интерфейс, обёрнутый вокруг
	// (*storage.DB)(nil), сам по себе не равен nil — прямое присваивание молча
	// обезвредило бы проверки `s.Store == nil` внутри сервиса, и вместо
	// внятной ошибки получилась бы паника на первом же обращении к базе.
	var store entityservice.Storage
	if s.store != nil {
		store = s.store
	}
	return &entityservice.Service{
		Store:        store,
		Reg:          s.reg,
		Interp:       s.interp,
		PrepareHook:  s.enrichHeaderRefs,
		EnrichTPRows: s.enrichTPRowsWithRefs,
		BuildVars:    s.buildDSLVarsWithMessagesTx,
		MakeThis: func(ctx context.Context, ctxSrc interpreter.CtxSource, obj *runtime.Object, e *metadata.Entity) interpreter.This {
			return s.newFormObjectThisLive(ctx, ctxSrc, obj, e, nil, false)
		},
		// Регистрация записи в планах обмена (план 86): exchange принимает
		// конкретный *storage.DB, а сервис знает только порт Storage, поэтому
		// вызов попадает в него швом — симметрично удалению ниже.
		RegisterExchangeSave: func(ctx context.Context, e *metadata.Entity, id uuid.UUID, deletion bool) error {
			return exchange.RegisterOnSave(ctx, s.store, s.reg.ExchangePlans(), e, id, deletion)
		},
		// Регистрация удаления в планах обмена (план 86): exchange живёт в
		// ui-слое, поэтому в сервис попадает швом.
		RegisterExchangeDelete: func(ctx context.Context, e *metadata.Entity, id uuid.UUID) error {
			return exchange.RegisterOnDelete(ctx, s.store, s.reg.ExchangePlans(), e, id)
		},
		// Исходящие веб-хуки (план 29): save/post диспетчеризуются из Save.
		Hooks: hooks,
		// Живой список (план 87): автопубликация «данные.<сущность>» после
		// успешной записи/проведения. nil при поднятой без шины (тесты/headless).
		ChangePublisher: s.newChangePublisher(),
		// Предел времени прикладного хука (#865). Берём общий предел запроса:
		// хук исполняется внутри запроса и внутри его транзакции, поэтому жить
		// дольше запроса ему незачем. 0 (предел не настроен) — прежнее
		// поведение без лимита.
		HookTimeout: s.operationTimeout(opEntitySave),
	}
}

// BuildJobDSLVars — полное DSL-окружение для регламентных заданий (план 101):
// scheduler вызывает его вместо собственного базового набора, чтобы задания
// имели Справочники/Документы/вложения/транзакции — как обработки из UI/procrun.
// Вместе с картой возвращается TxState: scheduler закрывает его после запуска.
func (s *Server) BuildJobDSLVars(ctx context.Context, mc *runtime.MovementsCollector, messages *[]string) (map[string]any, *interpreter.TxState) {
	return s.buildDSLVarsWithMessagesTx(ctx, mc, messages)
}

// catFactory реализует interpreter.CatalogObjectFactory: объекты справочников,
// создаваемые из DSL (Справочники.X.Создать() / Ссылка.ПолучитьОбъект()),
// получают табличные части и DSL-хук ПриЗаписи — симметрично документам
// (docWriter). Сохранение идёт через entityservice.Save, то есть тем же путём,
// что и запись из веб-формы: хук, ТЧ, движения, веб-хуки, планы обмена.
type catFactory struct {
	s      *Server
	ctxSrc docsCtxSource
}

func (s *Server) catObjectFactory(ctxSrc docsCtxSource) interpreter.CatalogObjectFactory {
	return &catFactory{s: s, ctxSrc: ctxSrc}
}

// NewCatalogObject — Справочники.X.Создать(). Значения по умолчанию и хук
// ПриСозданииНового (план 153) берутся из entityservice, как в форме и REST.
func (f *catFactory) NewCatalogObject(entity *metadata.Entity) any {
	res, err := f.s.entitySvc.NewObject(f.ctx(), entityservice.NewObjectRequest{Entity: entity})
	if err != nil {
		interpreter.RaiseUserError("Создать(" + entity.Name + "): " + err.Error())
	}
	if res.DSLError != "" {
		interpreter.RaiseUserError("ПриСозданииНового(" + entity.Name + "): " + res.DSLError)
	}
	return &catWriter{
		s:      f.s,
		ctxSrc: f.ctxSrc,
		entity: entity,
		obj:    res.Object,
	}
}

func (f *catFactory) LoadCatalogObject(entity *metadata.Entity, uuidStr string) (any, error) {
	id, err := uuid.Parse(uuidStr)
	if err != nil {
		return nil, fmt.Errorf("неверный идентификатор ссылки: %q", uuidStr)
	}
	ctx := f.ctx()
	if err := f.s.checkDSLRowAccess(ctx, entity, "read", id, nil); err != nil {
		return nil, err
	}
	obj, err := f.s.loadRuntimeObject(ctx, entity, id)
	if err != nil {
		return nil, err
	}
	version, err := f.s.store.EntityVersion(ctx, entity.Name, id)
	if err != nil {
		return nil, err
	}
	return &catWriter{
		s: f.s, ctxSrc: f.ctxSrc, entity: entity, obj: obj, loaded: true,
		expectedVersion: &version,
	}, nil
}

func (f *catFactory) ctx() context.Context {
	if f.ctxSrc != nil {
		return f.ctxSrc.Ctx()
	}
	return context.Background()
}

// catWriter — записываемый объект справочника с табличными частями.
//
//	Кл = Справочники.Клиенты.Создать();
//	Кл.Наименование = "ООО Ромашка";
//	Стр = Кл.Контакты.Добавить();
//	Стр.Вид = "Email"; Стр.Значение = "info@romashka.ru";
//	Ссылка = Кл.Записать();
type catWriter struct {
	s      *Server
	ctxSrc docsCtxSource
	entity *metadata.Entity
	obj    *runtime.Object
	// loaded — объект получен из БД (Ссылка.ПолучитьОбъект), а не создан.
	// saved — объект уже записан в этой сессии. Оба используются ЭтоНовый().
	loaded          bool
	saved           bool
	expectedVersion *int64
	// assigned — реквизиты, присвоенные модулем в этой сессии: их чтение не
	// маскируется, значение принадлежит текущей операции (план 88E).
	assigned map[string]bool
}

func (w *catWriter) ctx() context.Context {
	if w.ctxSrc != nil {
		return w.ctxSrc.Ctx()
	}
	return context.Background()
}

// Get: имя табличной части → tpProxy, иначе значение поля шапки. Реквизит
// прочитанного из БД объекта отдаётся по полевой политике роли (план 88E) —
// значение, присвоенное самим модулем, возвращается как есть.
func (w *catWriter) Get(name string) any {
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

func (w *catWriter) Set(name string, v any) {
	if w.assigned == nil {
		w.assigned = map[string]bool{}
	}
	w.assigned[strings.ToLower(strings.TrimSpace(name))] = true
	w.obj.Set(name, v)
}

// Fields — имена заполненных полей объекта: позволяет использовать объект как
// источник в ЗаполнитьЗначенияСвойств (совместимо с CatalogRecordWriter).
// forgetAssigned снимает признак «присвоено модулем» с перечисленных реквизитов:
// после этого Get() отдаёт их по полевой политике роли, а не как есть.
func (w *catWriter) forgetAssigned(names []string) {
	for _, n := range names {
		delete(w.assigned, strings.ToLower(strings.TrimSpace(n)))
	}
}

func (w *catWriter) Fields() []string {
	names := make([]string, 0, len(w.obj.Fields))
	for k := range w.obj.Fields {
		names = append(names, k)
	}
	return names
}

func (w *catWriter) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "записать", "write":
		if err := w.write(); err != nil {
			interpreter.RaiseUserError("Записать(" + w.entity.Name + "): " + err.Error())
		}
		return w.ref()
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
	case "удалитьеслинеизменен", "удалитьеслинеизменён", "deleteifunchanged":
		if err := w.deleteIfUnchanged(); err != nil {
			if errors.Is(err, storage.ErrVersionConflict) {
				return false
			}
			interpreter.RaiseUserError("УдалитьЕслиНеИзменен(" + w.entity.Name + "): " + err.Error())
		}
		return true
	}
	return nil
}

// deleteIfUnchanged binds physical deletion to the revision captured by
// ПолучитьОбъект(). It is intended for background cleanup code that first
// checks fields and must not delete a state written after that check.
func (w *catWriter) deleteIfUnchanged() error {
	if (!w.loaded && !w.saved) || w.expectedVersion == nil {
		return fmt.Errorf("объект ещё не прочитан или не записан")
	}
	ctx := w.ctx()
	id := w.accessID()
	if err := w.s.checkDSLRowAccess(ctx, w.entity, "delete", id, w.obj.Fields); err != nil {
		return err
	}
	if err := (dslCatalogDeleter{s: w.s}).DeleteCatalogRefVersioned(ctx, w.entity, id, *w.expectedVersion); err != nil {
		return err
	}

	wasLoaded, wasSaved, previousVersion := w.loaded, w.saved, w.expectedVersion
	w.loaded = false
	w.saved = false
	w.expectedVersion = nil
	storage.DeferUntilTxRollback(ctx, func() {
		w.loaded = wasLoaded
		w.saved = wasSaved
		w.expectedVersion = previousVersion
	})
	return nil
}

// write сохраняет объект через entityservice.Save — с запуском ПриЗаписи,
// записью табличных частей и веб-хуками, как при записи из веб-формы.
func (w *catWriter) write() error {
	ctx := w.ctx()
	isNew := !w.loaded && !w.saved
	if !isNew {
		if err := w.s.checkDSLRowAccess(ctx, w.entity, "write", w.accessID(), w.obj.Fields); err != nil {
			return err
		}
	}
	// План 88E: реквизит, видный модулю только под маской, не перезаписывается —
	// тот же контракт, что у формы и REST («нельзя изменить то, что не видно»).
	if !isNew {
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
	result, err := w.s.entitySvc.Save(ctx, entityservice.SaveRequest{
		Entity:          w.entity,
		ID:              w.obj.ID,
		IsNew:           isNew,
		Fields:          w.obj.Fields,
		TablePartRows:   w.obj.TablePartRows,
		ExpectedVersion: w.expectedVersion,
		Preflight: func(txCtx context.Context, obj *runtime.Object) error {
			if !isNew {
				return nil
			}
			if err := w.s.autoFillRowAccessFields(txCtx, w.entity, "write", obj.Fields); err != nil {
				return err
			}
			return w.s.checkDSLRowAccess(txCtx, w.entity, "write", uuid.Nil, obj.Fields)
		},
	})
	if err != nil {
		return err
	}
	if result.DSLError != "" {
		return fmt.Errorf("%s", result.DSLError)
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
	return nil
}

func (w *catWriter) accessID() uuid.UUID {
	if w.loaded || w.saved {
		return w.obj.ID
	}
	return uuid.Nil
}

// read перечитывает шапку и табличные части из БД (Объект.Прочитать()).
func (w *catWriter) read() error {
	if !w.loaded && !w.saved {
		return fmt.Errorf("объект ещё не записан")
	}
	if err := w.s.checkDSLRowAccess(w.ctx(), w.entity, "read", w.obj.ID, nil); err != nil {
		return err
	}
	obj, err := w.s.loadRuntimeObject(w.ctx(), w.entity, w.obj.ID)
	if err != nil {
		return err
	}
	w.obj = obj
	// Прочитанный объект целиком приехал из БД: присвоенного модулем в нём
	// больше нет, а сохранённый признак снимал бы маску с реальных значений
	// («Об.Телефон = ""; Об.Прочитать(); Сообщить(Об.Телефон)» отдавал реальный).
	w.assigned = nil
	version, err := w.s.store.EntityVersion(w.ctx(), w.entity.Name, w.obj.ID)
	if err != nil {
		return err
	}
	w.expectedVersion = &version
	w.loaded = true
	return nil
}

// TypeName — «Справочник.X» для ТипЗнч(): симметрично docWriter (issue #1137).
func (w *catWriter) TypeName() string {
	if w == nil || w.entity == nil {
		return "Неопределено"
	}
	return metadata.ObjectTypeName(w.entity.Kind, w.entity.Name)
}

// ref строит ссылку на записанный объект с менеджером-прокси, чтобы
// Ссылка.ПолучитьОбъект()/Удалить() работали и возвращали catWriter.
func (w *catWriter) ref() *interpreter.Ref {
	return &interpreter.Ref{
		UUID:    w.obj.ID.String(),
		Name:    w.displayName(),
		Type:    w.entity.Name,
		Kind:    w.entity.Kind,
		Manager: w.s.refManagerFor(w.entity, w.ctx()),
	}
}

func (w *catWriter) displayName() string {
	if value, configured := explicitPresentationValue(w.entity, w.obj.Fields); configured {
		if value != "" {
			return value
		}
		return shortObjectID(w.obj.ID.String())
	}

	for _, k := range []string{"наименование", "name"} {
		if v, ok := w.obj.Fields[k]; ok && v != nil {
			if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
				return s
			}
		}
	}
	return shortObjectID(w.obj.ID.String())
}

// explicitPresentationValue returns the first non-empty configured
// presentation value. The bool reports whether presentation is configured, so
// callers do not accidentally fall through to their legacy heuristics when all
// explicitly selected values are empty.
func explicitPresentationValue(entity *metadata.Entity, values map[string]any) (string, bool) {
	if entity == nil || len(entity.Presentation) == 0 {
		return "", false
	}
	for _, field := range metadata.LabelFields(entity) {
		_, value, ok := lookupMapCI(values, field.Name)
		if !ok || value == nil {
			continue
		}
		if label := strings.TrimSpace(fmt.Sprint(value)); label != "" {
			return label, true
		}
	}
	return "", true
}

func shortObjectID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}
