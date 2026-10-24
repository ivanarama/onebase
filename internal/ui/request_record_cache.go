package ui

import (
	"context"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Кэш записи на время одного запроса формы. Событие формы читает строку
// шапки несколько раз: восстановление неприсланных полей до обработчика,
// перечитывание после него, расчёт «изменена ли форма»; у широкой записи
// (заявка колл-центра) каждое чтение — десятки мс. Пока этот запрос ничего не
// записал (storage.WriteCounter), строка в базе та же, и повторное чтение
// отдаёт сохранённую копию; любая запись запроса кэш обесценивает.

type requestRecordCacheKey struct{}

type requestRecordCache struct {
	writes *storage.WriteCounter
	mu     sync.Mutex
	rows   map[string]cachedRecord
}

type cachedRecord struct {
	row map[string]any
	at  uint64
}

func withRequestRecordCache(ctx context.Context, writes *storage.WriteCounter) context.Context {
	if writes == nil {
		return ctx
	}
	return context.WithValue(ctx, requestRecordCacheKey{}, &requestRecordCache{writes: writes, rows: map[string]cachedRecord{}})
}

// recordForRequest — GetByID с кэшем запроса. Вызывающий получает свою копию
// карты: маскирование и нормализация меняют её на месте.
func (s *Server) recordForRequest(ctx context.Context, entity *metadata.Entity, id uuid.UUID) (map[string]any, error) {
	c, _ := ctx.Value(requestRecordCacheKey{}).(*requestRecordCache)
	if c == nil {
		return s.store.GetByID(ctx, entity.Name, id, entity)
	}
	key := entity.Name + "/" + id.String()
	at := c.writes.Load()
	c.mu.Lock()
	hit, ok := c.rows[key]
	c.mu.Unlock()
	if ok && hit.at == at {
		serverTimingCount(ctx, "recordHit")
		return copyRecord(hit.row), nil
	}
	if ok {
		// Запрос успел записать между чтениями — кэш обесценен.
		serverTimingCount(ctx, "recordStale")
		serverTimingNote(ctx, "recordStaleWrites", strings.Join(c.writes.Sample(), " | "))
	}
	endRead := serverTimingSpan(ctx, "recordRead")
	row, err := s.store.GetByID(ctx, entity.Name, id, entity)
	endRead()
	if err != nil || row == nil {
		return row, err
	}
	c.mu.Lock()
	c.rows[key] = cachedRecord{row: copyRecord(row), at: at}
	c.mu.Unlock()
	return row, nil
}

func copyRecord(row map[string]any) map[string]any {
	if row == nil {
		return nil
	}
	out := make(map[string]any, len(row))
	for k, v := range row {
		out[k] = v
	}
	return out
}
