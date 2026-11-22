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
	return db.WithTxScope(ctx, func(txCtx context.Context) error {
		d := db.dialect
		// Restore clears attachments before clearing the deletion queue. Lock
		// the live row first so an old snapshot cannot enqueue it after restore.
		var owner, loc string
		remove := "DELETE FROM _attachments WHERE id=" + d.Placeholder(1) + " RETURNING owner_name, loc"
		err := db.QueryRow(txCtx, remove, idArg(d, id)).Scan(&owner, &loc)
		if IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		insert := fmt.Sprintf(`INSERT INTO _attachment_deletion_queue (id, queue_name, owner_name, loc)
            VALUES (%s, %s, %s, %s) ON CONFLICT (id) DO NOTHING`,
			d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4))
		_, err = db.Exec(txCtx, insert, idArg(d, id), queue, owner, loc)
		return err
	})
}

// FlushAttachmentDeletionQueue removes committed files and keeps failed jobs
// for the next run. The job is claimed again in a transaction before any
// physical deletion, since the selection can have raced with a full restore.
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
		deleted, err := db.flushQueuedAttachment(ctx, owner, queue, job)
		if err != nil {
			result.Failed++
			result.LastError = fmt.Sprintf("вложение %s: %s", job, err)
			continue
		}
		if deleted {
			result.Deleted++
		}
	}
	return result, nil
}

func (db *DB) pendingAttachmentDeletes(ctx context.Context, owner, queue string) ([]uuid.UUID, error) {
	d := db.dialect
	q := fmt.Sprintf("SELECT id FROM _attachment_deletion_queue WHERE owner_name=%s AND queue_name=%s ORDER BY id", d.Placeholder(1), d.Placeholder(2))
	rows, err := db.Query(ctx, q, owner, queue)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []uuid.UUID
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, id)
	}
	return jobs, rows.Err()
}

func (db *DB) flushQueuedAttachment(ctx context.Context, owner, queue string, id uuid.UUID) (bool, error) {
	d := db.dialect
	removed := false
	err := db.WithTx(ctx, func(txCtx context.Context) error {
		// The DELETE holds the queue row until the file operation ends. Restore
		// clears this table in its transaction before publishing the new tree.
		claim := fmt.Sprintf(`DELETE FROM _attachment_deletion_queue
			WHERE id=%s AND owner_name=%s AND queue_name=%s RETURNING loc`,
			d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
		var loc string
		err := db.QueryRow(txCtx, claim, idArg(d, id), owner, queue).Scan(&loc)
		if IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		var live bool
		if err := db.QueryRow(txCtx, "SELECT EXISTS (SELECT 1 FROM _attachments WHERE id="+d.Placeholder(1)+")",
			idArg(d, id)).Scan(&live); err != nil {
			return err
		}
		if live {
			return nil
		}
		if err := db.removeQueuedAttachment(txCtx, owner, loc, id); err != nil {
			return err
		}
		removed = true
		return nil
	})
	return removed, err
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
