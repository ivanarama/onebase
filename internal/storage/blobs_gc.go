package storage

// Сборка мусора бинарников (blobs). Блоб может быть «общим» — один UUID
// встречается в image-полях нескольких записей (ручное переиспользование ссылки,
// импорт). Поэтому наивное удаление по удалению одной записи небезопасно; вместо
// этого — mark-and-sweep: собираем ВСЕ живые ссылки изо всех image-полей всех
// сущностей и удаляем только те блобы, на которые не ссылается никто. Grace-окно
// защищает недавно загруженные блобы (могут быть ещё не привязаны к записи).

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// GCStats — результат прохода сборки мусора.
type GCStats struct {
	TotalBlobs int         // всего блобов в _blobs
	LiveRefs   int         // уникальных живых ссылок (из image-полей)
	Orphans    []uuid.UUID // блобы без ссылок и старше grace-окна (кандидаты/удалённые)
	Protected  int         // всего не тронутых блобов без ссылок (сумма причин ниже)
	Deleted    int         // фактически удалено (0 при dryRun)

	// Причины защиты разделены: grace-окно временное (блоб станет кандидатом,
	// когда состарится), legacy-время неизвестно, а dsl-managed — постоянное.
	// Одним числом каждая из них ошибочно выглядела как «моложе grace».
	ProtectedRecent int // без ссылок, но моложе grace-окна
	ProtectedLegacy int // created_at=0: возраст неизвестен, поэтому удалять небезопасно
	ProtectedDSL    int // созданы из DSL (СохранитьКартинку) — исключены из sweep всегда
}

// CollectImageRefs возвращает множество UUID, на которые ссылаются image-поля
// всех переданных сущностей (живые ссылки). image-поля бывают только у сущностей
// верхнего уровня (в табличных частях запрещены), поэтому достаточно перебрать
// e.Fields. Идентификаторы таблиц/колонок берём из metadata как и в crud.go.
//
// Действующая публикация (_public_files) — тоже живая ссылка. Без этого
// опубликованная картинка, заменённая потом в карточке, собиралась сборщиком, и
// публичная ссылка начинала отвечать 404 при живой строке публикации (#1001).
func (db *DB) CollectImageRefs(ctx context.Context, entities []*metadata.Entity) (map[uuid.UUID]bool, error) {
	live := map[uuid.UUID]bool{}
	if err := db.collectPublishedBlobRefs(ctx, live); err != nil {
		return nil, err
	}
	for _, e := range entities {
		table := metadata.TableName(e.Name)
		for _, f := range e.Fields {
			if !metadata.IsImage(f.Type) {
				continue
			}
			col := metadata.ColumnName(f)
			q := fmt.Sprintf(`SELECT DISTINCT %s FROM %s WHERE %s IS NOT NULL AND %s <> ''`, col, table, col, col)
			rows, err := db.Query(ctx, q)
			if err != nil {
				return nil, fmt.Errorf("gc: чтение ссылок %s.%s: %w", table, col, err)
			}
			for rows.Next() {
				var ref string
				if err := rows.Scan(&ref); err != nil {
					rows.Close()
					return nil, fmt.Errorf("gc: scan %s.%s: %w", table, col, err)
				}
				// Невалидные значения (не UUID) игнорируем — это не ссылка на блоб.
				if id, err := uuid.Parse(ref); err == nil {
					live[id] = true
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, err
			}
			rows.Close()
		}
	}
	return live, nil
}

// collectPublishedBlobRefs добавляет к живым ссылкам блобы с действующей
// публикацией. Истёкшие публикации не защищают: по такой ссылке /pub уже
// отвечает 404, а строку публикации уберёт DeleteBlob вместе с блобом.
//
// Ошибку чтения глотать нельзя: молча пустой результат означал бы «публикаций
// нет», и сборщик удалил бы опубликованные файлы. Отсутствие самой таблицы —
// другое дело: служебную схему заводит EnsureServiceSchema, и до баз, где её
// нет, публикации просто не доехали.
func (db *DB) collectPublishedBlobRefs(ctx context.Context, live map[uuid.UUID]bool) error {
	ok, err := db.TableExists(ctx, "_public_files")
	if err != nil {
		return fmt.Errorf("gc: проверка таблицы публикаций: %w", err)
	}
	if !ok {
		return nil
	}
	rows, err := db.Query(ctx, `SELECT blob_id, expires_at FROM _public_files WHERE blob_id IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("gc: чтение публикаций: %w", err)
	}
	defer rows.Close()
	now := time.Now()
	for rows.Next() {
		var ref string
		// Срок читаем в any: SQLite отдаёт время строкой, и скан сразу в
		// *time.Time на нём падает (та же причина, что в publicFileWhere).
		var expiresRaw any
		if err := rows.Scan(&ref, &expiresRaw); err != nil {
			return fmt.Errorf("gc: scan публикации: %w", err)
		}
		if expiresRaw != nil {
			if expires, ok := parseTimeValue(expiresRaw); ok && now.After(expires) {
				continue
			}
		}
		if id, err := uuid.Parse(ref); err == nil {
			live[id] = true
		}
	}
	return rows.Err()
}

// blobRef — минимальные метаданные блоба для сборки мусора.
type blobRef struct {
	id         uuid.UUID
	createdAt  int64 // unix-секунды; 0 = легаси/неизвестное время → защищён
	dslManaged bool  // создан из DSL (СохранитьКартинку) → исключён из sweep (ревью #11)
}

// listBlobsForGC возвращает все блобы с временем создания и признаком DSL-managed.
func (db *DB) listBlobsForGC(ctx context.Context) ([]blobRef, error) {
	rows, err := db.Query(ctx, `SELECT id, created_at, dsl_managed FROM _blobs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []blobRef
	for rows.Next() {
		var idStr string
		var createdAt int64
		var dslManaged int64
		if err := rows.Scan(&idStr, &createdAt, &dslManaged); err != nil {
			return nil, err
		}
		id, err := uuid.Parse(idStr)
		if err != nil {
			continue // некорректный id в _blobs пропускаем
		}
		out = append(out, blobRef{id: id, createdAt: createdAt, dslManaged: dslManaged != 0})
	}
	return out, rows.Err()
}

// SweepOrphanBlobs выполняет mark-and-sweep: удаляет блобы, на которые не
// ссылается ни одно image-поле сущностей entities и которые старше grace-окна.
// При dryRun ничего не удаляет, только заполняет статистику (Orphans/Protected).
func (db *DB) SweepOrphanBlobs(ctx context.Context, entities []*metadata.Entity, grace time.Duration, dryRun bool) (GCStats, error) {
	live, err := db.CollectImageRefs(ctx, entities)
	if err != nil {
		return GCStats{}, err
	}
	all, err := db.listBlobsForGC(ctx)
	if err != nil {
		return GCStats{}, err
	}
	cutoff := time.Now().Add(-grace).Unix()

	st := GCStats{TotalBlobs: len(all), LiveRefs: len(live)}
	for _, b := range all {
		if live[b.id] {
			continue
		}
		// DSL-managed блобы (СохранитьКартинку, owner-less) исключаем из sweep: их
		// UUID мог попасть в строковое поле/константу/реквизит инфорегистра, которые
		// CollectImageRefs не сканирует, поэтому отсутствие image-ссылки ≠ сирота
		// (ревью #11).
		if b.dslManaged {
			st.Protected++
			st.ProtectedDSL++
			continue
		}
		// created_at=0 — это легаси/неизвестное время создания. Считать такой blob
		// «старым» нельзя, но и называть его моложе grace-окна неверно (особенно при
		// --min-age=0), поэтому причина защиты учитывается отдельно.
		if b.createdAt == 0 {
			st.Protected++
			st.ProtectedLegacy++
			continue
		}
		// Недавно созданные блобы (created_at строго новее cutoff) могут быть ещё
		// не привязаны к записи, поэтому их временно защищает grace-окно.
		if b.createdAt > cutoff {
			st.Protected++
			st.ProtectedRecent++
			continue
		}
		st.Orphans = append(st.Orphans, b.id)
		if !dryRun {
			if err := db.DeleteBlob(ctx, b.id); err != nil {
				return st, fmt.Errorf("gc: удаление %s: %w", b.id, err)
			}
			st.Deleted++
		}
	}
	return st, nil
}
