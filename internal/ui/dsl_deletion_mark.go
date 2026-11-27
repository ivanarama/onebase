package ui

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// isDeletionMarkName — стандартный реквизит ПометкаУдаления объекта.
func isDeletionMarkName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "пометкаудаления", "deletionmark":
		return true
	}
	return false
}

// dslSetDeletionMark ставит/снимает пометку удаления записанного объекта тем же
// путём, что Менеджер.ПометитьНаУдаление(Ссылка): право delete с RLS и
// markForDeletion (у проведённого документа пометка снимает проведение, как в 1С).
func (s *Server) dslSetDeletionMark(ctx context.Context, entity *metadata.Entity, id uuid.UUID, mark bool) error {
	if err := s.checkDSLRowAccess(ctx, entity, "delete", id, nil); err != nil {
		return err
	}
	return s.markForDeletion(ctx, entity, id, mark)
}
