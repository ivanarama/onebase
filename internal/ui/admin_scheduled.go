package ui

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
)

func (s *Server) scheduledList(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	states := s.sched.JobStates(r.Context())
	var runs []map[string]any
	for _, st := range states {
		last, _ := s.sched.Runs(r.Context(), st.Job.Name, 1)
		row := map[string]any{
			"Job":   st.Job,
			"State": st,
		}
		if len(last) > 0 {
			row["LastRun"] = last[0]
		}
		runs = append(runs, row)
	}
	s.render(w, r, "page-scheduled-list", map[string]any{
		"JobRows": runs,
	})
}

func (s *Server) scheduledDetail(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	name := chi.URLParam(r, "name")
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	st := s.sched.JobStateByName(r.Context(), name)
	if st == nil {
		http.Error(w, "job not found: "+name, 404)
		return
	}
	runs, _ := s.sched.Runs(r.Context(), st.Job.Name, 50)
	s.render(w, r, "page-scheduled-detail", map[string]any{
		"Job":   st.Job,
		"State": st,
		"Runs":  runs,
		"Msg":   r.URL.Query().Get("msg"),
		"Err":   r.URL.Query().Get("err"),
	})
}

func (s *Server) scheduledRunNow(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	name := chi.URLParam(r, "name")
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	// Идентификатор прогона админке не нужен: она сразу редиректит на карточку
	// задания, где виден весь список прогонов.
	if _, err := s.sched.RunNow(r.Context(), name); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, scheduler.ErrJobAlreadyRunning) {
			status = http.StatusConflict
		} else if errors.Is(err, scheduler.ErrSchedulerStopping) {
			status = http.StatusServiceUnavailable
		}
		http.Error(w, s.errText(r, err), status)
		return
	}
	http.Redirect(w, r, "/ui/admin/scheduled/"+url.PathEscape(name), http.StatusSeeOther)
}

// scheduledToggle переключает фактическую включённость задания (#991):
// административное решение пишется в _settings, переживает обновление
// конфигурации и подхватывается планировщиком со следующего тика — без
// рестарта базы. Ручной запуск при этом остаётся доступен и выключенному.
func (s *Server) scheduledToggle(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	name := chi.URLParam(r, "name")
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	st := s.sched.JobStateByName(r.Context(), name)
	if st == nil {
		http.Error(w, "job not found: "+name, 404)
		return
	}
	if err := s.store.SaveScheduledEnabled(r.Context(), name, !st.EffectiveOn); err != nil {
		s.scheduledRedirect(w, r, name, "", s.errText(r, err))
		return
	}
	action, value := "scheduled.enable", "on"
	if st.EffectiveOn {
		action, value = "scheduled.disable", "off"
	}
	s.auditScheduled(r, action, st, value)
	s.scheduledRedirect(w, r, name, "состояние задания изменено", "")
}

// scheduledReset убирает административное решение — задание снова следует
// конфигурации: обновление конфигурации меняет дефолт, а не перетирает
// чужое решение (и наоборот).
func (s *Server) scheduledReset(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	name := chi.URLParam(r, "name")
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	st := s.sched.JobStateByName(r.Context(), name)
	if st == nil {
		http.Error(w, "job not found: "+name, 404)
		return
	}
	if err := s.store.DeleteScheduledEnabled(r.Context(), name); err != nil {
		s.scheduledRedirect(w, r, name, "", s.errText(r, err))
		return
	}
	s.auditScheduled(r, "scheduled.reset", st, "")
	s.scheduledRedirect(w, r, name, "состояние задания возвращено к конфигурации", "")
}

// auditScheduled пишет в журнал аудита действие администратора над заданием:
// «кто выключил автобэкап» должен быть отвечаемым вопросом.
func (s *Server) auditScheduled(r *http.Request, action string, st *scheduler.JobState, newValue string) {
	e := &storage.AuditEntry{
		Action:     action,
		EntityKind: "scheduled",
		EntityName: st.Job.Name,
		NewValue:   newValue,
		IP:         r.RemoteAddr,
	}
	if u := auth.UserFromContext(r.Context()); u != nil {
		e.UserID = u.ID
		e.UserLogin = u.Login
	}
	_ = s.store.Log(r.Context(), e)
}

func (s *Server) scheduledRedirect(w http.ResponseWriter, r *http.Request, name, msg, errMsg string) {
	v := url.Values{}
	if msg != "" {
		v.Set("msg", msg)
	}
	if errMsg != "" {
		v.Set("err", errMsg)
	}
	dest := "/ui/admin/scheduled/" + url.PathEscape(name)
	if enc := v.Encode(); enc != "" {
		dest += "?" + enc
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}
