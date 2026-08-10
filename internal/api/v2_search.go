package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/search"
)

// Глобальный поиск в REST (план 82). Права считает общий слой internal/search
// — тот же, что обслуживает UI, поэтому выдача по одному запросу совпадает.

const (
	searchDefaultLimit = 20
	searchMaxLimit     = 100
)

// restSearchDeps связывает общий слой поиска с проверками прав REST.
type restSearchDeps struct{ h *handler }

func (d restSearchDeps) Entities() []*metadata.Entity { return d.h.reg.Entities() }

func (d restSearchDeps) CanRead(ctx context.Context, e *metadata.Entity) bool {
	return canREST(ctx, string(e.Kind), e.Name, "read")
}

func (d restSearchDeps) RowAllowed(ctx context.Context, e *metadata.Entity, row map[string]any) bool {
	return d.h.rowAllowed(ctx, e, "read", row)
}

func (d restSearchDeps) MaskedIndexedFields(ctx context.Context, e *metadata.Entity) []string {
	decisions := d.h.fieldDecisions(ctx, e)
	if len(decisions) == 0 {
		return nil
	}
	var out []string
	for _, f := range metadata.FullTextFields(e) {
		for name, dec := range decisions {
			if strings.EqualFold(name, f.Name) && dec.Masked() {
				out = append(out, f.Name)
				break
			}
		}
	}
	return out
}

func (d restSearchDeps) MaskedLabel(ctx context.Context, e *metadata.Entity, row map[string]any) string {
	d.h.maskRecord(ctx, e, row)
	return metadata.RowLabel(row, e)
}

type restSearchHit struct {
	Kind         string `json:"kind"`
	Entity       string `json:"entity"`
	ID           string `json:"id"`
	Title        string `json:"title"`
	DeletionMark bool   `json:"deletion_mark"`
	Posted       bool   `json:"posted,omitempty"`
}

func (h *handler) searchV2() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" {
			writeError(w, http.StatusBadRequest, "параметр q обязателен", "", 0)
			return
		}
		limit := searchDefaultLimit
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				writeError(w, http.StatusBadRequest, "limit должен быть положительным числом", "", 0)
				return
			}
			limit = min(n, searchMaxLimit)
		}
		// Числовое смещение снаружи не принимается: продолжение задаётся только
		// непрозрачным курсором из предыдущего ответа. Иначе перебор позиций дал
		// бы ту же утечку, что и выдача next_offset (см. internal/search/cursor.go).
		// Курсор разбирает сам search.Run — своими q и limit, чтобы продолжить
		// можно было только тот запрос, которым он выдан (#615).
		page, err := search.Run(r.Context(), h.store, restSearchDeps{h: h}, q, limit, r.URL.Query().Get("cursor"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "", 0)
			return
		}
		hits := make([]restSearchHit, 0, len(page.Items))
		for _, item := range page.Items {
			hits = append(hits, restSearchHit{
				Kind:         item.Kind,
				Entity:       item.Entity,
				ID:           item.ID.String(),
				Title:        item.Title,
				DeletionMark: item.DeletionMark,
				Posted:       item.Posted,
			})
		}
		w.Header().Set("X-Limit", strconv.Itoa(limit))
		writeJSONV2(w, http.StatusOK, restV2Envelope{
			Data: hits,
			Meta: &restV2Meta{
				Limit: limit,
				// Общее число совпадений не считается: чтобы его узнать, надо
				// прогнать через права весь индекс. Пагинация идёт по
				// next_cursor — непрозрачной позиции чтения.
				Total:      len(hits),
				NextCursor: page.Cursor,
				HasMore:    page.HasMore,
			},
		})
	}
}
