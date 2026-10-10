package ui

// Живой список (план 87, ступень A) — серверная сторона автопубликации. После
// успешной записи/проведения/удаления сущности с notify_changes платформа шлёт
// служебное событие «данные.<сущность>» ТОЛЬКО тем онлайн-пользователям, кому
// изменённая строка видна по RLS. Адресаты = объединение прав ДО и ПОСЛЕ
// изменения (прежний владелец убирает строку, новый — видит). Ошибка загрузки
// пользователя/политики → пропуск адресата (fail-closed), без отката бизнес-
// транзакции и БЕЗ отката на роль/«*».

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/realtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// changePublisher реализует entityservice.ChangePublisher поверх Server.
type changePublisher struct{ s *Server }

// changePublisher возвращает публикатор изменений для инъекции в entityservice/
// DSL-путь. nil, если шина не поднята (тесты/headless) — потребитель no-op.
func (s *Server) newChangePublisher() entityservice.ChangePublisher {
	if s == nil || s.hub == nil {
		return nil
	}
	return &changePublisher{s: s}
}

// PublishChange рассылает «данные.<сущность>» живым спискам с адресацией по RLS.
func (p *changePublisher) PublishChange(ctx context.Context, entityName, action string, before, after map[string]any) {
	s := p.s
	entity := s.reg.GetEntity(entityName)
	if entity == nil || !entity.NotifyChanges {
		return
	}
	ids := s.hub.ActiveIdentities()
	if len(ids) == 0 {
		return
	}
	ev := realtime.Event{
		Name: "данные." + strings.ToLower(entity.Name),
		Data: map[string]any{"действие": action},
	}
	// Легаси-режим без аутентификации: единый неявный пользователь, RLS нет —
	// широковещание. После появления пользователей путь строго row-aware.
	if s.authRepo == nil {
		s.hub.Publish("*", ev)
		return
	}
	logins := p.addressees(ctx, entity, before, after, ids)
	s.hub.PublishEphemeralToLogins(ev, logins)
}

// addressees возвращает логины подписчиков, кому видна строка до ИЛИ после
// изменения. Порядок сохранён; дубли невозможны (identity уникальны).
func (p *changePublisher) addressees(ctx context.Context, entity *metadata.Entity, before, after map[string]any, ids []realtime.Identity) []string {
	var out []string
	for _, id := range ids {
		u, err := p.loadUserForRLS(ctx, id.UserID)
		if err != nil {
			continue // fail-closed: аноним/удалённый/ошибка — не адресуем
		}
		if p.canSee(ctx, u, entity, before) || p.canSee(ctx, u, entity, after) {
			out = append(out, id.Login)
		}
	}
	return out
}

// canSee повторяет решение доступа к строке из GET списка: DecideWithLookup(read)
// + сопоставление предиката. nil-строка (нет pre/post-образа) → недоступна.
func (p *changePublisher) canSee(ctx context.Context, u *auth.User, entity *metadata.Entity, row map[string]any) bool {
	if row == nil {
		return false
	}
	dec, err := access.DecideWithLookup(u, string(entity.Kind), entity.Name, "read", entity, p.s.reg)
	if err != nil || !dec.Allowed {
		return false
	}
	if dec.Unrestricted {
		return true
	}
	return storage.MatchPredicateWithRefs(row, dec.Predicate, func(e *metadata.Entity, refID uuid.UUID) (map[string]any, bool) {
		if e == nil {
			return nil, false
		}
		r, err := p.s.store.GetByID(ctx, e.Name, refID, e)
		return r, err == nil
	})
}

// publishDocChange публикует изменение из DSL-пути документов (dsl_documents.go),
// который идёт мимо entityservice.Save. Снимки фиксируются до commit, публикация
// отложится до её commit (DeferUntilTxCommit); при откате — не сработает.
func (s *Server) publishDocChange(ctx context.Context, entity *metadata.Entity, id uuid.UUID, action string, before map[string]any) {
	cp := s.newChangePublisher()
	if cp == nil || entity == nil || !entity.NotifyChanges {
		return
	}
	name := entity.Name
	var after map[string]any
	if action != "удалён" {
		after, _ = s.store.GetByID(ctx, name, id, entity)
	}
	publish := func() {
		cp.PublishChange(context.Background(), name, action, before, after)
	}
	if storage.DeferUntilTxCommit(ctx, publish) {
		return
	}
	publish()
}

// loadUserForRLS собирает пользователя ровно как session-middleware: реквизиты +
// актуальные роли. Так решение адресации совпадает с решением GET списка.
func (p *changePublisher) loadUserForRLS(ctx context.Context, userID string) (*auth.User, error) {
	u, err := p.s.authRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if roles, err := p.s.authRepo.GetRolesForUser(ctx, u.ID); err == nil {
		u.Roles = roles
	}
	return u, nil
}
