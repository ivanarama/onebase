package storage

// Публикация вложений наружу (план 127).
//
// Вложения отдаются только пользователю с правом чтения владельца — правильно
// для админки, но означает, что показать файл анониму нельзя вовсе (картинка
// на сайте, ссылка на счёт для контрагента без учётной записи).
//
// Публичность выражена не флагом у вложения, а ОТДЕЛЬНОЙ записью с
// непредсказуемым токеном. Флаг + адрес вида /pub/<uuid вложения> означал бы:
// угадал (или подсмотрел в логах, в REST-ответе, в HTML админки) UUID —
// получил файл, причём навсегда. Токен же отзывается одним удалением строки.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// PublicFile — опубликованное вложение.
type PublicFile struct {
	Token        string
	AttachmentID uuid.UUID
	Filename     string
	CacheSeconds int
	ExpiresAt    *time.Time
	CreatedAt    time.Time
	CreatedBy    string
}

// PublishOptions — параметры публикации. Нулевое значение допустимо.
type PublishOptions struct {
	// Filename — имя файла при скачивании; пусто = имя вложения.
	Filename string
	// CacheSeconds — max-age в Cache-Control; 0 = defaultPublicCacheSeconds.
	CacheSeconds int
	// ExpiresAt — момент, после которого ссылка перестаёт работать; nil =
	// бессрочно.
	ExpiresAt *time.Time
}

const defaultPublicCacheSeconds = 3600

// EnsurePublicFilesSchema создаёт таблицу публикаций.
//
// Внешний ключ с каскадом обязателен: без него ссылка пережила бы удалённое
// вложение и отдавала 500 вместо 404.
func (db *DB) EnsurePublicFilesSchema(ctx context.Context) error {
	d := db.dialect
	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS _public_files (
			token         TEXT PRIMARY KEY,
			attachment_id %s NOT NULL REFERENCES _attachments(id) ON DELETE CASCADE,
			filename      TEXT NOT NULL DEFAULT '',
			cache_seconds INTEGER NOT NULL DEFAULT %d,
			expires_at    %s NULL,
			created_at    %s NOT NULL DEFAULT %s,
			created_by    TEXT NOT NULL DEFAULT ''
		)`, d.TypeUUID(), defaultPublicCacheSeconds, d.TypeTimestamp(), d.TypeTimestamp(), d.CurrentTimestampTZ())
	if _, err := db.Exec(ctx, ddl); err != nil {
		return err
	}
	// Уникальность по вложению делает публикацию идемпотентной: повторный вызов
	// в цикле рендера не плодит токены.
	_, err := db.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS _public_files_att ON _public_files(attachment_id)`)
	return err
}

// newPublicToken выдаёт 32 случайных байта в base64url — знание токена и есть
// право на файл, поэтому предсказуемость недопустима.
func newPublicToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("генерация токена публикации: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// PublishAttachment публикует вложение и возвращает токен. Повторный вызов для
// того же вложения возвращает существующий токен, обновляя параметры.
func (db *DB) PublishAttachment(ctx context.Context, attID uuid.UUID, opts PublishOptions) (string, error) {
	d := db.dialect
	if existing, err := db.PublicFileByAttachment(ctx, attID); err != nil {
		return "", err
	} else if existing != nil {
		// Обновляем ТОЛЬКО переданные поля: повторная публикация без опций —
		// обычное дело в цикле рендера, и она не должна сбрасывать настройки,
		// заданные при первой публикации.
		if opts.Filename == "" {
			opts.Filename = existing.Filename
		}
		if opts.CacheSeconds <= 0 {
			opts.CacheSeconds = existing.CacheSeconds
		}
		if opts.ExpiresAt == nil {
			opts.ExpiresAt = existing.ExpiresAt
		}
		q := fmt.Sprintf(`UPDATE _public_files SET filename=%s, cache_seconds=%s, expires_at=%s WHERE token=%s`,
			d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4))
		if _, err := db.Exec(ctx, q, opts.Filename, opts.CacheSeconds, opts.ExpiresAt, existing.Token); err != nil {
			return "", err
		}
		db.logPublicFileAudit(ctx, "publish", attID)
		return existing.Token, nil
	}
	if opts.CacheSeconds <= 0 {
		opts.CacheSeconds = defaultPublicCacheSeconds
	}

	token, err := newPublicToken()
	if err != nil {
		return "", err
	}
	createdBy := AuditUserLogin(ctx)
	q := fmt.Sprintf(`INSERT INTO _public_files (token, attachment_id, filename, cache_seconds, expires_at, created_at, created_by)
		VALUES (%s, %s, %s, %s, %s, %s, %s)`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5), d.Placeholder(6), d.Placeholder(7))
	if _, err := db.Exec(ctx, q, token, idArg(d, attID), opts.Filename, opts.CacheSeconds, opts.ExpiresAt, time.Now().UTC(), createdBy); err != nil {
		return "", err
	}
	// «Файл стал доступен всему интернету» обязано иметь автора и время.
	db.logPublicFileAudit(ctx, "publish", attID)
	return token, nil
}

// UnpublishAttachment отзывает публикацию. Отсутствие публикации — не ошибка.
func (db *DB) UnpublishAttachment(ctx context.Context, attID uuid.UUID) error {
	d := db.dialect
	q := fmt.Sprintf(`DELETE FROM _public_files WHERE attachment_id=%s`, d.Placeholder(1))
	if _, err := db.Exec(ctx, q, idArg(d, attID)); err != nil {
		return err
	}
	db.logPublicFileAudit(ctx, "unpublish", attID)
	return nil
}

// PublicFileByToken находит публикацию по токену из URL.
func (db *DB) PublicFileByToken(ctx context.Context, token string) (*PublicFile, error) {
	return db.publicFileWhere(ctx, "token", token)
}

// PublicFileByAttachment находит публикацию вложения (nil, если не публиковалось).
func (db *DB) PublicFileByAttachment(ctx context.Context, attID uuid.UUID) (*PublicFile, error) {
	return db.publicFileWhere(ctx, "attachment_id", idArg(db.dialect, attID))
}

func (db *DB) publicFileWhere(ctx context.Context, column string, value any) (*PublicFile, error) {
	d := db.dialect
	q := fmt.Sprintf(`SELECT token, attachment_id, filename, cache_seconds, expires_at, created_at, created_by
		FROM _public_files WHERE %s=%s`, column, d.Placeholder(1))
	row := db.QueryRow(ctx, q, value)

	// Идентификатор сканируется прямо в uuid.UUID: его Scan понимает и текст
	// (SQLite), и сырые 16 байт (PostgreSQL). Промежуточное any здесь было
	// ошибкой — parseUUIDValue разбирает 16-байтовый массив PG как текст и не
	// находит там UUID.
	//
	// Даты, наоборот, читаем в any: SQLite отдаёт их строкой, и скан сразу в
	// *time.Time на нём падает.
	var (
		pf         PublicFile
		expiresRaw any
		createdRaw any
	)
	err := row.Scan(&pf.Token, &pf.AttachmentID, &pf.Filename, &pf.CacheSeconds, &expiresRaw, &createdRaw, &pf.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if t, ok := parseTimeValue(createdRaw); ok {
		pf.CreatedAt = t
	}
	if expiresRaw != nil {
		if t, ok := parseTimeValue(expiresRaw); ok {
			pf.ExpiresAt = &t
		}
	}
	return &pf, nil
}

// Expired сообщает, истёк ли срок публикации.
func (pf *PublicFile) Expired(now time.Time) bool {
	return pf.ExpiresAt != nil && now.After(*pf.ExpiresAt)
}

// logPublicFileAudit пишет действие в журнал аудита: «файл стал доступен всему
// интернету» обязано иметь автора и время.
//
// Сам токен в журнал НЕ попадает — журнал читают шире, чем сам файл, и запись
// превратилась бы во второй канал доступа к содержимому.
func (db *DB) logPublicFileAudit(ctx context.Context, action string, attID uuid.UUID) {
	u, _ := auditUserFromCtx(ctx)
	db.LogAction(ctx, action, "attachment", "_public_files", attID.String(), u.UserID, u.UserLogin, "")
}
