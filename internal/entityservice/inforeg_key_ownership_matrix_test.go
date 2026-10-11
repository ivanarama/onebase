package entityservice

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// All scenarios post through Save, including its DSL hook and transaction.
func infoOwnershipFixture(t *testing.T, db *storage.DB, keyed bool) (*metadata.InfoRegister, []*metadata.Entity, func() *Service) {
	t.Helper()
	ctx := context.Background()
	ir := &metadata.InfoRegister{Name: "ЦеныВладельцев", Recorder: true,
		Resources: []metadata.Field{{Name: "Цена", Type: metadata.FieldTypeNumber}}}
	if keyed {
		ir.Dimensions = []metadata.Field{{Name: "Товар", Type: metadata.FieldTypeString}}
	}
	docs := []*metadata.Entity{}
	programs := map[string]*ast.Program{}
	for _, name := range []string{"УстановкаЦеныА", "УстановкаЦеныБ"} {
		doc := &metadata.Entity{Name: name, Kind: metadata.KindDocument, Posting: true,
			Fields: []metadata.Field{{Name: "Товар", Type: metadata.FieldTypeString}, {Name: "Цена", Type: metadata.FieldTypeNumber}}}
		docs = append(docs, doc)
		programs[name] = mustParseProgramT(t, `Процедура OnPost()
   Дв = Движения.ЦеныВладельцев.Добавить();
   Дв.Товар = this.Товар;
   Дв.Цена = this.Цена;
  КонецПроцедуры`)
	}
	if err := db.Migrate(ctx, docs); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: docs, InfoRegs: []*metadata.InfoRegister{ir}, Programs: programs})
	return ir, docs, func() *Service {
		interp := interpreter.New()
		interp.LookupProc = reg.GetModuleProc
		return &Service{Store: db, Reg: reg, Interp: interp,
			BuildVars: func(c context.Context, mc *runtime.MovementsCollector, _ *[]string) (map[string]any, *interpreter.TxState) {
				return dslvars.Common{Ctx: c, Reg: reg, Store: db, Movements: mc}.Build(), nil
			}}
	}
}

func postInfoOwner(ctx context.Context, svc *Service, doc *metadata.Entity, id uuid.UUID, item string, price float64) error {
	res, err := svc.Save(ctx, SaveRequest{Entity: doc, ID: id, IsNew: true, Action: "post",
		Fields: map[string]any{"Товар": item, "Цена": price}})
	if err == nil && res.DSLError != "" {
		return fmt.Errorf("%s", res.DSLError)
	}
	return err
}

func TestInfoMovementOwnerIncludesDocumentTypeMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir, docs, newService := infoOwnershipFixture(t, db, true)
		id := uuid.New()
		if err := postInfoOwner(ctx, newService(), docs[0], id, "Гвоздь", 10); err != nil {
			t.Fatal(err)
		}
		err := postInfoOwner(ctx, newService(), docs[1], id, "Гвоздь", 20)
		if !errors.Is(err, storage.ErrInfoRegOwnershipConflict) {
			t.Errorf("ownership error is not recognized: %v", err)
		}
		if err == nil {
			t.Fatal("a different document type took over a key with the same UUID")
		}
		if !strings.Contains(err.Error(), docs[0].Name) || !strings.Contains(err.Error(), id.String()) {
			t.Fatalf("owner missing from error: %v", err)
		}
		var owner, typ, price string
		if err := db.QueryRow(ctx, "SELECT CAST(recorder AS TEXT), recorder_type, CAST(цена AS TEXT) FROM "+metadata.InfoRegTableName(ir.Name)).Scan(&owner, &typ, &price); err != nil {
			t.Fatal(err)
		}
		if owner != id.String() || typ != docs[0].Name || price != "10" {
			t.Fatalf("original movement changed: %s %s %s", owner, typ, price)
		}
		if _, err := db.GetByID(ctx, docs[1].Name, id, docs[1]); !storage.IsNotFound(err) {
			t.Fatalf("failed new document was not rolled back: %v", err)
		}
	})
}

func TestInfoMovementsWithoutPrimaryKeyMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir, docs, newService := infoOwnershipFixture(t, db, false)
		first, second := uuid.New(), uuid.New()
		for i, id := range []uuid.UUID{first, second} {
			if err := postInfoOwner(ctx, newService(), docs[0], id, "", float64(i+10)); err != nil {
				t.Fatalf("post document %d without register key: %v", i, err)
			}
		}
		var count int
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+metadata.InfoRegTableName(ir.Name)).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 2 {
			t.Fatalf("got %d movements, want one for each document", count)
		}
		if _, err := newService().Unpost(ctx, docs[0], second); err != nil {
			t.Fatal(err)
		}
		var owner string
		if err := db.QueryRow(ctx, "SELECT CAST(recorder AS TEXT) FROM "+metadata.InfoRegTableName(ir.Name)).Scan(&owner); err != nil {
			t.Fatal(err)
		}
		if owner != first.String() {
			t.Fatalf("Unpost removed another document's movement: %s", owner)
		}
	})
}

func TestConcurrentInfoMovementKeyOwnershipMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		ir, docs, newService := infoOwnershipFixture(t, db, true)
		// Hold both PostgreSQL INSERTs at a transaction advisory lock. A
		// preceding SELECT therefore sees a free key in both transactions.
		// SQLite serializes writers and needs no database-side barrier.
		lockKey := int64(uuid.New().ID())
		if db.Dialect().Name() == "postgres" {
			if _, err := db.Exec(ctx, fmt.Sprintf(`CREATE FUNCTION pause_info_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(%d); RETURN NEW; END $$`, lockKey)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, "CREATE TRIGGER pause_info_insert BEFORE INSERT ON "+metadata.InfoRegTableName(ir.Name)+" FOR EACH ROW EXECUTE FUNCTION pause_info_insert()"); err != nil {
				t.Fatal(err)
			}
		}
		ids := []uuid.UUID{uuid.New(), uuid.New()}
		services := []*Service{newService(), newService()}
		start := make(chan struct{})
		type result struct {
			index int
			err   error
		}
		results := make(chan result, 2)
		startPostings := func() {
			for i := range ids {
				go func(i int) {
					<-start
					results <- result{i, postInfoOwner(ctx, services[i], docs[i], ids[i], "Гвоздь", float64(10+i))}
				}(i)
			}
			close(start)
		}
		if db.Dialect().Name() == "postgres" {
			err := db.WithTxScope(ctx, func(lockCtx context.Context) error {
				if _, err := db.Exec(lockCtx, "SELECT pg_advisory_xact_lock($1)", lockKey); err != nil {
					return err
				}
				startPostings()
				for {
					var waiting int
					if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM pg_locks WHERE locktype = 'advisory' AND classid = 0 AND objid = $1 AND NOT granted", lockKey).Scan(&waiting); err != nil {
						return err
					}
					if waiting == 2 {
						return nil // Commit releases the barrier for both INSERTs.
					}
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(10 * time.Millisecond):
					}
				}
			})
			if err != nil {
				t.Fatal(err)
			}
		} else {
			startPostings()
		}
		completed := []result{<-results, <-results}
		winner, loser := -1, -1
		for _, r := range completed {
			if r.err == nil {
				if winner != -1 {
					t.Fatal("both documents posted the same register key")
				}
				winner = r.index
			} else {
				if loser != -1 {
					t.Fatalf("both postings failed: %v", r.err)
				}
				loser = r.index
				if !strings.Contains(r.err.Error(), "второй документ её не перехватывает") {
					t.Fatalf("not an ownership conflict: %v", r.err)
				}
			}
		}
		if winner < 0 || loser < 0 {
			t.Fatalf("winner=%d loser=%d", winner, loser)
		}
		var owner, typ, price string
		if err := db.QueryRow(ctx, "SELECT CAST(recorder AS TEXT), recorder_type, CAST(цена AS TEXT) FROM "+metadata.InfoRegTableName(ir.Name)).Scan(&owner, &typ, &price); err != nil {
			t.Fatal(err)
		}
		if owner != ids[winner].String() || typ != docs[winner].Name || price != fmt.Sprint(10+winner) {
			t.Fatalf("movement not owned by successful document: %s %s %s", owner, typ, price)
		}
		if row, err := db.GetByID(ctx, docs[winner].Name, ids[winner], docs[winner]); err != nil || row["posted"] != true {
			t.Fatalf("successful document not posted: %v %v", row, err)
		}
		if _, err := db.GetByID(ctx, docs[loser].Name, ids[loser], docs[loser]); !storage.IsNotFound(err) {
			t.Fatalf("failed posting was not rolled back: %v", err)
		}
	})
}
