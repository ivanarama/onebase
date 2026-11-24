package launcher

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// The status check can take two network deadlines. Keep the launcher list
// interactive and prevent an old URL from publishing after a record is edited.
func (h *handler) startClientProbeLocked(base *Base) {
	if h.clientProbes[base.ID] != nil {
		return
	}
	if h.clientProbes == nil {
		h.clientProbes = make(map[string]*Base)
	}
	current := *base
	h.clientProbes[base.ID] = &current
	h.statusCache[base.ID] = baseStatus{probing: true, fetched: time.Now()}
	go func() {
		problem := probeClientServer(context.Background(), &current, h.clientProbeClient)
		h.statusMu.Lock()
		defer h.statusMu.Unlock()
		if h.clientProbes[current.ID] != &current {
			return
		}
		delete(h.clientProbes, current.ID)
		h.statusCache[current.ID] = baseStatus{running: problem == "", problem: problem, fetched: time.Now()}
	}()
}

func (h *handler) clientProbeStatus(w http.ResponseWriter, r *http.Request) {
	base, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil || !base.Client() {
		http.NotFound(w, r)
		return
	}
	st := h.baseStatuses([]*Base{base})[base.ID]
	message := string(st.problem)
	if st.probing {
		message = "Проверяем подключение"
	} else if st.running {
		message = "сервер отвечает"
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"pending": st.probing, "running": st.running, "text": tr(resolveLang(r), message),
	})
}
