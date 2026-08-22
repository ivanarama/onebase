package storage_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

type mediaClaimResult struct {
	claimID    uuid.UUID
	blobID     uuid.UUID
	winnerID   uuid.UUID
	winnerBlob uuid.UUID
	payload    []byte
	created    bool
	err        error
}

func mediaClaimEntities(suffix string) (*metadata.Entity, *metadata.Entity) {
	sites := &metadata.Entity{
		Name: "СайтыClaim" + suffix,
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	media := &metadata.Entity{
		Name: "МедиаClaim" + suffix,
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Сайт", Type: metadata.FieldType("reference:" + sites.Name), RefEntity: sites.Name},
			{Name: "ВнешнийКлюч", Type: metadata.FieldTypeString},
			{Name: "Файл", Type: metadata.FieldTypeImage},
			{Name: "Активен", Type: metadata.FieldTypeBool},
		},
		Indexes: []metadata.IndexSpec{{Fields: []string{"Сайт", "ВнешнийКлюч"}, Unique: true}},
	}
	return sites, media
}

func writeMediaClaim(
	ctx context.Context,
	db *storage.DB,
	media *metadata.Entity,
	siteID uuid.UUID,
	externalKey string,
	payload []byte,
	holdBeforeCommit time.Duration,
) mediaClaimResult {
	result := mediaClaimResult{claimID: uuid.New(), payload: payload}
	result.err = db.WithTx(ctx, func(txCtx context.Context) error {
		fields := map[string]any{
			"Наименование": "Импорт " + externalKey,
			"Сайт":         siteID.String(),
			"ВнешнийКлюч":  externalKey,
			"Активен":      false,
		}
		// Claim must be the first write. A competing transaction that lost the
		// composite UNIQUE race must not upload bytes which can become orphaned.
		if err := db.Upsert(txCtx, media.Name, result.claimID, fields, media); err != nil {
			return err
		}
		blob, err := db.PutBlob(txCtx, "image/png", bytes.NewReader(payload), 1<<20,
			storage.BlobOwner{Kind: string(metadata.KindCatalog), Entity: media.Name})
		if err != nil {
			return err
		}
		result.blobID = blob.ID
		fields["Файл"] = blob.ID.String()
		fields["Активен"] = true
		if err := db.Upsert(txCtx, media.Name, result.claimID, fields, media); err != nil {
			return err
		}
		if holdBeforeCommit > 0 {
			time.Sleep(holdBeforeCommit)
		}
		return nil
	})
	if result.err == nil {
		result.created = true
		result.winnerID = result.claimID
		result.winnerBlob = result.blobID
	}
	return result
}

// createOrReadMediaClaim is the application-level top transaction protocol:
// after the losing transaction is fully rolled back, poll with a fresh context
// until the committed winner is visible. It mirrors the CMS DSL helper's
// top-level path; nested/savepoint callers must instead retry their outer tx.
func createOrReadMediaClaim(
	ctx context.Context,
	db *storage.DB,
	media *metadata.Entity,
	siteID uuid.UUID,
	externalKey string,
	payload []byte,
	holdBeforeCommit, wait time.Duration,
) mediaClaimResult {
	result := writeMediaClaim(ctx, db, media, siteID, externalKey, payload, holdBeforeCommit)
	if result.err == nil {
		return result
	}
	originalErr := result.err
	deadline := time.Now().Add(wait)
	for {
		winnerID, winnerBlob, found, err := findMediaClaimWinner(ctx, db, media, siteID, externalKey)
		if err != nil {
			result.err = errors.Join(originalErr, err)
			return result
		}
		if found {
			result.winnerID = winnerID
			result.winnerBlob = winnerBlob
			result.err = nil
			return result
		}
		if time.Now().After(deadline) {
			result.err = originalErr
			return result
		}
		select {
		case <-ctx.Done():
			result.err = errors.Join(originalErr, ctx.Err())
			return result
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func findMediaClaimWinner(
	ctx context.Context,
	db *storage.DB,
	media *metadata.Entity,
	siteID uuid.UUID,
	externalKey string,
) (uuid.UUID, uuid.UUID, bool, error) {
	placeholder := func(n int) string { return "?" }
	siteArg := any(siteID.String())
	if db.IsPostgres() {
		placeholder = func(n int) string { return fmt.Sprintf("$%d", n) }
		siteArg = siteID
	}
	query := fmt.Sprintf("SELECT id, %s FROM %s WHERE %s=%s AND %s=%s",
		mediaClaimColumn(media, "Файл"), metadata.TableName(media.Name),
		mediaClaimColumn(media, "Сайт"), placeholder(1),
		mediaClaimColumn(media, "ВнешнийКлюч"), placeholder(2))
	rows, err := db.Query(ctx, query, siteArg, externalKey)
	if err != nil {
		return uuid.Nil, uuid.Nil, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return uuid.Nil, uuid.Nil, false, rows.Err()
	}
	var claimID, blobID uuid.UUID
	if err := rows.Scan(&claimID, &blobID); err != nil {
		return uuid.Nil, uuid.Nil, false, err
	}
	if rows.Next() {
		return uuid.Nil, uuid.Nil, false, errors.New("composite media claim returned more than one winner")
	}
	if err := rows.Err(); err != nil {
		return uuid.Nil, uuid.Nil, false, err
	}
	return claimID, blobID, true, nil
}

func mediaClaimColumn(entity *metadata.Entity, name string) string {
	for _, field := range entity.Fields {
		if field.Name == name {
			return metadata.ColumnName(field)
		}
	}
	panic("test entity has no field " + name)
}

// TestMediaClaimAndOwnerBlobAreAtomic covers the import protocol used by CMS:
// claim the (site, external key) pair first, then save an owner-aware blob and
// activate the row in the same transaction. Two independent DB connections are
// important on SQLite: a single handle is limited to one open connection and
// would serialize the race before it reached the UNIQUE index.
func TestMediaClaimAndOwnerBlobAreAtomic(t *testing.T) {
	stagePair(t, func(t *testing.T, a, b *storage.DB) {
		ctx := context.Background()
		suffix := uuid.NewString()[:8]
		sites, media := mediaClaimEntities(suffix)
		if err := a.Migrate(ctx, []*metadata.Entity{sites, media}); err != nil {
			t.Fatalf("migrate media claim schema: %v", err)
		}
		if err := a.EnsureBlobTable(ctx); err != nil {
			t.Fatalf("ensure blobs: %v", err)
		}
		// Keeping bytes in the DB makes the row and its content participate in
		// exactly the same transaction on both SQLite and PostgreSQL. External
		// disk/S3 compensation is covered independently by blobs_tx_test.go.
		if err := a.SaveFileStorageMode(ctx, storage.FileStorageDB); err != nil {
			t.Fatalf("set DB blob mode: %v", err)
		}
		// Force SQLite's loser out of INSERT before the old 200 ms polling
		// window, while the winner is deliberately still uncommitted below.
		if !a.IsPostgres() {
			for _, db := range []*storage.DB{a, b} {
				if _, err := db.Exec(ctx, "PRAGMA busy_timeout=100"); err != nil {
					t.Fatalf("short SQLite busy timeout: %v", err)
				}
			}
		}

		siteID := uuid.New()
		if err := a.Upsert(ctx, sites.Name, siteID, map[string]any{"Наименование": "Основной"}, sites); err != nil {
			t.Fatalf("create site: %v", err)
		}

		const externalKey = "https://img.example.test/shared.png"
		start := make(chan struct{})
		results := make([]mediaClaimResult, 2)
		var wg sync.WaitGroup
		for i, db := range []*storage.DB{a, b} {
			wg.Add(1)
			go func(i int, db *storage.DB) {
				defer wg.Done()
				<-start
				results[i] = createOrReadMediaClaim(ctx, db, media, siteID, externalKey,
					[]byte(fmt.Sprintf("candidate-%d", i)), 450*time.Millisecond, 3*time.Second)
			}(i, db)
		}
		close(start)
		wg.Wait()

		var winner, loser *mediaClaimResult
		for i := range results {
			if results[i].err != nil {
				t.Fatalf("application-level claim %d failed instead of reading winner: %v", i, results[i].err)
			}
			if results[i].created {
				if winner != nil {
					t.Fatalf("both claims committed: %+v", results)
				}
				winner = &results[i]
			} else {
				loser = &results[i]
			}
		}
		if winner == nil || loser == nil {
			t.Fatalf("want one creator and one reader of its winner, got %+v", results)
		}
		if winner.winnerID != loser.winnerID || winner.winnerBlob != loser.winnerBlob {
			t.Fatalf("competitors returned different winners: %+v", results)
		}
		if loser.blobID != uuid.Nil {
			t.Fatalf("losing claim uploaded blob %s before detecting conflict", loser.blobID)
		}
		if _, err := a.GetByID(ctx, media.Name, loser.claimID, media); !storage.IsNotFound(err) {
			t.Fatalf("losing provisional claim survived: %v", err)
		}
		conflict := writeMediaClaim(ctx, a, media, siteID, externalKey, []byte("must-not-be-uploaded"), 0)
		if !isMediaClaimUniqueConflict(conflict.err) {
			t.Fatalf("retry after concurrent winner = %v, want composite unique violation", conflict.err)
		}
		if conflict.blobID != uuid.Nil {
			t.Fatalf("known duplicate uploaded blob %s before detecting conflict", conflict.blobID)
		}
		if _, err := a.GetByID(ctx, media.Name, conflict.claimID, media); !storage.IsNotFound(err) {
			t.Fatalf("conflicting retry left claim behind: %v", err)
		}

		assertMediaClaimTableCount(t, a, metadata.TableName(media.Name), 1)
		assertMediaClaimTableCount(t, a, "_blobs", 1)
		row, err := a.GetByID(ctx, media.Name, winner.winnerID, media)
		if err != nil {
			t.Fatalf("read committed claim: %v", err)
		}
		if got := fmt.Sprint(row["Файл"]); got != winner.winnerBlob.String() {
			t.Fatalf("claim file = %q, want %s", got, winner.winnerBlob)
		}
		blob, rc, err := a.OpenBlob(ctx, winner.winnerBlob)
		if err != nil {
			t.Fatalf("open committed blob: %v", err)
		}
		gotPayload, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			t.Fatalf("read committed blob: %v", err)
		}
		if !bytes.Equal(gotPayload, winner.payload) {
			t.Fatalf("committed blob payload = %q, want %q", gotPayload, winner.payload)
		}
		if blob.OwnerKind != string(metadata.KindCatalog) || blob.OwnerEntity != media.Name {
			t.Fatalf("committed blob owner = (%q, %q), want (%q, %q)",
				blob.OwnerKind, blob.OwnerEntity, metadata.KindCatalog, media.Name)
		}

		rollbackCause := errors.New("fail after owner-aware blob")
		rollbackClaimID := uuid.New()
		var rollbackBlobID uuid.UUID
		err = a.WithTx(ctx, func(txCtx context.Context) error {
			fields := map[string]any{
				"Наименование": "Откат",
				"Сайт":         siteID.String(), "ВнешнийКлюч": externalKey + "?rollback", "Активен": false,
			}
			if err := a.Upsert(txCtx, media.Name, rollbackClaimID, fields, media); err != nil {
				return err
			}
			blob, err := a.PutBlob(txCtx, "image/png", bytes.NewReader([]byte("rollback")), 1<<20,
				storage.BlobOwner{Kind: string(metadata.KindCatalog), Entity: media.Name})
			if err != nil {
				return err
			}
			rollbackBlobID = blob.ID
			fields["Файл"] = blob.ID.String()
			if err := a.Upsert(txCtx, media.Name, rollbackClaimID, fields, media); err != nil {
				return err
			}
			return rollbackCause
		})
		if !errors.Is(err, rollbackCause) {
			t.Fatalf("rollback error = %v, want %v", err, rollbackCause)
		}
		if _, err := a.GetByID(ctx, media.Name, rollbackClaimID, media); !storage.IsNotFound(err) {
			t.Fatalf("rolled-back claim survived: %v", err)
		}
		if _, _, err := a.OpenBlob(ctx, rollbackBlobID); err == nil {
			t.Fatalf("rolled-back blob %s remained readable", rollbackBlobID)
		}
		assertMediaClaimTableCount(t, a, metadata.TableName(media.Name), 1)
		assertMediaClaimTableCount(t, a, "_blobs", 1)
	})
}

func isMediaClaimUniqueConflict(err error) bool {
	return storage.IsUniqueViolation(err) || errors.Is(err, storage.ErrCodeDuplicate)
}

func assertMediaClaimTableCount(t *testing.T, db *storage.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("count %s = %d, want %d", table, got, want)
	}
}
