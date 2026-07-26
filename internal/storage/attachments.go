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
	_, err := db.Exec(ctx, ddl)
	return err
}

// ListAttachments returns all attachments for a given owner.
func (db *DB) ListAttachments(ctx context.Context, ownerKind, ownerName string, ownerID uuid.UUID) ([]Attachment, error) {
	d := db.dialect
	q := fmt.Sprintf(`
		SELECT id, owner_kind, owner_name, owner_id, filename, mime_type, size_bytes, uploaded_at, uploaded_by
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

// UploadAttachment saves a file to disk and records metadata in the database.
func (db *DB) UploadAttachment(ctx context.Context, ownerKind, ownerName string, ownerID uuid.UUID, filename, mimeType, uploadedBy string, r io.Reader, maxSizeBytes int64) (Attachment, error) {
	d := db.dialect
	id := uuid.New()
	dir := filepath.Join(db.filesDir, ownerName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Attachment{}, err
	}
	filePath := filepath.Join(dir, id.String())
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Attachment{}, err
	}
	defer f.Close()

	limited := io.LimitReader(r, maxSizeBytes+1)
	n, err := io.Copy(f, limited)
	if err != nil {
		os.Remove(filePath)
		return Attachment{}, err
	}
	if n > maxSizeBytes {
		_ = f.Close()
		os.Remove(filePath)
		return Attachment{}, fmt.Errorf("%w: %s", ErrAttachmentTooLarge,
			i18nerr.Errorf("файл превышает максимальный размер %d МБ", maxSizeBytes/(1024*1024)))
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(filePath)
		return Attachment{}, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(filePath)
		return Attachment{}, err
	}

	q := fmt.Sprintf(`
			INSERT INTO _attachments (id, owner_kind, owner_name, owner_id, filename, mime_type, size_bytes, uploaded_by)
			VALUES (%s,%s,%s,%s,%s,%s,%s,%s)
		`, d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4),
		d.Placeholder(5), d.Placeholder(6), d.Placeholder(7), d.Placeholder(8))
	if _, err := db.Exec(ctx, q,
		idArg(d, id), ownerKind, ownerName, idArg(d, ownerID), filename, mimeType, n, uploadedBy,
	); err != nil {
		os.Remove(filePath)
		return Attachment{}, err
	}
	a, err := db.GetAttachment(ctx, id)
	if err != nil {
		os.Remove(filePath)
		return Attachment{}, err
	}
	// The metadata INSERT may belong to an explicit DSL transaction. If that
	// transaction (or the current savepoint) rolls back, remove the physical
	// file as compensation so it cannot become an orphan.
	DeferUntilTxRollback(ctx, func() { _ = os.Remove(filePath) })
	return *a, nil
}

// GetAttachment returns attachment metadata by ID.
func (db *DB) GetAttachment(ctx context.Context, id uuid.UUID) (*Attachment, error) {
	d := db.dialect
	q := fmt.Sprintf(`
		SELECT id, owner_kind, owner_name, owner_id, filename, mime_type, size_bytes, uploaded_at, uploaded_by
		FROM _attachments WHERE id=%s
	`, d.Placeholder(1))
	row := db.QueryRow(ctx, q, idArg(d, id))
	return scanAttachment(row)
}

// OpenAttachment opens the file for a given attachment ID and returns its metadata.
func (db *DB) OpenAttachment(ctx context.Context, id uuid.UUID) (*os.File, *Attachment, error) {
	a, err := db.GetAttachment(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	filePath := filepath.Join(db.filesDir, a.OwnerName, id.String())
	f, err := os.Open(filePath)
	if err != nil {
		return nil, nil, err
	}
	return f, a, nil
}

// DeleteAttachment deletes metadata and removes the file from disk. Inside a
// transaction the physical removal is delayed until the outer commit, so a
// rollback cannot leave a restored metadata row pointing to a missing file.
func (db *DB) DeleteAttachment(ctx context.Context, id uuid.UUID) error {
	d := db.dialect
	a, err := db.GetAttachment(ctx, id)
	if err != nil {
		return err
	}
	filePath := filepath.Join(db.filesDir, a.OwnerName, id.String())
	q := fmt.Sprintf(`DELETE FROM _attachments WHERE id=%s`, d.Placeholder(1))
	if _, err = db.Exec(ctx, q, idArg(d, id)); err != nil {
		return err
	}
	removeFile := func() { _ = os.Remove(filePath) }
	if !DeferUntilTxCommit(ctx, removeFile) {
		removeFile()
	}
	return nil
}

// attachmentScanner is satisfied by both sql.Row and sql.Rows.
type attachmentScanner interface{ Scan(dest ...any) error }

func scanAttachment(row attachmentScanner) (*Attachment, error) {
	var idStr, ownerIDStr string
	var uploadedAtRaw any
	var a Attachment
	if err := row.Scan(&idStr, &a.OwnerKind, &a.OwnerName, &ownerIDStr, &a.Filename, &a.MimeType, &a.SizeBytes, &uploadedAtRaw, &a.UploadedBy); err != nil {
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
