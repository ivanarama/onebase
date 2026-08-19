package scheduler

// Точка входа очереди фоновых заданий (план 130, issue #848).
//
// Планировщик остаётся владельцем ИСПОЛНЕНИЯ (обработка, DSL-окружение,
// таймаут задания), но не владельцем ЦИКЛА: у задачи очереди свой журнал
// (`_job_queue`), свои попытки и своя аренда. Поэтому здесь нет ни
// single-flight, ни записи в `_scheduled_runs` — иначе у одной работы
// оказалось бы два хозяина, и они разъехались бы при первой же ошибке.

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
)

var (
	// ErrJobNotFound — задания с таким именем нет в конфигурации.
	ErrJobNotFound = errors.New("scheduled job not found")
	// ErrJobNotQueueable — нативное Go-задание (автобэкап, сброс демо). Такие
	// живут своим циклом с журналом прогонов и статусом accepted; исполнять их
	// через очередь означало бы завести второго владельца цикла.
	ErrJobNotQueueable = errors.New("native Go job cannot be queued")
)

// QueueableJob проверяет, что задание можно поставить в очередь. Нужен, чтобы
// отказ был в момент ПОСТАНОВКИ: иначе опечатка в имени всплыла бы только после
// исчерпания попыток, задачей в карантине и через минуты после вызова.
func (s *Scheduler) QueueableJob(jobName string) error {
	_, err := s.queueableJob(jobName)
	return err
}

// queueableJob отдаёт КОПИЮ задания: параметры задачи перекрываются поверх
// job.Params, и правка общей структуры планировщика испортила бы все следующие
// прогоны этого задания.
func (s *Scheduler) queueableJob(jobName string) (*metadata.ScheduledJob, error) {
	key := jobKey(jobName)
	if key == "" {
		return nil, errors.New("scheduled job name is required")
	}
	s.mu.Lock()
	job := cloneScheduledJob(s.jobByKeyLocked(key))
	_, native := s.goJobs[key]
	s.mu.Unlock()
	if job == nil {
		return nil, fmt.Errorf("%w: %s", ErrJobNotFound, jobName)
	}
	if native {
		return nil, fmt.Errorf("%w: %s", ErrJobNotQueueable, jobName)
	}
	return job, nil
}

// mergeTaskParams накладывает параметры задачи на params задания.
//
// Имена приводятся к тому виду, как параметр ОБЪЯВЛЕН у обработки. Это не
// косметика: DSL регистронезависим и хранит поля Структуры в нижнем регистре, а
// runProcessor сопоставляет params с объявленными параметрами точным
// совпадением. Без нормализации «Новый Структура("Узел", …)» превращался бы в
// ключ «узел», обработка получала бы значение по умолчанию, и потеря была бы
// молчаливой — задача отработала бы «успешно», сделав не то.
func (s *Scheduler) mergeTaskParams(job *metadata.ScheduledJob, params map[string]any) map[string]any {
	merged := make(map[string]any, len(job.Params)+len(params))
	for name, value := range job.Params {
		merged[name] = value
	}
	declared := make(map[string]string)
	if s.reg != nil {
		if proc := s.reg.GetProcessor(job.Processor); proc != nil {
			for _, p := range proc.Params {
				declared[strings.ToLower(p.Name)] = p.Name
			}
		}
	}
	for name, value := range params {
		if canonical, ok := declared[strings.ToLower(name)]; ok {
			// Параметр задания мог прийти из YAML в другом регистре — тогда
			// оба ключа означали бы один параметр, а победил бы случайный.
			if canonical != name {
				delete(merged, name)
			}
			name = canonical
		}
		merged[name] = value
	}
	return merged
}

// ExecuteJobOnce исполняет обработку задания один раз с параметрами задачи.
//
// Отличия от обычного прогона, все намеренные:
//   - нет single-flight: параллельное исполнение одного задания — смысл очереди;
//   - нет записи в журнал прогонов: судьбу задачи пишет очередь;
//   - параметры задачи перекрывают params задания поимённо — 360 задач круга
//     отличаются ровно узлом.
//
// Паника обработки перехватывается здесь, а не у вызывающего: исполнитель
// очереди — обычная горутина, и паника в ней уронила бы весь процесс.
func (s *Scheduler) ExecuteJobOnce(ctx context.Context, jobName string, params map[string]any) (output string, err error) {
	s.mu.Lock()
	stopping := s.stopping || s.sealed
	s.mu.Unlock()
	if stopping {
		return "", ErrSchedulerStopping
	}
	job, err := s.queueableJob(jobName)
	if err != nil {
		return "", err
	}
	if len(params) > 0 {
		job.Params = s.mergeTaskParams(job, params)
	}
	if job.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(job.Timeout)*time.Second)
		defer cancel()
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
			s.log.Error("scheduler: queued job panic", "job", jobName,
				"panic", recovered, "stack", string(debug.Stack()))
		}
	}()
	return s.runProcessor(ctx, job)
}
