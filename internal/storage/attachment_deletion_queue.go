package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
)

type AttachmentDeletionResult struct {
	Deleted   int
	Failed    int
	LastError string
}

func (db *DB) ensureAttachmentDeletionQueue(ctx context.Context) error {
	_, err := db.Exec(ctx, fmt.Sprintf(`
        CREATE TABLE IF NOT EXISTS _attachment_deletion_queue (
            id %s PRIMARY KEY,
            queue_name TEXT NOT NULL,
            owner_name TEXT NOT NULL,
            loc TEXT NOT NULL
        )`, db.dialect.TypeUUID()))
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `CREATE INDEX IF NOT EXISTS _attachment_deletion_queue_owner ON _attachment_deletion_queue(owner_name, queue_name)`)
	return err
}

func validAttachmentQueueName(queue string) bool {
	return strings.TrimSpace(queue) != "" && len(queue) <= 128
}

// QueueAttachmentDeletion removes the attachment from its owner and records the
// physical deletion in the same database transaction as the caller's changes.
func (db *DB) QueueAttachmentDeletion(ctx context.Context, id uuid.UUID, queue string) error {
	if err := writeAllowed(ctx); err != nil {
		return err
	}
	if !validAttachmentQueueName(queue) {
		return errors.New("неверное имя очереди удаления вложений")
	}
	return db.WithTxIfNeeded(ctx, func(txCtx context.Context) error {
		d := db.dialect
		q := fmt.Sprintf(`INSERT INTO _attachment_deletion_queue (id, queue_name, owner_name, loc)
            SELECT id, %s, owner_name, loc FROM _attachments WHERE id=%s
            ON CONFLICT (id) DO NOTHING`, d.Placeholder(1), d.Placeholder(2))
		if _, err := db.Exec(txCtx, q, queue, idArg(d, id)); err != nil {
			return err
		}
		_, err := db.Exec(txCtx, "DELETE FROM _attachments WHERE id="+d.Placeholder(1), idArg(d, id))
		return err
	})
}

// FlushAttachmentDeletionQueue removes committed files and keeps failed jobs
// for the next run. Once queued, a file never again has an attachment row.
func (db *DB) FlushAttachmentDeletionQueue(ctx context.Context, owner, queue string) (AttachmentDeletionResult, error) {
	var result AttachmentDeletionResult
	if err := writeAllowed(ctx); err != nil {
		return result, err
	}
	if HasTx(ctx) {
		return result, errors.New("физическое удаление вложений выполняется после фиксации транзакции")
	}
	if err := validateAttachmentOwnerName(owner); err != nil {
		return result, err
	}
	if !validAttachmentQueueName(queue) {
		return result, errors.New("неверное имя очереди удаления вложений")
	}
	pending, err := db.pendingAttachmentDeletes(ctx, owner, queue)
	if err != nil {
		return result, err
	}
	for _, job := range pending {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		err := db.removeQueuedAttachment(ctx, owner, job.loc, job.id)
		if err == nil {
			_, err = db.Exec(ctx, "DELETE FROM _attachment_deletion_queue WHERE id="+db.dialect.Placeholder(1), idArg(db.dialect, job.id))
		}
		if err != nil {
			result.Failed++
			result.LastError = fmt.Sprintf("вложение %s: %s", job.id, err)
			continue
		}
		result.Deleted++
	}
	return result, nil
}

type pendingAttachmentDelete struct {
	id  uuid.UUID
	loc string
}

func (db *DB) pendingAttachmentDeletes(ctx context.Context, owner, queue string) ([]pendingAttachmentDelete, error) {
	d := db.dialect
	q := fmt.Sprintf("SELECT id, loc FROM _attachment_deletion_queue WHERE owner_name=%s AND queue_name=%s ORDER BY id", d.Placeholder(1), d.Placeholder(2))
	rows, err := db.Query(ctx, q, owner, queue)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []pendingAttachmentDelete
	for rows.Next() {
		var job pendingAttachmentDelete
		var id string
		if err := rows.Scan(&id, &job.loc); err != nil {
			return nil, err
		}
		job.id, err = uuid.Parse(id)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (db *DB) removeQueuedAttachment(ctx context.Context, owner, location string, id uuid.UUID) error {
	switch location {
	case FileStorageS3:
		if db.blobStore == nil {
			return errors.New("хранилище S3 недоступно")
		}
		return db.blobStore.DeleteObject(ctx, db.attachmentObjectKey(owner, id))
	case "", FileStorageDisk:
		dir, err := db.RequireFilesDir()
		if err != nil {
			return err
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			return err
		}
		defer func() { _ = root.Close() }()
		err = root.Remove(owner + "/" + id.String())
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	default:
		return fmt.Errorf("неизвестный тип хранилища %q", location)
	}
}
