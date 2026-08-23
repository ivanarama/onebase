package ui

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

type cmsMediaCallResult struct {
	ref *interpreter.Ref
	err error
}

// TestCMSMediaImport_ConcurrentSQLiteClaimsUseOneBlob runs the shipped CMS
// module, not a Go transcription of its claim algorithm. Two independent DB
// handles and runtimes model two importer processes. The winner is held after
// it has written the Media claim but before PutBlob; the loser therefore gets
// SQLITE_BUSY, rolls its transaction back, and must poll the committed winner.
func TestCMSMediaImport_ConcurrentSQLiteClaimsUseOneBlob(t *testing.T) {
	ctx := context.Background()
	proj, err := project.Load(filepath.Join("..", "..", "examples", "cms"))
	if err != nil {
		t.Fatalf("load CMS: %v", err)
	}
	t.Cleanup(proj.Close)

	dbPath := filepath.Join(t.TempDir(), "cms-media-race.db")
	winnerDB, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("connect winner SQLite: %v", err)
	}
	t.Cleanup(winnerDB.Close)
	loserDB, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("connect loser SQLite: %v", err)
	}
	t.Cleanup(loserDB.Close)

	if err := winnerDB.Migrate(ctx, proj.Entities); err != nil {
		t.Fatalf("migrate CMS: %v", err)
	}
	ensureCMSMediaIntegrationSchema(t, ctx, winnerDB)
	// The loser must leave INSERT quickly enough to exercise the helper's
	// read-after-busy polling. With the operational 5s timeout it would merely
	// wait inside SQLite until the winner commits and miss the regression where
	// polling used to stop after 10x20ms.
	if _, err := loserDB.Exec(ctx, "PRAGMA busy_timeout=50"); err != nil {
		t.Fatalf("shorten loser busy timeout: %v", err)
	}

	sites := cmsEntity(t, proj, "Сайты")
	media := cmsEntity(t, proj, "Медиа")
	siteID := uuid.New()
	if err := winnerDB.Upsert(ctx, sites.Name, siteID, map[string]any{
		"Наименование": "Race test site",
		"Домен":        "race.test",
		"Активен":      true,
	}, sites); err != nil {
		t.Fatalf("seed site: %v", err)
	}
	siteRef := &interpreter.Ref{UUID: siteID.String(), Name: "Race test site", Type: sites.Name}

	winner, winnerReg, err := NewOfflineServer(proj, winnerDB)
	if err != nil {
		t.Fatalf("winner runtime: %v", err)
	}
	loser, loserReg, err := NewOfflineServer(proj, loserDB)
	if err != nil {
		t.Fatalf("loser runtime: %v", err)
	}

	winnerVars, winnerTx := winner.buildDSLVarsTx(ctx, nil)
	loserVars, loserTx := loser.buildDSLVarsTx(ctx, nil)
	originalPutImage, ok := winnerVars["СохранитьКартинку"].(interpreter.BuiltinFunc)
	if !ok {
		t.Fatalf("СохранитьКартинку has type %T", winnerVars["СохранитьКартинку"])
	}
	claimReached := make(chan struct{})
	releaseWinner := make(chan struct{})
	var claimOnce sync.Once
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseWinner) }) }
	defer release()
	winnerVars["СохранитьКартинку"] = interpreter.BuiltinFunc(func(args []any, file string, line int) (any, error) {
		claimOnce.Do(func() { close(claimReached) })
		<-releaseWinner
		return originalPutImage(args, file, line)
	})

	loserPolling := make(chan struct{})
	var pollOnce sync.Once
	// Shadow only this runtime's ordinary sleep builtin. Signalling from the
	// first backoff proves the loser has observed SQLITE_BUSY and rolled back;
	// sleeping the requested duration preserves both the old .02s and current
	// .05s production timings when this test is used as a regression check.
	loserVars["Приостановить"] = interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		pollOnce.Do(func() { close(loserPolling) })
		if len(args) != 1 {
			return nil, fmt.Errorf("unexpected sleep args: %#v", args)
		}
		seconds, err := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64)
		if err != nil {
			return nil, fmt.Errorf("parse sleep duration: %w", err)
		}
		time.Sleep(time.Duration(seconds * float64(time.Second)))
		return nil, nil
	})

	const (
		key    = "https://images.example/race.png"
		pngB64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
		mime   = "image/png"
	)
	wantPayload, err := base64.StdEncoding.DecodeString(pngB64)
	if err != nil {
		t.Fatal(err)
	}

	call := func(s *Server, reg *runtime.Registry, vars map[string]any, txState *interpreter.TxState, title string) cmsMediaCallResult {
		proc := reg.GetModuleNamespacedProc("ИмпортYML", "ЗаписатьКартинкуАтомарно")
		if proc == nil {
			return cmsMediaCallResult{err: fmt.Errorf("production helper not found")}
		}
		defer rollbackDSLExecution(txState)
		got, callErr := s.interp.Call(proc, nil, []any{siteRef, key, title, pngB64, mime}, vars)
		callErr = finishDSLExecution(txState, callErr)
		if callErr != nil {
			return cmsMediaCallResult{err: callErr}
		}
		ref, ok := got.(*interpreter.Ref)
		if !ok || ref == nil {
			return cmsMediaCallResult{err: fmt.Errorf("helper returned %T, want *interpreter.Ref", got)}
		}
		return cmsMediaCallResult{ref: ref}
	}

	winnerDone := make(chan cmsMediaCallResult, 1)
	go func() { winnerDone <- call(winner, winnerReg, winnerVars, winnerTx, "Winner") }()
	select {
	case <-claimReached:
	case result := <-winnerDone:
		if result.err != nil {
			t.Fatalf("winner failed before PutBlob: %v", result.err)
		}
		t.Fatal("winner returned before reaching PutBlob seam")
	case <-time.After(12 * time.Second):
		t.Fatal("winner did not reach PutBlob after writing claim")
	}

	loserDone := make(chan cmsMediaCallResult, 1)
	go func() { loserDone <- call(loser, loserReg, loserVars, loserTx, "Loser") }()
	select {
	case <-loserPolling:
	case result := <-loserDone:
		if result.err != nil {
			t.Fatalf("loser failed before read-after-busy polling: %v", result.err)
		}
		t.Fatal("loser returned before read-after-busy polling")
	case <-time.After(5 * time.Second):
		t.Fatal("loser did not enter read-after-busy polling")
	}
	// Measured from the first poll: longer than the former 10x20ms total
	// retry window. Releasing earlier could let the old implementation pass.
	time.Sleep(300 * time.Millisecond)
	release()

	winnerResult := awaitCMSMediaCall(t, "winner", winnerDone)
	loserResult := awaitCMSMediaCall(t, "loser", loserDone)
	if winnerResult.ref.UUID != loserResult.ref.UUID {
		t.Fatalf("different media refs: winner=%s loser=%s", winnerResult.ref.UUID, loserResult.ref.UUID)
	}
	// A third, completely fresh handle/runtime is the ordinary top-level repeat
	// path: its precheck must return the committed winner without a new claim.
	repeatDB, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("connect repeat SQLite: %v", err)
	}
	t.Cleanup(repeatDB.Close)
	repeat, repeatReg, err := NewOfflineServer(proj, repeatDB)
	if err != nil {
		t.Fatalf("repeat runtime: %v", err)
	}
	repeatVars, repeatTx := repeat.buildDSLVarsTx(ctx, nil)
	repeatResult := call(repeat, repeatReg, repeatVars, repeatTx, "Repeat")
	if repeatResult.err != nil {
		t.Fatalf("repeat import: %v", repeatResult.err)
	}
	if repeatResult.ref.UUID != winnerResult.ref.UUID {
		t.Fatalf("repeat returned %s, winner is %s", repeatResult.ref.UUID, winnerResult.ref.UUID)
	}

	var mediaCount, blobCount, publicationCount int
	if err := winnerDB.QueryRow(ctx, "SELECT COUNT(*) FROM "+metadata.TableName(media.Name)).Scan(&mediaCount); err != nil {
		t.Fatalf("count Media: %v", err)
	}
	if err := winnerDB.QueryRow(ctx, "SELECT COUNT(*) FROM _blobs").Scan(&blobCount); err != nil {
		t.Fatalf("count blobs: %v", err)
	}
	if err := winnerDB.QueryRow(ctx, "SELECT COUNT(*) FROM _public_files").Scan(&publicationCount); err != nil {
		t.Fatalf("count publications: %v", err)
	}
	if mediaCount != 1 || blobCount != 1 || publicationCount != 1 {
		t.Fatalf("race left media/blobs/publications = %d/%d/%d, want 1/1/1",
			mediaCount, blobCount, publicationCount)
	}

	mediaID, err := uuid.Parse(winnerResult.ref.UUID)
	if err != nil {
		t.Fatalf("parse Media ref: %v", err)
	}
	row, err := winnerDB.GetByID(ctx, media.Name, mediaID, media)
	if err != nil {
		t.Fatalf("load winning Media: %v", err)
	}
	blobID, err := uuid.Parse(strings.TrimSpace(fmt.Sprintf("%v", row["Файл"])))
	if err != nil {
		t.Fatalf("winning Media has invalid blob ref %#v: %v", row["Файл"], err)
	}
	publicURL := strings.TrimSpace(fmt.Sprintf("%v", row["ПубличнаяСсылка"]))
	if !strings.HasPrefix(publicURL, "/pub/") {
		t.Fatalf("winning Media public URL = %q", publicURL)
	}
	var publishedBlob string
	if err := winnerDB.QueryRow(ctx, "SELECT blob_id FROM _public_files").Scan(&publishedBlob); err != nil {
		t.Fatalf("read publication: %v", err)
	}
	if publishedBlob != blobID.String() {
		t.Fatalf("publication points to %s, Media points to %s", publishedBlob, blobID)
	}

	blob, rc, err := winnerDB.OpenBlob(ctx, blobID)
	if err != nil {
		t.Fatalf("open committed blob: %v", err)
	}
	payload, readErr := io.ReadAll(rc)
	closeErr := rc.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read committed blob: read=%v close=%v", readErr, closeErr)
	}
	if string(payload) != string(wantPayload) {
		t.Fatalf("committed blob payload differs: got %d bytes want %d", len(payload), len(wantPayload))
	}
	if blob.Mime != mime || blob.OwnerEntity != media.Name || blob.OwnerKind != string(media.Kind) {
		t.Fatalf("blob metadata = %+v, want mime=%s owner=%s/%s", blob, mime, media.Kind, media.Name)
	}
}

// Once the unique claim is ours, an image/storage failure is not a dedup race.
// The helper must roll the claim and blob back and return the original error
// immediately; entering its 200-step winner polling loop would hide outages for
// up to ten seconds and then report a misleading uniqueness conflict.
func TestCMSMediaImport_PostClaimFailureRollsBackWithoutPolling(t *testing.T) {
	ctx := context.Background()
	proj, err := project.Load(filepath.Join("..", "..", "examples", "cms"))
	if err != nil {
		t.Fatalf("load CMS: %v", err)
	}
	t.Cleanup(proj.Close)

	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "cms-media-failure.db"))
	if err != nil {
		t.Fatalf("connect SQLite: %v", err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatalf("migrate CMS: %v", err)
	}
	ensureCMSMediaIntegrationSchema(t, ctx, db)

	sites := cmsEntity(t, proj, "Сайты")
	media := cmsEntity(t, proj, "Медиа")
	siteID := uuid.New()
	if err := db.Upsert(ctx, sites.Name, siteID, map[string]any{
		"Наименование": "Failure test site",
		"Домен":        "failure.test",
		"Активен":      true,
	}, sites); err != nil {
		t.Fatalf("seed site: %v", err)
	}
	siteRef := &interpreter.Ref{UUID: siteID.String(), Name: "Failure test site", Type: sites.Name}

	s, reg, err := NewOfflineServer(proj, db)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	vars, txState := s.buildDSLVarsTx(ctx, nil)
	defer rollbackDSLExecution(txState)
	originalPutImage, ok := vars["СохранитьКартинку"].(interpreter.BuiltinFunc)
	if !ok {
		t.Fatalf("СохранитьКартинку has type %T", vars["СохранитьКартинку"])
	}
	putCalls := 0
	createdBlob := ""
	vars["СохранитьКартинку"] = interpreter.BuiltinFunc(func(args []any, file string, line int) (any, error) {
		putCalls++
		result, putErr := originalPutImage(args, file, line)
		if putErr != nil {
			return nil, putErr
		}
		createdBlob = fmt.Sprintf("%v", result)
		return nil, fmt.Errorf("forced post-claim image failure")
	})
	pollSleeps := 0
	vars["Приостановить"] = interpreter.BuiltinFunc(func(_ []any, _ string, _ int) (any, error) {
		pollSleeps++
		return nil, fmt.Errorf("unexpected winner polling")
	})

	proc := reg.GetModuleNamespacedProc("ИмпортYML", "ЗаписатьКартинкуАтомарно")
	if proc == nil {
		t.Fatal("production helper not found")
	}
	const pngB64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	_, callErr := s.interp.Call(proc, nil, []any{
		siteRef, "https://images.example/failure.png", "Failure", pngB64, "image/png",
	}, vars)
	callErr = finishDSLExecution(txState, callErr)
	if callErr == nil || !strings.Contains(callErr.Error(), "forced post-claim image failure") {
		t.Fatalf("helper error = %v, want forced post-claim failure", callErr)
	}
	if putCalls != 1 || createdBlob == "" {
		t.Fatalf("PutImage calls/blob = %d/%q, want one transient blob", putCalls, createdBlob)
	}
	if pollSleeps != 0 {
		t.Fatalf("post-claim failure entered winner polling %d time(s)", pollSleeps)
	}

	for name, queryText := range map[string]string{
		"Media":        "SELECT COUNT(*) FROM " + metadata.TableName(media.Name),
		"blobs":        "SELECT COUNT(*) FROM _blobs",
		"public files": "SELECT COUNT(*) FROM _public_files",
	} {
		var count int
		if err := db.QueryRow(ctx, queryText).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if count != 0 {
			t.Errorf("post-claim rollback left %d %s row(s)", count, name)
		}
	}
}

func ensureCMSMediaIntegrationSchema(t *testing.T, ctx context.Context, db *storage.DB) {
	t.Helper()
	for _, schema := range []struct {
		name   string
		ensure func(context.Context) error
	}{
		{"audit", db.EnsureAuditSchema},
		{"attachments", db.EnsureAttachmentTable},
		{"blobs", db.EnsureBlobTable},
		{"public files", db.EnsurePublicFilesSchema},
	} {
		if err := schema.ensure(ctx); err != nil {
			t.Fatalf("ensure %s schema: %v", schema.name, err)
		}
	}
	if err := db.SaveFileStorageMode(ctx, storage.FileStorageDB); err != nil {
		t.Fatalf("use DB blob storage: %v", err)
	}
}

func cmsEntity(t *testing.T, proj *project.Project, name string) *metadata.Entity {
	t.Helper()
	for _, entity := range proj.Entities {
		if entity.Name == name {
			return entity
		}
	}
	t.Fatalf("CMS entity %q not found", name)
	return nil
}

func awaitCMSMediaCall(t *testing.T, name string, done <-chan cmsMediaCallResult) cmsMediaCallResult {
	t.Helper()
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("%s import: %v", name, result.err)
		}
		return result
	case <-time.After(12 * time.Second):
		t.Fatalf("%s import timed out", name)
		return cmsMediaCallResult{}
	}
}
