package ui

// Перечитывание строк табличной части, изменённых обработчиком через общий
// модуль (issue #579): модуль переписал ТЧ в базе → форма показывает свежие
// строки; пользователь правил грид → его строки не затираются; обработчик правил
// ТЧ в памяти → его строки сохраняются.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func tpRefreshEntity() (*metadata.Entity, metadata.TablePart) {
	tp := metadata.TablePart{
		Name: "Товары",
		Fields: []metadata.Field{
			{Name: "Товар", Type: metadata.FieldTypeString},
			{Name: "Количество", Type: metadata.FieldTypeNumber},
		},
	}
	e := &metadata.Entity{
		Name:       "Заказ",
		Kind:       metadata.KindDocument,
		Fields:     []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{tp},
	}
	return e, tp
}

func tpRow(tovar string, kol float64) map[string]any {
	return map[string]any{"Товар": tovar, "Количество": kol}
}

// newTPRefreshFixture — база с документом «Заказ», в ТЧ которого лежат initial.
func newTPRefreshFixture(t *testing.T, initial []map[string]any) (*Server, *metadata.Entity, metadata.TablePart, uuid.UUID, *storage.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "tp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e, tp := tpRefreshEntity()
	if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, e.Name, id, map[string]any{"Номер": "1"}, e); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTablePartRows(ctx, e.Name, tp.Name, id, initial, tp); err != nil {
		t.Fatal(err)
	}
	return &Server{store: db}, e, tp, id, db, ctx
}

// postObj изображает объект, собранный из POST: строки ТЧ с числами как float64
// (как их отдаёт parseTablePartRows).
func postObj(id uuid.UUID, rows []map[string]any) *runtime.Object {
	return &runtime.Object{
		ID:            id,
		Type:          "Заказ",
		Kind:          metadata.KindDocument,
		Fields:        map[string]any{"номер": "1"},
		TablePartRows: map[string][]map[string]any{"Товары": rows},
	}
}

func tpNames(rows []map[string]any) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, tpCellNorm(metadata.Field{Name: "Товар", Type: metadata.FieldTypeString}, tpCellValue(r, "Товар")))
	}
	return out
}

// Модуль переписал строки ТЧ в базе, пользователь грид не трогал — форма обязана
// показать свежие строки. На коде без правки obj остался бы с POST-строками.
func TestRefreshTableParts_ModuleRewroteInDB(t *testing.T) {
	initial := []map[string]any{tpRow("A", 1)}
	s, e, tp, id, db, ctx := newTPRefreshFixture(t, initial)

	// POST совпадает с базой до обработчика (пользователь ничего не правил).
	obj := postObj(id, []map[string]any{tpRow("A", 1)})
	tpBefore := tablePartRowsSnapshot(obj.TablePartRows)
	tpDBBefore := s.tablePartRowsFromDB(ctx, e, id)

	// Обработчик через модуль переписал ТЧ в базе (добавил строку).
	if err := db.UpsertTablePartRows(ctx, e.Name, tp.Name, id, []map[string]any{tpRow("A", 1), tpRow("B", 2)}, tp); err != nil {
		t.Fatal(err)
	}

	s.refreshTablePartsWrittenByHandler(ctx, e, obj, tpBefore, tpDBBefore)

	got := tpNames(obj.TablePartRows["Товары"])
	if len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Fatalf("строки ТЧ не перечитаны из базы: %v", got)
	}
}

// Пользователь отредактировал строки в гриде (POST ≠ база), и их нельзя затирать
// перечитыванием — даже если модуль что-то изменил в базе.
func TestRefreshTableParts_UserEditedGridKept(t *testing.T) {
	initial := []map[string]any{tpRow("A", 1)}
	s, e, tp, id, db, ctx := newTPRefreshFixture(t, initial)

	// Пользователь поменял количество в гриде: POST отличается от базы.
	obj := postObj(id, []map[string]any{tpRow("A", 5)})
	tpBefore := tablePartRowsSnapshot(obj.TablePartRows)
	tpDBBefore := s.tablePartRowsFromDB(ctx, e, id)

	// Пусть модуль тоже что-то переписал в базе — правки пользователя всё равно
	// в приоритете.
	if err := db.UpsertTablePartRows(ctx, e.Name, tp.Name, id, []map[string]any{tpRow("C", 9)}, tp); err != nil {
		t.Fatal(err)
	}

	s.refreshTablePartsWrittenByHandler(ctx, e, obj, tpBefore, tpDBBefore)

	rows := obj.TablePartRows["Товары"]
	if len(rows) != 1 || tpCellNorm(tp.Fields[0], tpCellValue(rows[0], "Товар")) != "A" ||
		tpCellNorm(tp.Fields[1], tpCellValue(rows[0], "Количество")) != "5" {
		t.Fatalf("правки пользователя в гриде затёрты перечитыванием: %v", rows)
	}
}

// Обработчик изменил строки ТЧ в памяти (Объект.Товары.Добавить) — они его
// намерение и не должны затираться перечитыванием из базы.
func TestRefreshTableParts_HandlerEditedInMemoryKept(t *testing.T) {
	initial := []map[string]any{tpRow("A", 1)}
	s, e, tp, id, db, ctx := newTPRefreshFixture(t, initial)

	obj := postObj(id, []map[string]any{tpRow("A", 1)})
	tpBefore := tablePartRowsSnapshot(obj.TablePartRows)
	tpDBBefore := s.tablePartRowsFromDB(ctx, e, id)

	// Обработчик добавил строку в памяти (obj), в базу пока не записал.
	obj.TablePartRows["Товары"] = append(obj.TablePartRows["Товары"], tpRow("Z", 9))
	// А в базе тем временем совсем другое — перечитывание не должно победить.
	if err := db.UpsertTablePartRows(ctx, e.Name, tp.Name, id, []map[string]any{tpRow("Q", 7)}, tp); err != nil {
		t.Fatal(err)
	}

	s.refreshTablePartsWrittenByHandler(ctx, e, obj, tpBefore, tpDBBefore)

	got := tpNames(obj.TablePartRows["Товары"])
	if len(got) != 2 || got[0] != "A" || got[1] != "Z" {
		t.Fatalf("строки, добавленные обработчиком в памяти, не сохранены: %v", got)
	}
}
