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
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/shopspring/decimal"
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

		if _, err := db.FinishJobTask(ctx, claimed[0].Lease(), storage.JobTaskDone, "готово", "", now+5_000); err != nil {
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

func TestJobQueue_ПараметрыСохраняютЧислоДатуИСтроку(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		now := time.Now().UnixMilli()
		amount := decimal.RequireFromString("1234567890.010203")
		moment := time.Date(2026, 8, 22, 14, 35, 17, 123456000, time.FixedZone("MSK", 3*60*60))
		dateLikeString := moment.Format(time.RFC3339Nano)

		task, _, err := db.EnqueueJobTask(ctx, storage.JobTask{
			JobName: "Типы", Params: map[string]any{
				"Сумма": amount, "Момент": moment, "Строка": dateLikeString,
			},
			MaxAttempts: 1, AvailableAt: now, CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("EnqueueJobTask: %v", err)
		}
		stored, err := db.JobTaskByID(ctx, task.ID)
		if err != nil {
			t.Fatalf("JobTaskByID: %v", err)
		}
		gotAmount, ok := stored.Params["Сумма"].(decimal.Decimal)
		if !ok || !gotAmount.Equal(amount) {
			t.Fatalf("Сумма = %#v (%T), ожидалось Decimal %s", stored.Params["Сумма"], stored.Params["Сумма"], amount)
		}
		gotMoment, ok := stored.Params["Момент"].(time.Time)
		if !ok || !gotMoment.Equal(moment) {
			t.Fatalf("Момент = %#v (%T), ожидалась дата %s", stored.Params["Момент"], stored.Params["Момент"], moment)
		}
		if got, ok := stored.Params["Строка"].(string); !ok || got != dateLikeString {
			t.Fatalf("RFC3339-строка = %#v (%T), строковый тип потерян", stored.Params["Строка"], stored.Params["Строка"])
		}
	})
}

func TestJobQueue_СхемаДобавляетТокенПопыткиВСуществующуюТаблицу(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		d := db.Dialect()
		if _, err := db.Exec(ctx, `DROP TABLE _job_queue`); err != nil {
			t.Fatalf("DROP TABLE: %v", err)
		}
		oldDDL := fmt.Sprintf(`CREATE TABLE _job_queue (
			id %s PRIMARY KEY, job_name TEXT NOT NULL, params TEXT NOT NULL DEFAULT '',
			key TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'pending',
			attempts INTEGER NOT NULL DEFAULT 0, max_attempts INTEGER NOT NULL DEFAULT 1,
			available_at BIGINT NOT NULL DEFAULT 0, created_at BIGINT NOT NULL DEFAULT 0,
			started_at BIGINT NOT NULL DEFAULT 0, finished_at BIGINT NOT NULL DEFAULT 0,
			lease_until BIGINT NOT NULL DEFAULT 0, worker TEXT NOT NULL DEFAULT '',
			cancel_requested %s NOT NULL DEFAULT FALSE,
			error TEXT NOT NULL DEFAULT '', output TEXT NOT NULL DEFAULT '')`, d.TypeUUID(), d.TypeBool())
		if _, err := db.Exec(ctx, oldDDL); err != nil {
			t.Fatalf("создание старой схемы: %v", err)
		}
		id := uuid.New()
		insert := fmt.Sprintf(`INSERT INTO _job_queue (id, job_name) VALUES (%s, %s)`,
			d.Placeholder(1), d.Placeholder(2))
		if _, err := db.Exec(ctx, insert, id.String(), "СтараяЗадача"); err != nil {
			t.Fatalf("вставка строки старой схемы: %v", err)
		}

		if err := db.EnsureJobQueueSchema(ctx); err != nil {
			t.Fatalf("EnsureJobQueueSchema после обновления: %v", err)
		}
		if err := db.EnsureJobQueueSchema(ctx); err != nil {
			t.Fatalf("повторный EnsureJobQueueSchema: %v", err)
		}
		if has, err := d.ColumnExists(ctx, db, "_job_queue", "attempt_token"); err != nil || !has {
			t.Fatalf("attempt_token: exists=%v err=%v", has, err)
		}
		claimed, err := db.ClaimJobTasks(ctx, "worker/1", 1, 0, 60_000)
		if err != nil || len(claimed) != 1 || claimed[0].ID != id {
			t.Fatalf("ClaimJobTasks после миграции: claimed=%+v err=%v", claimed, err)
		}
		if claimed[0].AttemptToken == "" {
			t.Fatal("ClaimJobTasks не назначил fencing-токен")
		}
	})
}

func TestJobQueue_LegacyRFC3339СтрокаНеМеняетТипПриЧтении(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		id := uuid.New()
		d := db.Dialect()
		q := fmt.Sprintf(`INSERT INTO _job_queue (id, job_name, params) VALUES (%s, %s, %s)`,
			d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
		if _, err := db.Exec(ctx, q, id.String(), "СтараяЗадача",
			`{"Момент":"2026-08-22T14:35:17+03:00","Сумма":"41.25"}`); err != nil {
			t.Fatalf("вставка старой строки очереди: %v", err)
		}

		stored, err := db.JobTaskByID(ctx, id)
		if err != nil {
			t.Fatalf("JobTaskByID: %v", err)
		}
		if got, ok := stored.Params["Момент"].(string); !ok || got != "2026-08-22T14:35:17+03:00" {
			t.Fatalf("старая RFC3339-строка = %#v (%T), тип изменился", stored.Params["Момент"], stored.Params["Момент"])
		}
		// Без type-sidecar ни старый decimal, ни дата неотличимы от намеренных
		// строк. Их безопаснее оставить строками, чем менять поведение задачи.
		if got, ok := stored.Params["Сумма"].(string); !ok || got != "41.25" {
			t.Fatalf("старая числовая строка = %#v (%T)", stored.Params["Сумма"], stored.Params["Сумма"])
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
		claimed, err := db.ClaimJobTasks(ctx, "host/1", 10, now, now+60_000)
		if err != nil {
			t.Fatalf("ClaimJobTasks: %v", err)
		}
		duringRun, created := enqueue()
		if created || duringRun.ID != first.ID {
			t.Fatalf("во время исполнения ключ отпустили: created=%v id=%s", created, duringRun.ID)
		}

		// А терминальная задача ключ отпускает — иначе ту же работу нельзя было
		// бы повторить ни завтра, ни вообще.
		if _, err := db.FinishJobTask(ctx, claimed[0].Lease(), storage.JobTaskDone, "", "", now+1_000); err != nil {
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

		firstAttempt, err := db.ClaimJobTasks(ctx, "host/1", 1, now, now+60_000)
		if err != nil {
			t.Fatalf("ClaimJobTasks: %v", err)
		}
		if _, err := db.RetryJobTask(ctx, firstAttempt[0].Lease(), now+30_000, now, "узел не ответил"); err != nil {
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
		if _, err := db.FinishJobTask(ctx, claimed[0].Lease(), storage.JobTaskDead, "", "узел не ответил", now+31_000); err != nil {
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

func TestJobQueue_УстаревшаяПопыткаНеПродлеваетИНеПерезаписываетНовую(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		now := time.Now().UnixMilli()
		task, _, err := db.EnqueueJobTask(ctx, storage.JobTask{
			JobName: "ОбменСУзлом", MaxAttempts: 3, AvailableAt: now, CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("EnqueueJobTask: %v", err)
		}
		first, err := db.ClaimJobTasks(ctx, "старый-worker", 1, now, now+1_000)
		if err != nil || len(first) != 1 {
			t.Fatalf("первый ClaimJobTasks: claimed=%+v err=%v", first, err)
		}
		if requeued, dead, err := db.ReclaimExpiredJobTasks(ctx, now+2_000, "аренда истекла"); err != nil {
			t.Fatalf("ReclaimExpiredJobTasks: %v", err)
		} else if requeued != 1 || dead != 0 {
			t.Fatalf("reclaim: requeued=%d dead=%d, ожидалось 1/0", requeued, dead)
		}
		secondLeaseUntil := now + 120_000
		second, err := db.ClaimJobTasks(ctx, "новый-worker", 1, now+2_000, secondLeaseUntil)
		if err != nil || len(second) != 1 || second[0].Attempts != 2 {
			t.Fatalf("повторный ClaimJobTasks: claimed=%+v err=%v", second, err)
		}

		renewedLeaseUntil := now + 180_000
		cancelled, lost, err := db.RenewJobLeases(ctx,
			[]storage.JobTaskLease{first[0].Lease(), second[0].Lease()}, renewedLeaseUntil)
		if err != nil {
			t.Fatalf("stale RenewJobLeases: %v", err)
		}
		if len(cancelled) != 0 || len(lost) != 1 || lost[0] != first[0].Lease() {
			t.Fatalf("stale heartbeat: cancelled=%v lost=%v", cancelled, lost)
		}
		if _, err := db.FinishJobTask(ctx, first[0].Lease(), storage.JobTaskDone, "старый результат", "", now+3_000); !errors.Is(err, storage.ErrJobLeaseLost) {
			t.Fatalf("stale FinishJobTask = %v, ожидался ErrJobLeaseLost", err)
		}
		if _, err := db.RetryJobTask(ctx, first[0].Lease(), now+4_000, now+3_000, "старый отказ"); !errors.Is(err, storage.ErrJobLeaseLost) {
			t.Fatalf("stale RetryJobTask = %v, ожидался ErrJobLeaseLost", err)
		}

		stored, err := db.JobTaskByID(ctx, task.ID)
		if err != nil {
			t.Fatalf("JobTaskByID: %v", err)
		}
		if stored.Status != storage.JobTaskRunning || stored.Worker != "новый-worker" ||
			stored.Attempts != 2 || stored.LeaseUntil != renewedLeaseUntil || stored.Output != "" {
			t.Fatalf("устаревшая попытка изменила новую: %+v", stored)
		}
		if _, err := db.FinishJobTask(ctx, second[0].Lease(), storage.JobTaskDone, "новый результат", "", now+5_000); err != nil {
			t.Fatalf("новый владелец не смог завершить задачу: %v", err)
		}
	})
}

func TestJobQueue_ТокенПопыткиНеПовторяетсяПослеReplay(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		now := time.Now().UnixMilli()
		task, _, err := db.EnqueueJobTask(ctx, storage.JobTask{
			JobName: "ОбменСУзлом", MaxAttempts: 1, AvailableAt: now, CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("EnqueueJobTask: %v", err)
		}
		first, err := db.ClaimJobTasks(ctx, "worker/1", 1, now, now+60_000)
		if err != nil || len(first) != 1 || first[0].AttemptToken == "" {
			t.Fatalf("первый ClaimJobTasks: claimed=%+v err=%v", first, err)
		}
		if _, err := db.FinishJobTask(ctx, first[0].Lease(), storage.JobTaskDead, "", "ошибка", now+100); err != nil {
			t.Fatalf("FinishJobTask(dead): %v", err)
		}
		if err := db.ReplayJobTask(ctx, task.ID, now+200); err != nil {
			t.Fatalf("ReplayJobTask: %v", err)
		}
		second, err := db.ClaimJobTasks(ctx, "worker/1", 1, now+200, now+60_000)
		if err != nil || len(second) != 1 || second[0].Attempts != 1 {
			t.Fatalf("второй ClaimJobTasks: claimed=%+v err=%v", second, err)
		}
		if second[0].AttemptToken == "" || second[0].AttemptToken == first[0].AttemptToken {
			t.Fatalf("fencing-токен повторился после replay: first=%q second=%q",
				first[0].AttemptToken, second[0].AttemptToken)
		}
		if _, err := db.FinishJobTask(ctx, first[0].Lease(), storage.JobTaskDone, "старый результат", "", now+300); !errors.Is(err, storage.ErrJobLeaseLost) {
			t.Fatalf("FinishJobTask со старым токеном = %v, ожидался ErrJobLeaseLost", err)
		}
		if _, err := db.RetryJobTask(ctx, first[0].Lease(), now+400, now+300, "старая ошибка"); !errors.Is(err, storage.ErrJobLeaseLost) {
			t.Fatalf("RetryJobTask со старым токеном = %v, ожидался ErrJobLeaseLost", err)
		}
		if _, err := db.FinishJobTask(ctx, second[0].Lease(), storage.JobTaskDone, "новый результат", "", now+400); err != nil {
			t.Fatalf("FinishJobTask с новым токеном: %v", err)
		}
	})
}

func TestJobQueue_ОтменаВыигрываетГонкуСПовторомИЗавершением(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		now := time.Now().UnixMilli()

		retryTask, _, err := db.EnqueueJobTask(ctx, storage.JobTask{
			JobName: "ОбменСУзлом", MaxAttempts: 3, AvailableAt: now, CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("EnqueueJobTask(retry): %v", err)
		}
		retryAttempt, err := db.ClaimJobTasks(ctx, "worker/1", 1, now, now+60_000)
		if err != nil || len(retryAttempt) != 1 {
			t.Fatalf("ClaimJobTasks(retry): claimed=%+v err=%v", retryAttempt, err)
		}
		if state, err := db.RequestJobTaskCancel(ctx, retryTask.ID, now+100, "отменена во время ошибки"); err != nil {
			t.Fatalf("RequestJobTaskCancel(retry): %v", err)
		} else if state != "cancelling" {
			t.Fatalf("RequestJobTaskCancel(retry) = %q", state)
		}
		actual, err := db.RetryJobTask(ctx, retryAttempt[0].Lease(), now+30_000, now+200, "ошибка обработки")
		if err != nil {
			t.Fatalf("RetryJobTask после отмены: %v", err)
		}
		if actual != storage.JobTaskCancelled {
			t.Fatalf("RetryJobTask после отмены записал %q вместо cancelled", actual)
		}
		stored, err := db.JobTaskByID(ctx, retryTask.ID)
		if err != nil {
			t.Fatalf("JobTaskByID(retry): %v", err)
		}
		if stored.Status != storage.JobTaskCancelled || stored.Cancel ||
			stored.FinishedAt != now+200 || stored.Error != "отменена во время ошибки" {
			t.Fatalf("отмена потерялась при RetryJobTask: %+v", stored)
		}
		if claimed, err := db.ClaimJobTasks(ctx, "worker/2", 1, now+30_000, now+90_000); err != nil {
			t.Fatalf("ClaimJobTasks после отмены: %v", err)
		} else if len(claimed) != 0 {
			t.Fatalf("отменённая задача вернулась в pending и захвачена: %+v", claimed)
		}

		finishTask, _, err := db.EnqueueJobTask(ctx, storage.JobTask{
			JobName: "ОбменСУзлом", MaxAttempts: 1, AvailableAt: now, CreatedAt: now + 1,
		})
		if err != nil {
			t.Fatalf("EnqueueJobTask(finish): %v", err)
		}
		finishAttempt, err := db.ClaimJobTasks(ctx, "worker/1", 1, now, now+60_000)
		if err != nil || len(finishAttempt) != 1 || finishAttempt[0].ID != finishTask.ID {
			t.Fatalf("ClaimJobTasks(finish): claimed=%+v err=%v", finishAttempt, err)
		}
		if _, err := db.RequestJobTaskCancel(ctx, finishTask.ID, now+300, "отменена перед результатом"); err != nil {
			t.Fatalf("RequestJobTaskCancel(finish): %v", err)
		}
		actual, err = db.FinishJobTask(ctx, finishAttempt[0].Lease(), storage.JobTaskDone, "готово", "", now+400)
		if err != nil {
			t.Fatalf("FinishJobTask после отмены: %v", err)
		}
		if actual != storage.JobTaskCancelled {
			t.Fatalf("FinishJobTask после отмены записал %q вместо cancelled", actual)
		}
		stored, err = db.JobTaskByID(ctx, finishTask.ID)
		if err != nil {
			t.Fatalf("JobTaskByID(finish): %v", err)
		}
		if stored.Status != storage.JobTaskCancelled || stored.Cancel ||
			stored.FinishedAt != now+400 || stored.Error != "отменена перед результатом" {
			t.Fatalf("отмена потерялась при FinishJobTask: %+v", stored)
		}
	})
}

func TestJobQueue_ОтменаВыигрываетГонкуСИзъятиемИстекшейАренды(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		now := time.Now().UnixMilli()

		task, _, err := db.EnqueueJobTask(ctx, storage.JobTask{
			JobName: "ОбменСУзлом", MaxAttempts: 3, AvailableAt: now, CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("EnqueueJobTask: %v", err)
		}
		claimed, err := db.ClaimJobTasks(ctx, "worker/1", 1, now, now+1_000)
		if err != nil || len(claimed) != 1 {
			t.Fatalf("ClaimJobTasks: claimed=%+v err=%v", claimed, err)
		}
		if state, err := db.RequestJobTaskCancel(ctx, task.ID, now+100, "отменена до reclaim"); err != nil {
			t.Fatalf("RequestJobTaskCancel: %v", err)
		} else if state != "cancelling" {
			t.Fatalf("RequestJobTaskCancel = %q", state)
		}

		requeued, dead, err := db.ReclaimExpiredJobTasks(ctx, now+2_000, "аренда истекла")
		if err != nil {
			t.Fatalf("ReclaimExpiredJobTasks: %v", err)
		}
		if requeued != 0 || dead != 0 {
			t.Fatalf("отменённая задача была повторена или карантинирована: requeued=%d dead=%d", requeued, dead)
		}
		stored, err := db.JobTaskByID(ctx, task.ID)
		if err != nil {
			t.Fatalf("JobTaskByID: %v", err)
		}
		if stored.Status != storage.JobTaskCancelled || stored.Cancel ||
			stored.FinishedAt != now+2_000 || stored.Error != "отменена до reclaim" {
			t.Fatalf("reclaim потерял отмену: %+v", stored)
		}
		if claimed, err := db.ClaimJobTasks(ctx, "worker/2", 1, now+3_000, now+60_000); err != nil {
			t.Fatalf("ClaimJobTasks после reclaim: %v", err)
		} else if len(claimed) != 0 {
			t.Fatalf("отменённая задача снова захвачена: %+v", claimed)
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
		var runningAttempt storage.JobTask
		if claimed, err := db.ClaimJobTasks(ctx, "host/1", 1, now, now+60_000); err != nil {
			t.Fatalf("ClaimJobTasks: %v", err)
		} else if len(claimed) != 1 || claimed[0].ID != running.ID {
			t.Fatalf("захвачена не та задача: %+v", claimed)
		} else {
			runningAttempt = claimed[0]
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
		cancelled, lost, err := db.RenewJobLeases(ctx, []storage.JobTaskLease{runningAttempt.Lease()}, now+120_000)
		if err != nil {
			t.Fatalf("RenewJobLeases: %v", err)
		}
		if len(lost) != 0 {
			t.Fatalf("heartbeat потерял живую аренду: %v", lost)
		}
		if len(cancelled) != 1 || cancelled[0] != runningAttempt.Lease() {
			t.Fatalf("heartbeat не принёс отмену: %v", cancelled)
		}
		stored, err := db.JobTaskByID(ctx, running.ID)
		if err != nil {
			t.Fatalf("JobTaskByID: %v", err)
		}
		if stored.LeaseUntil != now+120_000 {
			t.Fatalf("аренда не продлена: %d", stored.LeaseUntil)
		}

		if _, err := db.FinishJobTask(ctx, runningAttempt.Lease(), storage.JobTaskCancelled, "", "отменена", now+1_000); err != nil {
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
		deadAttempt, err := db.ClaimJobTasks(ctx, "host/1", 1, now, now+60_000)
		if err != nil || len(deadAttempt) != 1 || deadAttempt[0].ID != dead.ID {
			t.Fatalf("ClaimJobTasks(dead): claimed=%+v err=%v", deadAttempt, err)
		}
		if _, err := db.FinishJobTask(ctx, deadAttempt[0].Lease(), storage.JobTaskDead, "", "сдалась", now+1_000); err != nil {
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
