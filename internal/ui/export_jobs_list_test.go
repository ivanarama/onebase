package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	reportpkg "github.com/ivantit66/onebase/internal/report"
)

func TestExportJobListHTTPFiltersOwnerAndShowsLiveJobs(t *testing.T) {
	s := newReportExportTestServer(t, &reportpkg.Report{Name: "Тест", Query: "ВЫБРАТЬ 1 КАК Номер"})
	jobs := s.exportJobStore()
	old := jobs.create("alice", "report", "Готовый отчёт", "excel")
	jobs.markDone(old.ID, reportExportFile{Data: []byte("PRIVATE FILE PAYLOAD"), Filename: "report.xlsx", ContentType: "application/octet-stream"})
	newer := jobs.create("alice", "report", "Ожидающий отчёт", "pdf")
	running := jobs.create("alice", "report", "Работающий отчёт", "pdf")
	jobs.markRunning(running.ID)
	failed := jobs.create("alice", "report", "Отчёт с ошибкой", "excel")
	jobs.markError(failed.ID, "ошибка выгрузки")
	foreign := jobs.create("bob", "report", "BOB PRIVATE REPORT", "pdf")
	expired := jobs.create("alice", "report", "EXPIRED REPORT", "excel")

	now := time.Now()
	jobs.mu.Lock()
	jobs.jobs[old.ID].CreatedAt = now.Add(-4 * time.Minute)
	jobs.jobs[newer.ID].CreatedAt = now.Add(-3 * time.Minute)
	jobs.jobs[running.ID].CreatedAt = now.Add(-2 * time.Minute)
	jobs.jobs[failed.ID].CreatedAt = now.Add(-time.Minute)
	jobs.jobs[expired.ID].ExpiresAt = now.Add(-time.Second)
	jobs.mu.Unlock()

	router := chi.NewRouter()
	s.Mount(router)
	request := func(path, login string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{ID: login, Login: login}))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	w := request("/ui/export-jobs", "alice")
	if w.Code != http.StatusOK {
		t.Fatalf("список вернул %d: %s", w.Code, w.Body.String())
	}
	page := w.Body.String()
	for _, text := range []string{old.ID, newer.ID, running.ID, failed.ID, "Готовый отчёт", "Ожидающий отчёт", "Работающий отчёт", "Отчёт с ошибкой", "В очереди", "Выполняется", "Готово", "Ошибка"} {
		if !strings.Contains(page, text) {
			t.Errorf("список не содержит %q", text)
		}
	}
	for _, text := range []string{foreign.ID, foreign.Name, expired.ID, expired.Name, "PRIVATE FILE PAYLOAD"} {
		if strings.Contains(page, text) {
			t.Errorf("список раскрыл %q", text)
		}
	}
	if strings.Index(page, failed.ID) >= strings.Index(page, running.ID) ||
		strings.Index(page, running.ID) >= strings.Index(page, newer.ID) ||
		strings.Index(page, newer.ID) >= strings.Index(page, old.ID) {
		t.Fatal("задания должны идти от новых к старым")
	}
	if !strings.Contains(page, "/ui/export-jobs/"+old.ID+"/download") || strings.Contains(page, "/ui/export-jobs/"+newer.ID+"/download") {
		t.Fatal("ссылка скачивания должна быть только у готового задания")
	}
	statusPage := request("/ui/export-jobs/"+old.ID, "alice").Body.String()
	if !strings.Contains(statusPage, `href="/ui/export-jobs"`) {
		t.Fatal("страница задания не ведёт в список выгрузок")
	}
	if strings.Contains(statusPage, "PRIVATE FILE PAYLOAD") {
		t.Fatal("страница задания раскрыла байты файла")
	}
	for _, path := range []string{"/ui/export-jobs/" + foreign.ID, "/ui/export-jobs/" + foreign.ID + "/download"} {
		if got := request(path, "alice").Code; got != http.StatusForbidden {
			t.Errorf("чужой путь %s вернул %d, нужен 403", path, got)
		}
	}
	if got := request("/ui/export-jobs", "bob").Body.String(); !strings.Contains(got, foreign.ID) || strings.Contains(got, old.ID) {
		t.Fatal("список Bob должен содержать только его задания")
	}
}

func TestExportJobListHTTPAnonymousSession(t *testing.T) {
	s := newReportExportTestServer(t, &reportpkg.Report{Name: "Тест", Query: "ВЫБРАТЬ 1 КАК Номер"})
	jobs := s.exportJobStore()
	anon := jobs.create("", "report", "Одиночная база", "excel")
	jobs.markDone(anon.ID, reportExportFile{Data: []byte("download contents"), Filename: "report.xlsx", ContentType: "application/octet-stream"})
	other := jobs.create("alice", "report", "Другой пользователь", "pdf")
	router := chi.NewRouter()
	s.Mount(router)

	request := func(path string, user *auth.User) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if user != nil {
			req = req.WithContext(auth.ContextWithUser(req.Context(), user))
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	page := request("/ui/export-jobs", nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), anon.ID) || strings.Contains(page.Body.String(), other.ID) {
		t.Fatalf("анонимный список неверен: %d %s", page.Code, page.Body.String())
	}
	file := request("/ui/export-jobs/"+anon.ID+"/download", nil)
	if file.Code != http.StatusOK || file.Body.String() != "download contents" {
		t.Fatalf("анонимное скачивание неверно: %d %s", file.Code, file.Body.String())
	}
	loggedIn := &auth.User{ID: "alice", Login: "alice"}
	if got := request("/ui/export-jobs/"+anon.ID, loggedIn).Code; got != http.StatusForbidden {
		t.Fatalf("вошедший пользователь видит анонимное задание: %d", got)
	}
}
