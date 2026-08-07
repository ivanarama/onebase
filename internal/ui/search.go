package ui

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/search"
)

// Глобальный поиск в интерфейсе (план 82): одна строка в шапке ищет по всем
// доступным пользователю справочникам и документам сразу. Права считает общий
// слой internal/search — тот же, что обслуживает REST и DSL, поэтому выдача
// не может разъехаться между точками входа.

const (
	searchPageSize = 20
	// searchDSLMaxLimit ограничивает выдачу ПолнотекстовогоПоиска: каждая
	// строка выдачи — чтение объекта и проверка политик, поэтому «дай тысячу»
	// из обработки не должно превращаться в тысячу запросов к базе.
	searchDSLMaxLimit = 200
)

// uiSearchDeps связывает общий слой поиска с проверками прав UI-сервера.
type uiSearchDeps struct{ s *Server }

func (d uiSearchDeps) Entities() []*metadata.Entity { return d.s.reg.Entities() }

func (d uiSearchDeps) CanRead(ctx context.Context, e *metadata.Entity) bool {
	return d.s.canCtx(ctx, string(e.Kind), e.Name, "read")
}

func (d uiSearchDeps) RowAllowed(ctx context.Context, e *metadata.Entity, row map[string]any) bool {
	return d.s.rowAllowsSelected(ctx, e, row)
}

func (d uiSearchDeps) MaskedIndexedFields(ctx context.Context, e *metadata.Entity) []string {
	return maskedIndexedFields(d.s.fieldDecisions(ctx, e), e)
}

func (d uiSearchDeps) MaskedLabel(ctx context.Context, e *metadata.Entity, row map[string]any) string {
	return d.s.maskedRecordLabel(ctx, e, row)
}

// maskedIndexedFields отбирает индексируемые реквизиты, значения которых
// пользователю не показываются целиком. По ним поиск не должен подтверждать
// совпадение — см. search.visibleMatch.
func maskedIndexedFields(decisions map[string]access.FieldDecision, e *metadata.Entity) []string {
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

// dslFullTextSearch — встроенная функция DSL ПолнотекстовыйПоиск(Текст, Лимит).
// Права те же, что у пользователя сессии: обработка не должна видеть больше,
// чем тот же пользователь увидел бы в интерфейсе.
func (s *Server) dslFullTextSearch(ctx context.Context, args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("ПолнотекстовыйПоиск: нужен аргумент — строка поиска")
	}
	text := strings.TrimSpace(fmt.Sprintf("%v", args[0]))
	if text == "" {
		return interpreter.NewArray(nil), nil
	}
	limit := searchPageSize
	if len(args) > 1 && args[1] != nil {
		n, err := strconv.Atoi(strings.TrimSpace(fmt.Sprintf("%v", args[1])))
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("ПолнотекстовыйПоиск: второй аргумент — количество результатов, положительное число")
		}
		limit = n
	}
	if limit > searchDSLMaxLimit {
		limit = searchDSLMaxLimit
	}

	page, err := search.Run(ctx, s.store, uiSearchDeps{s}, text, limit, 0)
	if err != nil {
		return nil, fmt.Errorf("ПолнотекстовыйПоиск: %w", err)
	}
	items := make([]any, 0, len(page.Items))
	for _, hit := range page.Items {
		entity := s.reg.GetEntity(hit.Entity)
		items = append(items, interpreter.NewStructFromMap(map[string]any{
			"Объект":          hit.Entity,
			"Вид":             hit.Kind,
			"Представление":   hit.Title,
			"Ссылка":          &interpreter.Ref{UUID: hit.ID.String(), Name: hit.Title, Type: hit.Entity, Manager: s.refManagerFor(entity, ctx)},
			"ПометкаУдаления": hit.DeletionMark,
			"Проведён":        hit.Posted,
		}))
	}
	return interpreter.NewArray(items), nil
}

func (s *Server) globalSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	// Продолжение листания — только непрозрачным курсором: сырое смещение
	// считается по просмотренным строкам индекса и выдавало бы наличие
	// совпадений, скрытых маской или строковой политикой.
	offset := search.DecodeCursor(r.URL.Query().Get("cursor"))

	data := map[string]any{"Q": q, "SearchQuery": q}
	if q == "" {
		s.render(w, r, "page-search", data)
		return
	}

	page, err := search.Run(r.Context(), s.store, uiSearchDeps{s}, q, searchPageSize, offset)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data["Results"] = page.Items
	data["HasMore"] = page.HasMore
	data["NextCursor"] = page.Cursor
	data["Searched"] = true
	s.render(w, r, "page-search", data)
}
