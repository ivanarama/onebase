package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/fsmode"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
)

var ErrAttachmentTooLarge = errors.New("attachment exceeds maximum size")

// Attachment represents a file attached to a document or catalog record.
type Attachment struct {
	ID         uuid.UUID `json:"id"`
	OwnerKind  string    `json:"owner_kind"`
	OwnerName  string    `json:"owner_name"`
	OwnerID    uuid.UUID `json:"owner_id"`
	Filename   string    `json:"filename"`
	MimeType   string    `json:"mime_type"`
	SizeBytes  int64     `json:"size_bytes"`
	UploadedAt time.Time `json:"uploaded_at"`
	UploadedBy string    `json:"uploaded_by"`
	// Loc — где лежит содержимое: 'disk' (по умолчанию/легаси) или 's3'. Пусто в
	// строке = легаси до появления колонки → трактуется как disk. План 110, этап 2b.
	Loc string `json:"-"`
}

// SanitizeAttachmentName нормализует имя загружаемого файла, пришедшее из
// заголовка multipart-формы (header.Filename) — оно полностью контролируется
// клиентом и НЕ доверенное. Защищает от:
//   - подмены пути (../, абсолютные/Windows-пути) — берём filepath.Base + срез
//     по обоим разделителям, т.к. на Linux '\\' не считается разделителем;
//   - хранимого XSS и порчи UI — вырезаем управляющие символы (в т.ч. \r\n);
//   - DoS по длине — ограничиваем 255 байтами (граница имени файла в большинстве ФС).
//
// Экранирование при выводе всё равно делается на стороне рендера (DOM/textContent),
// эта функция — вторая линия защиты «на входе». Живёт в storage (единый источник),
// чтобы и UI-, и REST-путь загрузки нормализовали имя одинаково.
func SanitizeAttachmentName(name string) string {
	// Срезаем как posix-, так и windows-путь независимо от ОС сервера.
	name = filepath.Base(name)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	// Убираем управляющие символы (включая \r, \n, \t, NUL) и невалидный UTF-8.
	var b strings.Builder
	for _, r := range name {
		if r == utf8.RuneError || r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	name = strings.TrimSpace(b.String())
	// Имена-«пути» и спецзначения после очистки сводим к безопасному дефолту.
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	// Ограничение длины (байтовое — граница имени файла в типовых ФС).
	const maxLen = 255
	if len(name) > maxLen {
		name = name[:maxLen]
		// Не оставляем «обрезанный» хвост невалидного UTF-8.
		for len(name) > 0 && !utf8.ValidString(name) {
			name = name[:len(name)-1]
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return "file"
		}
	}
	return name
}

// AttachmentExtAllowed сообщает, разрешено ли расширение файла настройкой
// attachments.allowed_types из app.yaml. Пустой список = без ограничений
// (разрешено всё). Сравнение регистронезависимое; элементы списка могут быть
// с ведущей точкой или без (".pdf" и "pdf" эквивалентны). Файл без расширения
// при непустом списке считается недопустимым — тип нельзя подтвердить.
func AttachmentExtAllowed(allowed []string, filename string) bool {
	if len(allowed) == 0 {
		return true
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	if ext == "" {
		return false
	}
	for _, a := range allowed {
		if strings.ToLower(strings.TrimPrefix(strings.TrimSpace(a), ".")) == ext {
			return true
		}
	}
	return false
}

// EnsureAttachmentTable creates the _attachments table if it does not exist.
func (db *DB) EnsureAttachmentTable(ctx context.Context) error {
	d := db.dialect
	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS _attachments (
			id          %s PRIMARY KEY,
			owner_kind  TEXT NOT NULL,
			owner_name  TEXT NOT NULL,
			owner_id    %s NOT NULL,
			filename    TEXT NOT NULL,
			mime_type   TEXT NOT NULL DEFAULT '',
			size_bytes  BIGINT NOT NULL DEFAULT 0,
			uploaded_at %s NOT NULL DEFAULT %s,
			uploaded_by TEXT NOT NULL DEFAULT ''
		)`, d.TypeUUID(), d.TypeUUID(), d.TypeTimestamp(), d.CurrentTimestampTZ())
	if _, err := db.Exec(ctx, ddl); err != nil {
		return err
	}
	// Где лежит содержимое вложения: 'disk' | 's3'. Пусто = легаси (до колонки) →
	// disk. Новые строки пишут loc явно, поэтому смена режима не осиротит уже
	// загруженные вложения. План 110, этап 2b.
	if err := db.AddColumnIfMissing(ctx, "_attachments", "loc", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("attachments: loc: %w", err)
	}
	return nil
}

// ListAttachments returns all attachments for a given owner.
func (db *DB) ListAttachments(ctx context.Context, ownerKind, ownerName string, ownerID uuid.UUID) ([]Attachment, error) {
	d := db.dialect
	q := fmt.Sprintf(`
		SELECT id, owner_kind, owner_name, owner_id, filename, mime_type, size_bytes, uploaded_at, uploaded_by, loc
		FROM _attachments
		WHERE owner_kind=%s AND owner_name=%s AND owner_id=%s
		ORDER BY uploaded_at DESC
	`, d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
	rows, err := db.Query(ctx, q, ownerKind, ownerName, idArg(d, ownerID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Attachment
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *a)
	}
	return result, nil
}

// UploadAttachment stores a file (on disk by default, or in S3 when
// file_storage=s3) and records metadata. In s3 mode the content is staged to a
// temp file to bound memory, then uploaded; the row records loc='s3'.
func (db *DB) UploadAttachment(ctx context.Context, ownerKind, ownerName string, ownerID uuid.UUID, filename, mimeType, uploadedBy string, r io.Reader, maxSizeBytes int64) (Attachment, error) {
	d := db.dialect
	id := uuid.New()

	loc := FileStorageDisk
	var n int64
	var compensate func() // отмена внешней записи при сбое INSERT / откате транзакции

	if db.GetFileStorageMode(ctx) == FileStorageS3 {
		if db.blobStore == nil {
			return Attachment{}, fmt.Errorf("attachments: file_storage=s3, но клиент S3 не сконфигурирован (file_storage.s3)")
		}
		key := db.attachmentObjectKey(ownerName, id)
		cnt, err := db.stagePutAttachmentS3(ctx, key, mimeType, r, maxSizeBytes)
		if err != nil {
			return Attachment{}, err
		}
		n, loc = cnt, FileStorageS3
		compensate = func() { _ = db.blobStore.DeleteObject(context.Background(), key) }
	} else {
		dir := filepath.Join(db.filesDir, ownerName)
		if err := os.MkdirAll(dir, fsmode.Dir); err != nil {
			return Attachment{}, err
		}
		filePath := filepath.Join(dir, id.String())
		f, err := os.OpenFile(filePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return Attachment{}, err
		}
		cnt, cpErr := io.Copy(f, io.LimitReader(r, maxSizeBytes+1))
		if cpErr != nil {
			discardPartial(f, filePath)
			return Attachment{}, cpErr
		}
		if cnt > maxSizeBytes {
			discardPartial(f, filePath)
			return Attachment{}, attachmentTooLarge(maxSizeBytes)
		}
		if err := f.Sync(); err != nil {
			discardPartial(f, filePath)
			return Attachment{}, err
		}
		if err := f.Close(); err != nil {
			removeFile(filePath)
			return Attachment{}, err
		}
		n = cnt
		compensate = func() { _ = os.Remove(filePath) }
	}

	q := fmt.Sprintf(`
			INSERT INTO _attachments (id, owner_kind, owner_name, owner_id, filename, mime_type, size_bytes, uploaded_by, loc)
			VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s)
		`, d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4),
		d.Placeholder(5), d.Placeholder(6), d.Placeholder(7), d.Placeholder(8), d.Placeholder(9))
	if _, err := db.Exec(ctx, q,
		idArg(d, id), ownerKind, ownerName, idArg(d, ownerID), filename, mimeType, n, uploadedBy, loc,
	); err != nil {
		compensate()
		return Attachment{}, err
	}
	a, err := db.GetAttachment(ctx, id)
	if err != nil {
		compensate()
		return Attachment{}, err
	}
	// The metadata INSERT may belong to an explicit DSL transaction. If that
	// transaction (or the current savepoint) rolls back, remove the stored
	// content (disk file / S3 object) so it cannot become an orphan.
	DeferUntilTxRollback(ctx, compensate)
	return *a, nil
}

// attachmentTooLarge builds the localized "file too large" error.
func attachmentTooLarge(maxSizeBytes int64) error {
	return fmt.Errorf("%w: %s", ErrAttachmentTooLarge,
		i18nerr.Errorf("файл превышает максимальный размер %d МБ", maxSizeBytes/(1024*1024)))
}

// attachmentObjectKey builds the bucket key for an attachment:
// [<prefix>/]attachments/<owner>/<id>.
func (db *DB) attachmentObjectKey(ownerName string, id uuid.UUID) string {
	seg := "attachments/" + ownerName + "/" + id.String()
	if db.blobPrefix == "" {
		return seg
	}
	return db.blobPrefix + "/" + seg
}

// stagePutAttachmentS3 buffers r to a temp file (bounded memory), enforces the
// size limit, then uploads to S3 under key. Returns the byte count.
func (db *DB) stagePutAttachmentS3(ctx context.Context, key, mime string, r io.Reader, maxSizeBytes int64) (int64, error) {
	tmp, err := os.CreateTemp("", "onebase-att-*")
	if err != nil {
		return 0, err
	}
	defer func() {
		discardPartial(tmp, tmp.Name())
	}()
	n, err := io.Copy(tmp, io.LimitReader(r, maxSizeBytes+1))
	if err != nil {
		return 0, err
	}
	if n > maxSizeBytes {
		return 0, attachmentTooLarge(maxSizeBytes)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	if err := db.blobStore.PutObject(ctx, key, tmp, n, mime); err != nil {
		return 0, fmt.Errorf("attachments: s3 put: %w", err)
	}
	return n, nil
}

// GetAttachment returns attachment metadata by ID.
func (db *DB) GetAttachment(ctx context.Context, id uuid.UUID) (*Attachment, error) {
	d := db.dialect
	q := fmt.Sprintf(`
		SELECT id, owner_kind, owner_name, owner_id, filename, mime_type, size_bytes, uploaded_at, uploaded_by, loc
		FROM _attachments WHERE id=%s
	`, d.Placeholder(1))
	row := db.QueryRow(ctx, q, idArg(d, id))
	return scanAttachment(row)
}

// OpenAttachment returns a seekable reader over the attachment content plus its
// metadata. For disk it is the *os.File; for S3 the object is downloaded to a
// temp file (seekable, so http.ServeContent keeps working) whose Close removes
// it. The caller must Close the returned reader.
func (db *DB) OpenAttachment(ctx context.Context, id uuid.UUID) (io.ReadSeekCloser, *Attachment, error) {
	a, err := db.GetAttachment(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if a.Loc == FileStorageS3 {
		if db.blobStore == nil {
			return nil, nil, fmt.Errorf("attachments: вложение %s в S3, но клиент S3 не сконфигурирован (file_storage.s3)", a.ID)
		}
		// Потоковая раздача (file_storage.s3.stream): ленивый Range-ридер прямо из
		// S3, без временной копии. Размер известен из метаданных, поэтому
		// http.ServeContent измеряет длину и обрабатывает Range без лишних запросов.
		if db.blobStream {
			return db.blobStore.OpenReadSeeker(ctx, db.attachmentObjectKey(a.OwnerName, a.ID), a.SizeBytes), a, nil
		}
		dir, err := db.downloadAttachmentTemp(ctx, a)
		if err != nil {
			return nil, nil, err
		}
		f, err := os.Open(filepath.Join(dir, a.ID.String()))
		if err != nil {
			removeFile(dir)
			return nil, nil, err
		}
		return &tempDirFile{File: f, dir: dir}, a, nil
	}
	f, err := os.Open(filepath.Join(db.filesDir, a.OwnerName, id.String()))
	if err != nil {
		return nil, nil, err
	}
	return f, a, nil
}

// MaterializeAttachment returns a local filesystem path to the attachment
// content plus a cleanup func (nil for disk — the path is the real storage
// file). For S3 the object is downloaded into a temp dir whose file basename is
// the attachment id (so demo-mode sandbox checks can recover the id from the
// path); cleanup removes that temp dir. Used by DSL ПутьКВложению and the email
// attach-by-path resolver, which need a real path rather than a reader.
func (db *DB) MaterializeAttachment(ctx context.Context, id uuid.UUID) (string, func(), *Attachment, error) {
	a, err := db.GetAttachment(ctx, id)
	if err != nil {
		return "", nil, nil, err
	}
	if a.Loc == FileStorageS3 {
		dir, err := db.downloadAttachmentTemp(ctx, a)
		if err != nil {
			return "", nil, nil, err
		}
		return filepath.Join(dir, a.ID.String()), func() { removeFile(dir) }, a, nil
	}
	return filepath.Join(db.filesDir, a.OwnerName, id.String()), nil, a, nil
}

// downloadAttachmentTemp fetches an S3-backed attachment into a fresh temp dir
// (file named <id>) and returns that dir. The caller owns the dir (removes it).
func (db *DB) downloadAttachmentTemp(ctx context.Context, a *Attachment) (string, error) {
	if db.blobStore == nil {
		return "", fmt.Errorf("attachments: вложение %s в S3, но клиент S3 не сконфигурирован (file_storage.s3)", a.ID)
	}
	rc, _, err := db.blobStore.GetObject(ctx, db.attachmentObjectKey(a.OwnerName, a.ID))
	if err != nil {
		return "", err
	}
	defer closeRead("объект вложения в хранилище", rc)
	base := filepath.Join(db.filesDir, "_attach_tmp")
	if err := os.MkdirAll(base, fsmode.Dir); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(base, "att-")
	if err != nil {
		return "", err
	}
	f, err := os.Create(filepath.Join(dir, a.ID.String()))
	if err != nil {
		removeFile(dir)
		return "", err
	}
	if _, err := io.Copy(f, rc); err != nil {
		discardPartial(f, dir)
		return "", err
	}
	if err := f.Close(); err != nil {
		removeFile(dir)
		return "", err
	}
	return dir, nil
}

// SweepAttachmentTemp removes leftover temp materializations of S3 attachments
// (filesDir/_attach_tmp). Safe to call at startup: those files are ephemeral
// copies recreated on demand. It backstops the rare case where the per-request
// context.AfterFunc cleanup cannot fire (e.g. headless procrun with a
// non-cancellable context). Best-effort; errors are ignored.
func (db *DB) SweepAttachmentTemp() {
	_ = os.RemoveAll(filepath.Join(db.filesDir, "_attach_tmp"))
}

// tempDirFile is an io.ReadSeekCloser over a temp file; Close closes the handle
// and removes the enclosing temp dir.
type tempDirFile struct {
	*os.File
	dir string
}

func (t *tempDirFile) Close() error {
	err := t.File.Close()
	// Каталог — временная материализация вложения из S3. Его неудалённость не
	// повод подменять ошибку закрытия файла, но и терять её незачем.
	removeFile(t.dir)
	return err
}

// DeleteAttachment deletes metadata and removes the content (disk file / S3
// object). Inside a transaction the physical removal is delayed until the outer
// commit, so a rollback cannot leave a metadata row pointing to missing content.
func (db *DB) DeleteAttachment(ctx context.Context, id uuid.UUID) error {
	d := db.dialect
	a, err := db.GetAttachment(ctx, id)
	if err != nil {
		return err
	}
	var remove func()
	if a.Loc == FileStorageS3 {
		if db.blobStore == nil {
			return fmt.Errorf("attachments: вложение %s в S3, но клиент S3 не сконфигурирован — удаление невозможно", id)
		}
		key := db.attachmentObjectKey(a.OwnerName, id)
		remove = func() { _ = db.blobStore.DeleteObject(context.Background(), key) }
	} else {
		filePath := filepath.Join(db.filesDir, a.OwnerName, id.String())
		remove = func() { _ = os.Remove(filePath) }
	}
	q := fmt.Sprintf(`DELETE FROM _attachments WHERE id=%s`, d.Placeholder(1))
	if _, err = db.Exec(ctx, q, idArg(d, id)); err != nil {
		return err
	}
	if !DeferUntilTxCommit(ctx, remove) {
		remove()
	}
	return nil
}

// attachmentScanner is satisfied by both sql.Row and sql.Rows.
type attachmentScanner interface{ Scan(dest ...any) error }

func scanAttachment(row attachmentScanner) (*Attachment, error) {
	var idStr, ownerIDStr string
	var uploadedAtRaw any
	var a Attachment
	if err := row.Scan(&idStr, &a.OwnerKind, &a.OwnerName, &ownerIDStr, &a.Filename, &a.MimeType, &a.SizeBytes, &uploadedAtRaw, &a.UploadedBy, &a.Loc); err != nil {
		return nil, err
	}
	a.UploadedAt = parseAuditTime(uploadedAtRaw)
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, fmt.Errorf("attachment id: %w", err)
	}
	a.ID = id
	ownerID, err := uuid.Parse(ownerIDStr)
	if err != nil {
		return nil, fmt.Errorf("attachment owner_id: %w", err)
	}
	a.OwnerID = ownerID
	return &a, nil
}
