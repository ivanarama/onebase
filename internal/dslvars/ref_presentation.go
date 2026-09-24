package dslvars

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// StoreRefPresenter — читатель подписей ссылочных констант для доверенных
// сред без собственной ролевой модели (регламентные задания, offline-прогоны):
// подпись строится той же metadata.RowLabel, что видит пользователь в списках.
// Удалённая или нечитаемая строка даёт пустую подпись — UUID, выданный за
// наименование, молча уезжал в письма и печатные формы (#1536). Средам с
// ролевой политикой (UI) нужен свой презентер: этот путь права не проверяет.
func StoreRefPresenter(store *storage.DB, reg *runtime.Registry) func(ctx context.Context, entityName, id string) string {
	return func(ctx context.Context, entityName, id string) string {
		if store == nil || reg == nil {
			return ""
		}
		entity := reg.GetEntity(strings.TrimSpace(entityName))
		if entity == nil {
			return ""
		}
		uid, err := uuid.Parse(strings.TrimSpace(id))
		if err != nil {
			return ""
		}
		fields := metadata.LabelFields(entity)
		if len(fields) == 0 {
			return ""
		}
		rows, err := store.GetFieldsByIDs(ctx, entity, []uuid.UUID{uid}, fields)
		if err != nil {
			return ""
		}
		row := rows[uid.String()]
		if row == nil {
			return ""
		}
		return metadata.RowLabel(row, entity)
	}
}
