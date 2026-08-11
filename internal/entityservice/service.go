// Package entityservice инкапсулирует логику сохранения сущностей (справочников
// и документов): запуск DSL-хука OnWrite/OnPost, упсёрт + табличные части +
// движения + проведение — в одной транзакции.
//
// Зачем выделено: раньше эта логика жила только в internal/ui (методы submit /
// submitEdit на *Server). REST API в internal/api делал упрощённый Upsert без
// хука/ТЧ/движений/проведения — то есть для API программа фактически работала
// только как голый CRUD без бизнес-правил. Теперь обе стороны зовут Service.Save,
// и при необходимости отличаются только тем, *как* они собирают DSL-переменные
// и пред-обработку объекта (см. PrepareHook / BuildVars).
package entityservice

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/exchange"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/webhook"
)

// SetPeriodFromFields выставляет в mc период по первому date-полю сущности.
// Регистронезависимый поиск ключа: формы кладут PascalCase, Object.Set —
// lowercase. Прежний прямой `fields[f.Name]` промахивался → period оставался
// time.Now() и движения дрейфовали по часовым поясам.
func SetPeriodFromFields(mc *runtime.MovementsCollector, entity *metadata.Entity, fields map[string]any) {
	for _, f := range entity.Fields {
		if f.Type != metadata.FieldTypeDate {
			continue
		}
		low := strings.ToLower(f.Name)
		for k, v := range fields {
			if strings.ToLower(k) != low {
				continue
			}
			if t := runtime.AsTime(v); !t.IsZero() {
				mc.SetPeriod(t)
			}
			break
		}
		return
	}
}

// Service выполняет сохранение объектов вместе с побочными эффектами.
type Service struct {
	Store  *storage.DB
	Reg    *runtime.Registry
	Interp *interpreter.Interpreter

	// PrepareHook вызывается перед запуском DSL-хука. Caller использует это
	// чтобы обогатить obj (например, заменить UUID-строки в полях шапки на
	// *interpreter.Ref{UUID,Name} — нужно чтобы Строка(ref) и ЗначениеРеквизита
	// работали в OnWrite/OnPost так же, как при вызове из обработки).
	// Может быть nil — тогда obj передаётся в хук «как есть».
	PrepareHook func(ctx context.Context, entity *metadata.Entity, obj *runtime.Object)

	// EnrichTPRows обогащает строки табличной части (аналог PrepareHook для ТЧ).
	// Может быть nil.
	EnrichTPRows func(ctx context.Context, tp metadata.TablePart, rows []map[string]any)

	// BuildVars собирает DSL-extraVars для контекста caller'а. mc — обязательный
	// (Движения). msgs (если не nil) — collector для builtin Сообщить, чтобы
	// caller мог отдать сообщения пользователю/в журнал.
	// Второй результат — состояние транзакций, которым caller обязан владеть до
	// завершения DSL-хука; Service гарантированно закрывает его на success/error/panic.
	// Может быть nil — DSL-хук запустится без extraVars (тогда Сообщить, HTTP,
	// Справочники и т.п. в нём не будут работать).
	BuildVars func(ctx context.Context, mc *runtime.MovementsCollector, msgs *[]string) (map[string]any, *interpreter.TxState)

	// MakeThis оборачивает (ctx, ctxSrc, obj, entity) в this для интерпретатора так, чтобы
	// внутри DSL-хука работали методы табличных частей: this.Товары.Добавить(),
	// this.Товары.Количество(), `Для Каждого Стр Из this.Товары`. Реализация
	// живёт в ui-слое (formObjectThis), здесь только хук. Если nil — Run
	// получает obj напрямую, что для документов без ТЧ тоже работает. ctxSrc
	// передаёт живой контекст DSL-транзакции объектным методам.
	MakeThis func(ctx context.Context, ctxSrc interpreter.CtxSource, obj *runtime.Object, entity *metadata.Entity) interpreter.This

	// RegisterExchangeDelete регистрирует удаление в планах обмена (план 86).
	// Вынесено швом, потому что exchange живёт в ui-слое; nil = обмен не
	// настроен. Без регистрации узлы разъехались бы молча.
	RegisterExchangeDelete func(ctx context.Context, entity *metadata.Entity, id uuid.UUID) error

	// Hooks — диспетчер исходящих веб-хуков (план 29). nil = веб-хуки не
	// настроены. Событие отправляется ПОСЛЕ успешной транзакции (асинхронно):
	// document.save/document.post или catalog.save в зависимости от вида и Action.
	Hooks *webhook.Dispatcher

	// ChangePublisher — опциональный потребитель события «строка изменилась»
	// (план 87, ступень A, живой список). nil = автопубликация выключена
	// (тесты/procrun/migrate). Реализация в ui рассылает служебное событие
	// живым спискам с адресацией строго по RLS.
	ChangePublisher ChangePublisher
}

// ChangePublisher принимает уведомление об успешном изменении строки сущности
// (после commit). entity — имя сущности; action ∈ {записан, проведён, удалён};
// before/after — immutable pre/post-образы строки (nil для create/delete
// соответственно) для адресации по правам.
type ChangePublisher interface {
	PublishChange(ctx context.Context, entity, action string, before, after map[string]any)
}

// publishChange публикует «данные.<сущность>» живым спискам после успешного
// сохранения (план 87). before захвачен до записи; after читается уже после
// commit свежим контекстом (tx-контекст после commit использовать нельзя).
func (s *Service) publishChange(ctx context.Context, req SaveRequest, isPosting bool, before map[string]any) {
	if s.ChangePublisher == nil || !req.Entity.NotifyChanges {
		return
	}
	action := "записан"
	if isPosting {
		action = "проведён"
	}
	entity, id, meta := req.Entity.Name, req.ID, req.Entity
	publish := func() {
		bg := context.Background()
		after, _ := s.Store.GetByID(bg, entity, id, meta)
		s.ChangePublisher.PublishChange(bg, entity, action, before, after)
	}
	if storage.DeferUntilTxCommit(ctx, publish) {
		return
	}
	publish()
}

// dispatchSaved отправляет веб-хук о записи/проведении объекта.
func (s *Service) dispatchSaved(ctx context.Context, req SaveRequest, isPosting bool) {
	if !s.Hooks.Enabled() {
		return
	}
	eventName := "catalog.save"
	if req.Entity.Kind == metadata.KindDocument {
		eventName = "document.save"
		if isPosting {
			eventName = "document.post"
		}
	}
	event := webhook.Event{
		Name:   eventName,
		Entity: req.Entity.Name,
		ID:     req.ID.String(),
		User:   storage.AuditUserLogin(ctx),
		Record: webhookRecord(req.Fields),
	}
	dispatch := func() { s.Hooks.Dispatch(event) }
	// Save may join an explicit DSL transaction. Do not publish a webhook for
	// data that can still be rolled back; the storage transaction invokes this
	// callback only after the outer commit.
	if storage.DeferUntilTxCommit(ctx, dispatch) {
		return
	}
	dispatch()
}

// webhookRecord копирует поля записи для шаблона тела хука, отбрасывая
// служебные псевдо-реквизиты (ссылка/reference — это *interpreter.Ref,
// в шаблоне он бесполезен).
func webhookRecord(fields map[string]any) map[string]any {
	rec := make(map[string]any, len(fields))
	for k, v := range fields {
		low := strings.ToLower(k)
		if low == "ссылка" || low == "reference" {
			continue
		}
		rec[k] = v
	}
	return rec
}

// SaveRequest — входной DTO для Service.Save.
type SaveRequest struct {
	Entity *metadata.Entity
	ID     uuid.UUID
	IsNew  bool // true → Upsert + авто-сценарии для нового объекта; false → UpsertVersioned

	Fields        map[string]any
	TablePartRows map[string][]map[string]any

	// Action: "" (просто Записать) | "post" | "post_and_close".
	// Для документов с Posting=true и Action=post* запускается OnPost вместо
	// OnWrite и в конце сохранения выставляется posted=true.
	Action string

	// ExpectedVersion — только для !IsNew. nil ⇒ без проверки optimistic
	// lock (поведение совместимо с прежним Upsert). Не-nil ⇒ UpsertVersioned
	// вернёт storage.ErrVersionConflict при несовпадении версии.
	ExpectedVersion *int64
}

// SaveResult — результат Service.Save.
type SaveResult struct {
	ID          uuid.UUID
	DSLError    string                      // если не пусто — хук вернул ошибку, БД не изменена
	DSLMessages []string                    // сообщения из builtin Сообщить
	Movements   *runtime.MovementsCollector // для отладки/инспекции (заполняется хуком OnPost)
}

// Save выполняет полный цикл сохранения: prepare → run hook → tx (upsert +
// table parts + movements + posting).
//
// Возвращает (result, nil) при успехе. Если DSL-хук вернул ошибку — это НЕ
// технический сбой: возвращается result.DSLError != "" и err == nil, caller
// сам решает как показать ошибку. Технические ошибки (БД, network) возвращаются
// как err != nil (включая storage.ErrVersionConflict при !IsNew с конфликтом
// версий — caller должен проверить errors.Is).
func (s *Service) Save(ctx context.Context, req SaveRequest) (SaveResult, error) {
	mc := runtime.NewMovementsCollector(req.Entity.Name, req.ID).WillPersist()
	SetPeriodFromFields(mc, req.Entity, req.Fields)
	lockCollector := runtime.NewLockCollector()
	defer lockCollector.ReleaseAll()

	obj := &runtime.Object{
		Type:          req.Entity.Name,
		Kind:          req.Entity.Kind,
		ID:            req.ID,
		Fields:        req.Fields,
		TablePartRows: req.TablePartRows,
	}

	// Псевдо-реквизит «Ссылка» самого объекта (аналог 1С). Без него this.Ссылка
	// в OnWrite/OnPost не указывал на сам документ — из-за чего запись ссылки на
	// себя в регистр сведений (Дв.Спецификация = this.Ссылка) давала NULL.
	if obj.Fields == nil {
		obj.Fields = map[string]any{}
	}
	selfRef := &interpreter.Ref{UUID: req.ID.String(), Type: req.Entity.Name}
	obj.Fields["ссылка"] = selfRef
	obj.Fields["reference"] = selfRef

	// Pre-hook enrichment: даём caller'у заменить UUID-строки на *Ref и т.п.
	if s.PrepareHook != nil {
		s.PrepareHook(ctx, req.Entity, obj)
	}
	if s.EnrichTPRows != nil {
		for _, tp := range req.Entity.TableParts {
			if rows, ok := obj.TablePartRows[tp.Name]; ok {
				s.EnrichTPRows(ctx, tp, rows)
			}
		}
	}

	// Выбор хука: OnPost при проведении документа, иначе OnWrite.
	isPosting := req.Entity.Posting && (req.Action == "post" || req.Action == "post_and_close")
	// Инвариант: помеченный на удаление документ нельзя провести (как в 1С).
	// Проверяем ДО запуска хука и записи, чтобы не терять правки полей.
	if isPosting && !req.IsNew {
		marked, err := s.Store.IsMarkedForDeletion(ctx, req.Entity.Name, req.ID)
		if err != nil {
			return SaveResult{}, err
		}
		if marked {
			return SaveResult{ID: req.ID, DSLError: storage.ErrPostingDeletionMarked.Error()}, nil
		}
	}
	// Дата запрета проведения (свёртка базы, план 74): документ свёрнутого
	// периода нельзя провести/перепровести — иначе движения вернутся и дадут
	// двойной счёт с опорными остатками. Проверяем по дате, которую проводим.
	if isPosting && mc.Period != nil {
		if lock, ok := s.Store.GetPostingLockDate(ctx); ok && storage.PostingFrozen(lock, *mc.Period) {
			return SaveResult{ID: req.ID, DSLError: storage.PostingFrozenError(lock).Error()}, nil
		}
	}
	hookName := "OnWrite"
	if isPosting {
		hookName = "OnPost"
	}
	proc := s.Reg.GetProcedure(req.Entity.Name, hookName)

	var msgs []string
	wasPosted := false
	// Pre-образ для живого списка (план 87): читаем строку ДО записи, чтобы
	// прежний владелец убрал её из своего списка при смене прав. Только когда
	// автопубликация реально включена — иначе лишнего чтения нет.
	var changeBefore map[string]any
	if s.ChangePublisher != nil && req.Entity.NotifyChanges && !req.IsNew {
		changeBefore, _ = s.Store.GetByID(ctx, req.Entity.Name, req.ID, req.Entity)
	}
	// Хук и все его DB-побочные записи выполняются в той же транзакции, что
	// шапка, ТЧ, движения и проведение. Для нового объекта сначала вставляется
	// полноценная шапка: FK-ссылки из создаваемых хуком объектов уже валидны, но
	// при любой последующей ошибке откатываются вместе с родителем.
	err := s.Store.WithTxScope(ctx, func(txCtx context.Context) error {
		if req.Entity.Posting && !req.IsNew && !isPosting {
			stored, err := s.Store.GetByID(txCtx, req.Entity.Name, req.ID, req.Entity)
			if err != nil {
				return err
			}
			wasPosted, _ = stored["posted"].(bool)
		}
		if proc != nil {
			if req.IsNew {
				if err := s.Store.UpsertProvisional(txCtx, req.Entity.Name, req.ID, obj.Fields, req.Entity); err != nil {
					return err
				}
			}
			txHookCtx := runtime.ContextWithLockCollector(txCtx, lockCollector)
			var vars map[string]any
			var txState *interpreter.TxState
			if s.BuildVars != nil {
				vars, txState = s.BuildVars(txHookCtx, mc, &msgs)
			}
			defer interpreter.RollbackTxExecution(txState)
			var thisVal interpreter.This = obj
			if s.MakeThis != nil {
				thisVal = s.MakeThis(txHookCtx, txState, obj, req.Entity)
			}
			runErr := s.Interp.Run(proc, thisVal, vars)
			if runErr = interpreter.FinishTxExecution(txState, runErr); runErr != nil {
				return &hookRunError{err: runErr}
			}
		}

		if err := s.Store.AdvisoryXactLock(txCtx, lockCollector.Keys()); err != nil {
			return err
		}
		if req.IsNew {
			var err error
			if proc != nil {
				// Строка уже вставлена перед hook. Обновляем изменённые hook-поля,
				// сохраняя стартовую _version=1.
				err = s.Store.UpsertPreserveVersion(txCtx, req.Entity.Name, req.ID, obj.Fields, req.Entity)
			} else {
				err = s.Store.Upsert(txCtx, req.Entity.Name, req.ID, obj.Fields, req.Entity)
			}
			if err != nil {
				return err
			}
		} else if req.ExpectedVersion == nil {
			if err := s.Store.Upsert(txCtx, req.Entity.Name, req.ID, obj.Fields, req.Entity); err != nil {
				return err
			}
		} else {
			if err := s.Store.UpsertVersioned(txCtx, req.Entity.Name, req.ID, obj.Fields, req.Entity, req.ExpectedVersion); err != nil {
				return err
			}
		}
		for _, tp := range req.Entity.TableParts {
			// Ключ отсутствует в запросе ⇒ ТЧ не передана — не трогаем
			// существующие строки. Ключ присутствует (в т.ч. с пустым
			// слайсом) ⇒ перезаписываем. Это отличает «не передано» от
			// «очистить»: UI всегда шлёт все ключи ТЧ (parseTablePartRows
			// кладёт пустой слайс для пустых), а частичные REST-запросы и
			// POST /post с пустым телом могут ключ опустить — тогда строки
			// ТЧ не должны затираться.
			rows, ok := req.TablePartRows[tp.Name]
			if !ok {
				continue
			}
			if rows == nil {
				rows = []map[string]any{}
			}
			if err := s.Store.UpsertTablePartRows(txCtx, req.Entity.Name, tp.Name, req.ID, rows, tp); err != nil {
				return err
			}
		}
		if err := s.writeMovements(txCtx, req.Entity.Name, req.ID, mc); err != nil {
			return err
		}
		// Регистрация изменения для планов обмена (план 86): объект из состава
		// плана → строки очереди каждому узлу-получателю. В той же транзакции —
		// регистрация атомарна с записью объекта.
		if err := s.registerExchange(txCtx, req.Entity, req.ID, false); err != nil {
			return err
		}
		if req.Entity.Posting {
			if isPosting {
				return s.Store.SetPosted(txCtx, req.Entity.Name, req.ID, true)
			}
			// Обычная запись существующего проводимого документа сохраняет
			// прежнюю семантику: это полноценная отмена проведения. Поэтому
			// недостаточно снять флаг и очистить движения — должен выполниться
			// OnUnpost в той же транзакции.
			if !req.IsNew && wasPosted {
				unpostMovements := runtime.NewMovementsCollector(req.Entity.Name, req.ID)
				SetPeriodFromFields(unpostMovements, req.Entity, obj.Fields)
				return s.unpostInTx(txCtx, req.Entity, req.ID, unpostMovements, &msgs, lockCollector)
			}
		}
		return nil
	})
	if err != nil {
		var hookErr *hookRunError
		if errors.As(err, &hookErr) {
			return SaveResult{ID: req.ID, DSLError: interpreter.FormatUserError(hookErr.err), DSLMessages: msgs, Movements: mc}, nil
		}
		return SaveResult{}, err
	}

	s.dispatchSaved(ctx, req, isPosting)
	s.publishChange(ctx, req, isPosting, changeBefore)

	return SaveResult{ID: req.ID, DSLMessages: msgs, Movements: mc}, nil
}

// hookRunError distinguishes a DSL business error from a technical storage
// error. Returning it from WithTx rolls the transaction back; Unpost then
// exposes the message through SaveResult, consistently with Save.
type hookRunError struct{ err error }

func (e *hookRunError) Error() string { return e.err.Error() }
func (e *hookRunError) Unwrap() error { return e.err }

func (s *Service) unpostInTx(
	txCtx context.Context,
	entity *metadata.Entity,
	id uuid.UUID,
	movements *runtime.MovementsCollector,
	messages *[]string,
	lockCollector *runtime.LockCollector,
) error {
	if err := s.clearMovements(txCtx, entity.Name, id); err != nil {
		return err
	}
	if err := s.Store.SetPosted(txCtx, entity.Name, id, false); err != nil {
		return err
	}
	proc := s.Reg.GetProcedure(entity.Name, "OnUnpost")
	if proc == nil {
		return nil
	}

	fields, err := s.Store.GetByID(txCtx, entity.Name, id, entity)
	if err != nil {
		return fmt.Errorf("отмена проведения %s: чтение документа: %w", entity.Name, err)
	}
	tpRows := make(map[string][]map[string]any, len(entity.TableParts))
	for _, tp := range entity.TableParts {
		rows, err := s.Store.GetTablePartRows(txCtx, entity.Name, tp.Name, id, tp)
		if err != nil {
			return fmt.Errorf("отмена проведения %s: чтение ТЧ %s: %w", entity.Name, tp.Name, err)
		}
		tpRows[tp.Name] = rows
	}

	obj := &runtime.Object{
		Type:          entity.Name,
		Kind:          entity.Kind,
		ID:            id,
		Fields:        fields,
		TablePartRows: tpRows,
	}
	if obj.Fields == nil {
		obj.Fields = map[string]any{}
	}
	selfRef := &interpreter.Ref{UUID: id.String(), Type: entity.Name}
	obj.Fields["ссылка"] = selfRef
	obj.Fields["reference"] = selfRef

	hookCtx := runtime.ContextWithLockCollector(txCtx, lockCollector)
	if s.PrepareHook != nil {
		s.PrepareHook(hookCtx, entity, obj)
	}
	if s.EnrichTPRows != nil {
		for _, tp := range entity.TableParts {
			if rows, ok := obj.TablePartRows[tp.Name]; ok {
				s.EnrichTPRows(hookCtx, tp, rows)
			}
		}
	}
	SetPeriodFromFields(movements, entity, obj.Fields)

	var vars map[string]any
	var txState *interpreter.TxState
	if s.BuildVars != nil {
		vars, txState = s.BuildVars(hookCtx, movements, messages)
	}
	defer interpreter.RollbackTxExecution(txState)
	var thisVal interpreter.This = obj
	if s.MakeThis != nil {
		thisVal = s.MakeThis(hookCtx, txState, obj, entity)
	}
	runErr := s.Interp.Run(proc, thisVal, vars)
	if runErr = interpreter.FinishTxExecution(txState, runErr); runErr != nil {
		return &hookRunError{err: runErr}
	}
	return s.Store.AdvisoryXactLock(txCtx, lockCollector.Keys())
}

// DeleteResult — результат Service.Delete. Как и у Save, отказ прикладного
// хука — не технический сбой: DSLError != "" при err == nil.
type DeleteResult struct {
	ID          uuid.UUID
	DSLError    string   // хук отменил удаление — объект на месте
	DSLMessages []string // сообщения из builtin Сообщить
}

// Delete физически удаляет объект, выполняя хуки модуля объекта
// «ПередУдалением» (может отменить удаление) и «ПослеУдаления».
//
// Единая точка на все пути удаления — одиночное из UI, две пачки, DSL и REST.
// Это не про переиспользование кода: хук, который срабатывает не везде, — не
// защита. Раньше события удаления были объявлены только в метаданных форм и не
// вызывались НИОТКУДА, поэтому `ПередУдалением: ПроверитьСсылки` молчал, а
// объект удалялся. Форма же — один путь из пяти: даже ожив её, мы оставили бы
// удаление из списка и по REST без проверки.
//
// Хук идёт ВНУТРИ транзакции удаления и до самого удаления: ВызватьИсключение в
// нём откатывает всё, включая уже снятые движения. «ПослеУдаления» выполняется
// после удаления в той же транзакции — он видит мир без объекта, и его ошибка
// возвращает объект на место.
func (s *Service) Delete(ctx context.Context, entity *metadata.Entity, id uuid.UUID) (DeleteResult, error) {
	result := DeleteResult{ID: id}
	lockCollector := runtime.NewLockCollector()
	defer lockCollector.ReleaseAll()

	err := s.Store.WithTxScope(ctx, func(txCtx context.Context) error {
		return s.deleteInTx(txCtx, entity, id, &result.DSLMessages, lockCollector)
	})
	if err != nil {
		var hookErr *hookRunError
		if errors.As(err, &hookErr) {
			result.DSLError = interpreter.FormatUserError(hookErr.err)
			return result, nil
		}
		return DeleteResult{}, err
	}
	return result, nil
}

func (s *Service) deleteInTx(
	txCtx context.Context,
	entity *metadata.Entity,
	id uuid.UUID,
	messages *[]string,
	lockCollector *runtime.LockCollector,
) error {
	// Объект читается ОДИН раз, до удаления, и отдаётся обоим хукам:
	// «ПослеУдаления» иначе увидел бы пустоту и не смог бы ни записать в
	// журнал, ни убрать связанные данные.
	obj, err := s.deleteHookObject(txCtx, entity, id)
	if err != nil {
		return err
	}
	if err := s.runDeleteHook(txCtx, entity, id, "BeforeDelete", obj, messages, lockCollector); err != nil {
		return err
	}
	// Движения снимаются до удаления регистратора: иначе остались бы строки,
	// ссылающиеся на несуществующий документ.
	if entity.Posting {
		if err := s.clearMovements(txCtx, entity.Name, id); err != nil {
			return err
		}
	}
	// Строки ТЧ — до объекта: иначе они остаются сиротами, ссылающимися на
	// несуществующего родителя (та же грабля описана в handlers_entity).
	for _, tp := range entity.TableParts {
		table := metadata.TablePartTableName(entity.Name, tp.Name)
		if _, err := s.Store.Exec(txCtx, "DELETE FROM "+table+" WHERE parent_id = "+s.Store.Dialect().Placeholder(1), id); err != nil {
			return err
		}
	}
	if s.RegisterExchangeDelete != nil {
		if err := s.RegisterExchangeDelete(txCtx, entity, id); err != nil {
			return err
		}
	}
	if err := s.Store.Delete(txCtx, entity.Name, id); err != nil {
		return err
	}
	return s.runDeleteHook(txCtx, entity, id, "AfterDelete", obj, messages, lockCollector)
}

// runDeleteHook исполняет хук удаления, если он объявлен. obj прочитан до
// удаления (см. deleteInTx) — «ПослеУдаления» получает данные удалённого
// объекта, иначе он не смог бы ничего про него сказать.
func (s *Service) runDeleteHook(
	txCtx context.Context,
	entity *metadata.Entity,
	id uuid.UUID,
	event string,
	obj *runtime.Object,
	messages *[]string,
	lockCollector *runtime.LockCollector,
) error {
	proc := s.Reg.GetProcedure(entity.Name, event)
	if proc == nil {
		return nil
	}
	if obj == nil {
		return nil // объекта уже нет — звать хук не о чем
	}
	hookCtx := runtime.ContextWithLockCollector(txCtx, lockCollector)
	if s.PrepareHook != nil {
		s.PrepareHook(hookCtx, entity, obj)
	}
	if s.EnrichTPRows != nil {
		for _, tp := range entity.TableParts {
			if rows, ok := obj.TablePartRows[tp.Name]; ok {
				s.EnrichTPRows(hookCtx, tp, rows)
			}
		}
	}
	// Коллектор движений детач-ный: при удалении писать движения некуда, и
	// молча проглотить их было бы тем же дефектом, что чинили в #743.
	var vars map[string]any
	var txState *interpreter.TxState
	if s.BuildVars != nil {
		vars, txState = s.BuildVars(hookCtx, runtime.NewMovementsCollector(entity.Name, id), messages)
	}
	defer interpreter.RollbackTxExecution(txState)
	var thisVal interpreter.This = obj
	if s.MakeThis != nil {
		thisVal = s.MakeThis(hookCtx, txState, obj, entity)
	}
	runErr := s.Interp.Run(proc, thisVal, vars)
	if runErr = interpreter.FinishTxExecution(txState, runErr); runErr != nil {
		return &hookRunError{err: runErr}
	}
	return s.Store.AdvisoryXactLock(txCtx, lockCollector.Keys())
}

// deleteHookObject читает удаляемый объект вместе с табличными частями.
// Возвращает (nil, nil), если записи уже нет.
func (s *Service) deleteHookObject(txCtx context.Context, entity *metadata.Entity, id uuid.UUID) (*runtime.Object, error) {
	fields, err := s.Store.GetByID(txCtx, entity.Name, id, entity)
	if errors.Is(err, sql.ErrNoRows) {
		// Записи уже нет: для «ПослеУдаления» это норма (объект только что
		// удалён этой же транзакцией), для «ПередУдалением» — гонка с чужим
		// удалением. Хук в обоих случаях звать не о чем.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("удаление %s: чтение объекта: %w", entity.Name, err)
	}
	if fields == nil {
		return nil, nil
	}
	tpRows := make(map[string][]map[string]any, len(entity.TableParts))
	for _, tp := range entity.TableParts {
		rows, err := s.Store.GetTablePartRows(txCtx, entity.Name, tp.Name, id, tp)
		if err != nil {
			return nil, fmt.Errorf("удаление %s: чтение ТЧ %s: %w", entity.Name, tp.Name, err)
		}
		tpRows[tp.Name] = rows
	}
	obj := &runtime.Object{Type: entity.Name, Kind: entity.Kind, ID: id, Fields: fields, TablePartRows: tpRows}
	if obj.Fields == nil {
		obj.Fields = map[string]any{}
	}
	selfRef := &interpreter.Ref{UUID: id.String(), Type: entity.Name}
	obj.Fields["ссылка"] = selfRef
	obj.Fields["reference"] = selfRef
	return obj, nil
}

// Unpost cancels document posting atomically: removes every movement, sets
// posted=false and then runs OnUnpost/ОбработкаУдаленияПроведения. The hook is
// deliberately executed after the storage changes but inside the same
// transaction, so it observes the unposted state and any hook error restores
// both the posted flag and the removed movements.
func (s *Service) Unpost(ctx context.Context, entity *metadata.Entity, id uuid.UUID) (SaveResult, error) {
	result := SaveResult{
		ID:        id,
		Movements: runtime.NewMovementsCollector(entity.Name, id),
	}
	lockCollector := runtime.NewLockCollector()
	defer lockCollector.ReleaseAll()

	err := s.Store.WithTxScope(ctx, func(txCtx context.Context) error {
		return s.unpostInTx(txCtx, entity, id, result.Movements, &result.DSLMessages, lockCollector)
	})
	if err != nil {
		var hookErr *hookRunError
		if errors.As(err, &hookErr) {
			result.DSLError = interpreter.FormatUserError(hookErr.err)
			return result, nil
		}
		return SaveResult{}, err
	}
	return result, nil
}

// FillRequest — входной DTO для Service.Fill (ввод на основании).
type FillRequest struct {
	// Receiver — сущность-приёмник (создаваемый объект). Должна содержать
	// SourceType в Receiver.BasedOn, иначе Fill вернёт ошибку.
	Receiver *metadata.Entity
	// SourceType — имя сущности-источника (тип объекта-основания).
	SourceType string
	// SourceID — идентификатор существующего объекта-источника в БД.
	SourceID uuid.UUID
}

// FillResult — результат Service.Fill: заполненные значения шапки и
// табличных частей приёмника. Caller использует их для рендеринга формы
// (UI) или передачи в Service.Save (программный сценарий).
type FillResult struct {
	Fields        map[string]any
	TablePartRows map[string][]map[string]any
	// DSLError != "" — хук ОбработкаЗаполнения вернул ошибку, см. Save.
	DSLError    string
	DSLMessages []string
}

// Fill реализует «Ввод на основании»: загружает объект-источник, запускает
// у приёмника DSL-хук ОбработкаЗаполнения(ДанныеЗаполнения) и возвращает
// заполненные поля + строки ТЧ.
//
// Источник передаётся в хук как *runtime.Object (Type/Fields/TablePartRows
// заполнены). Имя первого параметра процедуры берётся из её декларации —
// пользователь сам выбирает идентификатор (ДанныеЗаполнения, Основание и т.п.).
//
// Если у приёмника не объявлен хук — возвращается пустой результат без
// ошибки (поле может быть заполнено вручную через UI). Это симметрично
// поведению Save при отсутствии OnWrite.
//
// Проверка SourceType ∈ Receiver.BasedOn выполняется до загрузки — если
// связь не разрешена в YAML, возвращается ошибка.
func (s *Service) Fill(ctx context.Context, req FillRequest) (FillResult, error) {
	if req.Receiver == nil {
		return FillResult{}, errBadRequest("receiver is nil")
	}
	allowed := false
	for _, src := range req.Receiver.BasedOn {
		if strings.EqualFold(src, req.SourceType) {
			allowed = true
			break
		}
	}
	if !allowed {
		return FillResult{}, errBadRequest("entity " + req.Receiver.Name + " не вводится на основании " + req.SourceType)
	}
	src := s.Reg.GetEntity(req.SourceType)
	if src == nil {
		return FillResult{}, errBadRequest("неизвестный тип источника: " + req.SourceType)
	}

	row, err := s.Store.GetByID(ctx, src.Name, req.SourceID, src)
	if err != nil {
		return FillResult{}, err
	}
	srcFields := make(map[string]any, len(row))
	for _, f := range src.Fields {
		if v, ok := row[f.Name]; ok && v != nil {
			srcFields[strings.ToLower(f.Name)] = v
		}
	}
	srcTP := make(map[string][]map[string]any, len(src.TableParts))
	for _, tp := range src.TableParts {
		rows, err := s.Store.GetTablePartRows(ctx, src.Name, tp.Name, req.SourceID, tp)
		if err != nil {
			return FillResult{}, err
		}
		srcTP[tp.Name] = rows
	}
	srcObj := &runtime.Object{
		Type:          src.Name,
		Kind:          src.Kind,
		ID:            req.SourceID,
		Fields:        srcFields,
		TablePartRows: srcTP,
	}
	// Псевдо-реквизит «Ссылка» — аналог одноимённого в 1С. Позволяет хуку
	// записать ссылку на сам источник в поле приёмника:
	//   this.Основание = ДанныеЗаполнения.Ссылка
	// Менеджер не привязан (Manager=nil) — для записи UUID в reference-
	// колонку этого достаточно; полные операции через ссылку (Удалить,
	// ПолучитьОбъект) из хука обычно не нужны.
	srcObj.Fields["ссылка"] = &interpreter.Ref{UUID: req.SourceID.String(), Type: src.Name}
	srcObj.Fields["reference"] = srcObj.Fields["ссылка"]

	// Обогащаем UUID-строки в ссылочных полях источника до *Ref{…,Manager} —
	// иначе из хука ОбработкаЗаполнения нельзя было бы писать
	// this.Покупатель = ДанныеЗаполнения.Покупатель и попадать в выпадающий
	// список выбора у формы приёмника (он хранит UUID, а enrich-хук
	// возвращает Ref c UUID-полем).
	if s.PrepareHook != nil {
		s.PrepareHook(ctx, src, srcObj)
	}
	if s.EnrichTPRows != nil {
		for _, tp := range src.TableParts {
			if rows, ok := srcObj.TablePartRows[tp.Name]; ok {
				s.EnrichTPRows(ctx, tp, rows)
			}
		}
	}

	// Подготовка приёмника: пустой Object с инициализированными ТЧ.
	recvObj := runtime.NewObject(req.Receiver.Name, req.Receiver.Kind)
	for _, tp := range req.Receiver.TableParts {
		recvObj.TablePartRows[tp.Name] = []map[string]any{}
	}

	proc := s.Reg.GetProcedure(req.Receiver.Name, "OnFill")
	if proc == nil {
		// Нет хука — отдаём пустой объект, пользователь заполнит руками.
		return FillResult{Fields: recvObj.Fields, TablePartRows: recvObj.TablePartRows}, nil
	}

	var msgs []string
	var vars map[string]any
	var txState *interpreter.TxState
	if s.BuildVars != nil {
		vars, txState = s.BuildVars(ctx, runtime.NewMovementsCollector(req.Receiver.Name, recvObj.ID), &msgs)
	} else {
		vars = make(map[string]any)
	}
	defer interpreter.RollbackTxExecution(txState)
	// Имя параметра процедуры — как объявил пользователь (ДанныеЗаполнения,
	// Основание, Src и т.п.). Если параметров нет — хук вызывается без
	// источника (странный случай, но не ошибка).
	if len(proc.Params) > 0 {
		vars[proc.Params[0].Literal] = srcObj
	}

	// this для хука: обёртка с поддержкой методов ТЧ, если caller предоставил
	// фабрику; иначе — голый *Object (для документов без ТЧ всё равно работает).
	var thisVal interpreter.This = recvObj
	if s.MakeThis != nil {
		thisVal = s.MakeThis(ctx, txState, recvObj, req.Receiver)
	}
	runErr := s.Interp.Run(proc, thisVal, vars)
	if runErr = interpreter.FinishTxExecution(txState, runErr); runErr != nil {
		normalizeTPRowKeys(recvObj.TablePartRows, req.Receiver)
		if dslErr, ok := runErr.(*interpreter.DSLError); ok {
			return FillResult{Fields: recvObj.Fields, TablePartRows: recvObj.TablePartRows, DSLError: dslErr.UserMessage(), DSLMessages: msgs}, nil
		}
		return FillResult{Fields: recvObj.Fields, TablePartRows: recvObj.TablePartRows, DSLError: runErr.Error(), DSLMessages: msgs}, nil
	}
	normalizeTPRowKeys(recvObj.TablePartRows, req.Receiver)
	return FillResult{Fields: recvObj.Fields, TablePartRows: recvObj.TablePartRows, DSLMessages: msgs}, nil
}

// normalizeTPRowKeys приводит ключи строк ТЧ к PascalCase из metadata (как в
// Entity.TableParts[].Fields[].Name). Хук ОбработкаЗаполнения через
// MapThis.Set записывает ключи в lowercase — это удобно для DSL (case-
// insensitive чтение), но шаблон формы делает строгое {{index $row $fn}} по
// PascalCase, и значения «теряются» в UI: ссылочные поля не selected,
// number-поля показываются пустыми. Эта функция переименовывает ключи
// in-place, не трогая значения. Ключи, которых нет в metadata (мусор от
// DSL), остаются как есть.
func normalizeTPRowKeys(tpRows map[string][]map[string]any, recv *metadata.Entity) {
	if tpRows == nil || recv == nil {
		return
	}
	for _, tp := range recv.TableParts {
		rows := tpRows[tp.Name]
		if rows == nil {
			continue
		}
		for _, row := range rows {
			for _, f := range tp.Fields {
				if _, ok := row[f.Name]; ok {
					continue
				}
				low := strings.ToLower(f.Name)
				for k, v := range row {
					if k != f.Name && strings.ToLower(k) == low {
						row[f.Name] = v
						delete(row, k)
						break
					}
				}
			}
		}
	}
}

// errBadRequest — простая ошибка-маркер для невалидных запросов Fill.
// Caller (UI/DSL) различает её по тексту для подбора HTTP-кода.
type fillBadRequest struct{ msg string }

func (e *fillBadRequest) Error() string { return e.msg }

func errBadRequest(msg string) error { return &fillBadRequest{msg: msg} }

// IsBadRequest сообщает, является ли err клиентской ошибкой Fill (HTTP 400).
func IsBadRequest(err error) bool {
	_, ok := err.(*fillBadRequest)
	return ok
}

// registerExchange регистрирует изменение объекта в планах обмена (план 86).
// nil-Reg и отсутствие планов — быстрый выход без обращения к БД (обмен не
// настроен). deletion=true передаётся из пути пометки на удаление.
func (s *Service) registerExchange(ctx context.Context, entity *metadata.Entity, id uuid.UUID, deletion bool) error {
	if s.Reg == nil {
		return nil
	}
	plans := s.Reg.ExchangePlans()
	if len(plans) == 0 {
		return nil
	}
	return exchange.RegisterOnSave(ctx, s.Store, plans, entity, id, deletion)
}

// Repost перепроводит уже записанный документ: перечитывает его из БД, запускает
// ОбработкаПроведения (OnPost), пишет движения в регистры и ставит признак
// проведения — БЕЗ повторного Upsert, без регистрации в обмене (нет эха) и без
// изменения _version. Используется загрузкой пакета обмена (план 86, repost) для
// переноса проведённости документа на приёмник. Открывает собственную транзакцию,
// поэтому вызывается ВНЕ транзакции загрузки.
func (s *Service) Repost(ctx context.Context, entityName string, id uuid.UUID) error {
	ent := s.Reg.GetEntity(entityName)
	if ent == nil {
		return fmt.Errorf("перепроведение: сущность %q не найдена", entityName)
	}
	if !ent.Posting {
		return nil // сущность не проводится — нечего делать
	}
	fields, err := s.Store.GetByID(ctx, ent.Name, id, ent)
	if err != nil {
		return fmt.Errorf("перепроведение %s: чтение документа: %w", ent.Name, err)
	}
	tps := make(map[string][]map[string]any, len(ent.TableParts))
	for _, tp := range ent.TableParts {
		rows, err := s.Store.GetTablePartRows(ctx, ent.Name, tp.Name, id, tp)
		if err != nil {
			return fmt.Errorf("перепроведение %s: чтение ТЧ %s: %w", ent.Name, tp.Name, err)
		}
		tps[tp.Name] = rows
	}

	mc := runtime.NewMovementsCollector(ent.Name, id).WillPersist()
	SetPeriodFromFields(mc, ent, fields)
	// Дата запрета проведения (свёртка базы, план 74): в замороженный период не
	// перепроводим, иначе движения вернутся и дадут двойной счёт с опорными остатками.
	if mc.Period != nil {
		if lock, ok := s.Store.GetPostingLockDate(ctx); ok && storage.PostingFrozen(lock, *mc.Period) {
			return storage.PostingFrozenError(lock)
		}
	}
	lockCollector := runtime.NewLockCollector()
	defer lockCollector.ReleaseAll()

	obj := &runtime.Object{Type: ent.Name, Kind: ent.Kind, ID: id, Fields: fields, TablePartRows: tps}
	if obj.Fields == nil {
		obj.Fields = map[string]any{}
	}
	selfRef := &interpreter.Ref{UUID: id.String(), Type: ent.Name}
	obj.Fields["ссылка"] = selfRef
	obj.Fields["reference"] = selfRef
	if s.PrepareHook != nil {
		s.PrepareHook(ctx, ent, obj)
	}
	if s.EnrichTPRows != nil {
		for _, tp := range ent.TableParts {
			if rows, ok := obj.TablePartRows[tp.Name]; ok {
				s.EnrichTPRows(ctx, tp, rows)
			}
		}
	}

	// Хук выполняется ВНУТРИ транзакции записи (issue #458): раньше OnPost
	// работал вне её — БлокировкаДанных из хука не могла взять advisory lock,
	// а между чтением остатков и записью движений оставалось окно гонки.
	proc := s.Reg.GetProcedure(ent.Name, "OnPost")
	return s.Store.WithTx(ctx, func(txCtx context.Context) error {
		if proc != nil {
			hookCtx := runtime.ContextWithLockCollector(txCtx, lockCollector)
			var msgs []string
			var vars map[string]any
			var txState *interpreter.TxState
			if s.BuildVars != nil {
				vars, txState = s.BuildVars(hookCtx, mc, &msgs)
			}
			defer interpreter.RollbackTxExecution(txState)
			var thisVal interpreter.This = obj
			if s.MakeThis != nil {
				thisVal = s.MakeThis(hookCtx, txState, obj, ent)
			}
			runErr := s.Interp.Run(proc, thisVal, vars)
			if runErr = interpreter.FinishTxExecution(txState, runErr); runErr != nil {
				return fmt.Errorf("перепроведение %s: ОбработкаПроведения: %w", ent.Name, runErr)
			}
		}
		if err := s.Store.AdvisoryXactLock(txCtx, lockCollector.Keys()); err != nil {
			return err
		}
		if err := s.writeMovements(txCtx, ent.Name, id, mc); err != nil {
			return err
		}
		return s.Store.SetPosted(txCtx, ent.Name, id, true)
	})
}

// clearMovements removes every register row recorded by the document. Passing
// nil rows to the storage writers performs only DELETE-by-recorder, therefore
// it is safe to visit registers the document did not write to.
func (s *Service) clearMovements(ctx context.Context, entityName string, id uuid.UUID) error {
	for _, reg := range s.Reg.Registers() {
		if err := s.Store.WriteMovements(ctx, reg.Name, entityName, id, nil, reg, nil); err != nil {
			return err
		}
	}
	for _, ir := range s.Reg.InfoRegisters() {
		if err := s.Store.WriteInfoMovements(ctx, ir.Name, entityName, id, nil, ir, nil); err != nil {
			return err
		}
	}
	for _, ar := range s.Reg.AccountRegisters() {
		if err := s.Store.WriteAccountMovements(ctx, ar.Name, entityName, id, nil, ar, nil); err != nil {
			return err
		}
	}
	return nil
}

// totalsLockKeys собирает ключи advisory-локов итогов по всем регистрам, в
// которые пойдут движения. Ключи те же, что берут Write*Movements по
// отдельности — здесь они нужны, чтобы захватить их одним отсортированным
// вызовом и не зависеть от порядка обхода карты движений.
func (s *Service) totalsLockKeys(mc *runtime.MovementsCollector) []string {
	var keys []string
	for regName := range mc.All() {
		if reg := s.Reg.GetRegister(regName); reg != nil {
			if reg.TotalsUsable() {
				keys = append(keys, "register-totals|"+strings.ToLower(reg.Name))
			}
			continue
		}
		if ar := s.Reg.GetAccountRegister(regName); ar != nil && ar.TotalsUsable() {
			keys = append(keys, "account-totals|"+strings.ToLower(ar.Name))
		}
	}
	return keys
}

// writeMovements распределяет накопленные в mc движения по нужным типам
// регистров (накопления, счетов, сведений). Вынесено из ui.Server.saveMovements.
func (s *Service) writeMovements(ctx context.Context, docType string, docID uuid.UUID, mc *runtime.MovementsCollector) error {
	// Локи итогов берём ОДНИМ вызовом до записи (issue #626). Каждый
	// Write*Movements берёт свой лок сам, а обход mc.All() — обычная Go-map со
	// случайным порядком: одно проведение захватывало «товары», потом
	// «хозрасчётный», параллельное — наоборот, и PostgreSQL снимал одно из них
	// с «deadlock detected». AdvisoryXactLock сортирует ключи (normalizeAdvisoryKeys),
	// но только ВНУТРИ одного вызова — поэтому здесь и нужен общий список.
	// Повторный захват того же ключа в той же транзакции безвреден, так что
	// Write*Movements менять не требуется.
	//
	// Тот же инвариант уже записан в runtime/locks.go: «Acquire берёт мьютексы
	// по всем ключам в детерминированном порядке, чтобы избежать
	// кросс-deadlock'а между двумя проведениями».
	if err := s.Store.AdvisoryXactLock(ctx, s.totalsLockKeys(mc)); err != nil {
		return err
	}
	for regName, rows := range mc.All() {
		if reg := s.Reg.GetRegister(regName); reg != nil {
			if err := s.Store.WriteMovements(ctx, regName, docType, docID, rows, reg, mc.Period); err != nil {
				return err
			}
			continue
		}
		if ar := s.Reg.GetAccountRegister(regName); ar != nil {
			if err := s.Store.WriteAccountMovements(ctx, regName, docType, docID, rows, ar, mc.Period); err != nil {
				return err
			}
			continue
		}
		if ir := s.Reg.GetInfoRegister(regName); ir != nil {
			if err := s.Store.WriteInfoMovements(ctx, regName, docType, docID, rows, ir, mc.Period); err != nil {
				return err
			}
		}
	}
	return nil
}
