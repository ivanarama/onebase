package ui

// Приёмные HTTP-эндпоинты онлайн-обмена (план 86, фаза 2). Монтируются ВНЕ
// session-middleware (как HTTP-сервисы): базы аутентифицируются общим Bearer-
// токеном плана (_settings exchange.token.<план>), не cookie. Это
// trusted-cooperative модель: FromNode/параметр peer нужны для маршрутизации и
// bookkeeping, но не являются криптографически подтверждённой identity узла.
//
//	POST /exchange/{plan}/push       — принять пакет и загрузить в эту базу;
//	GET  /exchange/{plan}/pull?to=X  — собрать и отдать пакет для узла X.

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/exchange"
	"github.com/ivantit66/onebase/internal/metadata"
)

const maxExchangeBody = 64 << 20 // 64 MiB

// exchangeApplyOptions собирает параметры загрузки пакета для Go-путей приёма
// (онлайн push и монитор): обработчик правила конфликта hook и перепроведение
// проведённых документов (repost) поверх entityservice. entitySvc есть у сервера,
// поэтому перепроведение доступно на онлайн-приёмнике (в отличие от headless CLI).
func (s *Server) exchangeApplyOptions() exchange.ApplyOptions {
	return exchange.ApplyOptions{
		Hook: NewExchangeHook(s.store, s.reg, s.interp),
		Repost: func(ctx context.Context, entityType string, id uuid.UUID) error {
			return s.entitySvc.Repost(ctx, entityType, id)
		},
	}
}

// MountExchange монтирует эндпоинты онлайн-обмена на верхнеуровневый роутер.
func (s *Server) MountExchange(r chi.Router) {
	r.Post("/exchange/{plan}/push", s.exchangePush)
	r.Get("/exchange/{plan}/pull", s.exchangePull)
}

// exchangeAuthedPlan резолвит план из пути и проверяет Bearer-токен. При любой
// проблеме пишет ответ и возвращает nil.
func (s *Server) exchangeAuthedPlan(w http.ResponseWriter, r *http.Request) *metadata.ExchangePlan {
	plan := s.reg.GetExchangePlan(chi.URLParam(r, "plan"))
	if plan == nil {
		http.Error(w, "план обмена не найден", http.StatusNotFound)
		return nil
	}
	token, err := s.store.GetExchangeToken(r.Context(), plan.Name)
	if err != nil {
		http.Error(w, "настройки обмена недоступны", http.StatusInternalServerError)
		return nil
	}
	if strings.TrimSpace(token) == "" {
		http.Error(w, "онлайн-обмен по плану не настроен (нет токена)", http.StatusForbidden)
		return nil
	}
	got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
		http.Error(w, "неверный токен обмена", http.StatusUnauthorized)
		return nil
	}
	return plan
}

func (s *Server) exchangePair(w http.ResponseWriter, r *http.Request, plan *metadata.ExchangePlan, requestedPeer string) (thisNode, peerNode string, ok bool) {
	thisNode, err := s.store.GetExchangeThisNode(r.Context(), plan.Name)
	if err != nil {
		http.Error(w, "не удалось прочитать текущий узел", http.StatusInternalServerError)
		return "", "", false
	}
	thisDef := plan.Node(thisNode)
	if thisDef == nil {
		http.Error(w, "текущий узел базы не настроен", http.StatusConflict)
		return "", "", false
	}
	thisNode = thisDef.Code
	peerDef := plan.Node(strings.TrimSpace(requestedPeer))
	if peerDef == nil || strings.EqualFold(peerDef.Code, thisNode) {
		http.Error(w, "узел-партнёр не найден или совпадает с текущим", http.StatusForbidden)
		return "", "", false
	}
	// Допустимо только ребро топологии. Для пары/плоского плана это любой другой
	// узел; для звезды — только hub↔spoke. Проверяем обе стороны, чтобы helper
	// оставался корректным при будущей направленной топологии.
	allowed := func(from, to string) bool {
		for _, target := range plan.RegistrationTargets(from) {
			if strings.EqualFold(target, to) {
				return true
			}
		}
		return false
	}
	if !allowed(thisNode, peerDef.Code) || !allowed(peerDef.Code, thisNode) {
		http.Error(w, "обмен между указанными узлами запрещён топологией плана", http.StatusForbidden)
		return "", "", false
	}
	return thisNode, peerDef.Code, true
}

func (s *Server) exchangePush(w http.ResponseWriter, r *http.Request) {
	plan := s.exchangeAuthedPlan(w, r)
	if plan == nil {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxExchangeBody))
	if err != nil {
		status := http.StatusBadRequest
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, "тело пакета: "+err.Error(), status)
		return
	}
	pkg, err := exchange.ParsePackage(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	thisNode, peerNode, ok := s.exchangePair(w, r, plan, pkg.FromNode)
	if !ok {
		return
	}
	if !strings.EqualFold(pkg.FromNode, peerNode) || !strings.EqualFold(pkg.ToNode, thisNode) {
		http.Error(w, "пакет не принадлежит настроенной паре узлов", http.StatusForbidden)
		return
	}
	res, err := exchange.ApplyPackage(r.Context(), s.store, s.reg, plan, body, s.exchangeApplyOptions())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	respondJSONTo(w, res)
}

func (s *Server) exchangePull(w http.ResponseWriter, r *http.Request) {
	plan := s.exchangeAuthedPlan(w, r)
	if plan == nil {
		return
	}
	toNode := strings.TrimSpace(r.URL.Query().Get("to"))
	_, peerNode, ok := s.exchangePair(w, r, plan, toNode)
	if !ok {
		return
	}
	data, err := exchange.BuildPackage(r.Context(), s.store, s.reg, plan, peerNode)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data) //nolint:gosec // G705: ответ уходит с инертным типом (text/plain или application/json), браузер его не исполняет
}
