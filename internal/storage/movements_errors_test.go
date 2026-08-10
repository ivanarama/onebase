package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Очистка движений глотала ошибки: отказ DELETE давал «удалено 0», что
// неотличимо от честного «удалять было нечего» — и это на НЕОБРАТИМОЙ операции.
// Сухой прогон (CountMovementsOfRecorderType) ошибку возвращать научили в #622,
// а боевой прогон в двух строках ниже сохранил прежнее поведение: `onebase
// doctor --forget-document` печатал «Удалено: 0», «Итоги пересчитаны» и
// завершался кодом 0 при живых движениях (#615).
//
// Отдельно: проверка сирот молчала, когда таблицы документа нет в базе —
// запрос падал на «no such table», ошибка глоталась, и doctor рапортовал
// «движений без регистратора нет» при живой сироте.

func movementsFixture(t *testing.T) (*DB, *metadata.Entity, *metadata.Register) {
	t.Helper()
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	doc := &metadata.Entity{
		Name: "Реализация", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	reg := &metadata.Register{
		Name:       "Продажи",
		Dimensions: []metadata.Field{{Name: "Склад", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteMovements(ctx, reg.Name, doc.Name, uuid.New(),
		[]map[string]any{{"ВидДвижения": "Приход", "Склад": "Основной", "Количество": float64(1)}},
		reg, nil); err != nil {
		t.Fatal(err)
	}
	return db, doc, reg
}

// Триггер, запрещающий удаление из таблицы регистра, — так моделируется отказ
// DELETE (нет прав, повреждённая база, блокировка).
func blockDeletes(t *testing.T, db *DB, reg *metadata.Register) {
	t.Helper()
	ctx := context.Background()
	table := metadata.RegisterTableName(reg.Name)
	if _, err := db.Exec(ctx, "CREATE TRIGGER no_del BEFORE DELETE ON "+quoteIdent(table)+
		" BEGIN SELECT RAISE(ABORT, 'удаление запрещено'); END"); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteMovementsOfUnknownRecorderType_ОшибкаНеПроглатывается(t *testing.T) {
	ctx := context.Background()
	db, _, reg := movementsFixture(t)
	blockDeletes(t, db, reg)

	n, err := db.DeleteMovementsOfUnknownRecorderType(ctx, []*metadata.Register{reg}, []string{"Реализация"})
	if err == nil {
		t.Fatalf("отказ DELETE подан как успех: удалено %d, ошибки нет", n)
	}
	if !strings.Contains(err.Error(), "Продажи") {
		t.Errorf("в ошибке нет имени регистра: %v", err)
	}
}

func TestDeleteOrphanMovements_ОшибкаНеПроглатывается(t *testing.T) {
	ctx := context.Background()
	db, doc, reg := movementsFixture(t)
	blockDeletes(t, db, reg)

	n, err := db.DeleteOrphanMovements(ctx, []*metadata.Register{reg}, []*metadata.Entity{doc})
	if err == nil {
		t.Fatalf("отказ DELETE подан как успех: удалено %d, ошибки нет", n)
	}
}

// Тип есть в конфигурации, а таблицы документа в базе нет: все его движения
// осиротевшие. Раньше запрос падал, ошибка глоталась, и проверка рапортовала
// «сирот нет».
func TestOrphanMovements_ТаблицаДокументаОтсутствует(t *testing.T) {
	ctx := context.Background()
	db, doc, reg := movementsFixture(t)
	if _, err := db.Exec(ctx, "DROP TABLE "+quoteIdent(metadata.TableName(doc.Name))); err != nil {
		t.Fatal(err)
	}

	stats, err := db.OrphanMovements(ctx, []*metadata.Register{reg}, []*metadata.Entity{doc})
	if err != nil {
		t.Fatalf("OrphanMovements: %v", err)
	}
	var total int
	for _, s := range stats {
		total += s.Count
	}
	if total == 0 {
		t.Errorf("сирота не найдена, хотя таблицы документа нет вовсе: %+v", stats)
	}
}
