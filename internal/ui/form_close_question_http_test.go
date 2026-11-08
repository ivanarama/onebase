package ui

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

type closeQuestionChangeCounter struct{ count atomic.Int32 }

func (c *closeQuestionChangeCounter) PublishChange(context.Context, string, string, map[string]any, map[string]any) {
	c.count.Add(1)
}

// Drive the real modal/controller against the mounted HTTP server, then check
// durable rows and post-commit effects independently of the client's decision.
func TestBeforeCloseQuestionSavesOnceHTTPMatrix(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required for the close-question HTTP matrix")
	}
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		form := managedObjectForm()
		form.Elements = append(form.Elements, fieldEl("Наименование", "Объект.Наименование"))
		form.Handlers = map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}
		form.ProgramAST = mustParse(t, beforeCloseAsksQuestion)
		entity := &metadata.Entity{Name: "ЗакрытиеСВопросом", Kind: metadata.KindDocument, Posting: true, NotifyChanges: true,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
			Forms:  []*metadata.FormModule{form}}
		if err := db.Migrate(context.Background(), []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}})
		srv := &Server{store: db, reg: reg, interp: interpreter.New(), lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
		srv.entitySvc = srv.newEntityService(nil)
		counter := &closeQuestionChangeCounter{}
		srv.entitySvc.ChangePublisher = counter
		router := chi.NewRouter()
		srv.Mount(router)
		httpServer := httptest.NewServer(router)
		t.Cleanup(httpServer.Close)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "node", "--test", "--test-name-pattern=BeforeClose question saves once through mounted HTTP", "static/managed_close_intent_behavior_test.js")
		cmd.Env = append(os.Environ(), "ONEBASE_CLOSE_QUESTION_HTTP_URL="+httpServer.URL)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("close-question HTTP JavaScript (%s): %v\n%s", db.Dialect().Name(), err, output)
		}
		rows, err := db.List(context.Background(), entity.Name, entity, storage.ListParams{})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 9 || counter.count.Load() != 12 {
			t.Fatalf("want nine objects and twelve actual writes: rows=%d, publications=%d", len(rows), counter.count.Load())
		}
		fresh := 0
		for _, row := range rows {
			id, err := uuid.Parse(refValueString(row["id"]))
			if err != nil {
				t.Fatal(err)
			}
			persisted, err := db.GetByID(context.Background(), entity.Name, id, entity)
			if err != nil {
				t.Fatal(err)
			}
			wantVersion := int64(1)
			if persisted["Наименование"] == "Новая правка во время чтения файла" {
				fresh++
				wantVersion = 2
			} else if persisted["Наименование"] != "Черновик" {
				t.Fatalf("unexpected durable name: %v", persisted["Наименование"])
			}
			if version := persistedEntityVersion(persisted); version != wantVersion {
				t.Fatalf("unexpected durable version: got=%d want=%d", version, wantVersion)
			}
		}
		if fresh != 3 {
			t.Fatalf("want one newly saved input per mode, got %d", fresh)
		}
		t.Logf("real close-question modal/controller and HTTP (%s): %s", db.Dialect().Name(), output)
	})
}
