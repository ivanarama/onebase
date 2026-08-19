package storage_test

// Очередь фоновых заданий: захват, аренда, попытки, идемпотентность (план 130,
// issue #848).
//
// Матричный тест обязателен: захват на PostgreSQL идёт с FOR UPDATE SKIP
// LOCKED, а на SQLite — без него, то есть SQL здесь РАЗНЫЙ по построению.
// Отдельные тесты на диалект показали бы каждый своё ожидание и оба были бы
// зелёными, пока поведение молча разъезжается (правило из CLAUDE.md, повод —
// #607).

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestJobQueue_ПостановкаЗахватИЗавершение(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		now := time.Now().UnixMilli()

		task, created, err := db.EnqueueJobTask(ctx, storage.JobTask{
			JobName:     "ОбменСУзлом",
			Params:      map[string]any{"Узел": "N-042", "Полный": true},
			MaxAttempts: 3,
			AvailableAt: now,
			CreatedAt:   now,
		})
		if err != nil {
			t.Fatalf("EnqueueJobTask: %v", err)
		}
		if !created {
			t.Fatal("первая постановка вернула created=false")
		}

		stored, err := db.JobTaskByID(ctx, task.ID)
		if err != nil {
			t.Fatalf("JobTaskByID: %v", err)
		}
		if stored == nil {
			t.Fatal("задача не найдена сразу после постановки")
		}
		if stored.Status != storage.JobTaskPending || stored.Attempts != 0 {
			t.Fatalf("после постановки: статус=%q попытки=%d, ожидались pending/0", stored.Status, stored.Attempts)
		}
		// Параметры обязаны пережить сериализацию: ради них очередь и заводилась —
		// 360 задач круга отличаются ровно параметром узла.
		if stored.Params["Узел"] != "N-042" {
			t.Fatalf("параметр Узел = %#v, ожидался N-042", stored.Params["Узел"])
		}
		if stored.Params["Полный"] != true {
			t.Fatalf("параметр Полный = %#v, ожидался true", stored.Params["Полный"])
		}

		claimed, err := db.ClaimJobTasks(ctx, "host/1", 10, now, now+60_000)
		if err != nil {
			t.Fatalf("ClaimJobTasks: %v", err)
		}
		if len(claimed) != 1 || claimed[0].ID != task.ID {
			t.Fatalf("захвачено %d задач, ожидалась одна наша", len(claimed))
		}
		if claimed[0].Attempts != 1 {
			t.Fatalf("попытки после захвата = %d, ожидалась 1", claimed[0].Attempts)
		}
		if claimed[0].Params["Узел"] != "N-042" {
			t.Fatalf("захват потерял параметры: %#v", claimed[0].Params)
		}

		// Повторный захват не должен видеть уже взятую задачу.
		again, err := db.ClaimJobTasks(ctx, "host/2", 10, now, now+60_000)
		if err != nil {
			t.Fatalf("повторный ClaimJobTasks: %v", err)
		}
		if len(again) != 0 {
			t.Fatalf("вторая попытка захвата взяла %d задач, ожидался 0", len(again))
		}

		if err := db.FinishJobTask(ctx, task.ID, storage.JobTaskDone, "готово", "", now+5_000); err != nil {
			t.Fatalf("FinishJobTask: %v", err)
		}
		stored, err = db.JobTaskByID(ctx, task.ID)
		if err != nil {
			t.Fatalf("JobTaskByID после завершения: %v", err)
		}
		if stored.Status != storage.JobTaskDone || stored.Output != "готово" {
			t.Fatalf("после завершения: %+v", stored)
		}
		if got := stored.DurationMs(); got != 5_000 {
			t.Fatalf("длительность = %d мс, ожидалось 5000", got)
		}
	})
}

func TestJobQueue_КлючИдемпотентностиНеПлодитДубли(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		now := time.Now().UnixMilli()
		enqueue := func() (storage.JobTask, bool) {
			t.Helper()
			task, created, err := db.EnqueueJobTask(ctx, storage.JobTask{
				JobName: "ОбменСУзлом", Key: "обмен-N-042",
				MaxAttempts: 1, AvailableAt: now, CreatedAt: now,
			})
			if err != nil {
				t.Fatalf("EnqueueJobTask: %v", err)
			}
			return task, created
		}

		first, created := enqueue()
		if !created {
			t.Fatal("первая постановка вернула created=false")
		}
		second, created := enqueue()
		if created {
			t.Fatal("вторая постановка с тем же ключом создала дубль")
		}
		if second.ID != first.ID {
			t.Fatalf("вернулась чужая задача: %s вместо %s", second.ID, first.ID)
		}

		// Захват не освобождает ключ: продюсер, тикающий раз в минуту, не должен
		// дублировать работу, которая уже исполняется.
		if _, err := db.ClaimJobTasks(ctx, "host/1", 10, now, now+60_000); err != nil {
			t.Fatalf("ClaimJobTasks: %v", err)
		}
		duringRun, created := enqueue()
		if created || duringRun.ID != first.ID {
			t.Fatalf("во время исполнения ключ отпустили: created=%v id=%s", created, duringRun.ID)
		}

		// А терминальная задача ключ отпускает — иначе ту же работу нельзя было
		// бы повторить ни завтра, ни вообще.
		if err := db.FinishJobTask(ctx, first.ID, storage.JobTaskDone, "", "", now+1_000); err != nil {
			t.Fatalf("FinishJobTask: %v", err)
		}
		next, created := enqueue()
		if !created || next.ID == first.ID {
			t.Fatalf("после завершения ключ остался занят: created=%v id=%s", created, next.ID)
		}
	})
}

func TestJobQueue_ПовторИКарантинПоИсчерпаниюПопыток(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		now := time.Now().UnixMilli()
		task, _, err := db.EnqueueJobTask(ctx, storage.JobTask{
			JobName: "ОбменСУзлом", MaxAttempts: 2, AvailableAt: now, CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("EnqueueJobTask: %v", err)
		}

		if _, err := db.ClaimJobTasks(ctx, "host/1", 1, now, now+60_000); err != nil {
			t.Fatalf("ClaimJobTasks: %v", err)
		}
		if err := db.RetryJobTask(ctx, task.ID, now+30_000, "узел не ответил"); err != nil {
			t.Fatalf("RetryJobTask: %v", err)
		}
		stored, err := db.JobTaskByID(ctx, task.ID)
		if err != nil {
			t.Fatalf("JobTaskByID: %v", err)
		}
		if stored.Status != storage.JobTaskPending || stored.Attempts != 1 {
			t.Fatalf("после повтора: статус=%q попытки=%d", stored.Status, stored.Attempts)
		}
		if stored.Error != "узел не ответил" {
			t.Fatalf("причина повтора потеряна: %q", stored.Error)
		}

		// Отложенную задачу захват не берёт до наступления времени: без этого
		// экспоненциальный backoff превратился бы в busy loop.
		early, err := db.ClaimJobTasks(ctx, "host/1", 1, now+1_000, now+61_000)
		if err != nil {
			t.Fatalf("ранний ClaimJobTasks: %v", err)
		}
		if len(early) != 0 {
			t.Fatalf("захвачена задача, отложенная до %d", stored.AvailableAt)
		}

		claimed, err := db.ClaimJobTasks(ctx, "host/1", 1, now+30_000, now+90_000)
		if err != nil {
			t.Fatalf("ClaimJobTasks после отсрочки: %v", err)
		}
		if len(claimed) != 1 || claimed[0].Attempts != 2 {
			t.Fatalf("вторая попытка не состоялась: %+v", claimed)
		}
		if err := db.FinishJobTask(ctx, task.ID, storage.JobTaskDead, "", "узел не ответил", now+31_000); err != nil {
			t.Fatalf("FinishJobTask(dead): %v", err)
		}

		// Из карантина задача возвращается только руками — и со сброшенным
		// счётчиком, иначе повтор кончился бы, не начавшись.
		if err := db.ReplayJobTask(ctx, task.ID, now+40_000); err != nil {
			t.Fatalf("ReplayJobTask: %v", err)
		}
		stored, err = db.JobTaskByID(ctx, task.ID)
		if err != nil {
			t.Fatalf("JobTaskByID после повтора из карантина: %v", err)
		}
		if stored.Status != storage.JobTaskPending || stored.Attempts != 0 || stored.Error != "" {
			t.Fatalf("после повтора из карантина: %+v", stored)
		}
	})
}

func TestJobQueue_ИзъятиеЗадачиСИстёкшейАрендой(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		now := time.Now().UnixMilli()
		// Две задачи: у первой попытки ещё есть, у второй это была последняя.
		retryable, _, err := db.EnqueueJobTask(ctx, storage.JobTask{
			JobName: "ОбменСУзлом", MaxAttempts: 3, AvailableAt: now, CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("EnqueueJobTask: %v", err)
		}
		lastChance, _, err := db.EnqueueJobTask(ctx, storage.JobTask{
			JobName: "ОбменСУзлом", MaxAttempts: 1, AvailableAt: now, CreatedAt: now + 1,
		})
		if err != nil {
			t.Fatalf("EnqueueJobTask: %v", err)
		}
		if claimed, err := db.ClaimJobTasks(ctx, "умерший-процесс", 10, now, now+1_000); err != nil {
			t.Fatalf("ClaimJobTasks: %v", err)
		} else if len(claimed) != 2 {
			t.Fatalf("захвачено %d задач, ожидались 2", len(claimed))
		}

		// Процесс исполнителя умер: терминального UPDATE не случилось, аренда
		// истекла.
		requeued, dead, err := db.ReclaimExpiredJobTasks(ctx, now+2_000, "аренда истекла")
		if err != nil {
			t.Fatalf("ReclaimExpiredJobTasks: %v", err)
		}
		if requeued != 1 || dead != 1 {
			t.Fatalf("изъятие: возвращено %d, в карантин %d; ожидалось 1 и 1", requeued, dead)
		}

		first, err := db.JobTaskByID(ctx, retryable.ID)
		if err != nil {
			t.Fatalf("JobTaskByID: %v", err)
		}
		if first.Status != storage.JobTaskPending || first.Worker != "" {
			t.Fatalf("задача с оставшимися попытками не вернулась в очередь: %+v", first)
		}
		second, err := db.JobTaskByID(ctx, lastChance.ID)
		if err != nil {
			t.Fatalf("JobTaskByID: %v", err)
		}
		if second.Status != storage.JobTaskDead {
			t.Fatalf("задача без попыток не ушла в карантин: %+v", second)
		}

		// Живая аренда не трогается: иначе развёртка отбирала бы работу у
		// работающего исполнителя.
		if _, err := db.ClaimJobTasks(ctx, "живой", 1, now+2_000, now+100_000); err != nil {
			t.Fatalf("ClaimJobTasks живым исполнителем: %v", err)
		}
		requeued, dead, err = db.ReclaimExpiredJobTasks(ctx, now+3_000, "аренда истекла")
		if err != nil {
			t.Fatalf("ReclaimExpiredJobTasks (живая аренда): %v", err)
		}
		if requeued != 0 || dead != 0 {
			t.Fatalf("развёртка тронула живую аренду: возвращено %d, карантин %d", requeued, dead)
		}
	})
}

func TestJobQueue_ОтменаАрендаИУборка(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		now := time.Now().UnixMilli()
		// Сначала заводим и захватываем исполняемую, потом ставим ожидающую:
		// так роли задач не зависят от порядка выборки в захвате.
		running, _, err := db.EnqueueJobTask(ctx, storage.JobTask{
			JobName: "ОбменСУзлом", MaxAttempts: 3, AvailableAt: now, CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("EnqueueJobTask: %v", err)
		}
		if claimed, err := db.ClaimJobTasks(ctx, "host/1", 1, now, now+60_000); err != nil {
			t.Fatalf("ClaimJobTasks: %v", err)
		} else if len(claimed) != 1 || claimed[0].ID != running.ID {
			t.Fatalf("захвачена не та задача: %+v", claimed)
		}
		waiting, _, err := db.EnqueueJobTask(ctx, storage.JobTask{
			JobName: "ОбменСУзлом", MaxAttempts: 3, AvailableAt: now, CreatedAt: now + 1,
		})
		if err != nil {
			t.Fatalf("EnqueueJobTask: %v", err)
		}

		state, err := db.RequestJobTaskCancel(ctx, waiting.ID, now, "отменена пользователем")
		if err != nil {
			t.Fatalf("RequestJobTaskCancel (ожидающая): %v", err)
		}
		if state != storage.JobTaskCancelled {
			t.Fatalf("ожидающая задача отменилась как %q, ожидалось cancelled", state)
		}

		state, err = db.RequestJobTaskCancel(ctx, running.ID, now, "отменена пользователем")
		if err != nil {
			t.Fatalf("RequestJobTaskCancel (исполняемая): %v", err)
		}
		if state != "cancelling" {
			t.Fatalf("исполняемая задача отменилась как %q, ожидалось cancelling", state)
		}

		// Heartbeat продлевает аренду и приносит исполнителю пометку отмены —
		// это единственный момент, когда он смотрит в базу.
		cancelled, err := db.RenewJobLeases(ctx, []uuid.UUID{running.ID}, now+120_000)
		if err != nil {
			t.Fatalf("RenewJobLeases: %v", err)
		}
		if len(cancelled) != 1 || cancelled[0] != running.ID {
			t.Fatalf("heartbeat не принёс отмену: %v", cancelled)
		}
		stored, err := db.JobTaskByID(ctx, running.ID)
		if err != nil {
			t.Fatalf("JobTaskByID: %v", err)
		}
		if stored.LeaseUntil != now+120_000 {
			t.Fatalf("аренда не продлена: %d", stored.LeaseUntil)
		}

		if err := db.FinishJobTask(ctx, running.ID, storage.JobTaskCancelled, "", "отменена", now+1_000); err != nil {
			t.Fatalf("FinishJobTask: %v", err)
		}
		stats, err := db.JobQueueStats(ctx)
		if err != nil {
			t.Fatalf("JobQueueStats: %v", err)
		}
		if stats[storage.JobTaskCancelled] != 2 {
			t.Fatalf("глубина по cancelled = %d, ожидалось 2", stats[storage.JobTaskCancelled])
		}

		// Уборка сносит завершённые и не трогает карантин: он ждёт человека.
		dead, _, err := db.EnqueueJobTask(ctx, storage.JobTask{
			JobName: "ОбменСУзлом", MaxAttempts: 1, AvailableAt: now, CreatedAt: now + 2,
		})
		if err != nil {
			t.Fatalf("EnqueueJobTask: %v", err)
		}
		if err := db.FinishJobTask(ctx, dead.ID, storage.JobTaskDead, "", "сдалась", now+1_000); err != nil {
			t.Fatalf("FinishJobTask(dead): %v", err)
		}
		removed, err := db.PruneJobTasks(ctx, now+100_000, 10_000)
		if err != nil {
			t.Fatalf("PruneJobTasks: %v", err)
		}
		if removed != 2 {
			t.Fatalf("подрезано %d задач, ожидалось 2 (обе отменённые)", removed)
		}
		if survivor, err := db.JobTaskByID(ctx, dead.ID); err != nil {
			t.Fatalf("JobTaskByID: %v", err)
		} else if survivor == nil {
			t.Fatal("уборка снесла задачу из карантина")
		}
	})
}
