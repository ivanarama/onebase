package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestInfoRegDSLManagerAndRecordSetRotateEditRevisionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := stateInfoReg(false, false)
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		key := storage.InfoRegEditKey{Dimensions: map[string]any{"Узел": "N1"}}
		revision := func() string {
			t.Helper()
			r, err := db.InfoRegReadForEdit(ctx, ir, key, nil)
			if err != nil || r == nil {
				t.Fatalf("%v %v", r, err)
			}
			return r.Revision
		}
		manager := `З = РегистрыСведений.СостояниеУзлов.СоздатьМенеджерЗаписи(); З.Узел = "N1"; З.Состояние = "A"; З.Записать();`
		if _, err := runInfoRegDSL(t, db, ir, manager); err != nil {
			t.Fatal(err)
		}
		first := revision()
		if _, err := runInfoRegDSL(t, db, ir, manager); err != nil {
			t.Fatal(err)
		}
		second := revision()
		if first == second {
			t.Fatal("DSL no-op write reused revision")
		}
		recordSet := `Н = РегистрыСведений.СостояниеУзлов.СоздатьНаборЗаписей(); Н.Отбор.Узел = "N1"; С = Н.Добавить(); С.Состояние = "A"; Н.Записать();`
		if _, err := runInfoRegDSL(t, db, ir, recordSet); err != nil {
			t.Fatal(err)
		}
		third := revision()
		if second == third {
			t.Fatal("record set replacement reused revision")
		}
		if _, err := runInfoRegDSL(t, db, ir, manager+` З._edit_revision = "injected"; З.Записать();`); err == nil || !strings.Contains(err.Error(), "_edit_revision") {
			t.Fatalf("DSL accepted private field: %v", err)
		}
		messages, err := runInfoRegDSL(t, db, ir, `З = РегистрыСведений.СостояниеУзлов.СоздатьМенеджерЗаписи(); З.Узел = "N1"; З.Прочитать(); Сообщить(Строка(З._edit_revision));`)
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) != 1 || (messages[0] != "" && messages[0] != "Неопределено") {
			t.Fatalf("DSL exposed private field: %v", messages)
		}
	})
}
