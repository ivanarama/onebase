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

// PublicFile — опубликованный файл. Источник ровно один: либо вложение
// (план 22), либо блоб — содержимое поля типа image (план 65).
//
// Два источника здесь не от хорошей жизни: у пользователя «файл» один, картинка
// новости лежит в поле карточки, счёт — во вкладке вложений, и делить публичные
// ссылки по внутреннему устройству хранилища значило бы протащить реализацию
// наружу.
type PublicFile struct {
	Token string
	// AttachmentID заполнен для вложения, BlobID — для картинки; ровно одно.
	AttachmentID uuid.UUID
	BlobID       uuid.UUID
	Filename     string
	CacheSeconds int
	ExpiresAt    *time.Time
	CreatedAt    time.Time
	CreatedBy    string
}

// IsBlob сообщает, что источник — блоб (поле image), а не вложение.
func (pf *PublicFile) IsBlob() bool { return pf != nil && pf.BlobID != uuid.Nil }

// PublishOptions — параметры публикации. Нулевое значение допустимо.
type PublishOptions struct {
	// Filename — имя файла при скачивании; пусто = имя вложения.
	Filename string
	// CacheSeconds сохранён для совместимости с публикациями до v0.10.2.
	// Отзываемые ссылки всегда требуют ревалидации, поэтому HTTP max-age равен 0.
	// 0 при записи по-прежнему заменяется на defaultPublicCacheSeconds, чтобы не
	// ломать схему и повторную публикацию старых баз.
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
	// attachment_id и blob_id оба nullable: заполнен ровно один. Каскад на
	// вложения оставлен — без него ссылка пережила бы удалённый файл и отдавала
	// 500 вместо 404. У блобов своя жизнь и каскада нет, поэтому исчезнувший
	// блоб обрабатывается отдачей как 404.
	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS _public_files (
			token         TEXT PRIMARY KEY,
			attachment_id %s NULL REFERENCES _attachments(id) ON DELETE CASCADE,
			blob_id       %s NULL,
			filename      TEXT NOT NULL DEFAULT '',
			cache_seconds INTEGER NOT NULL DEFAULT %d,
			expires_at    %s NULL,
			created_at    %s NOT NULL DEFAULT %s,
			created_by    TEXT NOT NULL DEFAULT ''
		)`, d.TypeUUID(), d.TypeUUID(), defaultPublicCacheSeconds, d.TypeTimestamp(), d.TypeTimestamp(), d.CurrentTimestampTZ())
	if _, err := db.Exec(ctx, ddl); err != nil {
		return err
	}
	// Уникальность по источнику делает публикацию идемпотентной: повторный
	// вызов в цикле рендера не плодит токены.
	if _, err := db.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS _public_files_att ON _public_files(attachment_id)`); err != nil {
		return err
	}
	_, err := db.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS _public_files_blob ON _public_files(blob_id)`)
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

// PublishAttachment публикует вложение и возвращает токен.
func (db *DB) PublishAttachment(ctx context.Context, attID uuid.UUID, opts PublishOptions) (string, error) {
	return db.publish(ctx, "attachment_id", attID, opts)
}

// PublishBlob публикует картинку из поля image и возвращает токен.
func (db *DB) PublishBlob(ctx context.Context, blobID uuid.UUID, opts PublishOptions) (string, error) {
	return db.publish(ctx, "blob_id", blobID, opts)
}

// publish — общий путь обоих источников. Повторный вызов возвращает
// существующий токен, обновляя только переданные опции.
func (db *DB) publish(ctx context.Context, column string, id uuid.UUID, opts PublishOptions) (string, error) {
	d := db.dialect
	existing, err := db.publicFileWhere(ctx, column, idArg(d, id))
	if err != nil {
		return "", err
	}
	if existing != nil {
		return db.republish(ctx, column, id, existing, opts)
	}
	if opts.CacheSeconds <= 0 {
		opts.CacheSeconds = defaultPublicCacheSeconds
	}

	token, err := newPublicToken()
	if err != nil {
		return "", err
	}
	createdBy := AuditUserLogin(ctx)
	// ON CONFLICT DO NOTHING: между чтением выше и этой вставкой публикацию мог
	// завести сосед — два параллельных рендера страницы, впервые публикующей
	// картинку, оба видели «записи нет». Раньше второй падал на уникальном
	// индексе, и ошибка уходила пользователю исключением из ОпубликоватьФайл
	// (#1001). Целевой столбец не указан намеренно: конфликт возможен и по
	// token, и по источнику, а DO NOTHING без цели поддержан обоими диалектами.
	q := fmt.Sprintf(`INSERT INTO _public_files (token, %s, filename, cache_seconds, expires_at, created_at, created_by)
		VALUES (%s, %s, %s, %s, %s, %s, %s) ON CONFLICT DO NOTHING`, column,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5), d.Placeholder(6), d.Placeholder(7))
	if _, err := db.Exec(ctx, q, token, idArg(d, id), opts.Filename, opts.CacheSeconds, opts.ExpiresAt, time.Now().UTC(), createdBy); err != nil {
		return "", err
	}
	saved, err := db.publicFileWhere(ctx, column, idArg(d, id))
	if err != nil {
		return "", err
	}
	if saved == nil {
		return "", fmt.Errorf("публикация файла %s: строка не найдена после вставки", id)
	}
	if saved.Token != token {
		// Гонку выиграл сосед: его строка и есть публикация, но наши опции
		// применить всё равно надо — иначе тот, кто просил срок или имя файла,
		// молча получил бы чужие настройки.
		return db.republish(ctx, column, id, saved, opts)
	}
	// «Файл стал доступен всему интернету» обязано иметь автора и время.
	db.logPublicFileAudit(ctx, "publish", column, id)
	return token, nil
}

// republish обновляет существующую публикацию. Обновляет ТОЛЬКО переданные
// поля: повторная публикация без опций — обычное дело в цикле рендера, и она не
// должна сбрасывать настройки, заданные при первой публикации.
func (db *DB) republish(ctx context.Context, column string, id uuid.UUID, existing *PublicFile, opts PublishOptions) (string, error) {
	d := db.dialect
	if opts.Filename == "" {
		opts.Filename = existing.Filename
	}
	if opts.CacheSeconds <= 0 {
		opts.CacheSeconds = existing.CacheSeconds
	}
	if opts.ExpiresAt == nil && !existing.Expired(time.Now()) {
		// Живой срок повторная публикация сохраняет, истёкший — нет:
		// вернуть токен, по которому /pub уже отвечает 404, — это не
		// «опубликовать». Без нового срока публикация снова бессрочна.
		opts.ExpiresAt = existing.ExpiresAt
	}
	q := fmt.Sprintf(`UPDATE _public_files SET filename=%s, cache_seconds=%s, expires_at=%s WHERE token=%s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4))
	if _, err := db.Exec(ctx, q, opts.Filename, opts.CacheSeconds, opts.ExpiresAt, existing.Token); err != nil {
		return "", err
	}
	db.logPublicFileAudit(ctx, "publish", column, id)
	return existing.Token, nil
}

// UnpublishAttachment отзывает публикацию вложения. Отсутствие публикации — не
// ошибка.
func (db *DB) UnpublishAttachment(ctx context.Context, attID uuid.UUID) error {
	return db.unpublish(ctx, "attachment_id", attID)
}

// UnpublishBlob отзывает публикацию картинки.
func (db *DB) UnpublishBlob(ctx context.Context, blobID uuid.UUID) error {
	return db.unpublish(ctx, "blob_id", blobID)
}

func (db *DB) unpublish(ctx context.Context, column string, id uuid.UUID) error {
	d := db.dialect
	q := fmt.Sprintf(`DELETE FROM _public_files WHERE %s=%s`, column, d.Placeholder(1))
	tag, err := db.Exec(ctx, q, idArg(d, id))
	if err != nil {
		return err
	}
	// Отзыв, которого не было, в журнал не пишем: запись «отозвана публикация»
	// там, где публикации не существовало, при расследовании читается как факт
	// и уводит в сторону (#1001).
	if tag.RowsAffected > 0 {
		db.logPublicFileAudit(ctx, "unpublish", column, id)
	}
	return nil
}

// deletePublicFileByBlob убирает публикацию удаляемого блоба. Вызывается из
// DeleteBlob — у blob_id внешнего ключа нет, каскад его не подчистит.
func (db *DB) deletePublicFileByBlob(ctx context.Context, blobID uuid.UUID) error {
	// Таблицы может не быть: служебную схему заводит EnsureServiceSchema, а
	// блобы живут и в базах, до которых она не дошла. Отсутствие таблицы — не
	// ошибка удаления блоба.
	ok, err := db.TableExists(ctx, "_public_files")
	if err != nil || !ok {
		return nil
	}
	q := fmt.Sprintf(`DELETE FROM _public_files WHERE blob_id=%s`, db.dialect.Placeholder(1))
	_, err = db.Exec(ctx, q, idArg(db.dialect, blobID))
	return err
}

// PublicFileByToken находит публикацию по токену из URL.
func (db *DB) PublicFileByToken(ctx context.Context, token string) (*PublicFile, error) {
	return db.publicFileWhere(ctx, "token", token)
}

// PublicFileByAttachment находит публикацию вложения (nil, если не публиковалось).
func (db *DB) PublicFileByAttachment(ctx context.Context, attID uuid.UUID) (*PublicFile, error) {
	return db.publicFileWhere(ctx, "attachment_id", idArg(db.dialect, attID))
}

// PublicFileByBlob находит публикацию картинки.
func (db *DB) PublicFileByBlob(ctx context.Context, blobID uuid.UUID) (*PublicFile, error) {
	return db.publicFileWhere(ctx, "blob_id", idArg(db.dialect, blobID))
}

func (db *DB) publicFileWhere(ctx context.Context, column string, value any) (*PublicFile, error) {
	d := db.dialect
	q := fmt.Sprintf(`SELECT token, attachment_id, blob_id, filename, cache_seconds, expires_at, created_at, created_by
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
		attRaw     *uuid.UUID
		blobRaw    *uuid.UUID
		expiresRaw any
		createdRaw any
	)
	err := row.Scan(&pf.Token, &attRaw, &blobRaw, &pf.Filename, &pf.CacheSeconds, &expiresRaw, &createdRaw, &pf.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if attRaw != nil {
		pf.AttachmentID = *attRaw
	}
	if blobRaw != nil {
		pf.BlobID = *blobRaw
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
//
// Вид записи берётся из источника публикации, а не проставляется «attachment»
// всегда: при публикации картинки в record_id лежит ид блоба, и помеченный
// вложением он не находился среди _attachments — след расследования «какой файл
// засветили наружу» обрывался на пустом месте (#1001).
func (db *DB) logPublicFileAudit(ctx context.Context, action, column string, id uuid.UUID) {
	u, _ := auditUserFromCtx(ctx)
	db.LogAction(ctx, action, publicFileKind(column), "_public_files", id.String(), u.UserID, u.UserLogin, "")
}

// publicFileKind переводит колонку источника в вид записи аудита.
func publicFileKind(column string) string {
	if column == "blob_id" {
		return "blob"
	}
	return "attachment"
}
