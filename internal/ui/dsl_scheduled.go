package ui

// DSL-глобал РегламентныеЗадания / ScheduledJobs (#742, план 123).
//
//	ИдПрогона = РегламентныеЗадания.Запустить("ОбменСУзлами");
//	Прогон = РегламентныеЗадания.Прогон(ИдПрогона);
//	Если Прогон <> Неопределено И Прогон.Статус = "success" Тогда … КонецЕсли;
//
// Это ровно кнопка «Запустить сейчас» из админки, доступная коду конфигурации:
// тот же single-flight, тот же контекст планировщика, тот же таймаут задания.
// Параллелизма не даёт и не даёт намеренно — одно задание в один момент
// исполняется один раз (пул исполнителей — отдельная заявка). Ожидания
// завершения нет: сессия однопоточная, ждать в ней час нельзя.
//
// Прикладной результат задание возвращает через данные (регистр сведений), а не
// через журнал прогонов: _scheduled_runs — журнал исполнения, не транспорт.
//
// В песочницу глобал не попадает конструктивно: ui-переменные не инжектируются
// в `onebase eval` — единственное место с RestrictedProfile.

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
)

type scheduledJobsRoot struct {
	s      *Server
	ctxSrc docsCtxSource
}

func newScheduledJobsRoot(s *Server, ctxSrc docsCtxSource) *scheduledJobsRoot {
	return &scheduledJobsRoot{s: s, ctxSrc: ctxSrc}
}

func (r *scheduledJobsRoot) Get(_ string) any    { return nil }
func (r *scheduledJobsRoot) Set(_ string, _ any) {}

func (r *scheduledJobsRoot) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "запустить", "start":
		return r.start(args)
	case "прогон", "runinfo":
		return r.runInfo(args)
	}
	interpreter.RaiseUserError("РегламентныеЗадания: неизвестный метод «" + method +
		"» (доступны Запустить, Прогон)")
	return nil
}

func (r *scheduledJobsRoot) start(args []any) any {
	if len(args) != 1 {
		interpreter.RaiseUserError("РегламентныеЗадания.Запустить: ожидается один аргумент — имя задания")
	}
	name, ok := args[0].(string)
	if !ok || strings.TrimSpace(name) == "" {
		interpreter.RaiseUserError("РегламентныеЗадания.Запустить: имя задания должно быть непустой строкой")
	}
	sched := r.scheduler("Запустить")

	// Запуск из открытой транзакции — отказ, а не ожидание. На SQLite пул это
	// одно соединение: задание не получит его, пока инициатор держит
	// транзакцию, и будет голодать до своего таймаута, пока инициатор ждёт
	// задание. На PostgreSQL до взаимной блокировки не доходит, но задание всё
	// равно не увидит незакоммиченных данных инициатора, то есть «записал и тут
	// же запустил обработать» тихо не сработает. Лучше внятный отказ.
	ctx := r.ctx()
	if storage.HasTx(ctx) {
		interpreter.RaiseUserError("РегламентныеЗадания.Запустить: нельзя запускать задание внутри транзакции " +
			"(в том числе из ОбработкаПроведения) — запускайте после записи, из кода формы или обработки")
	}

	runID, err := sched.RunNow(ctx, name)
	if err != nil {
		r.raiseStartError(name, err)
	}
	return runID.String()
}

// raiseStartError переводит отказ планировщика в исключение DSL. Тексты разные
// намеренно: «уже выполняется» — штатная гонка с cron-тиком, её ловят
// Попытка/Исключение и продолжают работу; остальные два случая означают
// совсем другое, и путать их в диагностике нельзя.
func (r *scheduledJobsRoot) raiseStartError(name string, err error) {
	switch {
	case errors.Is(err, scheduler.ErrJobAlreadyRunning):
		interpreter.RaiseUserError("РегламентныеЗадания.Запустить: задание «" + name +
			"» уже выполняется")
	case errors.Is(err, scheduler.ErrSchedulerStopping):
		interpreter.RaiseUserError("РегламентныеЗадания.Запустить: сервер завершает работу, " +
			"задание «" + name + "» не запущено")
	default:
		interpreter.RaiseUserError("РегламентныеЗадания.Запустить: " + err.Error())
	}
}

func (r *scheduledJobsRoot) runInfo(args []any) any {
	if len(args) != 1 {
		interpreter.RaiseUserError("РегламентныеЗадания.Прогон: ожидается один аргумент — идентификатор прогона")
	}
	raw, ok := args[0].(string)
	if !ok {
		interpreter.RaiseUserError("РегламентныеЗадания.Прогон: идентификатор прогона должен быть строкой")
	}
	// Мусор в аргументе — ошибка кода, а не «журнал не помнит»: молчаливое
	// Неопределено здесь скрыло бы опечатку до самого разбора инцидента.
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		interpreter.RaiseUserError("РегламентныеЗадания.Прогон: «" + raw +
			"» не похоже на идентификатор прогона (ожидается значение, возвращённое Запустить)")
	}
	sched := r.scheduler("Прогон")
	run, err := sched.RunByID(r.ctx(), id)
	if err != nil {
		interpreter.RaiseUserError("РегламентныеЗадания.Прогон: " + err.Error())
	}
	if run == nil {
		// Прогона нет — это не ошибка: запись могла быть подрезана, а id
		// пережить её в прикладных данных.
		return nil
	}
	return scheduledRunStruct(run)
}

// scheduledRunStruct отдаёт прогон Структурой — ровно колонки журнала.
// Статуса accepted среди них нет: он достижим только для нативных Go-заданий,
// а сюда попадают прогоны заданий конфигурации.
func scheduledRunStruct(run *storage.ScheduledRun) any {
	st := interpreter.NewStructFromMap(nil)
	st.Set("Задание", run.JobName)
	st.Set("Статус", run.Status)
	st.Set("Начало", run.StartedAt)
	if run.FinishedAt != nil {
		st.Set("Конец", *run.FinishedAt)
	} else {
		st.Set("Конец", nil)
	}
	st.Set("ДлительностьМс", float64(run.DurationMs))
	st.Set("Ошибка", run.Error)
	st.Set("Вывод", run.Output)
	return st
}

// scheduler отдаёт планировщик или внятно отказывает: сервер может быть поднят
// без него (procrun, раннер конфигтестов), и «метод у Неопределено» в этом
// случае ничего не объясняет.
func (r *scheduledJobsRoot) scheduler(method string) *scheduler.Scheduler {
	if r.s == nil || r.s.sched == nil {
		interpreter.RaiseUserError("РегламентныеЗадания." + method +
			": планировщик недоступен в этом режиме (регламентные задания работают на запущенном сервере базы)")
	}
	return r.s.sched
}

func (r *scheduledJobsRoot) ctx() context.Context {
	if r.ctxSrc != nil {
		return r.ctxSrc.Ctx()
	}
	return context.Background()
}
