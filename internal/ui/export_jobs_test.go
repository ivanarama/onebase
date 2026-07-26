package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	reportpkg "github.com/ivantit66/onebase/internal/report"
)

func TestExportJobStoreLifecycleAndCleanup(t *testing.T) {
	store := newExportJobStore(time.Minute)
	t.Cleanup(store.Close)
	job := store.create("ivan", "report", "Продажи", "excel")
	if job.Status != exportJobQueued {
		t.Fatalf("новая задача должна быть queued, получено %q", job.Status)
	}

	store.markRunning(job.ID)
	got, ok := store.get(job.ID)
	if !ok {
		t.Fatal("задача должна существовать")
	}
	if got.Status != exportJobRunning || got.StartedAt.IsZero() {
		t.Fatalf("markRunning не обновил статус/время: %+v", got)
	}

	store.markDone(job.ID, reportExportFile{
		Data:        []byte("xlsx"),
		Filename:    "Продажи.xlsx",
		ContentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	})
	got, ok = store.get(job.ID)
	if !ok {
		t.Fatal("готовая задача должна существовать")
	}
	if got.Status != exportJobDone || got.Filename != "Продажи.xlsx" || string(got.Data) != "xlsx" {
		t.Fatalf("markDone сохранил неверный результат: %+v", got)
	}

	store.mu.Lock()
	store.jobs[job.ID].ExpiresAt = time.Now().Add(-time.Second)
	store.mu.Unlock()
	if _, ok := store.get(job.ID); ok {
		t.Fatal("истёкшая задача должна удаляться при чтении")
	}
}

func TestReportExportJobStartCompletesExcelAndDownloads(t *testing.T) {
	rep := &reportpkg.Report{Name: "Тест", Query: "ВЫБРАТЬ 1 КАК Номер, 2 КАК Сумма"}
	s := newReportExportTestServer(t, rep)
	s.exportJobs = newExportJobStore(time.Minute)
	s.ops = newOperationLimiter()

	r := reqWithChi("GET", "/ui/report/Тест/export/excel", url.Values{}, map[string]string{"name": "Тест", "format": "excel"})
	w := httptest.NewRecorder()
	s.reportExportJobStart(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("ожидался 303, получен %d: %s", w.Code, w.Body.String())
	}
	location := w.Header().Get("Location")
	const prefix = "/ui/export-jobs/"
	if !strings.HasPrefix(location, prefix) {
		t.Fatalf("редирект должен вести на страницу задачи, получено %q", location)
	}
	jobID := strings.TrimPrefix(location, prefix)

	var job exportJob
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, ok := s.exportJobs.get(jobID)
		if ok && (got.Status == exportJobDone || got.Status == exportJobError) {
			job = got
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if job.Status == "" {
		t.Fatal("фоновая задача не завершилась за отведённое время")
	}
	if job.Status != exportJobDone {
		t.Fatalf("ожидалась готовая задача, получено %q: %s", job.Status, job.Error)
	}
	if job.Filename != "Тест.xlsx" || job.ContentType == "" || len(job.Data) == 0 {
		t.Fatalf("готовый файл заполнен неверно: %+v", job)
	}

	dr := reqWithChi("GET", location+"/download", url.Values{}, map[string]string{"id": jobID})
	dw := httptest.NewRecorder()
	s.exportJobDownload(dw, dr)
	if dw.Code != http.StatusOK {
		t.Fatalf("скачивание должно вернуть 200, получено %d: %s", dw.Code, dw.Body.String())
	}
	if ct := dw.Header().Get("Content-Type"); ct != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cd := dw.Header().Get("Content-Disposition"); !strings.Contains(cd, ".xlsx") {
		t.Fatalf("Content-Disposition без .xlsx: %q", cd)
	}
	if dw.Body.Len() == 0 {
		t.Fatal("тело скачивания не должно быть пустым")
	}
}

func TestReportExportJobWaitsForExportSlot(t *testing.T) {
	rep := &reportpkg.Report{Name: "Очередь", Query: "ВЫБРАТЬ 1 КАК Номер"}
	s := newReportExportTestServer(t, rep)
	s.cfg.Limits.ExportConcurrency = 1
	s.exportJobs = newExportJobStore(time.Minute)
	s.ops = newOperationLimiter()

	release, ok := s.ops.tryAcquire(opReportExport, 1)
	if !ok {
		t.Fatal("не удалось занять единственный слот выгрузки")
	}
	slotHeld := true
	defer func() {
		if slotHeld {
			release()
		}
	}()

	job := s.exportJobs.create("", "report", rep.Name, "excel")
	r := reqWithChi("GET", "/ui/report/Очередь/export/excel", url.Values{}, map[string]string{"name": "Очередь", "format": "excel"})
	go s.runReportExportJob(r, job.ID, rep, "excel")

	time.Sleep(50 * time.Millisecond)
	got, ok := s.exportJobs.get(job.ID)
	if !ok {
		t.Fatal("задача должна существовать")
	}
	if got.Status != exportJobQueued {
		t.Fatalf("пока слот занят, задача должна ждать в queued, получено %q", got.Status)
	}

	release()
	slotHeld = false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, _ = s.exportJobs.get(job.ID)
		if got.Status == exportJobDone || got.Status == exportJobError {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got.Status != exportJobDone {
		t.Fatalf("после освобождения слота задача должна завершиться, получено %q: %s", got.Status, got.Error)
	}
}
