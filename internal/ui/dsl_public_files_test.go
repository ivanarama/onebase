package ui

// Тест DSL-пути публикации (план 127): файл публикуется вызовом из модуля, а
// затем скачивается по полученной ссылке — полный путь пользователя, а не
// вызов Go-функции напрямую.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestDSLPublicFile_PublishAndServe(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetFilesDir(filepath.Join(dir, "attachment-store"))

	cat := &metadata.Entity{
		Name:   "Медиа",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{cat}); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureAttachmentTable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePublicFilesSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveNetworkEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{cat}})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = s.newEntityService(nil)

	txState := interpreter.NewTxState(ctx)
	root := interpreter.NewCatalogsRoot(txState, db, registry).
		WithObjectFactory(s.catObjectFactory(txState))
	proxy := root.Get("Медиа").(*interpreter.CatalogProxy)
	w := proxy.CallMethod("создать", nil).(*catWriter)
	w.Set("Наименование", "Логотип")
	ref := w.CallMethod("записать", nil).(*interpreter.Ref)

	srcPath := filepath.Join(dir, "logo.png")
	if err := os.WriteFile(srcPath, []byte("PNG-CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}

	vars := map[string]any{}
	s.registerAttachmentBuiltins(vars, txState.Ctx)
	s.registerPublicFileBuiltins(vars, txState.Ctx)

	call := func(name string, args ...any) any {
		fn, ok := vars[name].(interpreter.BuiltinFunc)
		if !ok {
			t.Fatalf("функция %s не зарегистрирована в DSL", name)
		}
		res, err := fn(args, "", 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return res
	}

	attID := call("ПрисоединитьФайл", ref, srcPath).(string)

	// До публикации ссылки нет.
	if got := call("СсылкаНаФайл", attID); got != nil {
		t.Fatalf("СсылкаНаФайл до публикации = %v, ожидалось Неопределено", got)
	}

	opts := interpreter.NewStructFromMap(map[string]any{"КэшСекунд": float64(120)})
	url, ok := call("ОпубликоватьФайл", attID, opts).(string)
	if !ok || !strings.HasPrefix(url, "/pub/") {
		t.Fatalf("ОпубликоватьФайл вернул %q, ожидался путь /pub/<токен>", url)
	}
	if again := call("СсылкаНаФайл", attID); again != url {
		t.Errorf("СсылкаНаФайл=%v, ожидалась та же ссылка %q", again, url)
	}
	// Повторная публикация идемпотентна — иначе цикл рендера плодил бы токены.
	if repeat := call("ОпубликоватьФайл", attID); repeat != url {
		t.Errorf("повторная публикация дала другую ссылку: %v", repeat)
	}

	r := chi.NewRouter()
	s.MountServices(r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("скачивание по ссылке из DSL: status=%d", rec.Code)
	}
	if got := rec.Body.String(); got != "PNG-CONTENT" {
		t.Errorf("содержимое=%q", got)
	}
	if cc := rec.Header().Get("Cache-Control"); !publicFileCacheRequiresRevalidation(cc) {
		t.Errorf("Cache-Control=%q — отзываемая ссылка может остаться fresh в кэше", cc)
	}

	// Снятие публикации ломает ссылку немедленно.
	call("СнятьПубликациюФайла", attID)
	after := httptest.NewRecorder()
	r.ServeHTTP(after, httptest.NewRequest("GET", url, nil))
	if after.Code != http.StatusNotFound {
		t.Fatalf("после снятия публикации ссылка отвечает %d вместо 404", after.Code)
	}
}

// Тот же DSL-путь для картинок (поле image): у них свои функции, потому что
// блоб — не вложение, и до этого теста вся тройка не была покрыта вовсе.
func TestDSLPublicImage_PublishAndServe(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cat := &metadata.Entity{
		Name:   "Медиа",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{cat}); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureAttachmentTable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureBlobTable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePublicFilesSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveNetworkEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{cat}})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = s.newEntityService(nil)

	blob, err := db.PutBlob(ctx, "image/png", strings.NewReader("IMG-BYTES"), 1<<20,
		storage.BlobOwner{Kind: "catalog", Entity: "Медиа"})
	if err != nil {
		t.Fatalf("PutBlob: %v", err)
	}

	vars := map[string]any{}
	s.registerPublicFileBuiltins(vars, func() context.Context { return ctx })
	call := func(name string, args ...any) any {
		fn, ok := vars[name].(interpreter.BuiltinFunc)
		if !ok {
			t.Fatalf("функция %s не зарегистрирована в DSL", name)
		}
		res, err := fn(args, "", 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return res
	}

	id := blob.ID.String()
	if got := call("СсылкаНаКартинку", id); got != nil {
		t.Fatalf("СсылкаНаКартинку до публикации = %v, ожидалось Неопределено", got)
	}
	url, ok := call("ОпубликоватьКартинку", id).(string)
	if !ok || !strings.HasPrefix(url, "/pub/") {
		t.Fatalf("ОпубликоватьКартинку вернул %v, ожидался путь /pub/<токен>", url)
	}
	if again := call("СсылкаНаКартинку", id); again != url {
		t.Errorf("СсылкаНаКартинку=%v, ожидалась та же ссылка %q", again, url)
	}
	if repeat := call("ОпубликоватьКартинку", id); repeat != url {
		t.Errorf("повторная публикация дала другую ссылку: %v", repeat)
	}

	r := chi.NewRouter()
	s.MountServices(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("скачивание картинки по ссылке из DSL: status=%d", rec.Code)
	}
	if got := rec.Body.String(); got != "IMG-BYTES" {
		t.Errorf("содержимое=%q", got)
	}

	call("СнятьПубликациюКартинки", id)
	after := httptest.NewRecorder()
	r.ServeHTTP(after, httptest.NewRequest("GET", url, nil))
	if after.Code != http.StatusNotFound {
		t.Fatalf("после снятия публикации ссылка отвечает %d вместо 404", after.Code)
	}
}
