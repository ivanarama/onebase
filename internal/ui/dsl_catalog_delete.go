package ui

// Удаление справочника из DSL — Справочники.X.Удалить(Ссылка) и
// Ссылка.Удалить() — идёт через entityservice.Delete, тем же путём, что UI,
// REST и Документы.X.Удалить(): хуки модуля объекта «ПередУдалением»/
// «ПослеУдаления», проверка ссылок (CheckRefs), снятие строк ТЧ и регистрация
// в планах обмена. Раньше CatalogProxy удалял прямым db.Delete — запрет,
// написанный в конфигурации, обходился сменой способа удаления (issue #854).

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// dslCatalogDeleter реализует interpreter.CatalogDeleter поверх entityservice.
type dslCatalogDeleter struct{ s *Server }

func (d dslCatalogDeleter) DeleteCatalogRef(ctx context.Context, entity *metadata.Entity, id uuid.UUID) error {
	// Pre-образ живого списка (план 87) — как в UI-удалении: строка читается
	// ДО удаления, чтобы увидевшие её пользователи убрали её из списка.
	var delBefore map[string]any
	if entity.NotifyChanges {
		delBefore, _ = d.s.store.GetByID(ctx, entity.Name, id, entity)
	}
	res, err := d.s.entityService().Delete(ctx, entity, id)
	if err != nil {
		return err
	}
	appendDSLMessages(ctx, res.DSLMessages)
	if res.DSLError != "" {
		// Хук отменил удаление или объект используется. Для вызывающего
		// DSL-кода это прикладная ошибка (ловится Попыткой), объект на месте.
		return errors.New(res.DSLError)
	}
	d.s.publishDocChange(ctx, entity, id, "удалён", delBefore)
	// Веб-хук <kind>.delete (план 29) — как в UI-обработчике физического
	// удаления; пометка на удаление обратима и событием не считается.
	d.s.dispatchDocWebhook(ctx, string(entity.Kind)+".delete", entity, id, nil)
	return nil
}
