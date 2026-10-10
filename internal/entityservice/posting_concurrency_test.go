package entityservice

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dslvars"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Observe the lock attempt before it blocks on the real PostgreSQL document
// row; the wrapper does not replace transaction or lock behavior.
type concurrentUnpostStore struct {
	Storage
	locking     chan struct{}
	application string
	once        sync.Once
}

func (s *concurrentUnpostStore) LockMovementRecorder(ctx context.Context, entity *metadata.Entity, id uuid.UUID) error {
	if _, err := s.Exec(ctx, "SELECT set_config('application_name', $1, true)", s.application); err != nil {
		return err
	}
	s.once.Do(func() { close(s.locking) })
	return s.Storage.LockMovementRecorder(ctx, entity, id)
}

// Hold a completed Repost transaction before commit, just as FinalPreflight
// does for Save, while preserving the real database transaction.
type stagedRepostStore struct {
	Storage
	beforeCommit func() error
}

func (s *stagedRepostStore) WithTx(ctx context.Context, fn func(context.Context) error) error {
	return s.Storage.WithTx(ctx, func(txCtx context.Context) error {
		if err := fn(txCtx); err != nil {
			return err
		}
		return s.beforeCommit()
	})
}

func TestPosting_ConcurrentPostUnpost_Matrix(t *testing.T) {
	for _, writer := range []string{"save", "repost"} {
		t.Run(writer, func(t *testing.T) {
			for _, action := range []string{"unpost", "write"} {
				t.Run(action, func(t *testing.T) {
					dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
						if !db.IsPostgres() {
							t.Skip("PostgreSQL transaction visibility and advisory locks")
						}
						ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
						defer cancel()
						doc := &metadata.Entity{Name: "RaceDoc", Kind: metadata.KindDocument, Posting: true,
							Fields: []metadata.Field{{Name: "Flag", Type: metadata.FieldTypeBool}}}
						reg := &metadata.Register{Name: "RaceReg",
							Dimensions: []metadata.Field{{Name: "Key", Type: metadata.FieldTypeString}},
							Resources:  []metadata.Field{{Name: "Qty", Type: metadata.FieldTypeNumber}},
							Totals:     metadata.RegisterTotals{Enabled: true}}
						if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
							t.Fatal(err)
						}
						if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
							t.Fatal(err)
						}
						program := mustParseProgramT(t, `Процедура OnPost()
 Если this.Flag Тогда
  Дв = Движения.RaceReg.Добавить();
  Дв.Key = "key";
  Дв.Qty = 100;
 КонецЕсли;
КонецПроцедуры`)
						registry := runtime.NewRegistry()
						registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{doc}, Registers: []*metadata.Register{reg},
							Programs: map[string]*ast.Program{doc.Name: program}})
						interp := interpreter.New()
						interp.LookupProc = registry.GetModuleProc
						svc := &Service{Store: db, Reg: registry, Interp: interp,
							BuildVars: func(c context.Context, mc *runtime.MovementsCollector, _ *[]string) (map[string]any, *interpreter.TxState) {
								return dslvars.Common{Ctx: c, Reg: registry, Store: db, Movements: mc}.Build(), nil
							}}
						id := uuid.New()
						res, err := svc.Save(ctx, SaveRequest{Entity: doc, ID: id, IsNew: true, Action: "post", Fields: map[string]any{"Flag": false}})
						if err != nil || res.DSLError != "" {
							t.Fatalf("initial empty posting: err=%v, DSL=%s", err, res.DSLError)
						}
						staged, commit := make(chan struct{}), make(chan struct{})
						var commitOnce sync.Once
						unblock := func() { commitOnce.Do(func() { close(commit) }) }
						defer unblock()
						beforeCommit := func() error {
							close(staged)
							select {
							case <-commit:
								return nil
							case <-ctx.Done():
								return ctx.Err()
							}
						}
						if writer == "repost" {
							// Keep an empty posted document but make the next OnPost emit
							// a movement. Repost must read this state under its row lock.
							if err := db.Upsert(ctx, doc.Name, id, map[string]any{"Flag": true}, doc); err != nil {
								t.Fatal(err)
							}
						}
						postDone := make(chan error, 1)
						go func() {
							var err error
							if writer == "save" {
								res, saveErr := svc.Save(ctx, SaveRequest{Entity: doc, ID: id, Action: "post", Fields: map[string]any{"Flag": true},
									FinalPreflight: func(context.Context, *runtime.Object) error { return beforeCommit() }})
								err = saveErr
								if err == nil && res.DSLError != "" {
									err = fmt.Errorf("post: %s", res.DSLError)
								}
							} else {
								repostSvc := *svc
								repostSvc.Store = &stagedRepostStore{Storage: db, beforeCommit: beforeCommit}
								err = repostSvc.Repost(ctx, doc.Name, id)
							}
							postDone <- err
						}()
						select {
						case <-staged:
						case <-ctx.Done():
							t.Fatal("posting did not reach commit boundary")
						}
						store := &concurrentUnpostStore{Storage: db, locking: make(chan struct{}), application: "posting-race-" + id.String()}
						unpostSvc := &Service{Store: store, Reg: registry}
						unpostDone := make(chan error, 1)
						go func() {
							var res SaveResult
							var err error
							if action == "unpost" {
								res, err = unpostSvc.Unpost(ctx, doc, id)
							} else {
								res, err = unpostSvc.Save(ctx, SaveRequest{Entity: doc, ID: id, Fields: map[string]any{"Flag": false}})
							}
							if err == nil && res.DSLError != "" {
								err = fmt.Errorf("unpost: %s", res.DSLError)
							}
							unpostDone <- err
						}()
						select {
						case <-store.locking:
						case <-ctx.Done():
							t.Fatal("unposting did not reach recorder lock")
						}
						// Prove that the competing public operation is actually waiting
						// in PostgreSQL before releasing the first commit. A signal
						// just before the SELECT would still allow a scheduler race.
						ticker := time.NewTicker(10 * time.Millisecond)
						defer ticker.Stop()
						for {
							var waiting bool
							if err := db.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE application_name = $1 AND wait_event_type = 'Lock')", store.application).Scan(&waiting); err != nil {
								t.Fatal(err)
							}
							if waiting {
								break
							}
							select {
							case <-ticker.C:
							case err := <-unpostDone:
								t.Fatalf("competing operation completed before first commit: %v", err)
							case <-ctx.Done():
								t.Fatal("competing operation did not wait for recorder row")
							}
						}
						unblock()
						for name, result := range map[string]<-chan error{"post": postDone, "unpost": unpostDone} {
							select {
							case err := <-result:
								if err != nil {
									t.Fatalf("%s: %v", name, err)
								}
							case <-ctx.Done():
								t.Fatalf("%s timed out", name)
							}
						}
						fields, err := db.GetByID(ctx, doc.Name, id, doc)
						if err != nil {
							t.Fatal(err)
						}
						var rows int
						if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+metadata.RegisterTableName(reg.Name)+" WHERE recorder = $1", id.String()).Scan(&rows); err != nil {
							t.Fatal(err)
						}
						var total string
						if err := db.QueryRow(ctx, "SELECT CAST(COALESCE(SUM(qty), 0) AS TEXT) FROM "+metadata.RegisterTotalsTableName(reg.Name)).Scan(&total); err != nil {
							t.Fatal(err)
						}
						qty, err := strconv.ParseFloat(total, 64)
						if err != nil {
							t.Fatal(err)
						}
						t.Logf("after both commits: posted=%v movements=%d totals=%s", fields["posted"], rows, total)
						if fields["posted"] != false || rows != 0 || qty != 0 {
							t.Fatalf("successful unpost left inconsistent state: posted=%v movements=%d totals=%s", fields["posted"], rows, total)
						}
					})
				})
			}
		})
	}
}
