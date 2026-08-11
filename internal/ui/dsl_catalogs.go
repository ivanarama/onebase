package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
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
	return &entityservice.Service{
		Store:        s.store,
		Reg:          s.reg,
		Interp:       s.interp,
		PrepareHook:  s.enrichHeaderRefs,
		EnrichTPRows: s.enrichTPRowsWithRefs,
		BuildVars:    s.buildDSLVarsWithMessagesTx,
		MakeThis: func(ctx context.Context, ctxSrc interpreter.CtxSource, obj *runtime.Object, e *metadata.Entity) interpreter.This {
			return s.newFormObjectThisLive(ctx, ctxSrc, obj, e, nil, false)
		},
		// Исходящие веб-хуки (план 29): save/post диспетчеризуются из Save.
		Hooks: hooks,
		// Живой список (план 87): автопубликация «данные.<сущность>» после
		// успешной записи/проведения. nil при поднятой без шины (тесты/headless).
		ChangePublisher: s.newChangePublisher(),
	}
}

// BuildJobDSLVars — полное DSL-окружение для регламентных заданий (план 101):
// scheduler вызывает его вместо собственного базового набора, чтобы задания
// имели Справочники/Документы/вложения/транзакции — как обработки из UI/procrun.
// Вместе с картой возвращается TxState: scheduler закрывает его после запуска.
func (s *Server) BuildJobDSLVars(ctx context.Context, mc *runtime.MovementsCollector) (map[string]any, *interpreter.TxState) {
	return s.buildDSLVarsTx(ctx, mc)
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

func (f *catFactory) NewCatalogObject(entity *metadata.Entity) any {
	return &catWriter{
		s:      f.s,
		ctxSrc: f.ctxSrc,
		entity: entity,
		obj:    runtime.NewObject(entity.Name, entity.Kind),
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
	}
	return nil
}

// write сохраняет объект через entityservice.Save — с запуском ПриЗаписи,
// записью табличных частей и веб-хуками, как при записи из веб-формы.
func (w *catWriter) write() error {
	ctx := w.ctx()
	isNew := !w.loaded && !w.saved
	if w.accessID() == uuid.Nil {
		if err := w.s.autoFillRowAccessFields(ctx, w.entity, "write", w.obj.Fields); err != nil {
			return err
		}
	}
	if err := w.s.checkDSLRowAccess(ctx, w.entity, "write", w.accessID(), w.obj.Fields); err != nil {
		return err
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

// ref строит ссылку на записанный объект с менеджером-прокси, чтобы
// Ссылка.ПолучитьОбъект()/Удалить() работали и возвращали catWriter.
func (w *catWriter) ref() *interpreter.Ref {
	return &interpreter.Ref{
		UUID:    w.obj.ID.String(),
		Name:    w.displayName(),
		Type:    w.entity.Name,
		Manager: w.s.refManagerFor(w.entity, w.ctx()),
	}
}

func (w *catWriter) displayName() string {
	for _, k := range []string{"наименование", "name"} {
		if v, ok := w.obj.Fields[k]; ok && v != nil {
			if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
				return s
			}
		}
	}
	id := w.obj.ID.String()
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}
