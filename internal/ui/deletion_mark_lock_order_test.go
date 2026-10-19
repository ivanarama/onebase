package ui

// Единый порядок блокировок «строка регистратора → локи итогов» для пометки на
// удаление.
//
// Repost берёт строку документа (LockMovementRecorder) до записи движений.
// Пометка проведённого документа шла обратным порядком: сперва clearMovements,
// то есть Write*Movements(nil) и вместе с ними локи итогов, и только потом
// SetPosted, которому нужна строка. Два порядка на одном документе и одном
// регистре с totals.enabled давали цикл: Repost держит строку и ждёт итоги,
// пометка держит итоги и ждёт строку. PostgreSQL снимал одну из операций с
// 40P01. Проверяем через публичные входы: entityservice.Service.Repost и
// HTTP POST /ui/document/<Сущность>/<id>/delete?mark=1 через Server.Mount.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// heldRecorderStore держит строку регистратора захваченной: после настоящего
// LockMovementRecorder сигналит и ждёт разрешения продолжить. Поведение базы не
// подменяется — лок берётся настоящий, в настоящей транзакции Repost, и к
// локам итогов Repost ещё не подходил.
type heldRecorderStore struct {
	entityservice.Storage
	locked  chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *heldRecorderStore) LockMovementRecorder(ctx context.Context, entity *metadata.Entity, id uuid.UUID) error {
	if err := s.Storage.LockMovementRecorder(ctx, entity, id); err != nil {
		return err
	}
	s.once.Do(func() { close(s.locked) })
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func newMarkLockOrderServer(t *testing.T, db *storage.DB) (*Server, *metadata.Entity, *metadata.Register) {
	t.Helper()
	ctx := context.Background()
	doc := &metadata.Entity{
		Name:    "ГонкаПометкиДок",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	reg := &metadata.Register{
		Name:       "ГонкаПометкиРег",
		Dimensions: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
		Totals:     metadata.RegisterTotals{Enabled: true},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}
	program := mustParse(t, `Процедура ОбработкаПроведения()
  Дв = Движения.ГонкаПометкиРег.Добавить();
  Дв.Ключ = "ключ";
  Дв.Количество = ЭтотОбъект.Количество;
КонецПроцедуры`)
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{doc},
		Registers: []*metadata.Register{reg},
		Programs:  map[string]*ast.Program{doc.Name: program},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp,
		lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	// Ограничитель операций инициализируется лениво и без мьютекса — убираем
	// эту инициализацию из конкурентной части теста.
	s.ops = newOperationLimiter()
	s.entitySvc = s.newEntityService(nil)
	return s, doc, reg
}

// markViaHTTP — публичный вход пометки на удаление.
func markViaHTTP(s *Server, entity *metadata.Entity, id uuid.UUID) *httptest.ResponseRecorder {
	path := "/ui/" + strings.ToLower(string(entity.Kind)) + "/" + entity.Name +
		"/" + id.String() + "/delete?mark=1"
	rec := httptest.NewRecorder()
	router := chi.NewRouter()
	s.Mount(router)
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
	return rec
}

func postedDoc(t *testing.T, ctx context.Context, s *Server, doc *metadata.Entity) uuid.UUID {
	t.Helper()
	id := uuid.New()
	res, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{
		Entity: doc, ID: id, IsNew: true, Action: "post",
		Fields: map[string]any{"Количество": float64(100)},
	})
	if err != nil || res.DSLError != "" {
		t.Fatalf("проведение документа: err=%v, DSL=%s", err, res.DSLError)
	}
	return id
}

// markedState — состояние документа и регистра после пометки.
func markedState(t *testing.T, ctx context.Context, db *storage.DB,
	doc *metadata.Entity, reg *metadata.Register, id uuid.UUID) (posted, marked bool, movements int, totals float64) {
	t.Helper()
	if err := db.QueryRow(ctx, "SELECT posted, deletion_mark FROM "+metadata.TableName(doc.Name)+
		" WHERE id = $1", id.String()).Scan(&posted, &marked); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+metadata.RegisterTableName(reg.Name)+
		" WHERE recorder = $1", id.String()).Scan(&movements); err != nil {
		t.Fatal(err)
	}
	var sum string
	if err := db.QueryRow(ctx, "SELECT CAST(COALESCE(SUM(количество), 0) AS TEXT) FROM "+
		metadata.RegisterTotalsTableName(reg.Name)).Scan(&sum); err != nil {
		t.Fatal(err)
	}
	value, err := strconv.ParseFloat(sum, 64)
	if err != nil {
		t.Fatal(err)
	}
	return posted, marked, movements, value
}

func TestDeletionMark_LocksRecorderRowBeforeTotals_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s, doc, reg := newMarkLockOrderServer(t, db)

		// Последовательная проверка самого входа: пометка проведённого документа
		// снимает проведение и убирает движения с итогами. Нужна на обоих
		// диалектах — она держит сам сценарий, а не только порядок локов.
		id := postedDoc(t, ctx, s, doc)
		if posted, _, movements, totals := markedState(t, ctx, db, doc, reg, id); !posted || movements != 1 || totals != 100 {
			t.Fatalf("после проведения ожидались posted=true, 1 движение, итог 100; получили posted=%v, движений %d, итог %v",
				posted, movements, totals)
		}
		if rec := markViaHTTP(s, doc, id); rec.Code != http.StatusSeeOther {
			t.Fatalf("пометка на удаление: код %d, тело %s", rec.Code, rec.Body.String())
		}
		posted, marked, movements, totals := markedState(t, ctx, db, doc, reg, id)
		if posted || !marked || movements != 0 || totals != 0 {
			t.Fatalf("после пометки ожидались posted=false, пометка, 0 движений, итог 0; получили posted=%v, пометка=%v, движений %d, итог %v",
				posted, marked, movements, totals)
		}

		if !db.IsPostgres() {
			// Цикла нет: LockMovementRecorder на SQLite — no-op, а запись
			// сериализована самой базой.
			t.Skip("порядок локов проверяется на PostgreSQL")
		}

		// Repost держит строку регистратора и ещё не брал локи итогов; пометка
		// в это время идёт своим публичным входом. При обратном порядке локов
		// PostgreSQL снимает одну из операций с 40P01.
		raceID := postedDoc(t, ctx, s, doc)
		held := &heldRecorderStore{Storage: db,
			locked: make(chan struct{}), release: make(chan struct{})}
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(held.release) }) }
		defer release()

		repostSvc := *s.entitySvc
		repostSvc.Store = held
		repostDone := make(chan error, 1)
		go func() { repostDone <- repostSvc.Repost(ctx, doc.Name, raceID) }()
		select {
		case <-held.locked:
		case err := <-repostDone:
			t.Fatalf("перепроведение завершилось, не взяв строку регистратора: %v", err)
		case <-ctx.Done():
			t.Fatal("перепроведение не дошло до блокировки строки регистратора")
		}

		markDone := make(chan *httptest.ResponseRecorder, 1)
		go func() { markDone <- markViaHTTP(s, doc, raceID) }()

		// Пометка должна фактически ждать в PostgreSQL до того, как Repost
		// продолжит: иначе отпускание Repost до её блокировки скрыло бы цикл.
		// Транзакция Repost в этот момент idle in transaction, то есть
		// wait_event_type у неё не 'Lock'.
		waitingOnDoc := "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid <> pg_backend_pid()" +
			" AND wait_event_type = 'Lock' AND query ILIKE $1)"
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for waiting := false; !waiting; {
			if err := db.QueryRow(ctx, waitingOnDoc, "%"+metadata.TableName(doc.Name)+"%").Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			select {
			case <-ticker.C:
			case rec := <-markDone:
				t.Fatalf("пометка завершилась, не дождавшись перепроведения: код %d", rec.Code)
			case <-ctx.Done():
				t.Fatal("пометка не дошла до ожидания блокировки")
			}
		}

		release()
		select {
		case err := <-repostDone:
			if err != nil {
				t.Fatalf("перепроведение: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("перепроведение не завершилось")
		}
		select {
		case rec := <-markDone:
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("пометка под перепроведением: код %d, тело %s", rec.Code, rec.Body.String())
			}
		case <-ctx.Done():
			t.Fatal("пометка не завершилась")
		}

		posted, marked, movements, totals = markedState(t, ctx, db, doc, reg, raceID)
		if posted || !marked || movements != 0 || totals != 0 {
			t.Fatalf("после обоих коммитов ожидались posted=false, пометка, 0 движений, итог 0; получили posted=%v, пометка=%v, движений %d, итог %v",
				posted, marked, movements, totals)
		}
	})
}
