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
	"strings"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
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
	// Может быть nil — DSL-хук запустится без extraVars (тогда Сообщить, HTTP,
	// Справочники и т.п. в нём не будут работать).
	BuildVars func(ctx context.Context, mc *runtime.MovementsCollector, msgs *[]string) map[string]any

	// MakeThis оборачивает (ctx, obj, entity) в this для интерпретатора так, чтобы
	// внутри DSL-хука работали методы табличных частей: this.Товары.Добавить(),
	// this.Товары.Количество(), `Для Каждого Стр Из this.Товары`. Реализация
	// живёт в ui-слое (formObjectThis), здесь только хук. Если nil — Run
	// получает obj напрямую, что для документов без ТЧ тоже работает.
	MakeThis func(ctx context.Context, obj *runtime.Object, entity *metadata.Entity) interpreter.This

	// Hooks — диспетчер исходящих веб-хуков (план 29). nil = веб-хуки не
	// настроены. Событие отправляется ПОСЛЕ успешной транзакции (асинхронно):
	// document.save/document.post или catalog.save в зависимости от вида и Action.
	Hooks *webhook.Dispatcher
}

// dispatchSaved отправляет веб-хук о записи/проведении объекта.
func (s *Service) dispatchSaved(ctx context.Context, req SaveRequest, isPosting bool) {
	if !s.Hooks.Enabled() {
		return
	}
	event := "catalog.save"
	if req.Entity.Kind == metadata.KindDocument {
		event = "document.save"
		if isPosting {
			event = "document.post"
		}
	}
	s.Hooks.Dispatch(webhook.Event{
		Name:   event,
		Entity: req.Entity.Name,
		ID:     req.ID.String(),
		User:   storage.AuditUserLogin(ctx),
		Record: webhookRecord(req.Fields),
	})
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
	mc := runtime.NewMovementsCollector(req.Entity.Name, req.ID)
	SetPeriodFromFields(mc, req.Entity, req.Fields)
	lockCollector := runtime.NewLockCollector()
	hookCtx := runtime.ContextWithLockCollector(ctx, lockCollector)
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
	if proc != nil {
		var vars map[string]any
		if s.BuildVars != nil {
			vars = s.BuildVars(hookCtx, mc, &msgs)
		}
		var thisVal interpreter.This = obj
		if s.MakeThis != nil {
			thisVal = s.MakeThis(hookCtx, obj, req.Entity)
		}
		if err := s.Interp.Run(proc, thisVal, vars); err != nil {
			// DSL-ошибка (бизнес-правило), а не технический сбой: отдаём текст
			// в DSLError, БД не трогаем. И *interpreter.DSLError, и обычная
			// ошибка форматируются одинаково через Error().
			return SaveResult{ID: req.ID, DSLError: err.Error(), DSLMessages: msgs, Movements: mc}, nil
		}
	}

	// Транзакция: upsert + ТЧ + движения + проведение.
	err := s.Store.WithTx(ctx, func(ctx context.Context) error {
		if err := s.Store.AdvisoryXactLock(ctx, lockCollector.Keys()); err != nil {
			return err
		}
		if req.IsNew || req.ExpectedVersion == nil {
			if err := s.Store.Upsert(ctx, req.Entity.Name, req.ID, obj.Fields, req.Entity); err != nil {
				return err
			}
		} else {
			if err := s.Store.UpsertVersioned(ctx, req.Entity.Name, req.ID, obj.Fields, req.Entity, req.ExpectedVersion); err != nil {
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
			if err := s.Store.UpsertTablePartRows(ctx, req.Entity.Name, tp.Name, req.ID, rows, tp); err != nil {
				return err
			}
		}
		if err := s.writeMovements(ctx, req.Entity.Name, req.ID, mc); err != nil {
			return err
		}
		if req.Entity.Posting {
			if isPosting {
				return s.Store.SetPosted(ctx, req.Entity.Name, req.ID, true)
			}
			// «Записать» для уже проведённого документа (только при редактировании,
			// при IsNew проведения нет в принципе) — сбрасываем движения по ВСЕМ
			// типам регистров (накопления, бухгалтерии, сведений) и снимаем флаг
			// проведения. Раньше чистились только регистры накопления, из-за чего
			// движения бухгалтерии/сведений оставались осиротевшими.
			if !req.IsNew {
				for _, reg := range s.Reg.Registers() {
					if err := s.Store.WriteMovements(ctx, reg.Name, req.Entity.Name, req.ID, nil, reg, nil); err != nil {
						return err
					}
				}
				for _, ar := range s.Reg.AccountRegisters() {
					if err := s.Store.WriteAccountMovements(ctx, ar.Name, req.Entity.Name, req.ID, nil, ar, nil); err != nil {
						return err
					}
				}
				for _, ir := range s.Reg.InfoRegisters() {
					if err := s.Store.WriteInfoMovements(ctx, ir.Name, req.Entity.Name, req.ID, nil, ir, nil); err != nil {
						return err
					}
				}
				return s.Store.SetPosted(ctx, req.Entity.Name, req.ID, false)
			}
		}
		return nil
	})
	if err != nil {
		return SaveResult{}, err
	}

	s.dispatchSaved(ctx, req, isPosting)

	return SaveResult{ID: req.ID, DSLMessages: msgs, Movements: mc}, nil
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
	if s.BuildVars != nil {
		vars = s.BuildVars(ctx, runtime.NewMovementsCollector(req.Receiver.Name, recvObj.ID), &msgs)
	} else {
		vars = make(map[string]any)
	}
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
		thisVal = s.MakeThis(ctx, recvObj, req.Receiver)
	}
	if err := s.Interp.Run(proc, thisVal, vars); err != nil {
		normalizeTPRowKeys(recvObj.TablePartRows, req.Receiver)
		if dslErr, ok := err.(*interpreter.DSLError); ok {
			return FillResult{Fields: recvObj.Fields, TablePartRows: recvObj.TablePartRows, DSLError: dslErr.Error(), DSLMessages: msgs}, nil
		}
		return FillResult{Fields: recvObj.Fields, TablePartRows: recvObj.TablePartRows, DSLError: err.Error(), DSLMessages: msgs}, nil
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

// writeMovements распределяет накопленные в mc движения по нужным типам
// регистров (накопления, счетов, сведений). Вынесено из ui.Server.saveMovements.
func (s *Service) writeMovements(ctx context.Context, docType string, docID uuid.UUID, mc *runtime.MovementsCollector) error {
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
