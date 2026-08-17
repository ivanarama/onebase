package ui

// Запуск DSL-хуков (ОбработкаПроведения/ПриЗаписи) и сборка переменных
// окружения DSL для обработчиков.
// Выделено из handlers.go (план 55, этап 1) — перенос as-is.

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dslvars"
	"github.com/ivantit66/onebase/internal/exchange"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// exchangeRegistrar строит замыкание регистрации изменений в планах обмена
// (план 86) для прямых записей из DSL (справочники/документы), минующих
// entityservice.Save. Регистрация — no-op, если планов нет или this-node не задан.
func (s *Server) exchangeRegistrar() interpreter.ExchangeRegistrar {
	return func(ctx context.Context, entity *metadata.Entity, id uuid.UUID, deletion bool) error {
		if deletion {
			return exchange.RegisterOnDelete(ctx, s.store, s.reg.ExchangePlans(), entity, id)
		}
		return exchange.RegisterOnSave(ctx, s.store, s.reg.ExchangePlans(), entity, id, deletion)
	}
}

// langCtxKeyT — ключ контекста, несущий разрешённый язык интерфейса для
// request-scoped builtin'ов (НСтр). Вне запроса (планировщик/headless/фоновые
// задания) ключа нет, и язык берётся из настройки базы (s.cfg.Lang).
type langCtxKeyT struct{}

// withLang кладёт разрешённый язык запроса в контекст (см. langCtxKeyT). Пустой
// язык не пишем — пусть сработает откат к языку базы.
func withLang(ctx context.Context, lang string) context.Context {
	if lang == "" {
		return ctx
	}
	return context.WithValue(ctx, langCtxKeyT{}, lang)
}

// langFromCtx достаёт язык, положенный withLang; "" — если контекст его не несёт.
func langFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(langCtxKeyT{}).(string); ok {
		return v
	}
	return ""
}

// buildDSLVarsTx дополнительно отдаёт «живой» источник контекста (TxState).
// Он нужен вызывающему, чтобы менеджеры ссылок, построенные ДО открытия
// DSL-транзакции, всё равно выполняли ПолучитьОбъект()/Записать() ВНУТРИ неё:
// со статическим контекстом такой вызов уходит за вторым соединением, а пул
// SQLite — одно соединение, и запрос виснет до таймаута.
func (s *Server) buildDSLVarsTx(ctx context.Context, mc *runtime.MovementsCollector) (map[string]any, *interpreter.TxState) {
	// TxState is created before the common variable set so path resolvers and
	// all write-capable DSL objects observe the same live transaction context.
	txState := interpreter.NewTxState(ctx)
	// Базовый набор (Перечисления, Константы, Запрос, Предопределённые,
	// Движения, HTTP, Email) — общий с scheduler, см. internal/dslvars.
	vars := dslvars.Common{
		Ctx: ctx, CtxSource: txState, Reg: s.reg, Store: s.store, Mailer: s.mailer, Movements: mc,
		NetGuard:          s.netGuard(ctx),
		ExecGuard:         s.execGuard(ctx),
		Notifier:          s.notifier(),
		Interp:            s.interp, // для hook-правила конфликта в ПланыОбмена.ЗагрузитьПакет
		EmailFileResolver: s.emailAttachmentPathResolver(txState.Ctx),
	}.Build()

	// TxState несёт «живой» контекст. Транзакционные функции
	// (НачатьТранзакцию и т.д.) и запись справочников из обработки
	// (Справочники.X.Создать().Записать()) используют txState.Ctx(),
	// поэтому запись участвует в открытой DSL-транзакции.
	// Caller подключается ДО создания CatalogsRoot.WithManagerCaller —
	// он использует ctx как контекст для вызова процедур менеджера.
	mgrCaller := &managerCaller{s: s, ctxSrc: txState}
	rowAccess := s.dslRowAccessChecker()
	catalogs := interpreter.NewCatalogsRoot(txState, s.store, s.reg).
		WithManagerCaller(mgrCaller).
		WithRowAccessChecker(rowAccess).
		WithFieldSearchChecker(s.dslFieldSearchChecker()).
		WithExchangeRegistrar(s.exchangeRegistrar()).
		WithObjectFactory(s.catObjectFactory(txState)).
		WithDeleter(dslCatalogDeleter{s: s})
	// Документы.X.Создать()/.Записать()/.Провести() из обработки.
	documents := newDocsRoot(s, txState)
	// РегистрыНакопления.X.Остатки()/.Движения()/.ВыбратьПоРегистратору(Док).
	accumRegs := newAccumRegsRoot(s, txState)
	// #2 managed locks: builtin БлокировкаДанных() возвращает свежий LockObject,
	// привязанный к глобальному менеджеру server'а. Заблокировать() внутри
	// транзакции проведения сразу берёт pg_advisory_xact_lock — конкурирующее
	// проведение упрётся в блокировку ДО чтения остатков, а не после хука
	// (issue #458: двойное списание партий при параллельном ФИФО). Вне
	// транзакции (обработка без НачатьТранзакцию) поведение прежнее:
	// только внутрипроцессный мьютекс.
	lockFactory := interpreter.BuiltinFunc(func(_ []any, _ string, _ int) (any, error) {
		lo := runtime.NewLockObjectWithCollector(s.lockMgr, runtime.LockCollectorFromContext(ctx))
		lo.WithAdvisory(func(keys []string) {
			c := txState.Ctx()
			if !storage.HasTx(c) {
				return
			}
			if err := s.store.AdvisoryXactLock(c, keys); err != nil {
				interpreter.RaiseUserError(err.Error())
			}
		})
		return lo, nil
	})

	// API текущего пользователя для персональных настроек.
	// ТекущийПользователь() → объект {ИД, Имя, ПолноеИмя, Админ}.
	// ИмяПользователя()     → строка-логин (или "" для фоновых заданий).
	var curUserID, curUserLogin, curUserFullName string
	var curUserAdmin bool
	if u := auth.UserFromContext(ctx); u != nil {
		curUserID, curUserLogin, curUserFullName, curUserAdmin = u.ID, u.Login, u.FullName, u.IsAdmin
	}
	userObj := &interpreter.MapThis{M: map[string]any{
		"ИД": curUserID, "Имя": curUserLogin, "ПолноеИмя": curUserFullName, "Админ": curUserAdmin,
		"ID": curUserID, "Login": curUserLogin, "FullName": curUserFullName, "IsAdmin": curUserAdmin,
	}}
	currentUserFn := interpreter.BuiltinFunc(func(_ []any, _ string, _ int) (any, error) {
		return userObj, nil
	})
	userNameFn := interpreter.BuiltinFunc(func(_ []any, _ string, _ int) (any, error) {
		return curUserLogin, nil
	})
	// Compatibility-sensitive fallback: before v0.9.6 configurations could
	// freely declare an application procedure named ЗаписатьСобытиеАудита.
	// Such a procedure must win over the platform governance API.
	auditDecisionFn := interpreter.FallbackBuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) < 5 {
			return nil, fmt.Errorf("ЗаписатьСобытиеАудита: нужны действие, вид, объект, ссылка и идентификатор решения")
		}
		action := strings.ToLower(strings.TrimSpace(fmt.Sprint(args[0])))
		if action != "publish" && action != "rollback" {
			return nil, fmt.Errorf("ЗаписатьСобытиеАудита: действие должно быть publish или rollback")
		}
		kind := strings.TrimSpace(fmt.Sprint(args[1]))
		entityName := strings.TrimSpace(fmt.Sprint(args[2]))
		recordID := refValueString(args[3])
		if _, err := uuid.Parse(recordID); err != nil {
			return nil, fmt.Errorf("ЗаписатьСобытиеАудита: некорректная ссылка записи")
		}
		decisionID := strings.TrimSpace(fmt.Sprint(args[4]))
		if decisionID == "" {
			return nil, fmt.Errorf("ЗаписатьСобытиеАудита: идентификатор решения обязателен")
		}
		author := curUserLogin
		if len(args) > 5 && strings.TrimSpace(fmt.Sprint(args[5])) != "" {
			author = strings.TrimSpace(fmt.Sprint(args[5]))
		}
		if author == "" {
			return nil, fmt.Errorf("ЗаписатьСобытиеАудита: автор обязателен")
		}
		if err := s.store.LogDecisionAction(txState.Ctx(), action, kind, entityName, recordID, decisionID, author); err != nil {
			return nil, fmt.Errorf("ЗаписатьСобытиеАудита: %w", err)
		}
		return nil, nil
	})

	// ЗначениеРеквизитаОбъекта(Ссылка, "Реквизит") — чтение реквизита по
	// ссылке (ссылка несёт лишь UUID/наименование). Использует txState.Ctx(),
	// поэтому видит данные открытой DSL-транзакции.
	attrValueFn := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		return s.objectAttributeValue(txState.Ctx(), args)
	})
	attrValuesFn := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		return s.objectAttributeValues(txState.Ctx(), args)
	})

	// СохранитьКартинку(ДанныеBase64, ТипMIME="", Владелец=Неопределено) → UUID
	// бинарника в blob-хранилище.
	// Поле типа image хранит именно этот UUID. Данные — base64 картинки (сырой или
	// в виде data-URL «data:image/png;base64,...»); тип по умолчанию image/png.
	// Для image-поля третьим аргументом нужно передать ссылку на целевую запись:
	// тогда blob получает владельца-сущность, а доступ на запись проверяется до
	// сохранения. Owner-aware вариант разрешён только внутри DSL-транзакции, чтобы
	// метаданные blob и ссылка имели общий исход БД; для disk/S3 обычный rollback
	// запускает компенсационную очистку. Два аргумента сохраняют legacy-
	// поведение для UUID в строках/константах: owner-less blob доступен любому
	// аутентифицированному пользователю и навсегда исключён из GC.
	putImageFn := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("СохранитьКартинку: нужен аргумент — данные картинки в Base64")
		}
		dataB64 := strings.TrimSpace(fmt.Sprintf("%v", args[0]))
		if dataB64 == "" {
			return "", nil
		}
		mime := ""
		// data-URL: data:<mime>;base64,<...>
		if strings.HasPrefix(dataB64, "data:") {
			if i := strings.Index(dataB64, ";base64,"); i >= 0 {
				mime = strings.TrimPrefix(dataB64[:i], "data:")
				dataB64 = dataB64[i+len(";base64,"):]
			} else if i := strings.Index(dataB64, ","); i >= 0 {
				dataB64 = dataB64[i+1:]
			}
		}
		if mime == "" && len(args) > 1 && args[1] != nil {
			mime = strings.TrimSpace(fmt.Sprintf("%v", args[1]))
		}
		if mime == "" {
			mime = "image/png"
		}
		if !strings.HasPrefix(mime, "image/") {
			// блокируем нерастровые типы (например text/html), которые иначе
			// сохранились бы в blob с произвольным Content-Type.
			return nil, fmt.Errorf("СохранитьКартинку: тип %q не является изображением", mime)
		}
		// Размер проверяем ДО декодирования: декодированный размер ≈ len*3/4.
		// Иначе гигантский base64 материализуется в память целиком ещё до
		// отсечения лимитом в PutBlob (риск исчерпания памяти).
		if max := s.maxFileSizeBytes; max > 0 && int64(len(dataB64))/4*3 > max {
			return nil, fmt.Errorf("СохранитьКартинку: картинка превышает максимальный размер")
		}
		data, err := base64.StdEncoding.DecodeString(dataB64)
		if err != nil {
			return nil, fmt.Errorf("СохранитьКартинку: некорректный Base64: %w", err)
		}

		ctx := txState.Ctx()
		owner := storage.BlobOwner{DSLManaged: true}
		// Явное Неопределено эквивалентно отсутствующему optional-параметру и
		// сохраняет совместимый owner-less режим.
		if len(args) > 2 && args[2] != nil {
			ref, ok := args[2].(*interpreter.Ref)
			if !ok || ref == nil {
				return nil, fmt.Errorf("СохранитьКартинку: владелец должен быть ссылкой на запись")
			}
			if !storage.HasTx(ctx) {
				return nil, fmt.Errorf("СохранитьКартинку: картинку для поля объекта нужно сохранять внутри транзакции")
			}
			id, parseErr := uuid.Parse(strings.TrimSpace(ref.UUID))
			if parseErr != nil || strings.TrimSpace(ref.Type) == "" {
				return nil, fmt.Errorf("СохранитьКартинку: некорректная ссылка владельца")
			}
			entity := s.reg.GetEntity(ref.Type)
			if entity == nil {
				return nil, fmt.Errorf("СохранитьКартинку: тип владельца %q не найден", ref.Type)
			}
			if accessErr := s.checkDSLRowAccess(ctx, entity, "write", id, nil); accessErr != nil {
				return nil, fmt.Errorf("СохранитьКартинку: нет доступа к владельцу: %w", accessErr)
			}
			owner = storage.BlobOwner{Kind: string(entity.Kind), Entity: entity.Name}
		}

		blob, err := s.store.PutBlob(ctx, mime, bytes.NewReader(data), s.maxFileSizeBytes, owner)
		if err != nil {
			return nil, fmt.Errorf("СохранитьКартинку: %w", err)
		}
		return blob.ID.String(), nil
	})

	vars["Справочники"] = catalogs
	vars["Catalogs"] = catalogs
	vars["Документы"] = documents
	vars["Documents"] = documents
	vars["РегистрыНакопления"] = accumRegs
	vars["AccumulationRegisters"] = accumRegs
	infoRegs := newInfoRegsRoot(s, txState)
	vars["РегистрыСведений"] = infoRegs
	vars["InfoRegisters"] = infoRegs
	wsGlobal := newWSRoot(s, txState)
	vars["ВебСокет"] = wsGlobal
	vars["WebSocket"] = wsGlobal
	// Запуск регламентного задания по требованию (план 123). Живой контекст
	// txState нужен, чтобы отказать при открытой транзакции инициатора.
	scheduledJobs := newScheduledJobsRoot(s, txState)
	vars["РегламентныеЗадания"] = scheduledJobs
	vars["ScheduledJobs"] = scheduledJobs
	vars["БлокировкаДанных"] = lockFactory
	vars["DataLock"] = lockFactory
	vars["ТекущийПользователь"] = currentUserFn
	vars["CurrentUser"] = currentUserFn
	vars["ИмяПользователя"] = userNameFn
	vars["UserName"] = userNameFn
	vars["ЗаписатьСобытиеАудита"] = auditDecisionFn
	vars["WriteAuditDecision"] = auditDecisionFn
	vars["ЗначениеРеквизитаОбъекта"] = attrValueFn
	vars["ObjectAttributeValue"] = attrValueFn
	vars["ЗначенияРеквизитовОбъектов"] = attrValuesFn
	vars["ObjectAttributeValues"] = attrValuesFn
	vars["СохранитьКартинку"] = putImageFn
	vars["PutImage"] = putImageFn
	// Глобальный поиск из обработок (план 82). Живой контекст txState.Ctx() —
	// поиск видит данные открытой DSL-транзакции; права — пользователя сессии.
	fullTextSearchFn := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		return s.dslFullTextSearch(txState.Ctx(), args)
	})
	vars["ПолнотекстовыйПоиск"] = fullTextSearchFn
	vars["FullTextSearch"] = fullTextSearchFn
	// Вложения из DSL (план 105): ПрисоединитьФайл/СписокВложений/
	// ПутьКВложению/УдалитьВложение. Живой контекст — как у транзакций.
	s.registerAttachmentBuiltins(vars, txState.Ctx)
	// Публикация вложений наружу (план 127): ОпубликоватьФайл/СсылкаНаФайл/
	// СнятьПубликациюФайла. Рядом с остальными функциями вложений — тот же
	// контур прав.
	s.registerPublicFileBuiltins(vars, txState.Ctx)
	queryFactory := interpreter.NewQueryFactoryGuarded(txState.Ctx(), s.store, s.reg, s.compileDSLQueryWithRowAccess, s.dslQueryGuard)
	vars["__factory_Запрос"] = queryFactory
	vars["__factory_Query"] = queryFactory

	// транзакции из DSL (обработки/проведение). Раньше NewTxFunctions
	// использовался только в тестах — отсюда «unknown function
	// НачатьТранзакцию». Теперь подключаем к реальному рантайму.
	for k, v := range interpreter.NewTxFunctions(txState, s.store) {
		vars[k] = v
	}
	for k, v := range interpreter.NewSpreadsheetFunctions() {
		vars[k] = v
	}
	for k, v := range interpreter.NewChartFunctions() {
		vars[k] = v
	}

	// НСтр(ИсходнаяСтрока[, КодЯзыка]) — локализованная строка формата
	// "ru = '…'; en = '…'". Глобально язык по умолчанию "ru"; здесь подставляем
	// язык запроса (страницы кладут его через withLang, см. handlers_page), чтобы
	// НСтр без явного кода переводил на язык текущего пользователя — для
	// статической части динамически собираемых подписей («Отчёт за » + Период),
	// которые авто-перевод подписей блоков целиком не покрывает (план 66, п.3).
	// Вне запроса (планировщик/headless) — язык базы (s.cfg.Lang).
	nstrLang := langFromCtx(ctx)
	if nstrLang == "" {
		nstrLang = s.cfg.Lang
	}
	nstrFn := interpreter.NewNStrFunc(nstrLang)
	vars["НСтр"] = nstrFn
	vars["NStr"] = nstrFn

	return vars, txState
}

// buildDSLVarsWithMessagesTx добавляет Сообщить к полному окружению и отдаёт
// «живой» источник контекста (см. buildDSLVarsTx).
func (s *Server) buildDSLVarsWithMessagesTx(ctx context.Context, mc *runtime.MovementsCollector, msgs *[]string) (map[string]any, *interpreter.TxState) {
	ctx = withDSLMessageCollector(ctx, msgs)
	vars, txState := s.buildDSLVarsTx(ctx, mc)
	userKey := userKeyFromCtx(ctx)
	msgFunc := interpreter.BuiltinFunc(func(args []any, file string, line int) (any, error) {
		if len(args) > 0 {
			text := fmt.Sprintf("%v", args[0])
			if msgs != nil {
				*msgs = append(*msgs, text)
			}
			s.messages.Push(userKey, text)
		}
		return nil, nil
	})
	vars["Сообщить"] = msgFunc
	vars["Message"] = msgFunc
	return vars, txState
}

func (s *Server) runOnWriteCtx(ctx context.Context, obj *runtime.Object, mc *runtime.MovementsCollector) (string, []string) {
	proc := s.reg.GetProcedure(obj.Type, "OnWrite")
	if proc == nil {
		return "", nil
	}
	ctx = trustedDSLContext(ctx)
	// Симметрично runOnPostCtx: ссылки в полях шапки из формы приходят
	// сырыми UUID — обогащаем до *Ref{UUID,Name}, чтобы ЗначениеРеквизитаОбъекта
	// и Строка(ref) работали в ПриЗаписи так же, как при проведении.
	if entity := s.reg.GetEntity(obj.Type); entity != nil {
		s.enrichHeaderRefs(ctx, entity, obj)
		// Пустая ТЧ обязана быть видна как пустая, а не как «Неопределено»
		// (issue #842): иначе Товары.Количество() падает именно тогда, когда
		// проверка «строк нет» и должна сработать.
		obj.EnsureTableParts(entity)
		for _, tp := range entity.TableParts {
			for name, rows := range obj.TablePartRows {
				if strings.EqualFold(name, tp.Name) {
					s.enrichTPRowsWithRefs(ctx, tp, rows)
					break
				}
			}
		}
	}
	var msgs []string
	vars, txState := s.buildDSLVarsWithMessagesTx(ctx, mc, &msgs)
	defer rollbackDSLExecution(txState)
	runErr := s.interp.Run(proc, obj, vars)
	if runErr = finishDSLExecution(txState, runErr); runErr != nil {
		return interpreter.FormatUserError(runErr), msgs
	}
	return "", msgs
}

// callManagerProc вызывает процедуру модуля менеджера (X.manager.os) для
// сущности entityName. found=true если процедура объявлена — независимо от
// успеха/ошибки. Используется CatalogProxy/docProxy в качестве fallback после
// встроенных методов (Создать, НайтиПо…, Удалить).
//
// MovementsCollector создаётся пустой (UUID.Nil): методы менеджера не привязаны
// к экземпляру и не пишут движения; если пользователю нужны движения — он
// должен делать Документы.X.Создать().Записать() явно.
func (s *Server) callManagerProc(ctx context.Context, entityName, method string, args []any) (any, bool, error) {
	proc := s.reg.GetManagerProc(entityName, method)
	if proc == nil {
		return nil, false, nil
	}
	mc := runtime.NewMovementsCollector(entityName, uuid.Nil)
	vars, txState := s.buildDSLVarsTx(ctx, mc)
	defer rollbackDSLExecution(txState)
	result, err := s.interp.Call(proc, nil, args, vars)
	err = finishDSLExecution(txState, err)
	return result, true, err
}

// managerCaller адаптер для interpreter.ManagerCaller. Используется в
// buildDSLVars для подключения fallback к CatalogsRoot.
type managerCaller struct {
	s      *Server
	ctxSrc interpreter.CtxSource
}

func (m *managerCaller) CallManager(entityName, method string, args []any) (any, bool, error) {
	return m.s.callManagerProc(m.ctxSrc.Ctx(), entityName, method, args)
}

func (s *Server) runOnPostCtx(ctx context.Context, obj *runtime.Object, mc *runtime.MovementsCollector) (string, []string) {
	proc := s.reg.GetProcedure(obj.Type, "OnPost")
	if proc == nil {
		return "", nil
	}
	ctx = trustedDSLContext(ctx)
	// Симметрично табличным частям: ссылки в полях шапки из формы приходят
	// сырыми UUID — обогащаем до *Ref{UUID,Name}, чтобы string-измерения
	// (Склад, Касса, Контрагент) фильтровались по имени, как при проведении
	// из обработки. См. П.37.
	if entity := s.reg.GetEntity(obj.Type); entity != nil {
		s.enrichHeaderRefs(ctx, entity, obj)
		obj.EnsureTableParts(entity)
	}
	var msgs []string
	vars, txState := s.buildDSLVarsWithMessagesTx(ctx, mc, &msgs)
	defer rollbackDSLExecution(txState)
	runErr := s.interp.Run(proc, obj, vars)
	if runErr = finishDSLExecution(txState, runErr); runErr != nil {
		return interpreter.FormatUserError(runErr), msgs
	}
	return "", msgs
}
