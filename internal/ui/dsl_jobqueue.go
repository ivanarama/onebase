package ui

// DSL-глобал ФоновыеЗадания / BackgroundJobs (#848, план 130).
//
//	Ид = ФоновыеЗадания.Поставить("ОбменСУзлом", Новый Структура("Узел", Код), "обмен-" + Код);
//	Задача = ФоновыеЗадания.Задача(Ид);
//	Если Задача <> Неопределено И Задача.Статус = "dead" Тогда … КонецЕсли;
//
// Отличие от РегламентныеЗадания.Запустить — параллелизм: очередь исполняет
// одно и то же задание сразу многими исполнителями, каждый со своими
// параметрами. Ради этого сценария (обход ~360 узлов обмена круглосуточно) она
// и заводилась.
//
// Постановка внутри транзакции РАЗРЕШЕНА намеренно, в отличие от Запустить:
// строка задачи ложится в транзакцию инициатора и станет видимой исполнителю
// после коммита, а откат снимет задачу вместе с данными, ради которых её
// ставили. «Записал документ и поставил задачу на обмен» работает ровно так,
// как ожидается.
//
// В песочницу глобал не попадает конструктивно: ui-переменные не инжектируются
// в `onebase eval` — единственное место с RestrictedProfile.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/jobqueue"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
)

type jobQueueRoot struct {
	s      *Server
	ctxSrc docsCtxSource
}

func newJobQueueRoot(s *Server, ctxSrc docsCtxSource) *jobQueueRoot {
	return &jobQueueRoot{s: s, ctxSrc: ctxSrc}
}

func (r *jobQueueRoot) Get(_ string) any    { return nil }
func (r *jobQueueRoot) Set(_ string, _ any) {}

func (r *jobQueueRoot) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "поставить", "enqueue":
		return r.enqueue(args)
	case "задача", "task":
		return r.task(args)
	case "отменить", "cancel":
		return r.cancel(args)
	case "глубина", "depth":
		return r.depth(args)
	}
	interpreter.RaiseUserError("ФоновыеЗадания: неизвестный метод «" + method +
		"» (доступны Поставить, Задача, Отменить, Глубина)")
	return nil
}

func (r *jobQueueRoot) enqueue(args []any) any {
	if len(args) == 0 || len(args) > 3 {
		interpreter.RaiseUserError("ФоновыеЗадания.Поставить: ожидается от одного до трёх аргументов " +
			"(ИмяЗадания[, Параметры][, Ключ])")
	}
	name, ok := args[0].(string)
	if !ok || strings.TrimSpace(name) == "" {
		interpreter.RaiseUserError("ФоновыеЗадания.Поставить: имя задания должно быть непустой строкой")
	}
	name = strings.TrimSpace(name)

	var params map[string]any
	if len(args) > 1 && args[1] != nil {
		params = taskParamsFromDSL(args[1])
	}
	key := ""
	if len(args) > 2 && args[2] != nil {
		raw, ok := args[2].(string)
		if !ok {
			interpreter.RaiseUserError("ФоновыеЗадания.Поставить: ключ идемпотентности должен быть строкой")
		}
		key = strings.TrimSpace(raw)
	}

	// Имя задания проверяем ДО постановки: иначе опечатка всплыла бы задачей в
	// карантине через три неудачные попытки и минуты ожидания.
	r.checkJob(name)

	queue := r.queue("Поставить")
	task, created, err := queue.Enqueue(r.ctx(), name, params, key)
	if err != nil {
		if errors.Is(err, jobqueue.ErrQueueDisabled) {
			interpreter.RaiseUserError("ФоновыеЗадания.Поставить: очередь фоновых заданий выключена " +
				"(queue.workers: 0 в app.yaml) — задача не поставлена")
		}
		interpreter.RaiseUserError("ФоновыеЗадания.Поставить: " + err.Error())
	}
	_ = created // ключ идемпотентности вернул живую задачу — для кода это тот же id
	return task.ID.String()
}

// checkJob отказывает на именах, которые очередь исполнить не сможет.
func (r *jobQueueRoot) checkJob(name string) {
	if r.s == nil || r.s.sched == nil {
		interpreter.RaiseUserError("ФоновыеЗадания.Поставить: планировщик недоступен в этом режиме " +
			"(очередь работает на запущенном сервере базы)")
	}
	err := r.s.sched.QueueableJob(name)
	switch {
	case err == nil:
	case errors.Is(err, scheduler.ErrJobNotFound):
		interpreter.RaiseUserError("ФоновыеЗадания.Поставить: регламентного задания «" + name +
			"» нет в конфигурации")
	case errors.Is(err, scheduler.ErrJobNotQueueable):
		interpreter.RaiseUserError("ФоновыеЗадания.Поставить: «" + name +
			"» — встроенное задание платформы, его нельзя ставить в очередь")
	default:
		interpreter.RaiseUserError("ФоновыеЗадания.Поставить: " + err.Error())
	}
}

func (r *jobQueueRoot) task(args []any) any {
	if len(args) != 1 {
		interpreter.RaiseUserError("ФоновыеЗадания.Задача: ожидается один аргумент — идентификатор задачи")
	}
	id := r.parseID("Задача", args[0])
	task, err := r.queue("Задача").Task(r.ctx(), id)
	if err != nil {
		interpreter.RaiseUserError("ФоновыеЗадания.Задача: " + err.Error())
	}
	if task == nil {
		// Задачи нет — не ошибка: история могла быть подрезана ретенцией, а id
		// пережить её в прикладных данных.
		return nil
	}
	return jobTaskStruct(task)
}

func (r *jobQueueRoot) cancel(args []any) any {
	if len(args) != 1 {
		interpreter.RaiseUserError("ФоновыеЗадания.Отменить: ожидается один аргумент — идентификатор задачи")
	}
	id := r.parseID("Отменить", args[0])
	state, err := r.queue("Отменить").Cancel(r.ctx(), id)
	if err != nil {
		interpreter.RaiseUserError("ФоновыеЗадания.Отменить: " + err.Error())
	}
	// Истина и для снятой из очереди, и для той, которой отмена только
	// запрошена: для вызывающего разница не в том, что произошло сейчас, а в
	// том, будет ли задача исполнена — и в обоих случаях ответ «нет».
	return state != ""
}

func (r *jobQueueRoot) depth(args []any) any {
	if len(args) > 1 {
		interpreter.RaiseUserError("ФоновыеЗадания.Глубина: ожидается не больше одного аргумента — статус")
	}
	status := storage.JobTaskPending
	if len(args) == 1 && args[0] != nil {
		raw, ok := args[0].(string)
		if !ok {
			interpreter.RaiseUserError("ФоновыеЗадания.Глубина: статус должен быть строкой")
		}
		status = strings.TrimSpace(raw)
		// Неизвестный статус — опечатка, и молчаливый ноль в ответ был бы
		// худшим из исходов: «очередь пуста» выглядит как нормальная работа.
		if !knownTaskStatus(status) {
			interpreter.RaiseUserError("ФоновыеЗадания.Глубина: неизвестный статус «" + status +
				"» (допустимы pending, running, done, dead, cancelled)")
		}
	}
	stats, err := r.queue("Глубина").Stats(r.ctx())
	if err != nil {
		interpreter.RaiseUserError("ФоновыеЗадания.Глубина: " + err.Error())
	}
	return decimal.NewFromInt(stats[status])
}

func knownTaskStatus(status string) bool {
	switch status {
	case storage.JobTaskPending, storage.JobTaskRunning, storage.JobTaskDone,
		storage.JobTaskDead, storage.JobTaskCancelled:
		return true
	}
	return false
}

func (r *jobQueueRoot) parseID(method string, raw any) uuid.UUID {
	text, ok := raw.(string)
	if !ok {
		interpreter.RaiseUserError("ФоновыеЗадания." + method + ": идентификатор задачи должен быть строкой")
	}
	// Мусор в аргументе — ошибка кода, а не «очередь не помнит»: молчаливое
	// Неопределено скрыло бы опечатку до самого разбора инцидента.
	id, err := uuid.Parse(strings.TrimSpace(text))
	if err != nil {
		interpreter.RaiseUserError("ФоновыеЗадания." + method + ": «" + text +
			"» не похоже на идентификатор задачи (ожидается значение, возвращённое Поставить)")
	}
	return id
}

// taskParamsFromDSL переводит Структуру/Соответствие в параметры задачи.
//
// Допускаются скаляры — ровно то, чем бывают params задания в YAML: строка,
// число, булево, дата. Ссылки и коллекции отвергаются намеренно: задача живёт в
// базе между процессами, и «протащить» через неё живой объект нельзя — передайте
// код или идентификатор, а объект пусть задание прочитает само.
func taskParamsFromDSL(value any) map[string]any {
	out := map[string]any{}
	switch typed := value.(type) {
	case *interpreter.Struct:
		for _, field := range typed.Fields() {
			out[field] = taskParamValue(field, typed.Get(field))
		}
	case *interpreter.Map:
		for _, key := range typed.Keys() {
			name, ok := key.(string)
			if !ok {
				interpreter.RaiseUserError("ФоновыеЗадания.Поставить: ключи параметров должны быть строками")
			}
			out[name] = taskParamValue(name, typed.Get(key))
		}
	default:
		interpreter.RaiseUserError("ФоновыеЗадания.Поставить: параметры задаются Структурой или Соответствием")
	}
	return out
}

func taskParamValue(name string, value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case string, bool, int, int64, float64:
		return typed
	case decimal.Decimal:
		return typed
	case time.Time:
		// Storage сохраняет тип даты в служебной карте: строкой её здесь делать
		// нельзя, иначе обработка очереди получит RFC3339 вместо DSL Даты.
		return typed
	}
	interpreter.RaiseUserError("ФоновыеЗадания.Поставить: параметр «" + name +
		"» имеет неподдерживаемый тип — допустимы строка, число, булево и дата " +
		"(ссылку передавайте кодом или идентификатором)")
	return nil
}

// jobTaskStruct отдаёт задачу Структурой — ровно колонки очереди.
func jobTaskStruct(task *storage.JobTask) any {
	st := interpreter.NewStructFromMap(nil)
	st.Set("Задание", task.JobName)
	st.Set("Статус", task.Status)
	st.Set("Ключ", task.Key)
	st.Set("Попытки", decimal.NewFromInt(int64(task.Attempts)))
	st.Set("МаксПопыток", decimal.NewFromInt(int64(task.MaxAttempts)))
	st.Set("Создана", msToDSLTime(task.CreatedAt))
	st.Set("Начало", msToDSLTime(task.StartedAt))
	st.Set("Конец", msToDSLTime(task.FinishedAt))
	st.Set("ДоступнаС", msToDSLTime(task.AvailableAt))
	st.Set("ДлительностьМс", decimal.NewFromInt(task.DurationMs()))
	st.Set("Исполнитель", task.Worker)
	st.Set("Ошибка", task.Error)
	st.Set("Вывод", task.Output)
	params := interpreter.NewStructFromMap(nil)
	for name, value := range task.Params {
		params.Set(name, restoreTaskParam(value))
	}
	st.Set("Параметры", params)
	return st
}

// restoreTaskParam сохраняет совместимость со строковыми датами в задачах,
// поставленных до появления типизированной сериализации. Новые задачи storage
// уже возвращает с time.Time и этот эвристический путь не используют.
func restoreTaskParam(value any) any {
	if text, ok := value.(string); ok {
		if parsed, err := time.Parse(time.RFC3339, text); err == nil {
			return parsed
		}
	}
	return value
}

func msToDSLTime(ms int64) any {
	if ms <= 0 {
		return nil
	}
	return time.UnixMilli(ms)
}

// queue отдаёт очередь или внятно отказывает: сервер может быть поднят без неё
// (procrun, раннер конфигтестов), и «метод у Неопределено» в этом случае ничего
// не объясняет.
func (r *jobQueueRoot) queue(method string) *jobqueue.Pool {
	if r.s == nil || r.s.cfg.JobQueue == nil {
		interpreter.RaiseUserError("ФоновыеЗадания." + method +
			": очередь фоновых заданий недоступна в этом режиме (она работает на запущенном сервере базы)")
	}
	return r.s.cfg.JobQueue
}

func (r *jobQueueRoot) ctx() context.Context {
	if r.ctxSrc != nil {
		return r.ctxSrc.Ctx()
	}
	return context.Background()
}
