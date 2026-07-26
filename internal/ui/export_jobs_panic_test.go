package ui

// Паника в фоновой горутине экспорта не должна ронять процесс: она вне
// HTTP-цепочки, chi Recoverer её не прикрывает. runReportExportJob обязан
// перехватить панику и перевести джобу в статус «Ошибка».

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestRunReportExportJobRecoversPanic(t *testing.T) {
	s := &Server{}
	t.Cleanup(s.Close)
	jobs := s.exportJobStore()
	job := jobs.create("tester", "report", "Сломанный", "excel")

	r := httptest.NewRequest("POST", "/ui/report/x/export-job/excel", nil)
	// rep == nil → паника при обращении к rep.Name внутри джобы; recover
	// обязан пометить джобу ошибкой, а не уронить тестовый процесс.
	s.runReportExportJob(r, job.ID, nil, "excel")

	got, ok := jobs.get(job.ID)
	if !ok {
		t.Fatal("джоба пропала из стора")
	}
	if got.Status != exportJobError {
		t.Fatalf("статус = %q, ожидался error", got.Status)
	}
	if got.Error == "" {
		t.Fatal("текст ошибки должен быть заполнен")
	}
}

// Sweeper стора экспортов убирает просроченные джобы без обращений к API стора.
func TestExportJobStoreSweeper(t *testing.T) {
	jobs := newExportJobStore(50 * time.Millisecond)
	t.Cleanup(jobs.Close)
	job := jobs.create("tester", "report", "R", "pdf")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		jobs.mu.Lock()
		_, exists := jobs.jobs[job.ID]
		jobs.mu.Unlock()
		if !exists {
			return // sweeper убрал сам, get() не вызывался
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("sweeper не убрал просроченную джобу за 5 секунд")
}

func TestExportJobStoreCloseStopsSweeperAndClearsPayloads(t *testing.T) {
	jobs := newExportJobStore(time.Minute)
	job := jobs.create("tester", "report", "R", "pdf")
	jobs.markDone(job.ID, reportExportFile{Data: []byte("large payload")})

	jobs.Close()
	jobs.Close()

	select {
	case <-jobs.done:
	default:
		t.Fatal("sweeper did not stop")
	}
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	if len(jobs.jobs) != 0 {
		t.Fatalf("jobs retained after close: %d", len(jobs.jobs))
	}
}
