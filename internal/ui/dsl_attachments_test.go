package ui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Полный цикл вложений из DSL: ПрисоединитьФайл → СписокВложений →
// ПутьКВложению (содержимое совпадает) → УдалитьВложение.
func TestDSLAttachments_FullCycle(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetFilesDir(filepath.Join(dir, "attachment-store"))

	cat := &metadata.Entity{
		Name:   "Релизы",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{cat}); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureAttachmentTable(ctx); err != nil {
		t.Fatal(err)
	}

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{cat}})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = s.newEntityService(nil)

	// Запись справочника, к которой цепляем файл.
	txState := interpreter.NewTxState(ctx)
	root := interpreter.NewCatalogsRoot(txState, db, registry).
		WithObjectFactory(s.catObjectFactory(txState))
	proxy := root.Get("Релизы").(*interpreter.CatalogProxy)
	w := proxy.CallMethod("создать", nil).(*catWriter)
	w.Set("Наименование", "Тест 1.0")
	ref := w.CallMethod("записать", nil).(*interpreter.Ref)

	// Файл на диске.
	srcPath := filepath.Join(dir, "release.zip")
	if err := os.WriteFile(srcPath, []byte("BINARY-CONTENT"), 0o644); err != nil {
		t.Fatal(err)
	}

	vars := map[string]any{}
	s.registerAttachmentBuiltins(vars, txState.Ctx)

	call := func(name string, args ...any) any {
		fn := vars[name].(interpreter.BuiltinFunc)
		res, err := fn(args, "", 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return res
	}

	attID := call("ПрисоединитьФайл", ref, srcPath).(string)
	if attID == "" {
		t.Fatal("ПрисоединитьФайл вернул пустой ИД")
	}

	arr := call("СписокВложений", ref).(*interpreter.Array)
	if got := arr.CallMethod("количество", nil).(float64); got != 1 {
		t.Fatalf("вложений = %v, ожидалось 1", got)
	}
	st := arr.CallMethod("получить", []any{float64(0)}).(*interpreter.Struct)
	if got := st.Get("ИмяФайла"); got != "release.zip" {
		t.Fatalf("ИмяФайла = %v", got)
	}

	path := call("ПутьКВложению", attID).(string)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "BINARY-CONTENT" {
		t.Fatalf("содержимое вложения повреждено: %q", data)
	}

	// Attachment storage is outside the ordinary DSL sandbox. The email path
	// resolver allows the exact RLS-checked attachment, but no other outside
	// file, so ПутьКВложению can safely feed ПисьмоEmail.ПрисоединитьФайл.
	sandboxDir := filepath.Join(dir, "sandbox")
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	interpreter.SetFileSandbox(sandboxDir)
	defer interpreter.SetFileSandbox("")
	resolver := s.emailAttachmentPathResolver(txState.Ctx)
	if resolved, err := resolver(path); err != nil || resolved != path {
		t.Fatalf("stored attachment path rejected: path=%q resolved=%q err=%v", path, resolved, err)
	}
	outside := filepath.Join(dir, "not-an-attachment.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver(outside); err == nil {
		t.Fatal("resolver allowed an arbitrary file outside the DSL sandbox")
	}

	call("УдалитьВложение", attID)
	arr2 := call("СписокВложений", ref).(*interpreter.Array)
	if got := arr2.CallMethod("количество", nil).(float64); got != 0 {
		t.Fatal("вложение не удалено")
	}
}

// Совместимость с entityservice: интеграционная проверка, что билтины
// зарегистрированы в общем окружении buildDSLVars.
func TestDSLAttachments_RegisteredInBuildVars(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{})
	interp := interpreter.New()
	s := &Server{store: db, reg: registry, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = &entityservice.Service{Store: db, Reg: registry, Interp: interp}

	vars, _ := s.buildDSLVarsTx(ctx, runtime.NewMovementsCollector("processor", [16]byte{}))
	for _, name := range []string{"ПрисоединитьФайл", "СписокВложений", "ПутьКВложению", "УдалитьВложение"} {
		if _, ok := vars[name]; !ok {
			t.Fatalf("builtin %s не зарегистрирован в buildDSLVars", name)
		}
	}
}
