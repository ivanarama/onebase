package ui

// Монитор очереди фоновых заданий (план 130, issue #848).
//
// Отвечает на три вопроса, ради которых очередь вообще смотрят: сколько работы
// ждёт, что исполняется прямо сейчас и что застряло в карантине. Карантин —
// главное: задача, исчерпавшая попытки, никуда не денется сама, и без экрана с
// кнопкой «Повторить» её разбор превращается в SQL по служебной таблице.

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/storage"
)

// queueStatusOrder задаёт порядок карточек: сперва то, что требует внимания.
var queueStatusOrder = []struct {
	Status string
	Title  string
}{
	{storage.JobTaskPending, "Ждут исполнителя"},
	{storage.JobTaskRunning, "Исполняются"},
	{storage.JobTaskDead, "В карантине"},
	{storage.JobTaskDone, "Выполнены"},
	{storage.JobTaskCancelled, "Отменены"},
}

func (s *Server) jobQueueMonitor(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	queue := s.cfg.JobQueue
	data := map[string]any{
		"Available": queue != nil,
		"Enabled":   queue.Enabled(),
		"Workers":   queue.Workers(),
		"Degraded":  queue.Degraded(),
		"InFlight":  queue.InFlight(),
	}
	if queue == nil {
		s.render(w, r, "page-job-queue", data)
		return
	}

	status := strings.TrimSpace(r.URL.Query().Get("status"))
	stats, err := queue.Stats(r.Context())
	if err != nil {
		http.Error(w, s.errText(r, err), http.StatusInternalServerError)
		return
	}
	cards := make([]map[string]any, 0, len(queueStatusOrder))
	for _, item := range queueStatusOrder {
		cards = append(cards, map[string]any{
			"Status": item.Status,
			"Title":  item.Title,
			"Count":  stats[item.Status],
			"Active": status == item.Status,
		})
	}
	tasks, err := queue.List(r.Context(), storage.JobTaskFilter{Status: status, Limit: 200})
	if err != nil {
		http.Error(w, s.errText(r, err), http.StatusInternalServerError)
		return
	}
	rows := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		rows = append(rows, jobQueueRow(task))
	}
	data["Cards"] = cards
	data["Tasks"] = rows
	data["Status"] = status
	s.render(w, r, "page-job-queue", data)
}

// jobQueueRow переводит задачу в вид для шаблона: время из эпохи-миллисекунд
// превращается в Дату, чтобы шаблон форматировал его тем же fmtDate, что и
// остальные экраны.
func jobQueueRow(task storage.JobTask) map[string]any {
	id := task.ID.String()
	row := map[string]any{
		"ID": id,
		// Короткий идентификатор в строке — чтобы задачу из монитора можно было
		// сопоставить с логом (task=…) и с ФоновыеЗадания.Задача(Ид). Полный
		// лежит в title, потому что в терминальной строке кнопок нет и больше
		// его взять неоткуда.
		"ShortID":     id[:8],
		"JobName":     task.JobName,
		"Status":      task.Status,
		"Key":         task.Key,
		"Attempts":    task.Attempts,
		"MaxAttempts": task.MaxAttempts,
		"Worker":      task.Worker,
		"Error":       task.Error,
		"DurationMs":  task.DurationMs(),
		"Terminal":    task.Terminal(),
		"Quarantined": task.Status == storage.JobTaskDead,
		"Params":      jobQueueParamsText(task.Params),
	}
	if task.CreatedAt > 0 {
		row["CreatedAt"] = time.UnixMilli(task.CreatedAt)
	}
	if task.StartedAt > 0 {
		row["StartedAt"] = time.UnixMilli(task.StartedAt)
	}
	// Отложенный повтор видно только у ожидающих: у исполняемой задачи
	// available_at — это прошлое, и показывать его значило бы путать.
	if task.Status == storage.JobTaskPending && task.AvailableAt > time.Now().UnixMilli() {
		row["RetryAt"] = time.UnixMilli(task.AvailableAt)
	}
	return row
}

func jobQueueParamsText(params map[string]any) string {
	if len(params) == 0 {
		return ""
	}
	parts := make([]string, 0, len(params))
	for name, value := range params {
		text := strings.ReplaceAll(fmt.Sprintf("%v", value), "\n", " ")
		parts = append(parts, name+"="+strings.TrimSpace(text))
	}
	// Порядок обхода карты в Go случаен — сортируем, иначе строка параметров
	// скакала бы между обновлениями страницы.
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func (s *Server) jobQueueReplay(w http.ResponseWriter, r *http.Request) {
	s.jobQueueAction(w, r, func(id uuid.UUID) error {
		return s.cfg.JobQueue.Replay(r.Context(), id)
	})
}

func (s *Server) jobQueueCancel(w http.ResponseWriter, r *http.Request) {
	s.jobQueueAction(w, r, func(id uuid.UUID) error {
		_, err := s.cfg.JobQueue.Cancel(r.Context(), id)
		return err
	})
}

func (s *Server) jobQueueAction(w http.ResponseWriter, r *http.Request, action func(uuid.UUID) error) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	if s.cfg.JobQueue == nil {
		http.Error(w, "очередь фоновых заданий недоступна", http.StatusServiceUnavailable)
		return
	}
	id, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "id")))
	if err != nil {
		http.Error(w, "неверный идентификатор задачи", http.StatusBadRequest)
		return
	}
	if err := action(id); err != nil {
		http.Error(w, s.errText(r, err), http.StatusBadRequest)
		return
	}
	target := "/ui/admin/queue"
	if status := strings.TrimSpace(r.URL.Query().Get("status")); status != "" {
		target += "?status=" + status
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
