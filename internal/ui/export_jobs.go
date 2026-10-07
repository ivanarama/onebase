package ui

import (
	"context"
	"fmt"
	"net/http"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/excel"
	"github.com/ivantit66/onebase/internal/incident"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/sheet"
)

const defaultExportJobTTL = 30 * time.Minute

type exportJobStatus string

const (
	exportJobQueued  exportJobStatus = "queued"
	exportJobRunning exportJobStatus = "running"
	exportJobDone    exportJobStatus = "done"
	exportJobError   exportJobStatus = "error"
)

type exportJob struct {
	ID          string
	Owner       string
	Kind        string
	Name        string
	Format      string
	Status      exportJobStatus
	Filename    string
	ContentType string
	Data        []byte
	Error       string
	CreatedAt   time.Time
	StartedAt   time.Time
	FinishedAt  time.Time
	ExpiresAt   time.Time
}

// exportJobSummary contains only fields that are safe to render in HTML.
// In particular, it never carries the exported file's Data []byte.
type exportJobSummary struct {
	ID        string
	Kind      string
	Name      string
	Format    string
	Status    exportJobStatus
	Error     string
	CreatedAt time.Time
	ExpiresAt time.Time
}

func (job exportJob) summary() exportJobSummary {
	return exportJobSummary{
		ID: job.ID, Kind: job.Kind, Name: job.Name, Format: job.Format,
		Status: job.Status, Error: job.Error,
		CreatedAt: job.CreatedAt, ExpiresAt: job.ExpiresAt,
	}
}

type exportJobListItem struct {
	exportJobSummary
	StatusLabel  string
	FormatLabel  string
	CreatedText  string
	ExpiresText  string
	StatusURL    string
	DownloadURL  string
	Downloadable bool
}

type exportJobStore struct {
	mu        sync.Mutex
	jobs      map[string]*exportJob
	ttl       time.Duration
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func newExportJobStore(ttl time.Duration) *exportJobStore {
	if ttl <= 0 {
		ttl = defaultExportJobTTL
	}
	s := &exportJobStore{
		jobs: make(map[string]*exportJob),
		ttl:  ttl,
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	// Готовые файлы лежат в памяти (Data []byte), а уборка раньше происходила
	// только при create/get: если к джобе никто не обращался, большой экспорт
	// висел в RAM бессрочно. Фоновый sweeper снимает это ограничение.
	interval := ttl / 3
	if interval < time.Second {
		interval = time.Second
	}
	go func() {
		defer close(s.done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				s.mu.Lock()
				s.cleanupLocked(time.Now())
				s.mu.Unlock()
			}
		}
	}()
	return s
}

// Close stops the background sweeper and releases completed export payloads.
// It is safe to call more than once.
func (s *exportJobStore) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		close(s.stop)
		<-s.done
		s.mu.Lock()
		clear(s.jobs)
		s.mu.Unlock()
	})
}

func (s *exportJobStore) create(owner, kind, name, format string) exportJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.cleanupLocked(now)
	job := &exportJob{
		ID:        uuid.NewString(),
		Owner:     owner,
		Kind:      kind,
		Name:      name,
		Format:    format,
		Status:    exportJobQueued,
		CreatedAt: now,
		ExpiresAt: now.Add(s.ttl),
	}
	s.jobs[job.ID] = job
	return *job
}

func (s *exportJobStore) get(id string) (exportJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.cleanupLocked(now)
	job, ok := s.jobs[id]
	if !ok {
		return exportJob{}, false
	}
	return *job, true
}

// list returns a snapshot for one owner, newest first. Cleanup and owner
// filtering happen under the same lock, so expired or foreign jobs cannot
// enter a rendered list. File payloads are deliberately excluded.
func (s *exportJobStore) list(owner string) []exportJobSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked(time.Now())
	items := make([]exportJobSummary, 0)
	for _, job := range s.jobs {
		if job.Owner == owner {
			items = append(items, job.summary())
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items
}

func (s *exportJobStore) markRunning(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job := s.jobs[id]; job != nil {
		now := time.Now()
		job.Status = exportJobRunning
		job.StartedAt = now
		job.ExpiresAt = now.Add(s.ttl)
	}
}

func (s *exportJobStore) markDone(id string, file reportExportFile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job := s.jobs[id]; job != nil {
		now := time.Now()
		job.Status = exportJobDone
		job.Filename = file.Filename
		job.ContentType = file.ContentType
		job.Data = file.Data
		job.Error = ""
		job.FinishedAt = now
		job.ExpiresAt = now.Add(s.ttl)
	}
}

func (s *exportJobStore) markError(id, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job := s.jobs[id]; job != nil {
		now := time.Now()
		job.Status = exportJobError
		job.Error = message
		job.FinishedAt = now
		job.ExpiresAt = now.Add(s.ttl)
	}
}

func (s *exportJobStore) cleanupLocked(now time.Time) {
	for id, job := range s.jobs {
		if !job.ExpiresAt.IsZero() && now.After(job.ExpiresAt) {
			delete(s.jobs, id)
		}
	}
}

type reportExportFile struct {
	Data        []byte
	Filename    string
	ContentType string
}

func (s *Server) reportExportJobStart(w http.ResponseWriter, r *http.Request) {
	rep := s.getReport(w, r)
	if rep == nil {
		return
	}
	if !s.requirePerm(w, r, "report", rep.Name, "run") {
		return
	}
	format, ok := normalizeReportExportFormat(chi.URLParam(r, "format"))
	if !ok {
		http.Error(w, "unknown export format", http.StatusNotFound)
		return
	}
	backgroundDone, ok := s.beginBackgroundJob()
	if !ok {
		http.Error(w, "server is shutting down", http.StatusServiceUnavailable)
		return
	}
	jobs := s.exportJobStore()
	job := jobs.create(currentUserLogin(r), "report", rep.Name, format)
	req := r.Clone(context.WithoutCancel(r.Context()))
	go func() {
		defer backgroundDone()
		s.runReportExportJob(req, job.ID, rep, format)
	}()
	http.Redirect(w, r, "/ui/export-jobs/"+job.ID, http.StatusSeeOther)
}

func (s *Server) runReportExportJob(r *http.Request, jobID string, rep *reportpkg.Report, format string) {
	// Горутина живёт вне HTTP-цепочки — chi Recoverer её не прикрывает, а
	// внутри генерация Excel/PDF по произвольным данным. Без recover одна
	// паника роняла весь процесс базы вместе со всеми сессиями.
	defer func() {
		if p := recover(); p != nil {
			// Инцидент регистрируем и здесь: пользователь видит только карточку
			// задачи, и без кода связать её со стеком в журнале нечем (план 116).
			// rep внутри recover не трогаем: сама паника бывает именно на нём.
			id := s.recordBackgroundPanic("экспорт отчёта, задача "+jobID,
				fmt.Sprintf("%v", p), string(debug.Stack()), currentUserLogin(r))
			s.exportJobStore().markError(jobID,
				incident.WithCode(fmt.Sprintf("внутренняя ошибка выгрузки: %v", p), id))
		}
	}()

	ctx, finish, ok := s.beginQueuedOperation(r, opReportExport, rep.Name)
	if !ok {
		msg := "слишком много одновременно выполняемых выгрузок, задача не дождалась свободного слота"
		if err := ctx.Err(); err != nil {
			msg += ": " + s.errText(r, err)
		}
		s.exportJobStore().markError(jobID, msg)
		return
	}

	s.exportJobStore().markRunning(jobID)
	stats := &reportExportStats{}
	opStatus := "ok"
	defer func() { finish(opStatus, stats.rows, stats.truncated, stats.attrs...) }()

	file, err := s.buildReportExportFile(ctx, r, rep, format, stats)
	if err != nil {
		opStatus = reportExportOpStatus(ctx, err)
		s.exportJobStore().markError(jobID, s.reportExportErrorText(r, err))
		return
	}
	s.exportJobStore().markDone(jobID, file)
}

func (s *Server) exportJobStatus(w http.ResponseWriter, r *http.Request) {
	job, ok := s.exportJobForRequest(w, r)
	if !ok {
		return
	}
	summary := job.summary()
	s.render(w, r, "page-export-job", map[string]any{
		"Job":            summary,
		"JobDone":        job.Status == exportJobDone,
		"JobFailed":      job.Status == exportJobError,
		"JobStatusLabel": exportJobStatusLabel(job.Status),
		"JobFormatLabel": exportJobFormatLabel(job.Format),
		"DownloadURL":    "/ui/export-jobs/" + job.ID + "/download",
		"BackURL":        exportJobBackURL(job),
		"CreatedAtText":  job.CreatedAt.Format("15:04:05"),
		"ExpiresAtText":  job.ExpiresAt.Format("15:04:05"),
	})
}

func (s *Server) exportJobList(w http.ResponseWriter, r *http.Request) {
	summaries := s.exportJobStore().list(currentUserLogin(r))
	items := make([]exportJobListItem, 0, len(summaries))
	for _, job := range summaries {
		statusURL := "/ui/export-jobs/" + job.ID
		items = append(items, exportJobListItem{
			exportJobSummary: job,
			StatusLabel:      exportJobStatusLabel(job.Status),
			FormatLabel:      exportJobFormatLabel(job.Format),
			CreatedText:      job.CreatedAt.Format("2006-01-02 15:04:05"),
			ExpiresText:      job.ExpiresAt.Format("2006-01-02 15:04:05"),
			StatusURL:        statusURL,
			DownloadURL:      statusURL + "/download",
			Downloadable:     job.Status == exportJobDone,
		})
	}
	s.render(w, r, "page-export-jobs", map[string]any{"Jobs": items})
}

func (s *Server) exportJobDownload(w http.ResponseWriter, r *http.Request) {
	job, ok := s.exportJobForRequest(w, r)
	if !ok {
		return
	}
	if job.Status != exportJobDone {
		http.Error(w, "export job is not ready", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", job.ContentType)
	w.Header().Set("Content-Disposition", contentDisposition(job.Filename))
	writeDownload(w, job.Filename, job.Data)
}

func (s *Server) exportJobForRequest(w http.ResponseWriter, r *http.Request) (exportJob, bool) {
	id := chi.URLParam(r, "id")
	job, ok := s.exportJobStore().get(id)
	if !ok {
		http.NotFound(w, r)
		return exportJob{}, false
	}
	if currentUserLogin(r) != job.Owner {
		s.renderForbidden(w, r)
		return exportJob{}, false
	}
	return job, true
}

func (s *Server) exportJobStore() *exportJobStore {
	s.exportJobsMu.Lock()
	defer s.exportJobsMu.Unlock()
	if s.exportJobs == nil {
		s.exportJobs = newExportJobStore(defaultExportJobTTL)
	}
	return s.exportJobs
}

func (s *Server) buildReportExportFile(ctx context.Context, r *http.Request, rep *reportpkg.Report, format string, stats *reportExportStats) (reportExportFile, error) {
	headers, rows, err := s.reportExportRowsWithContext(ctx, r, rep, stats)
	if err != nil {
		return reportExportFile{}, err
	}
	switch format {
	case "excel":
		data, err := excel.ExportList(headers, rows)
		if err != nil {
			return reportExportFile{}, newReportExportError(http.StatusInternalServerError, "Excel error", err)
		}
		return reportExportFile{
			Data:        data,
			Filename:    rep.Name + ".xlsx",
			ContentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		}, nil
	case "pdf":
		doc := buildReportSheet(reportDisplayTitle(rep, s.resolveLang(r)), headers, rows)
		data, err := doc.PDF(sheet.PDFOptions{Title: rep.Name})
		if err != nil {
			return reportExportFile{}, newReportExportError(http.StatusInternalServerError, "PDF error", err)
		}
		return reportExportFile{
			Data:        data,
			Filename:    rep.Name + ".pdf",
			ContentType: "application/pdf",
		}, nil
	default:
		return reportExportFile{}, fmt.Errorf("unknown export format: %s", format)
	}
}

func (s *Server) reportExportErrorText(r *http.Request, err error) string {
	if ee, ok := err.(*reportExportError); ok {
		return ee.prefix + ": " + s.errText(r, ee.err)
	}
	return "report export error: " + s.errText(r, err)
}

func normalizeReportExportFormat(format string) (string, bool) {
	switch strings.ToLower(format) {
	case "excel", "xlsx":
		return "excel", true
	case "pdf":
		return "pdf", true
	default:
		return "", false
	}
}

func exportJobFormatLabel(format string) string {
	switch format {
	case "excel":
		return "Excel"
	case "pdf":
		return "PDF"
	default:
		return format
	}
}

func exportJobStatusLabel(status exportJobStatus) string {
	switch status {
	case exportJobQueued:
		return "В очереди"
	case exportJobRunning:
		return "Выполняется"
	case exportJobDone:
		return "Готово"
	case exportJobError:
		return "Ошибка"
	default:
		return string(status)
	}
}

func exportJobBackURL(job exportJob) string {
	if job.Kind == "report" && job.Name != "" {
		return reportFormURL(job.Name)
	}
	return "/ui"
}
