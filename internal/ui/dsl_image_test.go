package ui

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func callDSLTestBuiltin(t *testing.T, vars map[string]any, name string, args ...any) (any, error) {
	t.Helper()
	fn, ok := vars[name].(interpreter.BuiltinFunc)
	if !ok {
		t.Fatalf("builtin %s не зарегистрирован", name)
	}
	return fn(args, "dsl-image-test.os", 1)
}

// Owner-aware СохранитьКартинку must not become a shortcut around entity/RLS
// checks. It also requires a transaction so the blob cannot be committed before
// the record that references it.
func TestSaveImageWithOwnerChecksAccessAndTransaction(t *testing.T) {
	entity := ownerCatalog("Фотографии", metadata.Field{Name: "Картинка", Type: metadata.FieldTypeImage})
	s, baseCtx := newSubmitTestServer(t, []*metadata.Entity{entity})
	if err := s.store.EnsureBlobTable(baseCtx); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SaveFileStorageMode(baseCtx, storage.FileStorageDB); err != nil {
		t.Fatal(err)
	}
	visibleID := seedOwnerRow(t, baseCtx, s, entity, "u", map[string]any{"Картинка": ""})
	hiddenID := seedOwnerRow(t, baseCtx, s, entity, "other", map[string]any{"Картинка": ""})
	userCtx := auth.ContextWithUser(context.Background(), rowOwnerUser("u", entity.Name, "write"))
	vars, txState := s.buildDSLVarsTx(userCtx, runtime.NewMovementsCollector("processor", [16]byte{}))
	data := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nowner-aware"))

	if _, err := callDSLTestBuiltin(t, vars, "СохранитьКартинку", data, "image/png",
		&interpreter.Ref{UUID: visibleID.String(), Type: entity.Name}); err == nil ||
		!strings.Contains(err.Error(), "внутри транзакции") {
		t.Fatalf("owner-aware вызов вне транзакции: %v", err)
	}

	if _, err := callDSLTestBuiltin(t, vars, "НачатьТранзакцию"); err != nil {
		t.Fatal(err)
	}
	if _, err := callDSLTestBuiltin(t, vars, "СохранитьКартинку", data, "image/png",
		&interpreter.Ref{UUID: hiddenID.String(), Type: entity.Name}); err == nil ||
		!strings.Contains(err.Error(), "нет доступа") {
		t.Fatalf("скрытая строка не была отклонена: %v", err)
	}
	if _, err := callDSLTestBuiltin(t, vars, "ОтменитьТранзакцию"); err != nil {
		t.Fatal(err)
	}

	if _, err := callDSLTestBuiltin(t, vars, "НачатьТранзакцию"); err != nil {
		t.Fatal(err)
	}
	got, err := callDSLTestBuiltin(t, vars, "СохранитьКартинку", data, "image/png",
		&interpreter.Ref{UUID: visibleID.String(), Type: entity.Name})
	if err != nil {
		t.Fatalf("видимая строка: %v", err)
	}
	id, err := uuid.Parse(got.(string))
	if err != nil {
		t.Fatalf("UUID результата: %v", err)
	}
	blob, rc, err := s.store.OpenBlob(txState.Ctx(), id)
	if err != nil {
		t.Fatalf("OpenBlob внутри транзакции: %v", err)
	}
	_ = rc.Close()
	if blob.OwnerKind != string(entity.Kind) || blob.OwnerEntity != entity.Name {
		t.Fatalf("владелец blob = %q/%q, ожидался %q/%q",
			blob.OwnerKind, blob.OwnerEntity, entity.Kind, entity.Name)
	}
	if _, err := callDSLTestBuiltin(t, vars, "ОтменитьТранзакцию"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.store.OpenBlob(baseCtx, id); err == nil {
		t.Fatal("rollback оставил owner-aware blob")
	}
}
