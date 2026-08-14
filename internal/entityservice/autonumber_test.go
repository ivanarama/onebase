package entityservice

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Автонумерация — свойство записи объекта, а не конкретного входа (#869).
//
// До этой правки нумеровали четыре разных места: форма и ИИ-действия (через
// AutoNumberField, то есть и справочники), REST v1 и v2 (жёстко по имени
// «Номер», то есть только документы) и DSL-запись документа. Справочник,
// созданный через REST или из модуля, оставался БЕЗ кода — при том что
// docs/features.md обещает «новые элементы получают код автоматически» без
// оговорок, а 117E сделал такой код ещё и обязательно уникальным.
//
// Проверка идёт через entityservice.Save — общую точку REST v1/v2 и DSL-записи
// справочника.
func numberedCatalog() *metadata.Entity {
	return &metadata.Entity{
		Name:      "Контрагенты",
		Kind:      metadata.KindCatalog,
		Numerator: &metadata.Numerator{Prefix: "К-", Length: 6},
		Fields: []metadata.Field{
			{Name: metadata.StandardCodeField, Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
}

func newNumberingService(t *testing.T, db *storage.DB, ents []*metadata.Entity) *Service {
	t.Helper()
	if err := db.Migrate(context.Background(), ents); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: ents})
	return &Service{Store: db, Reg: registry, Interp: interpreter.New()}
}

func TestSave_НумеруетСправочникMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		cat := numberedCatalog()
		svc := newNumberingService(t, db, []*metadata.Entity{cat})

		id := uuid.New()
		if _, err := svc.Save(ctx, SaveRequest{
			Entity: cat, ID: id, IsNew: true,
			Fields: map[string]any{"Наименование": "ООО Ромашка"},
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		row, err := db.GetByID(ctx, cat.Name, id, cat)
		if err != nil {
			t.Fatal(err)
		}
		code := fmt.Sprintf("%v", row[metadata.StandardCodeField])
		if strings.TrimSpace(code) == "" || code == "<nil>" {
			t.Fatalf("элемент справочника записан без кода: %v", row)
		}
		if !strings.HasPrefix(code, "К-") {
			t.Errorf("код %q не по нумератору (ожидался префикс «К-»)", code)
		}

		// Второй элемент получает СВОЙ код, а не тот же самый: 117E делает код
		// уникальным, и повтор упёрся бы в индекс уже у пользователя.
		id2 := uuid.New()
		if _, err := svc.Save(ctx, SaveRequest{
			Entity: cat, ID: id2, IsNew: true,
			Fields: map[string]any{"Наименование": "ООО Лютик"},
		}); err != nil {
			t.Fatalf("Save второго: %v", err)
		}
		row2, _ := db.GetByID(ctx, cat.Name, id2, cat)
		if code2 := fmt.Sprintf("%v", row2[metadata.StandardCodeField]); code2 == code {
			t.Errorf("оба элемента получили один код %q", code2)
		}
	})
}

// Явно заданный код не переписывается: платформа не должна затирать введённое
// пользователем.
func TestSave_ЯвныйКодСохраняется(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "num.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cat := numberedCatalog()
	svc := newNumberingService(t, db, []*metadata.Entity{cat})

	id := uuid.New()
	if _, err := svc.Save(ctx, SaveRequest{
		Entity: cat, ID: id, IsNew: true,
		Fields: map[string]any{metadata.StandardCodeField: "РУЧНОЙ-1", "Наименование": "X"},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	row, _ := db.GetByID(ctx, cat.Name, id, cat)
	if got := fmt.Sprintf("%v", row[metadata.StandardCodeField]); got != "РУЧНОЙ-1" {
		t.Errorf("явно заданный код заменён на %q", got)
	}
}

// Справочник БЕЗ нумератора код не получает: кодов у справочников не было
// вовсе, и раздача их всем подряд молча изменила бы данные существующих
// конфигураций.
func TestSave_БезНумератораКодНеВыдаётся(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "nonum.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cat := numberedCatalog()
	cat.Numerator = nil
	svc := newNumberingService(t, db, []*metadata.Entity{cat})

	id := uuid.New()
	if _, err := svc.Save(ctx, SaveRequest{
		Entity: cat, ID: id, IsNew: true,
		Fields: map[string]any{"Наименование": "X"},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	row, _ := db.GetByID(ctx, cat.Name, id, cat)
	if got := fmt.Sprintf("%v", row[metadata.StandardCodeField]); got != "<nil>" && strings.TrimSpace(got) != "" {
		t.Errorf("без numerator: выдан код %q", got)
	}
}
