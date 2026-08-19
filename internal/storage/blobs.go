package storage

// Хранилище бинарников (картинки полей типа image). Поддерживает два режима,
// как хранилища файлов в 1С: «тома на диске» (FileStorageDisk, по умолчанию) и
// «в информационной базе» (FileStorageDB, BLOB-колонка). Поле image в таблице
// сущности хранит только ссылку — UUID бинарника; раздаётся отдельным HTTP-
// обработчиком.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/fsmode"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
)

// Blob — метаданные бинарника. Содержимое лежит либо на диске
// (filesDir/_blobs/<id>), либо в колонке data таблицы _blobs.
type Blob struct {
	ID   uuid.UUID
	Mime string
	Size int64
	// Владелец — сущность, для которой загружен бинарник (kind+имя). Нужен
	// авторизации отдачи (imageServe проверяет право чтения на владельца).
	// Пустые значения = легаси-блоб или legacy-вызов DSL СохранитьКартинку без
	// ссылки-владельца — отдача таким требует лишь аутентификации.
	OwnerKind   string
	OwnerEntity string
}

// BlobOwner идентифицирует сущность-владельца бинарника для авторизации отдачи.
// Нулевое значение (пустые поля) означает «без владельца».
type BlobOwner struct {
	Kind   string // "catalog"|"document"|... (как в auth.User.Has)
	Entity string // имя сущности
	// DSLManaged помечает блоб, созданный legacy-вызовом DSL СохранитьКартинку без
	// владельца. UUID мог быть сохранён прикладным кодом в строковое поле,
	// константу или реквизит инфорегистра, которые сборщик мусора НЕ сканирует
	// (он смотрит только image-поля сущностей). Такие блобы исключаются из sweep,
	// чтобы Gc не удалил используемую картинку (ревью #11).
	DSLManaged bool
}

// blobsDirName — подкаталог filesDir для дискового режима хранения.
const blobsDirName = "_blobs"

// External storage is outside the database transaction. Rollback compensation
// must not hang transaction completion indefinitely when S3 is unavailable.
const blobExternalCleanupTimeout = 10 * time.Second

var ErrBlobTooLarge = errors.New("blob exceeds maximum size")

// EnsureBlobTable создаёт таблицу _blobs (метаданные + данные для db-режима).
// Колонка data заполняется только в режиме FileStorageDB; на диске она NULL.
func (db *DB) EnsureBlobTable(ctx context.Context) error {
	d := db.dialect
	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS _blobs (
			id   %s PRIMARY KEY,
			mime TEXT NOT NULL DEFAULT '',
			size BIGINT NOT NULL DEFAULT 0,
			data %s
		)`, d.TypeUUID(), d.TypeBytes())
	if _, err := db.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("blobs: create _blobs: %w", err)
	}
	// Владелец бинарника (сущность) — для авторизации отдачи (IDOR). Добавляем
	// через ALTER для уже существующих баз; пустые значения = легаси/без владельца.
	if err := db.AddColumnIfMissing(ctx, "_blobs", "owner_kind", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("blobs: owner_kind: %w", err)
	}
	if err := db.AddColumnIfMissing(ctx, "_blobs", "owner_entity", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("blobs: owner_entity: %w", err)
	}
	// Время создания (unix-секунды) — для grace-окна сборки мусора: GC не трогает
	// недавно загруженные блобы (могут быть ещё не привязаны к записи). 0 = легаси
	// (создан до появления колонки) → теперь трактуется КОНСЕРВАТИВНО как защищённый
	// (см. SweepOrphanBlobs, ревью #18), чтобы не удалить блоб с неизвестным временем.
	if err := db.AddColumnIfMissing(ctx, "_blobs", "created_at", "BIGINT NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("blobs: created_at: %w", err)
	}
	// Признак «создан из DSL» (СохранитьКартинку, без владельца). Такие блобы
	// исключаются из sweep — их UUID мог быть сохранён в строковое поле/константу/
	// реквизит инфорегистра, которые GC не сканирует (ревью #11). Легаси-блобы без
	// колонки получают 0 (НЕ managed) — это безопасно: они либо живы по image-ссылке,
	// либо защищены grace/created_at=0.
	if err := db.AddColumnIfMissing(ctx, "_blobs", "dsl_managed", "BIGINT NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("blobs: dsl_managed: %w", err)
	}
	// Где лежит содержимое блоба: 'disk' | 'db' | 's3'. Пусто = легаси-строка,
	// созданная до появления колонки: тогда источник определяется по наличию
	// данных в колонке data (непусто → db-байты, иначе → файл на диске). Новые
	// строки всегда пишут loc явно, поэтому смена режима хранения не осиротит
	// уже записанные блобы (каждый знает своё место). План 110, этап 2.
	if err := db.AddColumnIfMissing(ctx, "_blobs", "loc", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("blobs: loc: %w", err)
	}
	return nil
}

// PutBlob сохраняет бинарник и возвращает его метаданные с новым ID. Режим
// (disk|db) берётся из ui.file_storage. Размер ограничен maxSizeBytes
// (<=0 → 50 МБ по умолчанию).
func (db *DB) PutBlob(ctx context.Context, mime string, r io.Reader, maxSizeBytes int64, owner BlobOwner) (Blob, error) {
	if maxSizeBytes <= 0 {
		maxSizeBytes = 50 * 1024 * 1024
	}
	id := uuid.New()
	d := db.dialect
	createdAt := time.Now().Unix()
	// dsl_managed хранится как 0/1 (BIGINT) — единый тип на SQLite и PostgreSQL.
	dslManaged := int64(0)
	if owner.DSLManaged {
		dslManaged = 1
	}
	limited := io.LimitReader(r, maxSizeBytes+1)
	tooLarge := func() error {
		return fmt.Errorf("%w: %s", ErrBlobTooLarge,
			i18nerr.Errorf("файл превышает максимальный размер %d МБ", maxSizeBytes/(1024*1024)))
	}
	// insertMeta пишет строку _blobs без содержимого (disk/s3): байты лежат вне БД.
	insertMeta := func(size int64, loc string) error {
		q := fmt.Sprintf(`INSERT INTO _blobs (id, mime, size, owner_kind, owner_entity, created_at, dsl_managed, loc) VALUES (%s,%s,%s,%s,%s,%s,%s,%s)`,
			d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5), d.Placeholder(6), d.Placeholder(7), d.Placeholder(8))
		_, err := db.Exec(ctx, q, id.String(), mime, size, owner.Kind, owner.Entity, createdAt, dslManaged, loc)
		return err
	}
	result := func(size int64) Blob {
		return Blob{ID: id, Mime: mime, Size: size, OwnerKind: owner.Kind, OwnerEntity: owner.Entity}
	}

	switch db.GetFileStorageMode(ctx) {
	case FileStorageDB:
		data, err := io.ReadAll(limited)
		if err != nil {
			return Blob{}, err
		}
		if int64(len(data)) > maxSizeBytes {
			return Blob{}, tooLarge()
		}
		q := fmt.Sprintf(`INSERT INTO _blobs (id, mime, size, data, owner_kind, owner_entity, created_at, dsl_managed, loc) VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s)`,
			d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5), d.Placeholder(6), d.Placeholder(7), d.Placeholder(8), d.Placeholder(9))
		if _, err := db.Exec(ctx, q, id.String(), mime, int64(len(data)), data, owner.Kind, owner.Entity, createdAt, dslManaged, FileStorageDB); err != nil {
			return Blob{}, err
		}
		return result(int64(len(data))), nil

	case FileStorageS3:
		if db.blobStore == nil {
			return Blob{}, fmt.Errorf("blobs: file_storage=s3, но клиент S3 не сконфигурирован (file_storage.s3)")
		}
		// Как и в db-режиме, буферизуем в память (ограничено maxSizeBytes) —
		// нужен размер для Content-Length одиночного PUT.
		data, err := io.ReadAll(limited)
		if err != nil {
			return Blob{}, err
		}
		if int64(len(data)) > maxSizeBytes {
			return Blob{}, tooLarge()
		}
		blobStore := db.blobStore // rollback hook may run after runtime reconfiguration
		key := db.blobObjectKey(id)
		if err := blobStore.PutObject(ctx, key, bytes.NewReader(data), int64(len(data)), mime); err != nil {
			return Blob{}, fmt.Errorf("blobs: s3 put: %w", err)
		}
		compensate := func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), blobExternalCleanupTimeout)
			defer cancel()
			if err := blobStore.DeleteObject(cleanupCtx, key); err != nil {
				// The database outcome is already final, so this cannot be returned to
				// the caller. Keep it observable: an external orphan may require
				// operator cleanup/reconciliation.
				storageLog().Warn("не удалось удалить S3-блоб при компенсации записи/отката",
					"key", key, "err", err)
			}
		}
		if err := insertMeta(int64(len(data)), FileStorageS3); err != nil {
			// Компенсируем: объект уже залит, а строки нет — убираем объект.
			compensate()
			return Blob{}, err
		}
		// Метаданные участвуют в транзакции, а S3-объект — нет. При обычном откате
		// транзакции/сейвпоинта пытаемся удалить внешнее содержимое. Это компенсация,
		// а не строгая атомарность: crash или недоступный S3 могут оставить orphan.
		DeferUntilTxRollback(ctx, compensate)
		return result(int64(len(data))), nil

	default: // FileStorageDisk — файл на диске, в _blobs только метаданные.
		dir := filepath.Join(db.filesDir, blobsDirName)
		if err := os.MkdirAll(dir, fsmode.Dir); err != nil {
			return Blob{}, err
		}
		fp := filepath.Join(dir, id.String())
		f, err := os.Create(fp)
		if err != nil {
			return Blob{}, err
		}
		n, err := io.Copy(f, limited)
		if err != nil {
			discardPartial(f, fp)
			return Blob{}, err
		}
		if n > maxSizeBytes {
			discardPartial(f, fp)
			return Blob{}, tooLarge()
		}
		// Close на успешном пути — не уборка: он сбрасывает буфер, и его сбой
		// означает усечённый файл. Раньше ошибка терялась, и insertMeta ниже
		// регистрировала огрызок как полноценный блоб.
		if err := f.Close(); err != nil {
			removeFile(fp)
			return Blob{}, err
		}
		if err := insertMeta(n, FileStorageDisk); err != nil {
			removeFile(fp)
			return Blob{}, err
		}
		// Как и S3, файл находится вне транзакции БД. При обработанном rollback hook
		// компенсирует запись файла; crash между записью и hook остаётся внешним окном.
		DeferUntilTxRollback(ctx, func() { removeFile(fp) })
		return result(n), nil
	}
}

// OpenBlob возвращает метаданные и читателя содержимого бинарника. Вызывающий
// обязан закрыть rc. Источник (БД/диск) определяется наличием данных в колонке.
func (db *DB) OpenBlob(ctx context.Context, id uuid.UUID) (Blob, io.ReadCloser, error) {
	d := db.dialect
	var mime string
	var size int64
	var data []byte
	var dataNull bool
	var ownerKind, ownerEntity, loc string
	err := db.QueryRow(ctx,
		fmt.Sprintf(`SELECT mime, size, data, data IS NULL, owner_kind, owner_entity, loc FROM _blobs WHERE id=%s`, d.Placeholder(1)),
		id.String()).Scan(&mime, &size, &data, &dataNull, &ownerKind, &ownerEntity, &loc)
	if err != nil {
		return Blob{}, nil, err
	}
	b := Blob{ID: id, Mime: mime, Size: size, OwnerKind: ownerKind, OwnerEntity: ownerEntity}
	if size < 0 {
		return Blob{}, nil, fmt.Errorf("blobs: blob %s has negative size %d", id, size)
	}
	if loc == FileStorageS3 {
		if db.blobStore == nil {
			return Blob{}, nil, fmt.Errorf("blobs: блоб %s в S3, но клиент S3 не сконфигурирован", id)
		}
		rc, objectSize, err := db.blobStore.GetObject(ctx, db.blobObjectKey(id))
		if err != nil {
			return Blob{}, nil, err
		}
		if objectSize >= 0 && objectSize != size {
			_ = rc.Close()
			return Blob{}, nil, fmt.Errorf("blobs: S3 blob %s size mismatch: metadata=%d object=%d", id, size, objectSize)
		}
		return b, rc, nil
	}
	// An explicit db location is authoritative even for a zero-byte blob. A
	// NULL value is corruption, not a signal to fall back to an unrelated disk
	// file. Legacy rows (empty loc) use data presence to choose their backend.
	if loc == FileStorageDB {
		if dataNull {
			return Blob{}, nil, fmt.Errorf("blobs: blob %s has loc=db but NULL data", id)
		}
		if int64(len(data)) != size {
			return Blob{}, nil, fmt.Errorf("blobs: blob %s size mismatch: metadata=%d data=%d", id, size, len(data))
		}
		return b, io.NopCloser(bytes.NewReader(data)), nil
	}
	if loc == "" && !dataNull {
		if int64(len(data)) != size {
			return Blob{}, nil, fmt.Errorf("blobs: legacy blob %s size mismatch: metadata=%d data=%d", id, size, len(data))
		}
		return b, io.NopCloser(bytes.NewReader(data)), nil
	}
	if loc != "" && loc != FileStorageDisk {
		return Blob{}, nil, fmt.Errorf("blobs: blob %s has unsupported location %q", id, loc)
	}
	// disk mode, or a legacy row with NULL data, is backed by a file.
	f, err := os.Open(filepath.Join(db.filesDir, blobsDirName, id.String()))
	if err != nil {
		return Blob{}, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return Blob{}, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != size {
		_ = f.Close()
		return Blob{}, nil, fmt.Errorf("blobs: disk blob %s size mismatch: metadata=%d file=%d", id, size, info.Size())
	}
	return b, f, nil
}

// DeleteBlob удаляет содержимое бинарника (файл на диске / объект в S3, если
// есть) и строку метаданных. Отсутствующий блоб — не ошибка (идемпотентно, важно
// для сборки мусора). Для s3-блоба сначала удаляем объект; если это не удалось —
// строку НЕ трогаем (БД остаётся источником правды, удаление можно повторить).
func (db *DB) DeleteBlob(ctx context.Context, id uuid.UUID) error {
	d := db.dialect
	var loc string
	_ = db.QueryRow(ctx,
		fmt.Sprintf(`SELECT loc FROM _blobs WHERE id=%s`, d.Placeholder(1)), id.String()).Scan(&loc)
	if loc == FileStorageS3 {
		if db.blobStore == nil {
			return fmt.Errorf("blobs: блоб %s в S3, но клиент S3 не сконфигурирован — удаление невозможно", id)
		}
		if err := db.blobStore.DeleteObject(ctx, db.blobObjectKey(id)); err != nil {
			return fmt.Errorf("blobs: s3 delete: %w", err)
		}
	} else {
		// disk / db / легаси: файла на диске может не быть (db-режим) — это не ошибка.
		removeFile(filepath.Join(db.filesDir, blobsDirName, id.String()))
	}
	if _, err := db.Exec(ctx,
		fmt.Sprintf(`DELETE FROM _blobs WHERE id=%s`, d.Placeholder(1)), id.String()); err != nil {
		return err
	}
	// Публикация блоба каскадом не убирается: внешнего ключа у blob_id нет
	// осознанно (у блобов своя жизнь). Убираем строку сами — иначе публикация
	// исчезнувшего файла висела бы в _public_files вечно, отдавая 404 (#1001).
	return db.deletePublicFileByBlob(ctx, id)
}
