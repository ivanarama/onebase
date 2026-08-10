// Package dslvars содержит общую базу DSL-переменных, разделяемую UI-handlers
// (internal/ui) и фоновыми scheduled jobs (internal/scheduler). Раньше каждый
// из этих контекстов строил карту инжекций сам — две почти одинаковые
// реализации с тонкими отличиями, из-за чего добавление новой builtin требовало
// правок в обоих местах (и регулярно одно из них забывали).
//
// Сейчас обе стороны зовут Common.Build() для пересечения функционала
// (Перечисления, Константы, Запрос, ПредопределённыеЗначения, Движения,
// HTTP/Email), а сверху добавляют свои специфичные переменные (для UI это
// блокировки, текущий пользователь, Документы.Создать, транзакции из DSL,
// табличные документы, диаграммы; для scheduler — Параметры и Сообщить с
// привязкой к runtime-логу задания).
package dslvars

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/aiassist"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Common — параметры базовой DSL-карты. Заполните поля и вызовите Build().
type Common struct {
	Ctx context.Context
	// CtxSource, when set, supplies the current DSL transaction context to
	// write-capable common objects. nil wraps Ctx as a static source.
	CtxSource interpreter.CtxSource
	Reg       *runtime.Registry
	Store     *storage.DB
	Mailer    interpreter.EmailSender // nil допустим — email-функции запаникуют при вызове, не при сборке
	// EmailFileResolver authorizes paths used as email attachments. UI supplies
	// an RLS-aware resolver for files from attachment storage; nil keeps the
	// ordinary DSL file-sandbox behavior.
	EmailFileResolver interpreter.EmailFileResolver
	Movements         *runtime.MovementsCollector // nil допустим — попадёт в карту как Движения=nil (совместимо с прежним поведением)
	// NetGuard вызывается перед каждой сетевой операцией DSL (HTTP-клиент,
	// email). Возвращает ошибку, если сеть заблокирована предохранителем
	// (план 62). nil → сеть не ограничивается (для тестов/совместимости).
	NetGuard func() error
	// ExecGuard вызывается перед запуском команды ОС (ВыполнитьКоманду, план 67).
	// nil → команды ЗАПРЕЩЕНЫ (secure by default, в отличие от NetGuard): exec —
	// исполнение кода на сервере, разрешается только явной настройкой базы.
	ExecGuard func() error
	// Notifier публикует уведомления в real-time-шину «сервер → браузер»
	// (план 74). nil → функции ОтправитьУведомление/PublishNotification
	// остаются тихим no-op (фоновые задания/тесты без подключённой шины).
	Notifier interpreter.Notifier
	// Interp — интерпретатор конфигурации. Нужен ТОЛЬКО объекту ПланыОбмена, чтобы
	// правило конфликта hook (ПриКонфликтеОбмена) работало при загрузке пакета из
	// DSL (ЗагрузитьПакет). nil → hook на DSL-пути откатывается к by_time (как
	// прежде); остальные DSL-переменные от Interp не зависят.
	Interp *interpreter.Interpreter
}

// Build возвращает map с пересечением DSL-переменных, общих для UI и scheduler.
// Caller добавляет свои специфичные ключи сверху (например, "Сообщить",
// "Документы", транзакционные builtins) напрямую в возвращённую карту.
func (c Common) Build() map[string]any {
	enumsMap := make(map[string]any)
	for _, e := range c.Reg.Enums() {
		inner := make(map[string]any, len(e.Values))
		for _, v := range e.Values {
			inner[v] = v
		}
		enumsMap[e.Name] = &interpreter.MapThis{M: inner}
	}
	constsMap := make(map[string]any)
	if vals, err := c.Store.ListConstants(c.Ctx); err == nil {
		constsMap = vals
	}
	declaredConsts := make([]string, 0)
	for _, k := range c.Reg.Constants() {
		if k != nil {
			declaredConsts = append(declaredConsts, k.Name)
		}
	}
	queryFactory := interpreter.NewQueryFactory(c.Ctx, c.Store, c.Reg)
	predefined := interpreter.NewPredefinedRoot(c.Ctx, c.Store)

	vars := map[string]any{
		"Движения":     c.Movements,
		"Перечисления": &interpreter.MapThis{M: enumsMap},
		// Не MapThis: тот писал бы присвоенное значение только в память
		// процесса, и `Константы.Имя = Значение` молча не доезжало до базы
		// (#719). ConstantsRoot пишет сквозь.
		"Константы":                interpreter.NewConstantsRoot(c.Ctx, c.Store, declaredConsts, constsMap),
		"__factory_Запрос":         queryFactory,
		"__factory_Query":          queryFactory,
		"ПредопределённыеЗначения": predefined,
		"PredefinedValues":         predefined,
	}
	for k, v := range interpreter.NewHTTPFunctions(c.NetGuard) {
		vars[k] = v
	}
	for k, v := range interpreter.NewEmailFunctions(c.Mailer, c.NetGuard, c.EmailFileResolver) {
		vars[k] = v
	}
	for k, v := range interpreter.NewFileFunctions(nil) {
		vars[k] = v
	}
	for k, v := range interpreter.NewLLMFunctions(aiassist.New(c.Ctx, c.Store, nil)) {
		vars[k] = v
	}
	// Объекты HTTP-сервисов (HTTPСервисОтвет, ОтветJSON/ОтветТекст) — нужны
	// прежде всего обработчикам сервисов, но безвредны и в обработках/заданиях,
	// поэтому держим их в общем наборе (как HTTP-клиент).
	for k, v := range interpreter.NewServiceFunctions() {
		vars[k] = v
	}
	// Торговое оборудование (ПодключитьОборудование → ККТ/принтер/весы/дисплей/
	// эквайринг). OneBase — однопользовательское десктоп-приложение, сервер
	// исполняется на машине кассира, поэтому DSL-код обработок/форм подключается
	// к локальным устройствам напрямую (драйверы в internal/equipment). Без этой
	// инжекции ПодключитьОборудование не определена в рантайме конфигурации.
	for k, v := range interpreter.NewEquipmentFunctions() {
		vars[k] = v
	}

	// Уведомления в реальном времени (план 74): ОтправитьУведомление публикует
	// событие в шину сервер→браузер. c.Notifier == nil → тихий no-op.
	for k, v := range interpreter.NewNotifyFunctions(c.Notifier) {
		vars[k] = v
	}

	// Команды ОС (план 67). Выключены по умолчанию: если caller не задал
	// ExecGuard (фоновые задания/тесты), подставляем deny — secure by default
	// (в отличие от сети, где nil-guard разрешает для совместимости). Каждый
	// запуск пишется в журнал (defense-in-depth, поверх настроек аудита).
	execGuard := c.ExecGuard
	if execGuard == nil {
		execGuard = func() error { return errors.New("выполнение команд ОС отключено") }
	}
	var execAudit interpreter.ExecAudit
	if c.Store != nil {
		execAudit = func(command string, cmdArgs []string, code int) {
			detail := strings.Join(cmdArgs, " ")
			if len(detail) > 1000 {
				detail = detail[:1000]
			}
			_ = c.Store.Log(c.Ctx, &storage.AuditEntry{
				UserLogin:  storage.AuditUserLogin(c.Ctx),
				Action:     "exec",
				EntityName: command,
				Field:      detail,
				NewValue:   code,
			})
		}
	}
	for k, v := range interpreter.NewExecFunctions(execGuard, execAudit) {
		vars[k] = v
	}

	// СсылкаНаОбъект → URL карточки объекта /ui/<вид>/<сущность>/<id> (план 66).
	// Две формы:
	//   СсылкаНаОбъект(Ссылка)          — вид/сущность берутся из объекта ссылки;
	//   СсылкаНаОбъект(ИмяСущности, Ид) — для результатов запроса, где колонка
	//                                     Ссылка приходит UUID-строкой без типа.
	// Вид (catalog/document) определяется по сущности через реестр.
	objectURL := func(entity, id string) string {
		if entity == "" || id == "" {
			return ""
		}
		kind := "catalog"
		if ent := c.Reg.GetEntity(entity); ent != nil {
			kind = strings.ToLower(string(ent.Kind))
		}
		return "/ui/" + kind + "/" + entity + "/" + id
	}
	idStr := func(v any) string {
		if ref, ok := v.(*interpreter.Ref); ok {
			return ref.UUID
		}
		if v == nil {
			return ""
		}
		return fmt.Sprintf("%v", v)
	}
	objectRef := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		switch {
		case len(args) >= 2 && args[0] != nil:
			return objectURL(fmt.Sprintf("%v", args[0]), idStr(args[1])), nil
		case len(args) == 1 && args[0] != nil:
			if ref, ok := args[0].(*interpreter.Ref); ok {
				return objectURL(ref.Type, ref.UUID), nil
			}
		}
		return "", nil
	})
	vars["СсылкаНаОбъект"] = objectRef
	vars["ObjectRef"] = objectRef

	// Планы обмена (план 86): ПланыОбмена.<План>.ВыгрузитьИзменения("узел") /
	// .ЗагрузитьПакет(Пакет). Нужны и store, и реестр; без них не инжектируем.
	if c.Store != nil && c.Reg != nil {
		exchangeRoot := interpreter.NewExchangePlansRoot(c.Ctx, c.Store, c.Reg)
		// С доступным интерпретатором подключаем обработчик правила конфликта hook
		// к загрузке пакета из DSL (иначе hook на этом пути откатывается к by_time).
		if c.Interp != nil {
			exchangeRoot = exchangeRoot.WithHook(NewExchangeHook(c.Store, c.Reg, c.Interp))
		}
		vars["ПланыОбмена"] = exchangeRoot
		vars["ExchangePlans"] = exchangeRoot
	}

	// Нумераторы (issue #358): Нумераторы.СледующийНомер("Сущность"[, Дата]) —
	// доступ из DSL к тому же атомарному счётчику автонумерации, что и REST/UI-путь
	// создания записи. Нужен объектам, создаваемым из обработок/заданий, которые
	// идут мимо автонумерации хендлеров.
	if c.Store != nil && c.Reg != nil {
		ctxSource := c.CtxSource
		if ctxSource == nil {
			ctxSource = interpreter.NewStaticCtx(c.Ctx)
		}
		numerators := interpreter.NewNumeratorsRoot(ctxSource, c.Store, c.Reg)
		vars["Нумераторы"] = numerators
		vars["Numerators"] = numerators
	}
	return vars
}
